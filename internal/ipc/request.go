package ipc

// Request is the one-line NDJSON request a client sends over the socket
// (PLAN.md §8): {"cmd":"status"}, {"cmd":"watch"}, {"cmd":"metrics"},
// {"cmd":"dashboard"}, or {"cmd":"set"} (live-tune, see SetRequest — decoded
// separately from this bare Cmd-only shape once the server has dispatched
// on Cmd).
type Request struct {
	Cmd string `json:"cmd"`
}

// ErrorResponse is sent back for a malformed or unknown request.
type ErrorResponse struct {
	Error string `json:"error"`
}
