# config-watch

Polls a file or directory for content changes, runs a configurable check command,
and — only if that succeeds — runs a configurable reload command.

Built for targets on shared/network filesystems (e.g. CephFS) where a change made
by another client doesn't produce a local inotify event, so polling a content hash
is the reliable option rather than an event-driven watch.

One binary, one systemd template unit set. Each watched target gets its own
instance, configured entirely through its own TOML file.

## Build

```
make build
```

See [AGENTS.md](AGENTS.md) for the full build/test/release targets and repo layout.

## Install

Put the `config-watch` binary wherever you want it to live — `/usr/local/bin`,
`/usr/local/sbin`, wherever `make install` puts it (`/usr/local/bin` by default,
override with `make install BINDIR=/usr/local/sbin`), a distro package's own
path, doesn't matter — then run its own `install` subcommand **from that
location**, naming the instance you're setting up:

```
sudo /usr/local/bin/config-watch install haproxy
```

`install` does **not** copy, move, or otherwise manage the binary. It writes
(only if changed) `config-watch@.service`, `config-watch@.timer`, and
`config-watch-failure@.service` to `/etc/systemd/system/`, points
`config-watch@.service`'s `ExecStart=` at wherever the binary you just ran it
from actually is, creates `/etc/config-watch/haproxy.toml` with a sample
config if it doesn't already exist, runs `systemctl daemon-reload`, and
enables+starts `config-watch@haproxy.timer`. If you later move the binary,
re-run `install <instance>` to re-point the units at the new location.

The unit and config directories are overridable (pass the same override to
`uninstall` later):

```
sudo /usr/local/bin/config-watch install haproxy --unit-dir /etc/systemd/system \
                                                  --config-dir /etc/config-watch
```

## Configure an instance

Edit `/etc/config-watch/<name>.toml` — three fields, all required:

```toml
path = "/etc/haproxy"
check_cmd = "podman exec systemd-haproxy haproxy -c -f /etc/haproxy"
reload_cmd = "systemctl reload haproxy.service"
```

- `path` — file or directory to watch (a directory is hashed recursively over
  every regular file beneath it, not filtered by extension; a single file is
  hashed on its own)
- `check_cmd` — must exit 0 for the reload to proceed
- `reload_cmd` — run only if `check_cmd` succeeds

Unknown fields are rejected. The timer picks up config changes on its next
tick — no restart needed.

## Uninstall

```
sudo config-watch uninstall haproxy
```

Disables and stops `config-watch@haproxy.timer`, removes the shared unit
files, and reloads systemd. The instance's config file and state directory
are left in place, so re-running `install haproxy` picks the existing config
back up.

## Testing a check/reload cycle manually

`run` takes a config file path directly, so trying out a check/reload pair
doesn't require a systemd instance — just point `--config` at any TOML file
and `--state-dir` at a scratch directory:

```
cat > /tmp/test.toml <<EOF
path = "/etc/haproxy"
check_cmd = "podman exec systemd-haproxy haproxy -c -f /etc/haproxy"
reload_cmd = "systemctl reload haproxy.service"
EOF

config-watch run --config /tmp/test.toml --state-dir /tmp/config-watch-test
```

See `config-watch help run` for the full flag list.

## Behavior

- First run for a fresh instance: adopts the current hash without reloading
  (whatever the check/reload commands act on has already started against the
  current state).
- No change since last run: no-op.
- Changed, check succeeds: runs the reload command, records the new hash.
- Changed, check fails: reload is skipped, error is logged, exit code is
  non-zero (triggers `config-watch-failure@<name>.service`, which logs a tagged
  escalation line).

State is kept in `$STATE_DIRECTORY` — a single `content.sha256` file holding
the last-seen hash. This is set automatically by systemd via
`StateDirectory=config-watch/%i` in the unit, which resolves to
`/var/lib/config-watch/<name>/` and creates it (with correct ownership) on
first start. `install` does not create anything under `/var/lib` itself —
that directory's whole lifecycle (creation, permissions, and removal when the
instance is disabled) is systemd's job via `StateDirectory=`. Running
`config-watch run` manually outside systemd needs `--state-dir`/
`STATE_DIRECTORY` pointed at a directory you manage yourself.

`path` may itself be a symlink (or contain one at the point you watch it) —
the path is resolved before hashing, so an atomic update done by re-pointing
a symlink (a swapped `current -> release-N` convention, a Kubernetes-style
ConfigMap mount, etc.) is detected the same as an in-place edit.

## License

[MIT](LICENSE)
