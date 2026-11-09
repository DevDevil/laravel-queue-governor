package monitor

// RefreshBus is a zero-payload pub/sub signal: the daemon publishes to it
// after every applied config change (SIGHUP reload or a live tune), and
// every open {"cmd":"dashboard"} connection subscribes so it can re-send a
// fresh hello+snapshot pair without the client having to reconnect
// (TUI.md §9.6/§12). Same non-blocking, drop-on-full-subscriber shape as
// Bus/SampleBus (via pubsub) — a missed refresh signal is harmless, since
// the next tick's decision events (or a later refresh) will still get the
// client to the same place.
type RefreshBus struct {
	ps *pubsub[struct{}]
}

func NewRefreshBus() *RefreshBus {
	return &RefreshBus{ps: newPubsub[struct{}]()}
}

// Publish signals every current subscriber that a refresh is due.
func (b *RefreshBus) Publish() {
	b.ps.publish(struct{}{})
}

// Subscribe registers a new listener with the given buffer size. The
// returned unsubscribe func must be called exactly once when the listener
// is done (e.g. the dashboard client disconnects); it closes the channel.
func (b *RefreshBus) Subscribe(buffer int) (<-chan struct{}, func()) {
	return b.ps.Subscribe(buffer)
}
