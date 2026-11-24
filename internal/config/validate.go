package config

import (
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"strconv"
)

var validLogLevels = map[string]bool{
	"debug": true,
	"info":  true,
	"warn":  true,
	"error": true,
}

// ValidationError aggregates every hard-rule violation found by Validate,
// so `lqg validate` (and a SIGHUP reload) can report the full list in one
// pass instead of stopping at the first problem.
type ValidationError struct {
	Errors []string
}

func (e *ValidationError) Error() string {
	msg := fmt.Sprintf("%d config error(s):", len(e.Errors))
	for _, s := range e.Errors {
		msg += "\n  - " + s
	}
	return msg
}

// checkPercentRange appends an error to errs if value isn't a valid
// percentage (0-100) — shared by global.max_iowait and every app's
// max_iowait, the only two fields PLAN.md §7 gives this range. prefix is
// the already-formatted "global.max_iowait" or "apps[0] (name): max_iowait"
// lead-in, since the two call sites use different label conventions.
func checkPercentRange(errs *[]string, prefix string, value int) {
	if value < 0 || value > 100 {
		*errs = append(*errs, prefix+" must be between 0 and 100")
	}
}

// warnExceedsGlobal appends a non-fatal "per-app X exceeds global.Y"
// warning — shared by the max_load/max_iowait/max_concurrent-vs-global
// checks, which all have this exact shape but differ in field names (an
// app's max_concurrent vs global's max_concurrent_jobs) and value types
// (float64 vs int), so values arrive pre-formatted rather than this
// helper trying to format both.
func warnExceedsGlobal(warnings *[]string, label, appField, appVal, globalField, globalVal string) {
	*warnings = append(*warnings, fmt.Sprintf("%s: %s (%s) exceeds global.%s (%s)", label, appField, appVal, globalField, globalVal))
}

// Validate checks cfg against the rules in PLAN.md §7. A non-nil error
// means the config must be rejected (on SIGHUP, the previous config is
// kept instead). Warnings are non-fatal. On success it also resolves each
// app's user to a uid/gid and stores them on the AppConfig.
func Validate(cfg *Config) (warnings []string, err error) {
	var errs []string

	g := &cfg.Global
	if g.CheckInterval <= 0 {
		errs = append(errs, "global.check_interval must be > 0")
	}
	checkPercentRange(&errs, "global.max_iowait", g.MaxIOWait)
	if !validLogLevels[g.LogLevel] {
		errs = append(errs, fmt.Sprintf("global.log_level %q must be one of debug, info, warn, error", g.LogLevel))
	}
	if g.Nice < -20 || g.Nice > 19 {
		errs = append(errs, "global.nice must be between -20 and 19")
	}
	// A zero (i.e. omitted) max_system_load/max_concurrent_jobs isn't a
	// stricter ceiling, it's a permanently shut gate: every app's own load
	// check and the global concurrency check would both compare against 0
	// forever, so nothing is ever dispatched — silently, with no error
	// anywhere else to catch it.
	if g.MaxSystemLoad <= 0 {
		errs = append(errs, "global.max_system_load must be > 0")
	}
	if g.MaxConcurrentJobs <= 0 {
		errs = append(errs, "global.max_concurrent_jobs must be > 0")
	}

	seen := make(map[string]bool, len(cfg.Apps))
	for i := range cfg.Apps {
		appErrs, appWarnings := validateApp(&cfg.Apps[i], g, i, seen)
		errs = append(errs, appErrs...)
		warnings = append(warnings, appWarnings...)
	}

	logErrs, logWarnings := validateLogging(cfg)
	errs = append(errs, logErrs...)
	warnings = append(warnings, logWarnings...)

	if len(errs) > 0 {
		return warnings, &ValidationError{Errors: errs}
	}
	return warnings, nil
}

// validateApp checks one app's config against PLAN.md §7's per-app rules,
// and — on success — resolves its user to a uid/gid on app itself. seen
// tracks app names already validated across the whole cfg.Apps slice, so
// duplicates are caught regardless of position.
func validateApp(app *AppConfig, g *GlobalConfig, i int, seen map[string]bool) (errs, warnings []string) {
	label := fmt.Sprintf("apps[%d]", i)
	if app.Name != "" {
		label = fmt.Sprintf("apps[%d] (%s)", i, app.Name)
	}

	if app.Name == "" {
		errs = append(errs, fmt.Sprintf("%s: name is required", label))
	} else if seen[app.Name] {
		errs = append(errs, fmt.Sprintf("%s: duplicate app name %q", label, app.Name))
	} else {
		seen[app.Name] = true
	}

	if app.Path == "" {
		errs = append(errs, fmt.Sprintf("%s: path is required", label))
	} else if info, statErr := os.Stat(filepath.Join(app.Path, "artisan")); statErr != nil || info.IsDir() {
		// The || relies on short-circuit evaluation: info is nil when
		// statErr != nil, so info.IsDir() must not run in that case.
		errs = append(errs, fmt.Sprintf("%s: path %q does not contain an artisan file", label, app.Path))
	}

	if app.User == "" {
		errs = append(errs, fmt.Sprintf("%s: user is required", label))
	} else if u, lookupErr := user.Lookup(app.User); lookupErr != nil {
		errs = append(errs, fmt.Sprintf("%s: user %q is not resolvable: %v", label, app.User, lookupErr))
	} else {
		uid, uidErr := strconv.ParseUint(u.Uid, 10, 32)
		gid, gidErr := strconv.ParseUint(u.Gid, 10, 32)
		if uidErr != nil || gidErr != nil {
			errs = append(errs, fmt.Sprintf("%s: user %q has a non-numeric uid/gid", label, app.User))
		} else {
			app.ResolvedUID = uint32(uid)
			app.ResolvedGID = uint32(gid)
		}
	}

	if app.PHPBinary == "" {
		errs = append(errs, fmt.Sprintf("%s: php_binary is required", label))
	} else if info, statErr := os.Stat(app.PHPBinary); statErr != nil {
		errs = append(errs, fmt.Sprintf("%s: php_binary %q not found", label, app.PHPBinary))
	} else if info.IsDir() || info.Mode()&0111 == 0 {
		errs = append(errs, fmt.Sprintf("%s: php_binary %q is not executable", label, app.PHPBinary))
	}

	if app.Connection == "" {
		errs = append(errs, fmt.Sprintf("%s: connection is required", label))
	}
	if len(app.Queues) == 0 {
		errs = append(errs, fmt.Sprintf("%s: queues must have at least one entry", label))
	}

	switch app.Mode {
	case ModePerJob:
	case ModeBatch:
		if app.BatchSize <= 0 {
			errs = append(errs, fmt.Sprintf("%s: batch_size must be > 0 when mode is batch", label))
		}
	default:
		errs = append(errs, fmt.Sprintf("%s: mode %q must be %q or %q", label, app.Mode, ModePerJob, ModeBatch))
	}

	checkPercentRange(&errs, label+": max_iowait", app.MaxIOWait)
	if app.MinInterval < 0 {
		errs = append(errs, fmt.Sprintf("%s: min_interval must be >= 0", label))
	}
	if app.SpikeCooldown < 0 {
		errs = append(errs, fmt.Sprintf("%s: spike_cooldown must be >= 0", label))
	}
	if app.Nice != nil && (*app.Nice < -20 || *app.Nice > 19) {
		errs = append(errs, fmt.Sprintf("%s: nice must be between -20 and 19", label))
	}
	// Same "0 is a shut gate, not a stricter one" reasoning as
	// global.max_system_load/max_concurrent_jobs above.
	if app.MaxLoad <= 0 {
		errs = append(errs, fmt.Sprintf("%s: max_load must be > 0", label))
	}
	if app.MaxConcurrent <= 0 {
		errs = append(errs, fmt.Sprintf("%s: max_concurrent must be > 0", label))
	}

	if !app.Scheduler.Enabled && (app.Scheduler.RespectLoad || app.Scheduler.Stagger) {
		warnings = append(warnings, fmt.Sprintf("%s: scheduler.respect_load/stagger set but scheduler.enabled is false — schedule:run will not run", label))
	}

	if app.MaxLoad > g.MaxSystemLoad {
		warnExceedsGlobal(&warnings, label, "max_load", fmt.Sprintf("%.2f", app.MaxLoad), "max_system_load", fmt.Sprintf("%.2f", g.MaxSystemLoad))
	}
	if app.MaxIOWait > g.MaxIOWait {
		warnExceedsGlobal(&warnings, label, "max_iowait", fmt.Sprintf("%d", app.MaxIOWait), "max_iowait", fmt.Sprintf("%d", g.MaxIOWait))
	}
	if app.MaxConcurrent > g.MaxConcurrentJobs {
		warnExceedsGlobal(&warnings, label, "max_concurrent", fmt.Sprintf("%d", app.MaxConcurrent), "max_concurrent_jobs", fmt.Sprintf("%d", g.MaxConcurrentJobs))
	}

	return errs, warnings
}
