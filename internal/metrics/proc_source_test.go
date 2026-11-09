package metrics

import "testing"

func TestProcSource_FirstSampleHasNoIOWaitDelta(t *testing.T) {
	s := &ProcSource{LoadAvgPath: "testdata/loadavg_normal", StatPath: "testdata/stat_sample1"}

	m, err := s.Sample()
	if err != nil {
		t.Fatalf("Sample: %v", err)
	}
	if m.Load1 != 0.52 {
		t.Errorf("Load1 = %v, want 0.52", m.Load1)
	}
	if m.IOWaitPct != 0 {
		t.Errorf("IOWaitPct = %v, want 0 (no prior sample yet)", m.IOWaitPct)
	}
}

func TestProcSource_SecondSampleComputesDelta(t *testing.T) {
	s := &ProcSource{LoadAvgPath: "testdata/loadavg_normal", StatPath: "testdata/stat_sample1"}
	if _, err := s.Sample(); err != nil {
		t.Fatal(err)
	}

	s.StatPath = "testdata/stat_sample2"
	m, err := s.Sample()
	if err != nil {
		t.Fatalf("Sample: %v", err)
	}
	if m.IOWaitPct != 25.0 {
		t.Errorf("IOWaitPct = %v, want 25.0", m.IOWaitPct)
	}
}
