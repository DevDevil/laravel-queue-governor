# lqg — Laravel Queue Governor

A single, load-aware daemon that sits in front of `php artisan queue:work` for
every Laravel app on a server, enforcing **one global system-load ceiling**
across all of them — plus a live, full-screen TUI dashboard you can tune in
real time.

`lqg` is a *governor* in the mechanical sense: it regulates job throughput
against system load, the way a centrifugal governor holds an engine's speed
under changing load. It is **not** a worker manager or a Horizon replacement —
it decides *whether* and *how fast* to invoke `artisan`, per app, and gets out
of the way otherwise.

## The problem

Running `queue:work` under Supervisor keeps workers alive but is blind to
system load: a burst of heavy jobs (PDF generation, imports, external API
calls) on one app can drive load up and degrade every other app — and the web
server — on the same box. [Laravel Horizon](https://laravel.com/docs/horizon)
doesn't throttle on system load and only knows about one app at a time.
Supervisor only supervises.

`lqg` runs as a single daemon for the whole server, so it's the one place
that can actually enforce a *global* ceiling — per-app-only limits would let
every app individually sit "under its own limit" while collectively still
flattening the box.

## Features

- **One global load ceiling** (absolute `loadavg`, the same numbers `uptime`
  shows) across every configured app, plus an I/O-wait guard for jobs that
  thrash disk/DB rather than CPU.
- **Per-app state machine** (idle → running → cooldown → throttled) with its
  own load/I/O-wait ceilings, concurrency cap, dispatch pacing, and fair
  round-robin scheduling against the other apps.
- **`per_job` or `batch` dispatch modes** per app — fine-grained load
  re-checking between every job, or higher-throughput batches.
- **Optional Laravel scheduler per app** — `lqg` can also run each app's
  `schedule:run` every minute (no cron entry per app), never overlapping
  itself, optionally staggered within the minute and/or load-aware, and
  visible in `lqg status`/`lqg watch`.
- **Framework-agnostic** — it execs `artisan`, nothing else. No Laravel
  internals, no version coupling. Each app can use its own PHP binary.
- **Safe privilege drop** — one root daemon execs each app's worker as that
  app's own configured user via `setuid` on exec. No `sudo`, no sudoers file,
  no shell interpolation of job/queue content, ever.
- **A live, full-screen TUI dashboard** (`lqg watch`) — braille history
  graphs for load/I/O-wait, per-app status with live per-job sub-items, and a
  tailing decision log explaining exactly why an app is or isn't picking up
  work right now.
- **Live-tune performance knobs from the TUI** — press `e`/`g` in `lqg watch`
  to adjust ceilings, concurrency, cooldowns, priority, or nice on a
  *running* daemon, with inline validation and no restart. Session-only by
  design (see [`docs/tui-guide.md`](docs/tui-guide.md)).
- **Lightweight** — the daemon's hot path (poll → decide → dispatch) is
  stdlib + `/proc` only. No heavy dependencies anywhere near the loop that
  actually runs on every tick.

## A look at `lqg watch`

```
┌ full terminal width ─────────────────────────────────────────────┐
│ ╭─ load ────────────────╮ ╭─ iowait ───────────────────╮         │  graphs row
│ │   ⣀⣠⣤⣶⣿⣷⣄      3.82   │ │   ⣀⣄⣀⣠⣤⣶⣄⣀       14        │         │  fixed height
│ │  ⣰⣿⣿⣿⣿⣿⣿⣿⣄   / 6.00  │ │  ⣠⣶⣿⣿⣿⣿⣿⣶⣄     / 25 %     │         │  (≈9 rows)
│ ╰────────────────────────╯ ╰────────────────────────────╯         │
│ ╭─ apps ───────────────────────────────────── running 2/2 ─╮      │  apps panel
│ │ app            state       load     io  mode     jobs  …  │      │  height grows
│ │ wonen31        ● running   3.8/4.0  12  per_job  1284  —  │      │  with app count
│ │ …                                                         │      │  (scrolls if many)
│ ╰───────────────────────────────────────────────────────────╯      │
│ ╭─ log ─────────────────────────────────────────────────────╮      │  log panel
│ │ 14:22:09 wonen31 dispatched job #1284  load 3.8 io 12%     │      │  FILLS remaining
│ │ …                                                          │      │  vertical space,
│ ╰────────────────────────────────────────────────────────────╯      │  tails newest
│ 1 help   2 sort   3 filter   e edit   g global   space pause   q quit│  footer, 1 row
└────────────────────────────────────────────────────────────────────┘
```

Full color, live-updating, resizes with your terminal. See
[`docs/tui-guide.md`](docs/tui-guide.md) for every panel and keybinding.

## Quick start

```bash
# 1. build a static binary
CGO_ENABLED=0 go build -o lqg ./cmd/lqg

# 2. install it (creates the "lqg" group, installs the binary + systemd unit + a sample config)
sudo ./packaging/install.sh ./lqg

# 3. edit /etc/lqg/config.yaml for your apps, then validate it
sudo lqg validate /etc/lqg/config.yaml

# 4. start it
sudo systemctl enable --now lqg

# 5. watch it
lqg status
lqg watch
```

A minimal one-app `config.yaml`:

```yaml
global:
  socket_path: /run/lqg/lqg.sock
  check_interval: 5
  max_system_load: 6.0
  max_iowait: 25
  max_concurrent_jobs: 2
  log_level: info

apps:
  - name: wonen31
    path: /var/www/wonen31
    user: www-data
    php_binary: /usr/bin/php8.3
    connection: redis
    queues: [default]
    mode: per_job
    max_load: 4.0
    max_iowait: 20
    max_concurrent: 1
    min_interval: 2
    spike_cooldown: 30
```

Full install walkthrough (including a systemd hardening step that's easy to
miss): [`docs/installation.md`](docs/installation.md). Full field reference:
[`docs/configuration.md`](docs/configuration.md).

## CLI at a glance

| Command        | What it does                                                        |
|----------------|-----------------------------------------------------------------------|
| `lqg run`      | Runs the daemon in the foreground — what systemd invokes.             |
| `lqg status`   | One-shot table: every app's state, load, jobs, last pickup, reason.   |
| `lqg watch`    | The live full-screen TUI dashboard (falls back to plain NDJSON when piped, or with `--plain`). |
| `lqg validate` | Parses and validates a config file, exits non-zero on error — safe for CI/pre-deploy checks. |

Full flags and example output: [`docs/cli-reference.md`](docs/cli-reference.md).

## Documentation

- [`docs/installation.md`](docs/installation.md) — full install/upgrade walkthrough.
- [`docs/configuration.md`](docs/configuration.md) — every `config.yaml` field.
- [`docs/cli-reference.md`](docs/cli-reference.md) — every subcommand and flag.
- [`docs/tui-guide.md`](docs/tui-guide.md) — using `lqg watch`, including live tuning.

## Non-goals

- Not a replacement for Horizon's job insight, retries, or metrics dashboard.
- No web UI.
- No distributed / multi-server coordination — one daemon per box.
- Doesn't manage failed jobs or retries; that stays with Laravel.

## License

MIT — see [`LICENSE`](LICENSE). Copyright (c) 2026 Johan Overeem.
