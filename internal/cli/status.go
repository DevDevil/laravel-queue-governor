package cli

import (
	"fmt"
	"io"
	"text/tabwriter"
	"time"

	"github.com/DevDevil/laravel-queue-governor/internal/ipc"
)

// RenderStatusTable writes a one-shot snapshot table (PLAN.md §8: state,
// current load/iowait, jobs done this session, last pickup time, current
// reason if idle/throttled) to w.
func RenderStatusTable(w io.Writer, snap ipc.StatusSnapshot) {
	tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "NAME\tSTATE\tLOAD\tIOWAIT\tJOBS\tLAST DISPATCH\tREASON\tSCHEDULER")
	for _, a := range snap.Apps {
		fmt.Fprintf(tw, "%s\t%s\t%.2f\t%.1f%%\t%d\t%s\t%s\t%s\n",
			a.Name, a.State, a.Load, a.IOWait, a.JobsSession,
			formatLastDispatch(a.LastDispatch), formatReason(a.Reason),
			FormatScheduleSummary(a.Schedule, snap.Ts))
	}
	tw.Flush()
}

func formatLastDispatch(t *time.Time) string {
	if t == nil {
		return "never"
	}
	return t.Format(time.RFC3339)
}

func formatReason(r *string) string {
	if r == nil {
		return "-"
	}
	return *r
}
