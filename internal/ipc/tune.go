package ipc

// Field name constants for {"cmd":"set"}'s SetRequest.Field — the complete,
// fixed allowlist of "performance knob" fields a running daemon can be
// live-tuned on from `lqg watch` (PLAN.md §7): session-only, never written
// back to the config file. Identity/main-app fields (name, path, user,
// php_binary, connection, queues, mode, json_output) are deliberately
// absent and can never be reached through this path — the actual
// enforcement point is internal/daemon/tune.go's setGlobalField/
// setAppField, not this list (this list is documentation + TUI menu
// building only).
const (
	FieldCheckInterval     = "check_interval"     // global only
	FieldMaxSystemLoad     = "max_system_load"    // global only
	FieldMaxIOWait         = "max_iowait"         // global OR app; Scope disambiguates
	FieldMaxConcurrentJobs = "max_concurrent_jobs" // global only
	FieldNice              = "nice"               // global OR app; Scope disambiguates

	// FieldMaxLoad is the app-scope counterpart of FieldMaxSystemLoad — the
	// one dual-scope tunable with two different field names instead of one
	// shared name + Scope, matching config.AppConfig.MaxLoad/
	// GlobalConfig.MaxSystemLoad's own non-parallel naming.
	FieldMaxLoad       = "max_load"       // app only
	FieldMaxConcurrent = "max_concurrent" // app only
	FieldMinInterval   = "min_interval"   // app only
	FieldSpikeCooldown = "spike_cooldown" // app only
	FieldPriority      = "priority"       // app only
	FieldBatchSize     = "batch_size"     // app only
)

// GlobalTunableFields / AppTunableFields are the ordered field lists the
// TUI's edit overlay renders (internal/cli) — ordering here is display
// ordering, not an enforcement mechanism (see the const block doc above).
var GlobalTunableFields = []string{FieldCheckInterval, FieldMaxSystemLoad, FieldMaxIOWait, FieldMaxConcurrentJobs, FieldNice}

var AppTunableFields = []string{FieldMaxLoad, FieldMaxIOWait, FieldMaxConcurrent, FieldMinInterval, FieldSpikeCooldown, FieldPriority, FieldNice, FieldBatchSize}

// SetRequest is {"cmd":"set"}'s payload: a live, session-only mutation of
// exactly one tunable field (PLAN.md §7) on the running daemon — never
// persisted to the config file. Value is the new value as literal text
// (e.g. "4.5", "10", "-5"), parsed server-side per the target field's known
// type, so the wire format never needs to distinguish int/float64/*int
// itself — it's exactly what a human typed into the TUI's edit box.
type SetRequest struct {
	Cmd   string `json:"cmd"` // "set"
	Scope string `json:"scope"` // "global" | "app"
	App   string `json:"app,omitempty"` // required when scope == "app"
	Field string `json:"field"`
	Value string `json:"value"`
}

// SetResponse is the one-shot reply to {"cmd":"set"} — one line, then the
// connection closes, same shape as the "status" response.
type SetResponse struct {
	OK       bool     `json:"ok"`
	Error    string   `json:"error,omitempty"`
	Warnings []string `json:"warnings,omitempty"` // config.Validate's non-fatal warnings, present even when OK
}
