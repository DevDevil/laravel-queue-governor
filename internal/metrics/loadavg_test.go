package metrics

import "testing"

func TestReadLoadAvg(t *testing.T) {
	load1, err := ReadLoadAvg("testdata/loadavg_normal")
	if err != nil {
		t.Fatalf("ReadLoadAvg: %v", err)
	}
	if load1 != 0.52 {
		t.Errorf("load1 = %v, want 0.52", load1)
	}
}

func TestReadLoadAvg_Malformed(t *testing.T) {
	if _, err := ReadLoadAvg("testdata/loadavg_malformed"); err == nil {
		t.Fatal("ReadLoadAvg: expected error for malformed input, got nil")
	}
}

func TestReadLoadAvg_MissingFile(t *testing.T) {
	if _, err := ReadLoadAvg("testdata/does-not-exist"); err == nil {
		t.Fatal("ReadLoadAvg: expected error for missing file, got nil")
	}
}

func TestNormalizeLoad(t *testing.T) {
	cases := []struct {
		load1  float64
		numCPU int
		want   float64
	}{
		{load1: 4.0, numCPU: 4, want: 1.0},
		{load1: 6.0, numCPU: 2, want: 3.0},
		{load1: 2.0, numCPU: 0, want: 2.0}, // guarded against divide-by-zero
	}

	for _, c := range cases {
		got := NormalizeLoad(c.load1, c.numCPU)
		if got != c.want {
			t.Errorf("NormalizeLoad(%v, %v) = %v, want %v", c.load1, c.numCPU, got, c.want)
		}
	}
}
