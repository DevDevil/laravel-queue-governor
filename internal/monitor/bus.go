package monitor

import "github.com/DevDevil/laravel-queue-governor/internal/ipc"

// subscriberBufferSize is the channel buffer every Bus/SampleBus
// subscriber gets. Large enough to absorb a burst without a slow reader
// (a `watch`/`dashboard` client stalled on I/O) tripping the drop-rather-
// than-block behavior on every tick; not so large that a permanently
// stalled subscriber wastes meaningful memory before its drops start.
const subscriberBufferSize = 64

// Bus is a simple in-process pub/sub for decision events: the daemon loop
// publishes once per tick, and any number of `lqg watch` connections
// subscribe. Publish never blocks on a slow or absent subscriber — a full
// subscriber channel just drops the event, since watch is a best-effort
// live tail, not a guaranteed-delivery log, and a stalled subscriber must
// never stall the tick loop.
type Bus struct {
	ps *pubsub[ipc.DecisionEvent]
}

func NewBus() *Bus {
	return &Bus{ps: newPubsub[ipc.DecisionEvent]()}
}

// Publish fans events out to every current subscriber, as one tick's
// batch — see pubsub.publishBatch for why this is a batch call rather
// than one publish per event.
func (b *Bus) Publish(events []ipc.DecisionEvent) {
	b.ps.publishBatch(events)
}

// Subscribe registers a new listener with the given buffer size. The
// returned unsubscribe func must be called exactly once when the listener
// is done (e.g. the watch client disconnects); it closes the channel.
func (b *Bus) Subscribe(buffer int) (<-chan ipc.DecisionEvent, func()) {
	return b.ps.Subscribe(buffer)
}
