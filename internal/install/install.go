// Package install implements the "install" and "uninstall" subcommands: it
// drops the embedded systemd template units and a per-instance sample TOML
// config onto disk, wires the units to wherever the config-watch binary
// currently lives, and reloads/enables systemd — and, for uninstall,
// reverses that for one named instance.
//
// It deliberately does not copy, move, or otherwise manage the binary
// itself — the expected workflow is that the operator puts the binary
// wherever they want it first (/usr/local/bin, /usr/local/sbin, a distro
// package's own path, ...) and then runs "config-watch install <instance>"
// from that location. The installed units' ExecStart= is pointed at that
// resolved path.
package install

import (
	"bytes"
	_ "embed"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"text/template"
)

//go:embed units/config-watch@.service
var serviceTemplate string

//go:embed units/config-watch@.timer
var timerTemplate string

//go:embed units/config-watch-failure@.service
var failureServiceTemplate string

// Default install locations. Both are overridable — see cmd/config-watch's
// "install"/"uninstall" flags.
const (
	DefaultUnitDir   = "/etc/systemd/system"
	DefaultConfigDir = "/etc/config-watch"
)

// Options controls where InstallInstance/UninstallInstance operate. Every
// field defaults to the corresponding Default* constant.
type Options struct {
	UnitDir   string
	ConfigDir string
}

type tmplData struct {
	BinPath   string
	ConfigDir string
}

// InstallInstance resolves the currently running binary's path, writes the
// shared systemd template units (only if their rendered content changed)
// and a sample TOML config for instance (only if one doesn't already
// exist), reloads systemd, and enables+starts the instance's timer.
func InstallInstance(instance string, opts Options) error {
	binPath, err := resolveBinaryPath()
	if err != nil {
		return fmt.Errorf("resolving binary path: %w", err)
	}

	if err := checkDirs(opts); err != nil {
		return err
	}

	data := tmplData{BinPath: binPath, ConfigDir: opts.ConfigDir}

	units := map[string]string{
		filepath.Join(opts.UnitDir, "config-watch@.service"):         serviceTemplate,
		filepath.Join(opts.UnitDir, "config-watch@.timer"):           timerTemplate,
		filepath.Join(opts.UnitDir, "config-watch-failure@.service"): failureServiceTemplate,
	}
	for path, tmpl := range units {
		if err := writeTemplateIfChanged(path, tmpl, data); err != nil {
			return err
		}
	}

	cfgPath := filepath.Join(opts.ConfigDir, instance+".toml")
	if _, err := os.Stat(cfgPath); os.IsNotExist(err) {
		fmt.Println("install: writing sample config", cfgPath)
		if err := os.WriteFile(cfgPath, []byte(sampleConfig()), 0o644); err != nil {
			return fmt.Errorf("writing sample config: %w", err)
		}
	} else {
		fmt.Println("install: config", cfgPath, "already exists, skipping")
	}

	if err := exec.Command("systemctl", "daemon-reload").Run(); err != nil {
		fmt.Fprintln(os.Stderr, "config-watch: warning: systemctl daemon-reload failed:", err)
	}

	timer := fmt.Sprintf("config-watch@%s.timer", instance)
	fmt.Println("install: enabling and starting", timer)
	if out, err := exec.Command("systemctl", "enable", "--now", timer).CombinedOutput(); err != nil {
		return fmt.Errorf("enabling timer %s: %w\n%s", timer, err, strings.TrimSpace(string(out)))
	}

	fmt.Println("Installed, using the config-watch binary at", binPath)
	fmt.Println("Edit", cfgPath, "to configure this instance.")

	return nil
}

// UninstallInstance disables and stops the named timer instance, removes
// the shared template unit files, and reloads the systemd daemon. The
// instance's config file and state directory are intentionally left in
// place.
func UninstallInstance(instance string, opts Options) error {
	timer := fmt.Sprintf("config-watch@%s.timer", instance)

	fmt.Println("uninstall: disabling", timer)
	if out, err := exec.Command("systemctl", "disable", "--now", timer).CombinedOutput(); err != nil {
		// Non-fatal: the unit may already be stopped or never enabled.
		fmt.Println("uninstall: note:", strings.TrimSpace(string(out)))
	}

	units := []string{
		filepath.Join(opts.UnitDir, "config-watch@.service"),
		filepath.Join(opts.UnitDir, "config-watch@.timer"),
		filepath.Join(opts.UnitDir, "config-watch-failure@.service"),
	}
	for _, path := range units {
		if err := removeFileIfExists(path); err != nil {
			return err
		}
	}

	fmt.Println("uninstall: reloading systemd daemon")
	if err := exec.Command("systemctl", "daemon-reload").Run(); err != nil {
		fmt.Fprintln(os.Stderr, "config-watch: warning: systemctl daemon-reload failed:", err)
	}

	fmt.Printf("uninstall: done (config %s/%s.toml was not removed)\n", opts.ConfigDir, instance)
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

// writeTemplateIfChanged renders tmpl with data and writes it to path, but
// only if the rendered content differs from what's already there — so
// re-running install after no relevant change doesn't perturb the unit's
// mtime or spuriously log a rewrite.
func writeTemplateIfChanged(path, tmpl string, data tmplData) error {
	t := template.Must(template.New(filepath.Base(path)).Parse(tmpl))

	var buf bytes.Buffer
	if err := t.Execute(&buf, data); err != nil {
		return fmt.Errorf("rendering %s: %w", path, err)
	}
	rendered := buf.Bytes()

	existing, err := os.ReadFile(path)
	if err == nil && bytes.Equal(bytes.TrimSpace(existing), bytes.TrimSpace(rendered)) {
		fmt.Println("install:", path, "unchanged, skipping")
		return nil
	}

	fmt.Println("install: writing", path)
	return os.WriteFile(path, rendered, 0o644)
}

func removeFileIfExists(path string) error {
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("removing %s: %w", path, err)
	}
	fmt.Println("uninstall: removed", path)
	return nil
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

func sampleConfig() string {
	return `# File or directory to watch for content changes. A directory is hashed
# recursively over every regular file beneath it (not filtered by
# extension); a single file is hashed on its own.
path = "/path/to/config"

# Run before reloading. Must exit non-zero to abort — a failing check
# leaves whatever's currently running untouched and logs why.
#
# Example, validating an HAProxy config directory served from a Podman
# container named systemd-haproxy:
#   check_cmd = "podman exec systemd-haproxy haproxy -c -f /etc/haproxy"
check_cmd = "/path/to/check-command --check"

# Run only if check_cmd succeeds.
#   reload_cmd = "systemctl reload some.service"
reload_cmd = "systemctl reload some.service"
`
}
