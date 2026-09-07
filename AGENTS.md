# config-watch — Agent / Contributor Instructions

## Collaboration Philosophy

Development is a collaborative process. Agents are expected to exercise independent
technical judgment, not passive agreement. When a different solution better aligns with
industry standards or formal specifications (e.g., systemd unit semantics, POSIX shell),
agents should raise concerns and advocate for the more appropriate approach.

## Project Overview

`config-watch` polls a file or directory for content changes, validates the new state
with a configurable check command, and — only if that succeeds — runs a configurable
reload command.

- Written in **Go 1.25**. Always **`CGO_ENABLED=0`**. **Linux only**.
- Produces a **single static binary** from `cmd/config-watch/`.
- Designed to run as a systemd **oneshot** service triggered by a **timer**, one
  instance per watched target (`config-watch@<name>.service` /
  `config-watch@<name>.timer`), configured through its own TOML file at
  `<config-dir>/<name>.toml`, passed to `ExecStart=` as `run --config ...%i.toml`
  — the instance name flows in from systemd's `%i` as a CLI argument, not an
  `EnvironmentFile`.
- This is a polling design, not inotify-based — built for targets on shared/network
  filesystems (e.g. CephFS) where a change made by another client doesn't produce a
  local inotify event, so hashing content on a timer is the reliable option.
- Follows the same install/config/version pattern as `anycast-sentinel` (see that
  repo's AGENTS.md) — that pattern is the shared standard across demicloud's Go
  CLIs; if you're adding a fourth tool of this shape, start from there.

---

## Repository Layout

```text
cmd/
  config-watch/
    main.go         ← CLI dispatch (run/install/uninstall/version), pflag parsing
internal/
  config/
    config.go       ← Config struct (path/check_cmd/reload_cmd), toml tags
    load.go         ← Load(path): decode + DisallowUnknownFields + required-field checks
    load_test.go
  version/
    version.go      ← build-time metadata vars + Print(), set via -ldflags
  watch/
    watch.go        ← "run" subcommand: hash, compare, check, reload
    watch_test.go
  install/
    install.go      ← "install"/"uninstall" subcommands: renders + writes units
                       (only if changed), points ExecStart= at the binary's own
                       resolved path — never touches the binary itself
    units/
      config-watch@.service          ← {{.BinPath}} / {{.ConfigDir}} template fields
      config-watch@.timer
      config-watch-failure@.service  ← OnFailure= target, logs an escalation line
```

Units are embedded in the binary via `go:embed` (`internal/install/install.go`) — there
are no unit files shipped or read from disk at install time. They are rendered with
`text/template` (not raw string substitution) and written via `writeTemplateIfChanged`,
which skips the write (and the `install`-time log line) when the rendered content
matches what's already on disk — re-running `install <instance>` after an unrelated
config edit doesn't perturb unit files or force an unnecessary daemon-reload. If you add
a new embedded template that needs to reference an install path, add the field to
`tmplData` and reference it as `{{.FieldName}}` — do not hardcode `/etc/config-watch` or
an assumed binary path into a template file.

There is no per-instance example file anymore (no more `example.env`) — `install`
writes `<config-dir>/<instance>.toml` directly from `sampleConfig()` in
`internal/install/install.go`, and only when that file doesn't already exist.

**`install` does not manage the binary.** There is deliberately no `--bin-dir` flag and
no code path that copies, moves, or writes the config-watch binary anywhere. The expected
workflow is: the operator puts the binary wherever they want it first, then runs
`config-watch install <instance>` *from that location* — `resolveBinaryPath()` in `install.go` uses
`os.Executable()` + `filepath.EvalSymlinks` to find where it's actually running from, and
that resolved path is what gets baked into `ExecStart=`. Do not reintroduce a
binary-copying step; if the operator moves the binary, the fix is "re-run install", not
"config-watch tracks/updates its own location."

**`install` does not create anything under `/var/lib`.** Per-instance state
(`/var/lib/config-watch/<name>/content.sha256`) is owned entirely by systemd via each
unit's `StateDirectory=config-watch/%i` — systemd creates it (with correct ownership) on
first start and removes it when the instance is disabled. Don't add `os.MkdirAll` calls
for state paths in `internal/install` or `internal/watch`; the only place a state
directory gets created outside systemd's own handling is `watch.writeState`'s
`os.MkdirAll(filepath.Dir(path), ...)`, which exists solely so `config-watch run
--state-dir ...` works when invoked manually, outside systemd, for testing.

---

## Build Commands

```bash
# Development build → build/config-watch
make build

# Run tests / static analysis
make test
make vet

# Copy the built binary to $(BINDIR) (default: /usr/local/bin)
make install

# Stripped, trimpath, multi-arch release tarballs → dist/
make release

# Remove build/ and dist/
make clean
```

**Never build into the repo root.** Always target `build/` explicitly or use `make`.
Compiled binaries belong only in `build/` or `dist/`, both of which are gitignored.

```bash
# Correct
make build
go build -o build/config-watch ./cmd/config-watch/

# Wrong — drops binary in the working directory
go build ./cmd/config-watch/
```

---

## Git Workflow

**After every meaningful change, make a commit.** This applies to:

- Bug fixes (including single-line corrections)
- New features or behaviour changes
- Refactors or code reorganisation
- Documentation / AGENTS.md updates
- Build / Makefile changes

**Before committing, run the test suite and confirm it passes:**

```bash
make test && make vet
```

Do not commit if any test fails. If a change intentionally removes functionality,
update or delete the affected tests in the same commit. If you add new logic, add
corresponding tests before committing.

Do **not** push automatically; only commit locally unless the user explicitly asks
to push.

Use conventional-commits style:

- `feat:` — new user-visible feature
- `fix:` — bug fix
- `refactor:` — internal restructure, no behaviour change
- `docs:` — documentation only
- `build:` — Makefile, CI, or toolchain changes
- `chore:` — everything else (dependency updates, file renames, etc.)

---

## Key Conventions

### Go

- **Always `CGO_ENABLED=0`.** Linux-only tool, no C libraries needed.
- Version metadata is injected at link time via `-ldflags` targeting
  `internal/version`'s package-level vars (`Version`, `Commit`, `BuildDate`,
  `BuiltBy` in `internal/version/version.go`) — see `LDFLAGS`/`RELEASE_LDFLAGS`
  in the `Makefile`. `make build` sets only `Version` (dev builds), `make release`
  sets all four. `version.Print()` is the only thing that reads them.
- CLI flag parsing uses `github.com/spf13/pflag`, scoped per-subcommand with its own
  `flag.NewFlagSet` (see `runCmd`/`installCmd`/`uninstallCmd` in `main.go`) — there is
  no top-level flag set, since `run`/`install`/`uninstall`/`version` take entirely
  different arguments. `usage()`/`usageFor(sub)` in `main.go` hold the actual help
  text; each subcommand's `FlagSet.Usage` is wired to `usageFor` so `-h`/`--help`
  (which pflag intercepts automatically, even for flags it doesn't define) prints the
  right subcommand help. When adding a flag to a subcommand, update both the
  `FlagSet` and its `usageFor` case — they aren't generated from each other.
- `internal/watch`, `internal/config`, and `internal/install` do not import each
  other. `watch` knows nothing about install paths, systemd units, or TOML; `config`
  only knows how to decode and validate one instance's file; `install` knows nothing
  about hashing or the check/reload cycle. `main.go` is the only place that wires
  `config.Load`'s output into a `watch.Config`.
- `watch.Config` is passed by value into `watch.Run` so the check/reload/hash/state
  logic is testable without touching the environment or a real systemd
  `StateDirectory`. `watch.ResolveStateDir` is the only place in `internal/watch`
  that reads `os.Getenv` — `WatchPath`/`CheckCmd`/`ReloadCmd` come from
  `config.Load`, not the environment or flags, so there's no merge logic for them.
- `config.Load(path)` uses `go-toml/v2`'s `DisallowUnknownFields()` — a typo'd key in
  an instance's TOML file is a load error, not a silently-ignored field. A new
  setting needs a field on `config.Config` (with a `toml` tag), a required-field
  check in `Load`, a line in `sampleConfig()` (`internal/install/install.go`), and a
  place to read it off `*config.Config` in `runCmd` (`main.go`) — none of these are
  generated from the others, so add all of them together.

### Testing

- `internal/watch`'s tests exercise `Run` end-to-end against real temp files and real
  `sh -c` commands (`true`/`false`/`touch <marker>`) rather than mocking the shell —
  the whole point of this tool is shelling out to check/reload commands, so a test that
  mocks that away wouldn't catch a regression there.
- `internal/config`'s tests are table-driven over raw TOML strings written to a temp
  file (see `writeTemp` in `load_test.go`), covering the valid case, each missing
  field, and unknown-field rejection — mirrors `anycast-sentinel/internal/config`'s
  test shape.
- Any new install-path logic in `internal/install` should stay testable by taking an
  `Options` value rather than reading flags or the environment directly, mirroring how
  `watch.Run` takes a `Config`.

### Security

This binary is invoked by a systemd `oneshot` service (typically to run as root or
under whatever user runs the target service) and both `check_cmd` and `reload_cmd`
(from the instance's TOML config) are shelled out via `sh -c`. Apply extra scrutiny to
any change that:

- Adds new places where a command string is passed to `exec.Command`/`sh -c`.
- Changes what populates the `check_cmd`/`reload_cmd` values written by
  `install`'s `sampleConfig()`, or how `config.Load` validates them — these become
  arbitrary shell commands run on every timer tick.
- Touches `internal/install`'s file/directory writes (`os.WriteFile`, `os.MkdirAll`) —
  these run with whatever privilege level `config-watch install`/`uninstall` is
  invoked at (normally root, via `sudo`).

`check_cmd` and `reload_cmd` are operator-supplied configuration, not untrusted
external input — there is no sanitization step for them by design, the same way a
systemd `ExecStart=` line isn't sanitized.
