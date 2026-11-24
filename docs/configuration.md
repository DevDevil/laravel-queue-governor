# Configuration reference

`lqg` reads one YAML file (`/etc/lqg/config.yaml` by default) with two
top-level keys: `global:` (settings shared by the whole daemon) and `apps:`
(a list, one entry per Laravel app). Always validate a change before
reloading it for real:

```bash
sudo lqg validate /etc/lqg/config.yaml
```

Validation errors are hard failures (the config is rejected — on `SIGHUP` or
a fresh start, an invalid file never takes effect). A few checks are
warnings only (printed, but the config still loads) — noted below.

## `global:`

| Field                  | Type    | Meaning |
|-------------------------|---------|---------|
| `socket_path`            | string  | Path to the Unix control socket `lqg status`/`lqg watch` connect to. Owned `root:lqg`, mode `0660`. Not reloadable at runtime (a `SIGHUP` or live tune never re-binds it). |
| `check_interval`         | int (seconds), **required > 0** | How often the daemon polls load/I/O-wait and makes dispatch decisions. |
| `max_system_load`        | float, **required > 0** | Hard load ceiling across **every** app combined — an absolute `loadavg` number, the same you'd read from `uptime`. Zero isn't "no limit," it's a permanently shut gate, so it's rejected rather than silently accepted. |
| `max_iowait`              | int 0–100 (percent), **required** | Global I/O-wait ceiling, computed from `/proc/stat` deltas. Catches disk/DB-bound bursts a CPU-only view would miss. |
| `max_concurrent_jobs`     | int, **required > 0** | Hard cap on simultaneously in-flight `artisan` processes, across all apps. Works together with `max_system_load` — load is a lagging signal, so the concurrency cap is what prevents overshoot in the window before load reflects newly-dispatched jobs. |
| `log_level`               | string, one of `debug`/`info`/`warn`/`error` | Daemon log verbosity. |
| `nice`                    | int -20..19, default `0` | OS scheduling niceness applied to every worker process unless a specific app overrides it. `0` means "leave it untouched." Unrelated to `priority` below, which only affects `lqg`'s own fair-dispatch ordering, never OS-level CPU scheduling. |

## `apps:` (a list)

| Field           | Type    | Meaning |
|------------------|---------|---------|
| `name`            | string, **required, unique** | Identifies the app everywhere — CLI output, logs, IPC. |
| `path`            | string, **required** | The app's root directory; must contain an `artisan` file. |
| `user`            | string, **required** | The OS user to run this app's worker as — must resolve to a real uid/gid. The daemon runs as root and drops privileges to this user via `setuid` on exec (no `sudo`). |
| `php_binary`      | string, **required** | Path to the PHP binary to invoke — must exist and be executable. Different apps can use different PHP versions. |
| `connection`      | string, **required** | Passed straight to `queue:work <connection>` — whatever your app's `config/queue.php` calls it (`redis`, `database`, ...). |
| `queues`          | list of strings, **required, at least one** | Passed as `--queue=a,b,c`, in the order given. |
| `mode`            | `per_job` \| `batch`, **required** | `per_job` runs `queue:work --once` — load is re-checked between *every single job*, the finest-grained control, at the cost of a Laravel bootstrap per job. `batch` runs `queue:work --max-jobs=N --stop-when-empty` — load is only re-checked between batches, higher throughput, coarser control. |
| `batch_size`      | int, **required > 0 when `mode: batch`** | The `N` in `--max-jobs=N`. Unused in `per_job` mode. |
| `max_load`        | float, **required > 0** | This app's own load ceiling — same absolute scale as `global.max_system_load`. Same "zero is a shut gate" rule applies. Should stay ≤ `global.max_system_load` (warning, not an error, if it doesn't). |
| `max_iowait`      | int 0–100, **required** | This app's own I/O-wait ceiling. Should stay ≤ `global.max_iowait` (warning if not). |
| `max_concurrent`  | int, **required > 0** | Max simultaneous `artisan` processes for *this app alone*. Should stay ≤ `global.max_concurrent_jobs` (warning if not). |
| `min_interval`    | int (seconds), **required ≥ 0** | Minimum gap enforced between this app's dispatches, even when otherwise eligible. `0` is valid — no extra gap. |
| `spike_cooldown`  | int (seconds), **required ≥ 0** | Extra pause tacked on *after* `min_interval` has elapsed, if a load/I/O-wait spike was observed during/around the last run. |
| `priority`        | int, optional | Higher = preferred when multiple apps are eligible in the same tick (fair round-robin dispatch, least-recently-served first). Has no effect on OS scheduling. |
| `nice`            | int -20..19, optional | OS niceness for *this app's* worker only — overrides `global.nice`. Because it's genuinely optional (distinct from "unset"), you can explicitly set `nice: 0` to opt an app back into normal priority even while `global.nice` is non-zero elsewhere. |
| `json_output`     | bool, default `false` | Adds `--json` to this app's `queue:work` invocation. Requires a Laravel version whose `queue:work` supports it. When set, the runner parses real per-job status (job class, `starting`/`success`/`released_after_exception`/`failed`) off stdout — shown live in `lqg watch`'s per-app sub-items, and used to gate opening a 2nd/3rd concurrent slot on *confirmed* work rather than an idle poll. |

### `scheduler:` (optional, per app)

Has `lqg` also run the app's Laravel scheduler — `php artisan schedule:run`,
as the app's `user` — once a minute, so you don't need a separate cron entry
per app and the scheduler shows up in `lqg status`/`lqg watch`.

| Field           | Type    | Meaning |
|------------------|---------|---------|
| `enabled`        | bool, default `false` | Run `schedule:run` every minute. `false` (or no `scheduler:` block) = exactly the old behaviour. |
| `respect_load`   | bool, default `false` | Skip a minute when this app's or the global load/I/O-wait ceiling is exceeded, or `max_concurrent_jobs` is full. Off by default: scheduled tasks are time-critical. |
| `stagger`        | bool, default `false` | Start at a fixed per-app second within the minute (derived from the app name) instead of `:00`, so many apps don't all start at once. |

Behaviour:

- **Never overlaps itself:** if the previous `schedule:run` for the app is
  still running when the next minute comes due, that minute is skipped
  (`schedule_overlap`, logged).
- **Counts towards `max_concurrent_jobs`:** while it runs, queue workers get
  one slot fewer. With `respect_load: false` it starts even when that cap is
  full.
- Setting `respect_load`/`stagger` without `enabled: true` gives a warning.
- Enable/disable by editing the file and `systemctl reload lqg` — no restart.
  Not changeable from the live-tune overlay.
- Remove the app's old cron entry (`* * * * * php artisan schedule:run`)
  when you enable this, or the scheduler runs twice.

```yaml
apps:
  - name: shop-a
    # ... queue settings ...
    scheduler:
      enabled: true
      respect_load: false
      stagger: true
```

## A two-app example

```yaml
global:
  socket_path: /run/lqg/lqg.sock
  check_interval: 5
  max_system_load: 6.0
  max_iowait: 25
  max_concurrent_jobs: 2
  log_level: info
  nice: 0

apps:
  - name: wonen31
    path: /var/www/wonen31
    user: www-data
    php_binary: /usr/bin/php8.3
    connection: redis
    queues: [default, emails]
    mode: per_job
    batch_size: 10
    max_load: 4.0
    max_iowait: 20
    max_concurrent: 1
    min_interval: 2
    spike_cooldown: 30
    priority: 10
    nice: 5
    json_output: true
    scheduler:
      enabled: true
      stagger: true

  - name: squaremanager
    path: /var/www/squaremanager
    user: www-data
    php_binary: /usr/bin/php8.4
    connection: database
    queues: [default]
    mode: batch
    batch_size: 25
    max_load: 3.0
    max_iowait: 20
    max_concurrent: 1
    min_interval: 1
    spike_cooldown: 20
    priority: 5
```

## Changing config on a running daemon

Two different mechanisms, deliberately kept separate:

- **Edit the file, then `sudo systemctl reload lqg`** (`SIGHUP`) — re-reads
  and re-validates the whole file; an invalid file is rejected and the
  daemon keeps running on whatever it already had. Persists across restarts,
  since it *is* the file.
- **`lqg watch`'s live-tune overlay** (`e`/`g`) — adjusts one performance
  knob on the running daemon immediately, without touching the file at all.
  Session-only: reverts on the next restart or `SIGHUP`. Only performance
  knobs are reachable this way (ceilings, concurrency, cooldowns, priority,
  nice, batch size) — identity fields like `path`, `user`, `php_binary`,
  `connection`, `queues`, `mode`, `json_output` are never reachable through
  it. See [`tui-guide.md`](tui-guide.md#live-tuning) for the full list and
  how to use it.
