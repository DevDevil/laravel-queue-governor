package runner

import (
	"fmt"
	"strings"
	"time"

	"github.com/DevDevil/laravel-queue-governor/internal/config"
)

// BuildSpec builds the explicit argv for one dispatch of an app's queue
// worker (PLAN.md §6): per_job runs --once, so load is re-checked between
// every job; batch runs --max-jobs/--stop-when-empty, checked only
// between batches. Built only from validated config (name, connection,
// queues, batch_size, resolved uid/gid) — job/queue content never
// reaches this argv (CLAUDE.md: no sh -c, ever).
//
// global is only consulted for its Nice default — every other per-app
// setting here is already fully resolved on app itself.
func BuildSpec(global config.GlobalConfig, app config.AppConfig) RunSpec {
	queueFlag := "--queue=" + strings.Join(app.Queues, ",")

	var args []string
	if app.Mode == config.ModeBatch {
		args = []string{"artisan", "queue:work", app.Connection, queueFlag, fmt.Sprintf("--max-jobs=%d", app.BatchSize), "--stop-when-empty"}
	} else {
		args = []string{"artisan", "queue:work", app.Connection, queueFlag, "--once"}
	}
	if app.JSONOutput {
		args = append(args, "--json")
	}

	nice := global.Nice
	if app.Nice != nil {
		nice = *app.Nice
	}

	return RunSpec{
		PHPBinary: app.PHPBinary,
		Dir:       app.Path,
		Args:      args,
		UID:       app.ResolvedUID,
		GID:       app.ResolvedGID,
		Nice:      nice,
	}
}

// emptyRunThreshold is the queue_idle heuristic from PLAN.md §6: a
// genuinely empty poll only pays Laravel's bootstrap cost (~50-200ms per
// §6), so a run finishing faster than this is inferred to have found no
// work. This can misclassify a very fast real job as empty — an accepted,
// documented tradeoff: the cooldown duration is the same either way
// (PLAN.md §4), only the reported reason differs.
const emptyRunThreshold = 300 * time.Millisecond

// DidWork reports whether a run appears to have processed anything, for
// feeding appstate.Machine.OnRunComplete's didWork parameter.
func DidWork(result RunResult) bool {
	return result.Duration >= emptyRunThreshold
}
