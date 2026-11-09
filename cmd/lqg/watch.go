package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/DevDevil/laravel-queue-governor/internal/cli"
	"github.com/DevDevil/laravel-queue-governor/internal/ipc"
)

func newWatchCmd() *cobra.Command {
	var socketPath string
	var plain bool

	cmd := &cobra.Command{
		Use:   "watch",
		Short: "Live decision event stream",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
			defer stop()

			isTTY := isTerminal(os.Stdout)

			// A real terminal gets the TUI.md-spec full-screen dashboard
			// by default; --plain forces a plain coloured NDJSON stream
			// instead. Piped/redirected output always falls back to
			// plain NDJSON regardless of --plain, since bubbletea needs
			// a real terminal and piping should stay valid, parseable
			// JSON either way (ticket 13).
			if isTTY && !plain {
				return cli.RunDashboardTUI(ctx, socketPath)
			}

			out := cmd.OutOrStdout()
			return cli.StreamWatch(ctx, socketPath, func(e ipc.DecisionEvent) error {
				return cli.WriteWatchLine(out, e, isTTY)
			})
		},
	}

	cmd.Flags().StringVar(&socketPath, "socket", "/run/lqg/lqg.sock", "path to the daemon's unix socket")
	cmd.Flags().BoolVar(&plain, "plain", false, "force the plain coloured NDJSON stream instead of the TUI dashboard")
	return cmd
}

func isTerminal(f *os.File) bool {
	info, err := f.Stat()
	if err != nil {
		return false
	}
	return info.Mode()&os.ModeCharDevice != 0
}
