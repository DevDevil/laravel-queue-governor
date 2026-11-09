package monitor

import (
	"sync"
	"testing"
	"time"

	"github.com/DevDevil/laravel-queue-governor/internal/ipc"
)

func TestMetricHistory_SnapshotEmptyInitially(t *testing.T) {
	h := NewMetricHistory(3)
	if got := h.Snapshot(); len(got) != 0 {
		t.Errorf("Snapshot() = %v, want empty", got)
	}
}

func TestMetricHistory_AddAndSnapshotOrder(t *testing.T) {
	h := NewMetricHistory(3)
	h.Add(ipc.MetricSample{Load: 1})
	h.Add(ipc.MetricSample{Load: 2})
	h.Add(ipc.MetricSample{Load: 3})

	got := h.Snapshot()
	want := []float64{1, 2, 3}
	if len(got) != len(want) {
		t.Fatalf("len(Snapshot()) = %d, want %d", len(got), len(want))
	}
	for i, w := range want {
		if got[i].Load != w {
			t.Errorf("Snapshot()[%d].Load = %v, want %v", i, got[i].Load, w)
		}
	}
}

func TestMetricHistory_DropsOldestAtCapacity(t *testing.T) {
	h := NewMetricHistory(3)
	for i := 1; i <= 5; i++ {
		h.Add(ipc.MetricSample{Load: float64(i)})
	}

	got := h.Snapshot()
	want := []float64{3, 4, 5} // oldest (1, 2) dropped
	if len(got) != len(want) {
		t.Fatalf("len(Snapshot()) = %d, want %d", len(got), len(want))
	}
	for i, w := range want {
		if got[i].Load != w {
			t.Errorf("Snapshot()[%d].Load = %v, want %v", i, got[i].Load, w)
		}
	}
}

func TestMetricHistory_SnapshotIsACopy(t *testing.T) {
	h := NewMetricHistory(3)
	h.Add(ipc.MetricSample{Load: 1})

	got := h.Snapshot()
	got[0].Load = 999 // mutate the returned slice

	again := h.Snapshot()
	if again[0].Load != 1 {
		t.Errorf("internal state mutated via a returned Snapshot; got %v, want 1", again[0].Load)
	}
}

func TestMetricHistory_ZeroCapacityClampedToOne(t *testing.T) {
	h := NewMetricHistory(0)
	h.Add(ipc.MetricSample{Load: 1})
	h.Add(ipc.MetricSample{Load: 2})

	got := h.Snapshot()
	if len(got) != 1 || got[0].Load != 2 {
		t.Errorf("Snapshot() = %v, want [{Load:2}] (capacity clamped to 1)", got)
	}
}

func TestMetricHistory_ConcurrentAddAndSnapshot(t *testing.T) {
	h := NewMetricHistory(50)
	var wg sync.WaitGroup

	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			h.Add(ipc.MetricSample{Ts: time.Now(), Load: float64(i)})
			_ = h.Snapshot()
		}(i)
	}
	wg.Wait()

	if got := len(h.Snapshot()); got != 20 {
		t.Errorf("len(Snapshot()) = %d, want 20", got)
	}
}
