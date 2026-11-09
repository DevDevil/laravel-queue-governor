package monitor

import "github.com/DevDevil/laravel-queue-governor/internal/ipc"

// SampleBus is a minimal in-process pub/sub for MetricSamples — the same
// non-blocking-publish, drop-on-full-subscriber design as Bus, kept as its
// own small type rather than generalized: Bus.Publish takes a batch
// (multiple decisions per tick), but there's only ever one MetricSample
// per sampling tick, so the two Publish methods don't actually share a
// shape worth forcing together (Subscribe/unsubscribe is shared, via
// pubsub).
type SampleBus struct {
	ps *pubsub[ipc.MetricSample]
}

func NewSampleBus() *SampleBus {
	return &SampleBus{ps: newPubsub[ipc.MetricSample]()}
}

// Publish fans one sample out to every current subscriber. Never blocks
// on a slow/absent subscriber — a full channel just drops the sample.
func (b *SampleBus) Publish(sample ipc.MetricSample) {
	b.ps.publish(sample)
}

// Subscribe registers a new listener with the given buffer size. The
// returned unsubscribe func must be called exactly once when the listener
// is done; it closes the channel.
func (b *SampleBus) Subscribe(buffer int) (<-chan ipc.MetricSample, func()) {
	return b.ps.Subscribe(buffer)
}
