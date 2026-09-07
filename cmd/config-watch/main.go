// Command config-watch polls a file or directory for content changes,
// validates the new state with a configurable check command, and — if
// that succeeds — runs a configurable reload command.
//
// It is designed to run as one instance per systemd template unit
// (config-watch@<name>.service), with each instance configured through its
// own TOML file — see "config-watch install" and internal/config.
package main

import (
	"fmt"
	"os"

	"github.com/demicloud/config-watch/internal/config"
	"github.com/demicloud/config-watch/internal/install"
	"github.com/demicloud/config-watch/internal/version"
	"github.com/demicloud/config-watch/internal/watch"
	flag "github.com/spf13/pflag"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(0)
	}

	switch os.Args[1] {
	case "run":
		runCmd(os.Args[2:])
	case "install":
		installCmd(os.Args[2:])
	case "uninstall":
		uninstallCmd(os.Args[2:])
	case "version", "--version", "-V":
		version.Print()
	case "help", "--help", "-h":
		if len(os.Args) > 2 {
			usageFor(os.Args[2])
		} else {
			usage()
		}
	default:
		fmt.Fprintf(os.Stderr, "unknown subcommand %q\n\n", os.Args[1])
		usage()
		os.Exit(1)
	}
}

func runCmd(args []string) {
	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	fs.Usage = func() { usageFor("run") }
	flagConfig := fs.StringP("config", "c", "", "path to the instance's TOML config file")
	stateDir := fs.String("state-dir", "", "directory to store the content hash in (env: "+watch.EnvStateDir+")")
	if err := fs.Parse(args); err != nil {
		if err == flag.ErrHelp {
			os.Exit(0)
		}
		os.Exit(1)
	}

	if *flagConfig == "" {
		fmt.Fprintf(os.Stderr, "run: --config is required\n\n")
		usageFor("run")
		os.Exit(1)
	}

	cfg, err := config.Load(*flagConfig)
	if err != nil {
		fmt.Fprintln(os.Stderr, "config-watch: config error:", err)
		os.Exit(1)
	}

	resolvedStateDir, err := watch.ResolveStateDir(*stateDir)
	if err != nil {
		fmt.Fprintln(os.Stderr, "config-watch:", err)
		os.Exit(1)
	}

	if err := watch.Run(watch.Config{
		WatchPath: cfg.Path,
		CheckCmd:  cfg.CheckCmd,
		ReloadCmd: cfg.ReloadCmd,
		StateDir:  resolvedStateDir,
	}); err != nil {
		fmt.Fprintln(os.Stderr, "config-watch:", err)
		os.Exit(1)
	}
}

func installCmd(args []string) {
	fs := flag.NewFlagSet("install", flag.ContinueOnError)
	fs.Usage = func() { usageFor("install") }
	unitDir := fs.String("unit-dir", install.DefaultUnitDir, "directory to install systemd unit files into")
	configDir := fs.String("config-dir", install.DefaultConfigDir, "directory for per-instance TOML config files")
	if err := fs.Parse(args); err != nil {
		if err == flag.ErrHelp {
			os.Exit(0)
		}
		os.Exit(1)
	}

	if fs.NArg() != 1 {
		fmt.Fprintf(os.Stderr, "install: instance name required\n\n")
		usageFor("install")
		os.Exit(1)
	}

	if err := install.InstallInstance(fs.Arg(0), install.Options{
		UnitDir:   *unitDir,
		ConfigDir: *configDir,
	}); err != nil {
		fmt.Fprintln(os.Stderr, "config-watch:", err)
		os.Exit(1)
	}
}

func uninstallCmd(args []string) {
	fs := flag.NewFlagSet("uninstall", flag.ContinueOnError)
	fs.Usage = func() { usageFor("uninstall") }
	unitDir := fs.String("unit-dir", install.DefaultUnitDir, "directory the systemd unit files were installed into")
	configDir := fs.String("config-dir", install.DefaultConfigDir, "directory the per-instance TOML config files live in")
	if err := fs.Parse(args); err != nil {
		if err == flag.ErrHelp {
			os.Exit(0)
		}
		os.Exit(1)
	}

	if fs.NArg() != 1 {
		fmt.Fprintf(os.Stderr, "uninstall: instance name required\n\n")
		usageFor("uninstall")
		os.Exit(1)
	}

	if err := install.UninstallInstance(fs.Arg(0), install.Options{
		UnitDir:   *unitDir,
		ConfigDir: *configDir,
	}); err != nil {
		fmt.Fprintln(os.Stderr, "config-watch:", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Print(`config-watch — poll a file or directory and reload a service on change

Usage:
  config-watch <subcommand> [flags]

Subcommands:
  run        Run one check-and-reload cycle (invoked by the systemd service)
  install    Install the systemd templates and enable a timer instance
  uninstall  Disable a timer instance and remove the template unit files
  version    Show version information (aliases: --version, -V)
  help       Show help for a subcommand

Run 'config-watch help <subcommand>' for subcommand usage.
`)
}

func usageFor(sub string) {
	switch sub {
	case "run":
		fmt.Print(`config-watch run

Hashes the watched file or directory named by --config's "path" field. If
the hash has changed since the last run, runs check_cmd; if that succeeds,
runs reload_cmd and records the new hash.

Passing --config with a path other than the installed
<config-dir>/<instance>.toml lets a watch be exercised manually, with
--state-dir pointed at a scratch directory, without a systemd instance.

Usage:
  config-watch run --config <path> [flags]

Flags:
  -c, --config string      Path to the instance's TOML config file (required)
      --state-dir string   Where the content hash is stored (env: STATE_DIRECTORY,
                            set automatically by systemd via StateDirectory=)
  -h, --help                Show this help
`)

	case "install":
		fmt.Print(`config-watch install

Writes the config-watch@.service, config-watch@.timer, and
config-watch-failure@.service systemd template units (only if their
rendered content changed), reloads systemd, and enables+starts the named
instance's timer. Creates <config-dir>/<instance>.toml with a sample
config if it doesn't already exist.

This does NOT copy or move the config-watch binary anywhere. Put the
binary wherever you want it to live first (/usr/local/bin, /usr/local/sbin,
...), then run "config-watch install <instance>" from that location — the
installed units' ExecStart= is pointed at wherever this binary is
currently running from.

Usage:
  config-watch install <instance> [flags]

Arguments:
  <instance>     Instance name — enables config-watch@<instance>.timer

Flags:
      --unit-dir     Directory to install systemd unit files into (default: ` + install.DefaultUnitDir + `)
      --config-dir   Directory for per-instance TOML config files (default: ` + install.DefaultConfigDir + `)
  -h, --help         Show this help

After installing, edit <config-dir>/<instance>.toml to configure the
watch, then the timer will pick it up on its next tick.
`)
	case "uninstall":
		fmt.Print(`config-watch uninstall

Disables and stops the named timer instance, then removes the shared
template unit files and reloads the systemd daemon.

The instance's config file (<config-dir>/<instance>.toml) and state
directory are not removed.

Usage:
  config-watch uninstall <instance> [flags]

Arguments:
  <instance>     Instance name — disables config-watch@<instance>.timer

Flags:
      --unit-dir     Directory the systemd unit files were installed into (default: ` + install.DefaultUnitDir + `)
      --config-dir   Directory the per-instance TOML config files live in (default: ` + install.DefaultConfigDir + `)
  -h, --help         Show this help
`)
	case "version":
		fmt.Print(`config-watch version

Prints version, commit, build date, and toolchain information.

Usage:
  config-watch version
  config-watch --version
  config-watch -V
`)
	default:
		fmt.Fprintf(os.Stderr, "unknown subcommand %q\n\n", sub)
		usage()
		os.Exit(1)
	}
}
