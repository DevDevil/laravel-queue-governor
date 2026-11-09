package metrics

import "testing"

func TestReadCPUStat(t *testing.T) {
	stat, err := ReadCPUStat("testdata/stat_sample1")
	if err != nil {
		t.Fatalf("ReadCPUStat: %v", err)
	}

	want := CPUStat{User: 1000, Nice: 200, System: 1500, Idle: 90000, IOWait: 500, IRQ: 10, SoftIRQ: 100, Steal: 0}
	if stat != want {
		t.Errorf("ReadCPUStat = %+v, want %+v", stat, want)
	}
}

func TestReadCPUStat_MissingFile(t *testing.T) {
	if _, err := ReadCPUStat("testdata/does-not-exist"); err == nil {
		t.Fatal("ReadCPUStat: expected error for missing file, got nil")
	}
}

func TestReadCPUStat_NoAggregateLine(t *testing.T) {
	if _, err := ReadCPUStat("testdata/stat_no_aggregate"); err == nil {
		t.Fatal("ReadCPUStat: expected error when no aggregate cpu line is present, got nil")
	}
}

func TestIOWaitPercent(t *testing.T) {
	s1, err := ReadCPUStat("testdata/stat_sample1")
	if err != nil {
		t.Fatal(err)
	}
	s2, err := ReadCPUStat("testdata/stat_sample2")
	if err != nil {
		t.Fatal(err)
	}

	got := IOWaitPercent(s1, s2)
	if got != 25.0 {
		t.Errorf("IOWaitPercent = %v, want 25.0", got)
	}
}

func TestIOWaitPercent_NoElapsedTime(t *testing.T) {
	s, err := ReadCPUStat("testdata/stat_sample1")
	if err != nil {
		t.Fatal(err)
	}
	if got := IOWaitPercent(s, s); got != 0 {
		t.Errorf("IOWaitPercent(same, same) = %v, want 0", got)
	}
}

func TestIOWaitPercent_NonMonotonic(t *testing.T) {
	s1, err := ReadCPUStat("testdata/stat_sample1")
	if err != nil {
		t.Fatal(err)
	}
	s2, err := ReadCPUStat("testdata/stat_sample2")
	if err != nil {
		t.Fatal(err)
	}

	// passed in reverse (curr older than prev) — must not underflow.
	if got := IOWaitPercent(s2, s1); got != 0 {
		t.Errorf("IOWaitPercent(newer, older) = %v, want 0", got)
	}
}
