// Package watch implements the "run" subcommand: hash a watched file or
// directory, and — if the hash changed since the last run — validate the
// new state with a check command and, only on success, run a reload
// command.
//
// It is normally invoked once per systemd template unit instance
// (config-watch@<name>.service), with every setting supplied through an
// EnvironmentFile — see internal/install and units/example.env. Every
// setting can also be given as a command-line flag instead (or as well —
// a flag wins over the environment when both are set), so a watch can be
// exercised manually without a systemd instance.
package watch

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
)

// Environment variables read by Run. STATE_DIRECTORY is populated
// automatically by systemd when the unit sets StateDirectory=; it is not
// something an operator sets by hand in the instance .env file.
const (
	EnvWatchPath = "CONFIG_WATCH_PATH"
	EnvCheckCmd  = "CONFIG_WATCH_CHECK_CMD"
	EnvReloadCmd = "CONFIG_WATCH_RELOAD_CMD"
	EnvStateDir  = "STATE_DIRECTORY"
)

const stateFileName = "content.sha256"

// Config holds everything one watch instance needs. It is normally built
// by LoadConfig from the environment, but Run takes it directly so the
// core logic can be tested without an env or a real systemd state
// directory.
type Config struct {
	WatchPath string // file or directory to hash
	StateDir  string // where content.sha256 is kept
	CheckCmd  string // must exit 0 for the reload to proceed
	ReloadCmd string // run only if CheckCmd succeeds
}

// LoadConfig resolves a Config from flags and the environment: any
// non-empty field in flags is used as-is, and every field left blank falls
// back to the corresponding environment variable. Passing a zero Config
// resolves purely from the environment — the shape a systemd instance's
// EnvironmentFile supplies.
//
// It returns an error that names every setting still missing after that
// merge, mentioning both the flag and the environment variable that can
// supply it.
func LoadConfig(flags Config) (Config, error) {
	cfg := Config{
		WatchPath: firstNonEmpty(flags.WatchPath, os.Getenv(EnvWatchPath)),
		StateDir:  firstNonEmpty(flags.StateDir, os.Getenv(EnvStateDir)),
		CheckCmd:  firstNonEmpty(flags.CheckCmd, os.Getenv(EnvCheckCmd)),
		ReloadCmd: firstNonEmpty(flags.ReloadCmd, os.Getenv(EnvReloadCmd)),
	}

	var missing []string
	if cfg.WatchPath == "" {
		missing = append(missing, "--path (or "+EnvWatchPath+")")
	}
	if cfg.StateDir == "" {
		missing = append(missing, "--state-dir (or "+EnvStateDir+", set automatically by systemd via StateDirectory=)")
	}
	if cfg.CheckCmd == "" {
		missing = append(missing, "--check-cmd (or "+EnvCheckCmd+")")
	}
	if cfg.ReloadCmd == "" {
		missing = append(missing, "--reload-cmd (or "+EnvReloadCmd+")")
	}
	if len(missing) > 0 {
		return Config{}, fmt.Errorf("missing required settings: %v", missing)
	}

	return cfg, nil
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// Run performs one check-and-reload cycle for cfg.
func Run(cfg Config) error {
	current, err := hashPath(cfg.WatchPath)
	if err != nil {
		return fmt.Errorf("hashing %s: %w", cfg.WatchPath, err)
	}

	stateFile := filepath.Join(cfg.StateDir, stateFileName)

	stored, err := readState(stateFile)
	if err != nil {
		return fmt.Errorf("reading state: %w", err)
	}

	if current == stored {
		return nil
	}

	if stored == "" {
		// First run: adopt current state without reloading — whatever
		// consumes this path already started against it.
		return writeState(stateFile, current)
	}

	if err := runShell(cfg.CheckCmd); err != nil {
		return fmt.Errorf("check command failed, not reloading (hash %s -> %s): %w", stored, current, err)
	}

	if err := runShell(cfg.ReloadCmd); err != nil {
		return fmt.Errorf("reload command failed: %w", err)
	}

	return writeState(stateFile, current)
}

// hashPath returns a single hex-encoded sha256 digest over the contents of
// path. If path is a directory, every regular file beneath it is included,
// in sorted path order; if path is a single file, the digest covers that
// file alone.
//
// This is deliberately not filtered by extension: a directory-style config
// load (e.g. "haproxy -f <dir>") has no extension convention of its own,
// and a filtered hash risks silently missing a file the consumer actually
// loads.
//
// path itself is resolved through symlinks before walking: shared/network
// filesystems commonly publish an atomic update by re-pointing a top-level
// symlink (e.g. a "current" symlink swapped to a new release directory, or
// Kubernetes' ConfigMap "..data" convention) rather than editing content in
// place, and an unresolved symlink root isn't a directory or a regular file
// by fs.DirEntry's reckoning — WalkDir would silently visit nothing beneath
// it.
func hashPath(path string) (string, error) {
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", err
	}

	var paths []string
	err = filepath.WalkDir(resolved, func(p string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.Type().IsRegular() {
			paths = append(paths, p)
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	if len(paths) == 0 {
		return "", fmt.Errorf("no regular files found under %s", path)
	}
	sort.Strings(paths)

	combined := sha256.New()
	for _, p := range paths {
		digest, err := hashFile(p)
		if err != nil {
			return "", err
		}
		fmt.Fprintf(combined, "%s  %s\n", digest, p)
	}

	return hex.EncodeToString(combined.Sum(nil)), nil
}

func hashFile(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func readState(path string) (string, error) {
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return string(b), nil
}

func writeState(path, hash string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(hash), 0o644)
}

func runShell(cmd string) error {
	c := exec.Command("sh", "-c", cmd)
	c.Stdout = os.Stdout
	c.Stderr = os.Stderr
	return c.Run()
}
