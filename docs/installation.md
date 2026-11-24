# Installation

## Prerequisites

- **Go 1.24+** to build the binary (nothing else — the build is
  `CGO_ENABLED=0`, so the result is a single static binary with no runtime
  dependencies).
- **systemd** to supervise the daemon. `packaging/lqg.service` is a systemd
  unit; there's currently no init-script equivalent for non-systemd
  distributions (Alpine/OpenRC, Void/runit, etc.) — this covers the
  mainstream server distros (Debian/Ubuntu, RHEL/Fedora/Rocky/Alma, Arch,
  openSUSE, Amazon Linux, ...).
- **Root** for the install step (it creates a system group and installs a
  systemd unit) and to run the daemon itself (it needs root to `setuid` into
  each app's configured user when execing `artisan`).

## 1. Build

```bash
CGO_ENABLED=0 go build -o lqg ./cmd/lqg
```

This produces a single static binary. Copy it anywhere, or straight into the
next step.

## 2. Install

```bash
sudo ./packaging/install.sh ./lqg
```

`install.sh` (see `packaging/install.sh`) does exactly this, and nothing
more:

- Creates the `lqg` system group, if it doesn't already exist.
- Adds the user who ran `sudo ./install.sh` (`$SUDO_USER`) to the `lqg`
  group, so they can use `lqg status`/`lqg watch` without `sudo` — after
  logging out and back in (or `newgrp lqg`); the script reminds you.
- Installs the binary to `/usr/local/bin/lqg`.
- Installs `packaging/lqg.service` to `/etc/systemd/system/lqg.service`.
- Installs `packaging/config.sample.yaml` to `/etc/lqg/config.yaml` — **only
  if a config doesn't already exist there.** It never overwrites a real
  deployment's config, so re-running the install script to update the
  binary is always safe.
- Runs `systemctl daemon-reload`.

## 3. Configure

Edit `/etc/lqg/config.yaml` for your apps — see
[`configuration.md`](configuration.md) for the full field reference. Then
validate it before you ever start the service:

```bash
sudo lqg validate /etc/lqg/config.yaml
```

This parses the file and checks every rule (paths exist and contain
`artisan`, users resolve to a uid/gid, `php_binary` is executable, ceilings
are sane, and so on) without starting anything. Non-zero exit on any
problem — safe to wire into CI or a pre-deploy check.

## 4. The systemd hardening step people miss

`packaging/lqg.service` runs with `ProtectSystem=strict`, which mounts the
**entire filesystem read-only** for the daemon *and every process it execs* —
including each app's queue worker. A real Laravel app needs to write its own
logs and cache (`storage/logs`, `storage/framework/*`, etc.), and `/run/lqg`
alone isn't enough for that.

Add every configured app's writable path via a drop-in override (preferred
over editing the unit file directly, so a future `install.sh` re-run doesn't
clobber your list):

```bash
sudo systemctl edit lqg
```

```ini
[Service]
ReadWritePaths=/run/lqg /var/www/wonen31/storage /var/www/squaremanager/storage
```

If you skip this step, workers will start and *look* like they're running
fine, but every write (a log line, a cached view, a session file) will fail
silently from the app's point of view — a classic "works until it doesn't"
trap. Do this before starting the service for real.

## 5. Start it

```bash
sudo systemctl enable --now lqg
```

Check it came up:

```bash
sudo lqg status
```

(You need root, or membership in the `lqg` group — see below — to reach the
daemon's Unix socket.)

## 6. Observe without root

The daemon's control socket (`/run/lqg/lqg.sock` by default) is owned
`root:lqg`, mode `0660`. Add yourself to the `lqg` group to run
`lqg status`/`lqg watch` without `sudo`:

```bash
sudo usermod -aG lqg "$USER"
# log out and back in for the new group membership to take effect
```

`install.sh` already does this for the user who ran it. If you lack access,
`lqg status`/`lqg watch` say so explicitly — including whether you're not in
the group at all, or were added but your current login session predates it —
instead of a bare `permission denied` (or, for `watch`, a dashboard stuck on
"disconnected"). The same goes for a daemon that isn't running.

## Upgrading / redeploying

To ship a newly built binary:

```bash
CGO_ENABLED=0 go build -o lqg ./cmd/lqg
sudo ./packaging/install.sh ./lqg
sudo systemctl restart lqg
```

`install.sh` is safe to re-run any time — it never touches an existing
config. The restart is brief (the unit's `Restart=on-failure` also means a
crash gets it back up automatically); in-flight `queue:work` child processes
are not tracked across a daemon restart, so a restart during heavy dispatch
will let those finish (or get killed by `ProtectSystem`'s own process
lifecycle) rather than the daemon waiting for them.

One case that **doesn't** need any of this: a config change made through
`lqg watch`'s live-tuning (`e`/`g`, see
[`tui-guide.md`](tui-guide.md#live-tuning)) takes effect immediately on the
already-running daemon over its existing socket — no rebuild, no restart, no
`systemctl` involved at all. It's also session-only: a real restart (or
`SIGHUP`) always reverts to whatever `config.yaml` says.

## Reloading config without a restart

The daemon re-reads and re-validates `config.yaml` on `SIGHUP`:

```bash
sudo systemctl reload lqg
```

Same validation as `lqg validate` — an invalid file is logged and the
previous, already-running config is kept untouched. Safe to try.
