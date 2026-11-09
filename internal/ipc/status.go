package ipc

import "time"

// ActiveRun is one currently in-flight run: its OS process id, when it
// started, and — for a json_output app once the runner has parsed a
// `--json` line for it — the job class currently (or most recently)
// being processed and its status ("starting", "success",
// "released_after_exception", "failed"). Job/Status are "" (both
// omitempty) for an app that isn't configured with json_output, or
// before the first event for this run has arrived yet.
type ActiveRun struct {
	PID    int       `json:"pid"`
	Start  time.Time `json:"start"`
	Job    string    `json:"job,omitempty"`
	Status string    `json:"status,omitempty"`
}

// AppStatus is one app's row in `lqg status`'s snapshot table (PLAN.md
// §8): state, current load/iowait, jobs dispatched this session, last
// pickup time, and the current reason if idle/throttled. LastDispatch and
// Reason use pointers so "never dispatched" / "currently eligible, no
// reason" serialize as explicit null rather than a misleading zero value.
type AppStatus struct {
	Name         string     `json:"name"`
	State        string     `json:"state"`
	Load         float64    `json:"load"`
	IOWait       float64    `json:"iowait"`
	JobsSession  int        `json:"jobs_session"`
	LastDispatch *time.Time `json:"last_dispatch"`
	Reason       *string    `json:"reason"`

	// Mode/MaxLoadCeiling/MaxIOWaitCeiling/MaxConcurrent are this app's
	// static config (TUI.md §8.1's mode column, §9.4's per-app ceiling
	// for focus-mode graphs) — unlike the fields above, these don't
	// change tick-to-tick, only on a config reload.
	Mode             string  `json:"mode"`
	MaxLoadCeiling   float64 `json:"max_load_ceiling"`
	MaxIOWaitCeiling int     `json:"max_iowait_ceiling"`
	MaxConcurrent    int     `json:"max_concurrent"`

	// BatchSize is the configured max-jobs cap for batch mode (TUI.md
	// §10's "dispatched batch(<n>)" log line) — meaningless when Mode
	// isn't "batch".
	BatchSize int `json:"batch_size"`

	// MinInterval/SpikeCooldown/Priority/Nice are this app's remaining
	// tunable config (PLAN.md §7's live-tune allowlist, TUI.md §9.6) not
	// otherwise on the wire — needed so the edit overlay can show current
	// values. Nice is a pointer, mirroring config.AppConfig.Nice: nil means
	// "inherits global.nice", non-nil is an explicit per-app override
	// (including an explicit 0).
	MinInterval   int  `json:"min_interval"`
	SpikeCooldown int  `json:"spike_cooldown"`
	Priority      int  `json:"priority"`
	Nice          *int `json:"nice"`

	// ActiveRuns is each currently in-flight run for this app (TUI.md
	// apps panel's per-job sub-items) — omitempty since most apps most of
	// the time have none.
	ActiveRuns []ActiveRun `json:"active_runs,omitempty"`

	// JSONOutput mirrors this app's json_output config (static, like
	// Mode/MaxLoadCeiling above) — the CLI needs it to know whether to
	// expect ActiveRuns[i].Job/.Status to ever be populated for this app
	// at all, versus rendering the sub-item without them.
	JSONOutput bool `json:"json_output"`
}

// StatusSnapshot is the daemon's response to a {"cmd":"status"} request —
// one row per configured app, as of Ts.
type StatusSnapshot struct {
	Ts   time.Time   `json:"ts"`
	Apps []AppStatus `json:"apps"`
}
