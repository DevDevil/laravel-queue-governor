package monitor

import (
	"testing"
	"time"

	"github.com/DevDevil/laravel-queue-governor/internal/ipc"
)

func TestBus_PublishWithNoSubscribersDoesNotPanic(t *testing.T) {
	b := NewBus()
	b.Publish([]ipc.DecisionEvent{{App: "a"}}) // must simply do nothing
}

func TestBus_SubscribeReceivesPublishedEvents(t *testing.T) {
	b := NewBus()
	ch, unsubscribe := b.Subscribe(4)
	defer unsubscribe()

	b.Publish([]ipc.DecisionEvent{{App: "a"}, {App: "b"}})

	select {
	case e := <-ch:
		if e.App != "a" {
			t.Errorf("first event App = %q, want a", e.App)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for first event")
	}

	select {
	case e := <-ch:
		if e.App != "b" {
			t.Errorf("second event App = %q, want b", e.App)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for second event")
	}
}

func TestBus_MultipleSubscribersEachGetTheEvent(t *testing.T) {
	b := NewBus()
	ch1, unsub1 := b.Subscribe(4)
	defer unsub1()
	ch2, unsub2 := b.Subscribe(4)
	defer unsub2()

	b.Publish([]ipc.DecisionEvent{{App: "a"}})

	for _, ch := range []<-chan ipc.DecisionEvent{ch1, ch2} {
		select {
		case e := <-ch:
			if e.App != "a" {
				t.Errorf("App = %q, want a", e.App)
			}
		case <-time.After(time.Second):
			t.Fatal("timed out waiting for event on a subscriber")
		}
	}
}

func TestBus_FullSubscriberDropsRatherThanBlocks(t *testing.T) {
	b := NewBus()
	ch, unsubscribe := b.Subscribe(1) // buffer of 1, never drained
	defer unsubscribe()

	done := make(chan struct{})
	go func() {
		// If Publish blocked on the full channel, this would hang forever
		// and the test would time out below.
		b.Publish([]ipc.DecisionEvent{{App: "a"}})
		b.Publish([]ipc.DecisionEvent{{App: "b"}}) // channel already full — must be dropped, not block
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Publish blocked on a full subscriber channel instead of dropping")
	}

	// Only the first event made it through.
	e := <-ch
	if e.App != "a" {
		t.Errorf("App = %q, want a", e.App)
	}
	select {
	case extra := <-ch:
		t.Errorf("unexpected second event delivered: %+v", extra)
	default:
	}
}

func TestBus_UnsubscribeStopsDeliveryAndClosesChannel(t *testing.T) {
	b := NewBus()
	ch, unsubscribe := b.Subscribe(4)
	unsubscribe()

	b.Publish([]ipc.DecisionEvent{{App: "a"}})

	_, open := <-ch
	if open {
		t.Error("channel should be closed after unsubscribe")
	}
}
