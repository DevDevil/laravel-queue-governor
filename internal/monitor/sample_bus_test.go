package monitor

import (
	"testing"
	"time"

	"github.com/DevDevil/laravel-queue-governor/internal/ipc"
)

func TestSampleBus_PublishWithNoSubscribersDoesNotPanic(t *testing.T) {
	b := NewSampleBus()
	b.Publish(ipc.MetricSample{Load: 1})
}

func TestSampleBus_SubscribeReceivesPublishedSample(t *testing.T) {
	b := NewSampleBus()
	ch, unsubscribe := b.Subscribe(4)
	defer unsubscribe()

	b.Publish(ipc.MetricSample{Load: 3.8})

	select {
	case s := <-ch:
		if s.Load != 3.8 {
			t.Errorf("Load = %v, want 3.8", s.Load)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for sample")
	}
}

func TestSampleBus_MultipleSubscribersEachGetTheSample(t *testing.T) {
	b := NewSampleBus()
	ch1, unsub1 := b.Subscribe(4)
	defer unsub1()
	ch2, unsub2 := b.Subscribe(4)
	defer unsub2()

	b.Publish(ipc.MetricSample{Load: 1})

	for _, ch := range []<-chan ipc.MetricSample{ch1, ch2} {
		select {
		case s := <-ch:
			if s.Load != 1 {
				t.Errorf("Load = %v, want 1", s.Load)
			}
		case <-time.After(time.Second):
			t.Fatal("timed out waiting for sample on a subscriber")
		}
	}
}

func TestSampleBus_FullSubscriberDropsRatherThanBlocks(t *testing.T) {
	b := NewSampleBus()
	ch, unsubscribe := b.Subscribe(1)
	defer unsubscribe()

	done := make(chan struct{})
	go func() {
		b.Publish(ipc.MetricSample{Load: 1})
		b.Publish(ipc.MetricSample{Load: 2}) // channel already full — must drop, not block
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Publish blocked on a full subscriber channel instead of dropping")
	}

	s := <-ch
	if s.Load != 1 {
		t.Errorf("Load = %v, want 1", s.Load)
	}
	select {
	case extra := <-ch:
		t.Errorf("unexpected second sample delivered: %+v", extra)
	default:
	}
}

func TestSampleBus_UnsubscribeClosesChannel(t *testing.T) {
	b := NewSampleBus()
	ch, unsubscribe := b.Subscribe(4)
	unsubscribe()

	b.Publish(ipc.MetricSample{Load: 1})

	_, open := <-ch
	if open {
		t.Error("channel should be closed after unsubscribe")
	}
}
