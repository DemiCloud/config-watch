// Package install implements the "install" subcommand: it drops the
// embedded systemd template units and an example EnvironmentFile onto
// disk, wires the units to wherever the config-watch binary currently
// lives, and reloads systemd.
//
// It deliberately does not copy, move, or otherwise manage the binary
// itself — the expected workflow is that the operator puts the binary
// wherever they want it first (/usr/local/bin, /usr/local/sbin, a distro
// package's own path, ...) and then runs "config-watch install" from
// that location. The installed units' ExecStart= is pointed at that
// resolved path.
package install

import (
	"bytes"
	_ "embed"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

//go:embed units/config-watch@.service
var unitService []byte

//go:embed units/config-watch@.timer
var unitTimer []byte

//go:embed units/config-watch-failure@.service
var unitFailureService []byte

//go:embed units/example.env
var exampleEnv []byte

// Default install locations. Both are overridable — see cmd/config-watch's
// "install" flags.
const (
	DefaultUnitDir   = "/etc/systemd/system"
	DefaultConfigDir = "/etc/config-watch"
)

// Options controls where Run installs things. Every field defaults to the
// corresponding Default* constant.
type Options struct {
	UnitDir   string
	ConfigDir string
}

// Run installs the systemd template units and an example EnvironmentFile
// according to opts, pointing the units' ExecStart= at the currently
// running binary's resolved path.
func Run(opts Options) error {
	binPath, err := resolveBinaryPath()
	if err != nil {
		return fmt.Errorf("resolving binary path: %w", err)
	}

	if err := checkDirs(opts); err != nil {
		return err
	}

	vars := templateVars{BinPath: binPath, ConfigDir: opts.ConfigDir}

	units := map[string][]byte{
		"config-watch@.service":         unitService,
		"config-watch@.timer":           unitTimer,
		"config-watch-failure@.service": unitFailureService,
	}
	for name, contents := range units {
		if err := os.WriteFile(filepath.Join(opts.UnitDir, name), render(contents, vars), 0o644); err != nil {
			return fmt.Errorf("writing %s: %w", name, err)
		}
	}

	if err := os.WriteFile(filepath.Join(opts.ConfigDir, "example.env"), render(exampleEnv, vars), 0o644); err != nil {
		return fmt.Errorf("writing example.env: %w", err)
	}

	if err := exec.Command("systemctl", "daemon-reload").Run(); err != nil {
		fmt.Fprintln(os.Stderr, "config-watch: warning: systemctl daemon-reload failed:", err)
	}

	fmt.Println("Installed, using the config-watch binary at", binPath)
	fmt.Println("For each new instance <name>:")
	fmt.Println("  1. cp", filepath.Join(opts.ConfigDir, "example.env"), filepath.Join(opts.ConfigDir, "<name>.env"))
	fmt.Println("  2. edit that file")
	fmt.Println("  3. systemctl enable --now config-watch@<name>.timer")
	fmt.Println("     (the timer, not the .service — the service is the oneshot the timer triggers)")

	return nil
}

// checkDirs creates opts.ConfigDir if missing and confirms opts.UnitDir
// already exists — it's a standard system path that should exist on any
// systemd host, so a missing one likely means a typo'd override rather
// than something to silently create.
func checkDirs(opts Options) error {
	info, err := os.Stat(opts.UnitDir)
	if err != nil {
		return fmt.Errorf("checking %s: %w", opts.UnitDir, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("%s is not a directory", opts.UnitDir)
	}

	if err := os.MkdirAll(opts.ConfigDir, 0o755); err != nil {
		return fmt.Errorf("creating %s: %w", opts.ConfigDir, err)
	}

	return nil
}

type templateVars struct {
	BinPath   string
	ConfigDir string
}

// render substitutes the {{BIN_PATH}} / {{CONFIG_DIR}} placeholders in an
// embedded unit or env template with vars' actual values.
func render(template []byte, vars templateVars) []byte {
	out := bytes.ReplaceAll(template, []byte("{{BIN_PATH}}"), []byte(vars.BinPath))
	out = bytes.ReplaceAll(out, []byte("{{CONFIG_DIR}}"), []byte(vars.ConfigDir))
	return out
}

// resolveBinaryPath returns the absolute, symlink-resolved path to the
// currently running config-watch binary — wherever the operator put it.
func resolveBinaryPath() (string, error) {
	self, err := os.Executable()
	if err != nil {
		return "", err
	}
	return filepath.EvalSymlinks(self)
}
