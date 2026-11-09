# Using `lqg watch`

`lqg watch` opens a full-screen, live-updating dashboard on a real terminal
(falls back to a plain NDJSON stream when piped, or with `--plain` — see
[`cli-reference.md`](cli-reference.md#lqg-watch)).

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

## The four panels

- **Graphs (top)** — braille history lines for system load and I/O-wait, ~5
  minutes of history, filled area under the curve. The line, the numeric
  readout, and the per-app `load` cell in the apps table all colour by the
  same threshold rule: **green** under 70% of the ceiling, **amber**
  70–100%, **red** at or over.
- **Apps (middle)** — one row per configured app: state, current load
  (`current/ceiling`), I/O-wait, mode, jobs dispatched this session, and the
  current reason if it's not running. Every currently in-flight run for an
  app gets its own indented sub-item line underneath, showing elapsed time
  and PID — and, for an app configured with `json_output: true`, the real
  job class and status once the runner has parsed a `--json` event for it.
- **Log (bottom, fills remaining space)** — tails the live decision stream,
  newest at the bottom. The daemon emits a decision for every app on every
  tick, so this is the ground truth of "picked up or not, and why" — the
  same skip-reason vocabulary as `lqg status`.
- **Footer (1 row)** — the keybinding legend, always visible.

## State colours

| Dot | State | Meaning |
|-----|-------|---------|
| ● (green)  | `running`   | Currently executing a job/batch. |
| ◐ (amber)  | `cooldown`  | Just finished; waiting out `min_interval` and/or `spike_cooldown`. |
| ○ (red)    | `throttled` | Blocked by its own or the global load/I/O-wait ceiling. |
| ○ (grey)   | `idle`      | Eligible, waiting to be scheduled. |

## Keybindings

| Key(s)      | Action |
|-------------|--------|
| `↑`/`↓`, `k`/`j` | Move the selection in the apps table. |
| `2`, `s`         | Cycle sort: name → state → load → jobs → name. |
| `3`, `f`         | Focus the selected app — dims/hides the rest, retitles the graphs to that app's own ceiling, filters the log to just it. `Esc` exits. |
| `space`, `p`     | Pause / resume live updates (still buffers underneath; resuming catches up in order). |
| `r`              | Force a reconnect to the daemon now (also automatic every ~2s if the connection drops). |
| `e`              | Edit the **selected app's** performance knobs — see [Live tuning](#live-tuning). |
| `g`              | Edit the daemon's **global** performance knobs — see [Live tuning](#live-tuning). |
| `1`, `?`         | Toggle the help overlay (keybindings + colour legend). |
| `Esc`            | Close whatever overlay is open, or exit focus mode. |
| `q`, `Ctrl-C`    | Quit. |

If the daemon disconnects (restart, network hiccup), a banner appears in the
footer and `lqg watch` keeps retrying in the background — it never crashes
or exits on its own over a lost connection.

## Live tuning

`e`/`g` open an overlay listing a fixed set of **performance knobs** —
things that control *how aggressively* the daemon runs, never *what* it
runs. Editing a field sends it straight to the running daemon over the
control socket; the daemon re-validates it (the exact same rules as
`lqg validate`) before applying it, and an inline error or non-fatal
warning shows right there in the overlay if something's off.

**Important — this is session-only.** A live tune is *never* written back
to `config.yaml`. A daemon restart, or a `SIGHUP`/`systemctl reload lqg`,
always reverts to whatever the file says. If you want a change to stick,
edit the file too.

**Identity/main-app fields are never reachable this way** — `name`, `path`,
`user`, `php_binary`, `connection`, `queues`, `mode`, `json_output` can only
ever be changed by editing `config.yaml` and reloading. Only the fields
below are tunable live:

### Global (`g`)

| Field                  | Unit | What it does |
|-------------------------|------|--------------|
| `check_interval`         | seconds | How often the daemon polls load and makes decisions — how fast it reacts. |
| `max_system_load`        | —    | Hard load ceiling across all apps combined (absolute, like `uptime`). |
| `max_iowait`              | %    | Global I/O-wait ceiling — catches disk/DB thrashing CPU alone would miss. |
| `max_concurrent_jobs`     | —    | Max worker processes running at once, across every app. |
| `nice`                    | —    | Default OS scheduling priority for every worker (-20 high … 19 low). |

### Per-app (`e`, on the selected app)

| Field           | Unit | What it does |
|------------------|------|--------------|
| `max_load`        | —    | This app's own load ceiling (should stay ≤ the global ceiling). |
| `max_iowait`      | %    | This app's own I/O-wait ceiling. |
| `max_concurrent`  | —    | Max simultaneous worker processes for this app alone. |
| `min_interval`    | s    | Minimum gap between this app's dispatches. |
| `spike_cooldown`  | s    | Extra pause tacked on after a load spike is observed. |
| `priority`        | —    | Fair-dispatch weight — higher gets preferred when apps compete. |
| `nice`            | —    | OS scheduling priority for this app's worker (-20 high … 19 low). |
| `batch_size`      | —    | Max jobs per batch (only used when `mode: batch`). |

### How to use it

1. Press `e` (edits whichever app is currently selected) or `g` (global).
2. `↑`/`↓`/`j`/`k` to move between fields — a live description of the
   selected field shows at the bottom of the overlay.
3. `Enter` opens the value input, pre-filled with the current value. Type a
   new number (digits, one leading `-`, one `.`); `Backspace` to correct.
4. `Enter` again submits it. On success the overlay shows the field list
   again with the new value; on a validation error, the overlay stays open
   with the error shown inline so you can fix and retry.
5. `Esc` backs out one level (value input → field list → closed).

A per-app `nice` that's unset (inheriting `global.nice`) shows as
`(inherits global.nice)`. There's no way to clear an override back to
"inherit" from the TUI — only to set an explicit number; remove the field
from `config.yaml` and reload if you want to go back to inheriting.
