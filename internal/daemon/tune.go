package daemon

import (
	"fmt"
	"reflect"
	"strconv"
	"time"

	"github.com/DevDevil/laravel-queue-governor/internal/config"
	"github.com/DevDevil/laravel-queue-governor/internal/ipc"
)

// tuneApplyTimeout bounds how long Tune waits for its own Reload to
// actually land before reporting success — see waitApplied.
const tuneApplyTimeout = 2 * time.Second

// Tune applies one live, session-only field mutation (PLAN.md §7's
// performance-knob allowlist, ipc.GlobalTunableFields/AppTunableFields) to
// the running daemon. It clones Loop's current effective config (via
// ConfigSnapshot, safely off the single-writer goroutine), applies the
// single requested field, then re-validates the WHOLE derived config with
// the existing config.Validate — reusing every bound/enum rule with zero
// duplication. All of this, including Validate's os.Stat/user.Lookup calls,
// runs on the CALLING goroutine (the monitor.Server connection goroutine),
// never inside Run's own select loop, so a slow filesystem check during
// validation can never stall ticking. Only a config that passes Validate is
// handed to the existing Reload (which cannot itself fail — the mutation
// there is pure in-memory).
//
// Tune does not return until waitApplied confirms the change is actually
// visible on Scheduler.Global/Apps, not merely enqueued: Reload itself is
// fire-and-forget (a buffered channel send), so without this, a caller
// acting on Tune's success — e.g. cmd/lqg/run.go re-reading global info to
// refresh the dashboard's hello frame right after a successful tune —
// could race ahead of Run's own goroutine actually applying it and observe
// stale values.
//
// Known limitation (accepted): the initial ConfigSnapshot() read and the
// eventual Reload() write still aren't atomic as a pair across *different*
// Tune calls. Two concurrent Tune calls (or a Tune racing a SIGHUP) can
// each snapshot before the other's Reload lands, so the loser's Reload
// silently reverts the winner's change. This is a human-interactive,
// single-actor-typical feature (one keypress at a time); the failure mode
// is "my edit didn't stick, try again" rather than corruption, and is no
// worse than the pre-existing SIGHUP-vs-SIGHUP race.
func (l *Loop) Tune(scope, appName, field, value string) (warnings []string, err error) {
	cfg := l.ConfigSnapshot()

	switch scope {
	case "global":
		if err := setGlobalField(&cfg.Global, field, value); err != nil {
			return nil, err
		}
	case "app":
		idx := -1
		for i := range cfg.Apps {
			if cfg.Apps[i].Name == appName {
				idx = i
				break
			}
		}
		if idx < 0 {
			return nil, fmt.Errorf("unknown app %q", appName)
		}
		if err := setAppField(&cfg.Apps[idx], field, value); err != nil {
			return nil, err
		}
	default:
		return nil, fmt.Errorf("unknown scope %q (must be \"global\" or \"app\")", scope)
	}

	warnings, err = config.Validate(&cfg)
	if err != nil {
		return warnings, err
	}

	l.Reload(cfg.Global, cfg.Apps)
	if !l.waitApplied(cfg) {
		target := scope
		if scope == "app" {
			target = "app " + appName
		}
		return warnings, fmt.Errorf("tune of %s.%s=%q was accepted but timed out waiting for it to apply — a concurrent tune or SIGHUP may have won the race, see Tune's doc comment", target, field, value)
	}
	return warnings, nil
}

// waitApplied polls ConfigSnapshot (bounded by tuneApplyTimeout) until it
// deep-equals want, confirming Run's own goroutine actually processed the
// Reload this Tune call just enqueued before Tune reports success.
func (l *Loop) waitApplied(want config.Config) bool {
	deadline := time.Now().Add(tuneApplyTimeout)
	for {
		if reflect.DeepEqual(l.ConfigSnapshot(), want) {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(time.Millisecond)
	}
}

// setGlobalField / setAppField are the REAL enforcement boundary for the
// live-tune allowlist: only fields with an explicit case here are
// reachable, structurally — a hand-crafted request naming e.g. "path" or
// "user" hits default and is rejected, regardless of what
// ipc.GlobalTunableFields/AppTunableFields (client-side menu building only)
// contain.
func setGlobalField(g *config.GlobalConfig, field, value string) error {
	switch field {
	case ipc.FieldCheckInterval:
		n, err := parseInt(field, value)
		if err != nil {
			return err
		}
		g.CheckInterval = n
	case ipc.FieldMaxSystemLoad:
		f, err := parseFloat(field, value)
		if err != nil {
			return err
		}
		g.MaxSystemLoad = f
	case ipc.FieldMaxIOWait:
		n, err := parseInt(field, value)
		if err != nil {
			return err
		}
		g.MaxIOWait = n
	case ipc.FieldMaxConcurrentJobs:
		n, err := parseInt(field, value)
		if err != nil {
			return err
		}
		g.MaxConcurrentJobs = n
	case ipc.FieldNice:
		n, err := parseInt(field, value)
		if err != nil {
			return err
		}
		g.Nice = n
	default:
		return fmt.Errorf("field %q is not a tunable global field", field)
	}
	return nil
}

func setAppField(a *config.AppConfig, field, value string) error {
	switch field {
	case ipc.FieldMaxLoad:
		f, err := parseFloat(field, value)
		if err != nil {
			return err
		}
		a.MaxLoad = f
	case ipc.FieldMaxIOWait:
		n, err := parseInt(field, value)
		if err != nil {
			return err
		}
		a.MaxIOWait = n
	case ipc.FieldMaxConcurrent:
		n, err := parseInt(field, value)
		if err != nil {
			return err
		}
		a.MaxConcurrent = n
	case ipc.FieldMinInterval:
		n, err := parseInt(field, value)
		if err != nil {
			return err
		}
		a.MinInterval = n
	case ipc.FieldSpikeCooldown:
		n, err := parseInt(field, value)
		if err != nil {
			return err
		}
		a.SpikeCooldown = n
	case ipc.FieldPriority:
		n, err := parseInt(field, value)
		if err != nil {
			return err
		}
		a.Priority = n
	case ipc.FieldNice:
		n, err := parseInt(field, value)
		if err != nil {
			return err
		}
		a.Nice = &n
	case ipc.FieldBatchSize:
		n, err := parseInt(field, value)
		if err != nil {
			return err
		}
		a.BatchSize = n
	default:
		return fmt.Errorf("field %q is not a tunable app field", field)
	}
	return nil
}

func parseInt(field, value string) (int, error) {
	n, err := strconv.Atoi(value)
	if err != nil {
		return 0, fmt.Errorf("field %q: %q is not a valid integer", field, value)
	}
	return n, nil
}

func parseFloat(field, value string) (float64, error) {
	f, err := strconv.ParseFloat(value, 64)
	if err != nil {
		return 0, fmt.Errorf("field %q: %q is not a valid number", field, value)
	}
	return f, nil
}
