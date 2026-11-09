package cli

import (
	"context"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

// reconnectBackoff is TUI.md §12.4's "auto-retry the connect every ~2s".
const reconnectBackoff = 2 * time.Second

// RunDashboardTUI drives the TUI.md full-screen dashboard against the
// daemon's socket until the user quits (q/ctrl+c) or ctx is canceled.
// Runs on the alternate screen buffer (TUI.md §3) so the user's
// scrollback is restored on exit. A dropped connection (daemon restarted
// or stopped) does not end the program: runDashboardConnection keeps
// retrying every reconnectBackoff (or immediately on 'r') until ctx is
// canceled, and the model shows a disconnect banner meanwhile (§12.4).
func RunDashboardTUI(ctx context.Context, socketPath string) error {
	frames := make(chan dashboardFrame)
	disconnectCh := make(chan error, 1)
	retryNow := make(chan struct{}, 1)

	go runDashboardConnection(ctx, socketPath, frames, disconnectCh, retryNow)

	p := tea.NewProgram(newDashboardModel(frames, disconnectCh, retryNow, socketPath), tea.WithAltScreen())
	_, err := p.Run()
	return err
}

// runDashboardConnection owns the reconnect loop: connect, stream frames
// until the connection ends (error or the daemon closing it), report the
// disconnect, wait (reconnectBackoff, or until retryNow fires from the
// 'r' keybind), then try again — forever, until ctx is canceled. frames
// stays open for the whole loop (not just one connection attempt); it's
// only closed when the loop itself exits for good.
func runDashboardConnection(ctx context.Context, socketPath string, frames chan<- dashboardFrame, disconnectCh chan<- error, retryNow <-chan struct{}) {
	defer close(frames)

	for {
		err := StreamDashboard(ctx, socketPath, func(f dashboardFrame) error {
			select {
			case frames <- f:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		})
		if ctx.Err() != nil {
			return
		}

		select {
		case disconnectCh <- err:
		case <-ctx.Done():
			return
		}

		select {
		case <-time.After(reconnectBackoff):
		case <-retryNow:
		case <-ctx.Done():
			return
		}
	}
}
