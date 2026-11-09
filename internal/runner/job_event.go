package runner

import "encoding/json"

// JobEvent is one line of `queue:work --json`'s per-job lifecycle output
// (Laravel's WorkCommand::writeOutputAsJson): Status is "starting",
// "success", "released_after_exception", or "failed"; Job is the job's
// fully-qualified class name. Only the fields this daemon actually uses
// are parsed — Laravel's JSON line also carries id/uuid/connection/queue/
// attempts/exception/message/timestamp/duration, none of which are needed
// to answer "is this run doing real work, and what."
type JobEvent struct {
	Status string `json:"status"`
	Job    string `json:"job"`
}

// parseJobEvent attempts to read one line of a run's stdout as a
// JobEvent. Most lines won't be — an app without json_output configured
// prints ordinary human-readable text, and even a json_output app's
// stream can have blank lines — so a non-JSON or JSON-but-not-our-shape
// line is reported as ok == false rather than an error: the caller just
// skips it and keeps reading, since stdout content this daemon doesn't
// recognize is expected, not exceptional.
func parseJobEvent(line []byte) (JobEvent, bool) {
	var e JobEvent
	if err := json.Unmarshal(line, &e); err != nil || e.Status == "" {
		return JobEvent{}, false
	}
	return e, true
}
