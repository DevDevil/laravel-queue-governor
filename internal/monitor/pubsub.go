package monitor

import "sync"

// pubsub is the shared subscribe/unsubscribe core behind Bus, SampleBus,
// and RefreshBus: non-blocking publish (a full subscriber channel just
// drops the value rather than stalling the publisher) and exactly-once
// close on unsubscribe. The three wrapper types differ only in what they
// publish and how — a batch of DecisionEvents, one MetricSample, or a bare
// struct{} signal — which is why each keeps its own thin Publish method
// instead of this type trying to cover every shape too.
type pubsub[T any] struct {
	mu   sync.Mutex
	subs map[chan T]struct{}
}

func newPubsub[T any]() *pubsub[T] {
	return &pubsub[T]{subs: make(map[chan T]struct{})}
}

// publish sends v to every current subscriber, dropping (never blocking)
// on any subscriber whose buffer is full.
func (p *pubsub[T]) publish(v T) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for ch := range p.subs {
		select {
		case ch <- v:
		default:
		}
	}
}

// publishBatch sends every value in vs to every current subscriber, under
// one lock acquisition for the whole batch (so a Subscribe/unsubscribe
// racing this call sees the batch as a single atomic unit, not torn
// mid-way through) — what Bus.Publish needs since a scheduler tick
// publishes several DecisionEvents at once.
func (p *pubsub[T]) publishBatch(vs []T) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for ch := range p.subs {
		for _, v := range vs {
			select {
			case ch <- v:
			default:
			}
		}
	}
}

// Subscribe registers a new listener with the given buffer size. The
// returned unsubscribe func must be called exactly once when the listener
// is done; it closes the channel.
func (p *pubsub[T]) Subscribe(buffer int) (<-chan T, func()) {
	ch := make(chan T, buffer)

	p.mu.Lock()
	p.subs[ch] = struct{}{}
	p.mu.Unlock()

	unsubscribe := func() {
		p.mu.Lock()
		delete(p.subs, ch)
		p.mu.Unlock()
		close(ch)
	}

	return ch, unsubscribe
}
