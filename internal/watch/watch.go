// Package watch implements the "run" subcommand: hash a watched file or
// directory, and — if the hash changed since the last run — validate the
// new state with a check command and, only on success, run a reload
// command.
//
// It is normally invoked once per systemd template unit instance
// (config-watch@<name>.service), with WatchPath/CheckCmd/ReloadCmd loaded
// from that instance's TOML config file (see internal/config) and StateDir
// resolved via ResolveStateDir. Passing a config path other than the
// installed one lets a watch be exercised manually without a systemd
// instance.
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

// EnvStateDir is populated automatically by systemd when the unit sets
// StateDirectory=; it is not something an operator sets by hand in the
// instance's TOML config.
const EnvStateDir = "STATE_DIRECTORY"

const stateFileName = "content.sha256"

// Config holds everything one watch instance needs. It is normally built
// from a TOML config file (internal/config) plus a resolved state
// directory, but Run takes it directly so the core logic can be tested
// without a real systemd state directory.
type Config struct {
	WatchPath string // file or directory to hash
	StateDir  string // where content.sha256 is kept
	CheckCmd  string // must exit 0 for the reload to proceed
	ReloadCmd string // run only if CheckCmd succeeds
}

// ResolveStateDir resolves the state directory to use: flag wins if
// non-empty, otherwise it falls back to STATE_DIRECTORY (set automatically
// by systemd via a unit's StateDirectory=). Returns an error naming both
// ways to supply it if neither is set.
func ResolveStateDir(flag string) (string, error) {
	if flag != "" {
		return flag, nil
	}
	if env := os.Getenv(EnvStateDir); env != "" {
		return env, nil
	}
	return "", fmt.Errorf("missing required setting: --state-dir (or systemd's StateDirectory=, via %s)", EnvStateDir)
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
