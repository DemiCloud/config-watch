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
  `config-watch@<name>.timer`), configured entirely through an `EnvironmentFile`.
- This is a polling design, not inotify-based — built for targets on shared/network
  filesystems (e.g. CephFS) where a change made by another client doesn't produce a
  local inotify event, so hashing content on a timer is the reliable option.

---

## Repository Layout

```text
cmd/
  config-watch/
    main.go         ← CLI dispatch (run/install/version), pflag parsing for "install"
internal/
  watch/
    watch.go        ← "run" subcommand: hash, compare, check, reload
    watch_test.go
  install/
    install.go      ← "install" subcommand: embeds + writes units, points them at
                       the binary's own resolved path — never touches the binary
    units/
      config-watch@.service          ← {{BIN_PATH}} / {{CONFIG_DIR}} placeholders
      config-watch@.timer
      config-watch-failure@.service  ← OnFailure= target, logs an escalation line
      example.env                    ← {{CONFIG_DIR}} placeholder in its header comment
```

Units are embedded in the binary via `go:embed` (`internal/install/install.go`) — there
are no unit files shipped or read from disk at install time. The `{{BIN_PATH}}` and
`{{CONFIG_DIR}}` placeholders in the embedded templates are substituted with the actual
values (resolved binary path, and the `--config-dir` value or its default) by `render()`
in `internal/install/install.go` before the files are written out. If you add a new
embedded template that needs to reference an install path, add the placeholder there and
extend `render()` — do not hardcode `/etc/config-watch` or an assumed binary path into a
template file.

**`install` does not manage the binary.** There is deliberately no `--bin-dir` flag and
no code path that copies, moves, or writes the config-watch binary anywhere. The expected
workflow is: the operator puts the binary wherever they want it first, then runs
`config-watch install` *from that location* — `resolveBinaryPath()` in `install.go` uses
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
- Version metadata is injected at link time via `-ldflags`. The variables
  (`version`, `commit`, `date`, `builtBy`) live in `cmd/config-watch/main.go`;
  `make build` sets only `version` (dev builds), `make release` sets all four.
- CLI flag parsing uses `github.com/spf13/pflag`, scoped per-subcommand with its own
  `flag.NewFlagSet` (see `runCmd`/`installCmd` in `main.go`) — there is no top-level
  flag set, since `run`/`install`/`version` take entirely different arguments.
  `usage()`/`usageFor(sub)` in `main.go` hold the actual help text; each subcommand's
  `FlagSet.Usage` is wired to `usageFor` so `-h`/`--help` (which pflag intercepts
  automatically, even for flags it doesn't define) prints the right subcommand help.
  When adding a flag to a subcommand, update both the `FlagSet` and its `usageFor`
  case — they aren't generated from each other.
- `internal/watch` and `internal/install` do not import each other. `watch` knows
  nothing about install paths or systemd units; `install` knows nothing about hashing
  or the check/reload cycle.
- `watch.Config` is passed by value into `watch.Run` so the check/reload/hash/state
  logic is testable without touching the environment or a real systemd
  `StateDirectory`. `watch.LoadConfig` is the only place that reads `os.Getenv`.
- `watch.LoadConfig(flags Config)` merges CLI flags over environment variables
  (flag wins when both are set, per-field) — this is what lets `config-watch run`
  be driven by a systemd `EnvironmentFile`, plain flags, or a mix of both. A new
  setting needs a field on `Config`, an `Env*` constant, a merge line in
  `LoadConfig`, a "missing" message entry, a flag in `runCmd` (`main.go`), and a
  line in the `run` case of `usageFor` — none of these are generated from the
  others, so add all of them together.

### Testing

- `internal/watch`'s tests exercise `Run` end-to-end against real temp files and real
  `sh -c` commands (`true`/`false`/`touch <marker>`) rather than mocking the shell —
  the whole point of this tool is shelling out to check/reload commands, so a test that
  mocks that away wouldn't catch a regression there.
- Any new install-path logic in `internal/install` should stay testable by taking an
  `Options` value rather than reading flags or the environment directly, mirroring how
  `watch.Run` takes a `Config`.

### Security

This binary is invoked by a systemd `oneshot` service (typically to run as root or
under whatever user runs the target service) and both `CONFIG_WATCH_CHECK_CMD` and
`CONFIG_WATCH_RELOAD_CMD` are shelled out via `sh -c`. Apply extra scrutiny to any
change that:

- Adds new places where a command string is passed to `exec.Command`/`sh -c`.
- Changes what populates `EnvironmentFile` values written by `install` — these become
  arbitrary shell commands run on every timer tick.
- Touches `internal/install`'s file/directory writes (`os.WriteFile`, `os.MkdirAll`) —
  these run with whatever privilege level `config-watch install` is invoked at
  (normally root, via `sudo`).

`CONFIG_WATCH_CHECK_CMD` and `CONFIG_WATCH_RELOAD_CMD` are operator-supplied
configuration, not untrusted external input — there is no sanitization step for them by
design, the same way a systemd `ExecStart=` line isn't sanitized.
