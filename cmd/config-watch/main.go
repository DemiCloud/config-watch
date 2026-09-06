// Command config-watch polls a file or directory for content changes,
// validates the new state with a configurable check command, and — if
// that succeeds — runs a configurable reload command.
//
// It is designed to run as one instance per systemd template unit
// (config-watch@<name>.service), with each instance configured entirely
// through an EnvironmentFile — see "config-watch install" and
// internal/install/units/example.env.
package main

import (
	"fmt"
	"os"
	"runtime"

	"github.com/demicloud/config-watch/internal/install"
	"github.com/demicloud/config-watch/internal/watch"
	flag "github.com/spf13/pflag"
)

// Build-time metadata, set via:
//
//	go build -ldflags "-X main.version=vX.Y.Z -X main.commit=... -X main.date=... -X main.builtBy=..."
var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
	builtBy = "unknown"
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
	case "version", "--version", "-V":
		printVersion()
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
	path := fs.String("path", "", "file or directory to watch (env: "+watch.EnvWatchPath+")")
	checkCmd := fs.String("check-cmd", "", "command that must exit 0 before reloading (env: "+watch.EnvCheckCmd+")")
	reloadCmd := fs.String("reload-cmd", "", "command to run after a successful check (env: "+watch.EnvReloadCmd+")")
	stateDir := fs.String("state-dir", "", "directory to store the content hash in (env: "+watch.EnvStateDir+")")
	if err := fs.Parse(args); err != nil {
		if err == flag.ErrHelp {
			os.Exit(0)
		}
		os.Exit(1)
	}

	cfg, err := watch.LoadConfig(watch.Config{
		WatchPath: *path,
		CheckCmd:  *checkCmd,
		ReloadCmd: *reloadCmd,
		StateDir:  *stateDir,
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, "config-watch:", err)
		os.Exit(1)
	}

	if err := watch.Run(cfg); err != nil {
		fmt.Fprintln(os.Stderr, "config-watch:", err)
		os.Exit(1)
	}
}

func installCmd(args []string) {
	fs := flag.NewFlagSet("install", flag.ContinueOnError)
	fs.Usage = func() { usageFor("install") }
	unitDir := fs.String("unit-dir", install.DefaultUnitDir, "directory to install systemd unit files into")
	configDir := fs.String("config-dir", install.DefaultConfigDir, "directory for per-instance .env files")
	if err := fs.Parse(args); err != nil {
		if err == flag.ErrHelp {
			os.Exit(0)
		}
		os.Exit(1)
	}

	if err := install.Run(install.Options{
		UnitDir:   *unitDir,
		ConfigDir: *configDir,
	}); err != nil {
		fmt.Fprintln(os.Stderr, "config-watch:", err)
		os.Exit(1)
	}
}

func printVersion() {
	fmt.Printf("config-watch %s\n", version)
	fmt.Printf("commit:    %s\n", commit)
	fmt.Printf("built:     %s\n", date)
	fmt.Printf("builtBy:   %s\n", builtBy)
	fmt.Printf("go:        %s\n", runtime.Version())
	fmt.Printf("os/arch:   %s/%s\n", runtime.GOOS, runtime.GOARCH)
}

func usage() {
	fmt.Print(`config-watch — poll a file or directory and reload a service on change

Usage:
  config-watch <subcommand> [flags]

Subcommands:
  run        Run one check-and-reload cycle (invoked by the systemd service)
  install    Install the systemd templates and an example EnvironmentFile
  version    Show version information
  help       Show help for a subcommand

Run 'config-watch help <subcommand>' for subcommand usage.
`)
}

func usageFor(sub string) {
	switch sub {
	case "run":
		fmt.Print(`config-watch run

Hashes the watched file or directory. If the hash has changed since the
last run, runs the check command; if that succeeds, runs the reload
command and records the new hash.

Every setting can be given as a flag or an environment variable — this is
what config-watch@<name>.service's EnvironmentFile normally supplies, but
passing flags instead lets a watch be exercised manually. A flag wins over
its environment variable when both are set.

Usage:
  config-watch run [flags]

Flags:
      --path string         File or directory to watch (env: CONFIG_WATCH_PATH)
      --check-cmd string    Must exit 0 for the reload to proceed (env: CONFIG_WATCH_CHECK_CMD)
      --reload-cmd string   Run only if --check-cmd succeeds (env: CONFIG_WATCH_RELOAD_CMD)
      --state-dir string    Where the content hash is stored (env: STATE_DIRECTORY,
                             set automatically by systemd via StateDirectory=)
  -h, --help                Show this help
`)
	case "install":
		fmt.Print(`config-watch install

Writes the config-watch@.service, config-watch@.timer, and
config-watch-failure@.service systemd template units, drops an example
EnvironmentFile, and runs systemctl daemon-reload.

This does NOT copy or move the config-watch binary anywhere. Put the
binary wherever you want it to live first (/usr/local/bin, /usr/local/sbin,
...), then run "config-watch install" from that location — the installed
units' ExecStart= is pointed at wherever this binary is currently running
from.

Usage:
  config-watch install [flags]

Flags:
      --unit-dir     Directory to install systemd unit files into (default: ` + install.DefaultUnitDir + `)
      --config-dir   Directory for per-instance .env files (default: ` + install.DefaultConfigDir + `)
  -h, --help         Show this help

After installing, for each new instance <name>:
  1. cp <config-dir>/example.env <config-dir>/<name>.env
  2. edit that file
  3. systemctl enable --now config-watch@<name>.timer
`)
	case "version":
		fmt.Print(`config-watch version

Prints version, commit, build date, and toolchain information.

Usage:
  config-watch version
`)
	default:
		fmt.Fprintf(os.Stderr, "unknown subcommand %q\n\n", sub)
		usage()
		os.Exit(1)
	}
}
