# config-watch

Polls a file or directory for content changes, runs a configurable check command,
and — only if that succeeds — runs a configurable reload command.

Built for targets on shared/network filesystems (e.g. CephFS) where a change made
by another client doesn't produce a local inotify event, so polling a content hash
is the reliable option rather than an event-driven watch.

One binary, one systemd template unit set. Each watched target gets its own
instance, configured entirely through an `EnvironmentFile`.

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
location**:

```
sudo /usr/local/bin/config-watch install
```

`install` does **not** copy, move, or otherwise manage the binary. It writes
`config-watch@.service`, `config-watch@.timer`, and
`config-watch-failure@.service` to `/etc/systemd/system/`, points those units'
`ExecStart=` at wherever the binary you just ran it from actually is, drops an
example env file at `/etc/config-watch/example.env`, and runs
`systemctl daemon-reload`. If you later move the binary, re-run `install` to
re-point the units at the new location.

The unit and config directories are overridable:

```
sudo /usr/local/bin/config-watch install --unit-dir /etc/systemd/system \
                                          --config-dir /etc/config-watch
```

## Configure an instance

```
cp /etc/config-watch/example.env /etc/config-watch/<name>.env
```

Edit the three required variables — see `example.env` for the full description of
each:

- `CONFIG_WATCH_PATH` — file or directory to watch (a directory is hashed
  recursively over every regular file beneath it, not filtered by extension; a
  single file is hashed on its own)
- `CONFIG_WATCH_CHECK_CMD` — must exit 0 for the reload to proceed
- `CONFIG_WATCH_RELOAD_CMD` — run only if the check command succeeds

Example, watching an HAProxy config directory and validating/reloading it in a
Podman-managed HAProxy container:

```
CONFIG_WATCH_PATH=/etc/haproxy
CONFIG_WATCH_CHECK_CMD=podman exec systemd-haproxy haproxy -c -f /etc/haproxy
CONFIG_WATCH_RELOAD_CMD=systemctl reload haproxy.service
```

Then enable the **timer** (not the service — the service is the oneshot the timer
triggers):

```
sudo systemctl enable --now config-watch@<name>.timer
```

## Testing a check/reload cycle manually

Every setting above can also be passed as a flag to `config-watch run` instead
of (or in addition to — a flag wins over its environment variable) sourcing it
from the instance's `.env` file. This is useful for trying out a check/reload
pair before wiring up a systemd instance:

```
config-watch run --path /etc/haproxy \
                  --check-cmd 'podman exec systemd-haproxy haproxy -c -f /etc/haproxy' \
                  --reload-cmd 'systemctl reload haproxy.service' \
                  --state-dir /tmp/config-watch-test
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

`CONFIG_WATCH_PATH` may itself be a symlink (or contain one at the point you
watch it) — the path is resolved before hashing, so an atomic update done by
re-pointing a symlink (a swapped `current -> release-N` convention, a
Kubernetes-style ConfigMap mount, etc.) is detected the same as an in-place
edit.

## License

[MIT](LICENSE)
