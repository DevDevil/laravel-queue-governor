# CLI reference

`lqg` is a single binary with four subcommands.

## `lqg run`

Runs the daemon in the foreground. This is what the systemd unit invokes —
you normally don't run this by hand except to test a config interactively.

```
lqg run [--config path]
```

| Flag       | Default                | Meaning |
|------------|-------------------------|---------|
| `--config` | `/etc/lqg/config.yaml`  | Path to the config file. |

Loads and validates the config, refuses to start on an invalid one, then
runs until `SIGTERM`/`SIGINT` (clean shutdown) or `SIGHUP` (reload config in
place). Logs to stderr:

```
$ sudo lqg run --config /etc/lqg/config.yaml
lqg: listening on /run/lqg/lqg.sock, 2 app(s), check_interval=5s
```

## `lqg status`

One-shot snapshot table of every configured app.

```
lqg status [--socket path]
```

| Flag       | Default                | Meaning |
|------------|--------------------------|---------|
| `--socket` | `/run/lqg/lqg.sock`      | Path to the daemon's Unix control socket. |

```
$ lqg status
NAME           STATE      LOAD  IOWAIT  JOBS  LAST DISPATCH         REASON         SCHEDULER
saas_seed_spa  running    2.57  0.0%    14    2026-08-08T14:39:38Z  -              schedule:run exit 0 (1.2s)  next 41s
wonen31        throttled  4.29  0.0%    3     never                 app_load_high  -
```

`SCHEDULER` is `-` unless the app has `scheduler.enabled` (see
[`configuration.md`](configuration.md#scheduler-optional-per-app)); otherwise
it shows whether `schedule:run` is running, the last run's exit code and
duration, the most recent skip (if newer than the last run), and when it's
next due.

`REASON` is `-` when an app is dispatched/running with nothing to explain;
otherwise it's one of the skip-reason vocabulary (`app_load_high`,
`min_interval`, `spike_cooldown`, `global_concurrency`, ... — the full list
and precedence rules are in `internal/scheduler/scheduler.go`, and every one of them is also
explained live in `lqg watch`'s log panel).

## `lqg watch`

The live dashboard — see [`tui-guide.md`](tui-guide.md) for the full guide
to every panel and keybinding. On a real terminal this opens the full-screen
TUI by default:

```
lqg watch [--socket path] [--plain]
```

| Flag       | Default                | Meaning |
|------------|--------------------------|---------|
| `--socket` | `/run/lqg/lqg.sock`      | Path to the daemon's Unix control socket. |
| `--plain`  | off                      | Force the plain, coloured NDJSON stream instead of the full-screen TUI, even on a real terminal. |

Piped or redirected output (`lqg watch | jq .`, `lqg watch > log.ndjson`)
always falls back to the plain NDJSON stream automatically, regardless of
`--plain` — the TUI needs a real terminal, and piped output needs to stay
valid, parseable JSON either way:

```
$ lqg watch --plain
{"ts":"2026-08-08T14:39:43Z","app":"wonen31","decision":"dispatched","reason":null,"job_seq":1284,"load":3.8,"iowait":12,"state":"running"}
{"ts":"2026-08-08T14:39:48Z","app":"squaremanager","decision":"skipped","reason":"min_interval","job_seq":null,"load":3.8,"iowait":12,"state":"cooldown"}
```

One line per app per scheduler tick, always — a skip always carries a
reason, so this is the ground truth of "picked up or not, and why," whether
or not you're watching the TUI.

## `lqg validate`

Parses and validates a config file without starting anything.

```
lqg validate [config]
```

Takes one optional positional argument (defaults to
`/etc/lqg/config.yaml`).

```
$ lqg validate /etc/lqg/config.yaml
warning: apps[0] (wonen31): max_load (4.00) exceeds global.max_system_load (3.00)
/etc/lqg/config.yaml: OK
```

Warnings (non-fatal — the config still passes) print first, prefixed
`warning:`. On a hard error it exits non-zero instead of printing `OK`:

```
$ lqg validate ./broken.yaml
Error: 2 config error(s):
  - apps[0] (wonen31): path "/var/www/wonen31" does not contain an artisan file
  - global.max_system_load must be > 0
```

Safe to run as a pre-deploy check or in CI — see
[`configuration.md`](configuration.md) for the full field/validation
reference.
