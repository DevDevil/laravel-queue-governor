package metrics

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

// ReadLoadAvg reads a /proc/loadavg-formatted file and returns the
// 1-minute load average (the first field). Production calls this with
// "/proc/loadavg"; tests point it at fixtures — no real /proc in unit
// tests.
func ReadLoadAvg(path string) (float64, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, fmt.Errorf("reading %s: %w", path, err)
	}

	fields := strings.Fields(string(data))
	if len(fields) < 1 {
		return 0, fmt.Errorf("unexpected loadavg format in %s: %q", path, string(data))
	}

	load1, err := strconv.ParseFloat(fields[0], 64)
	if err != nil {
		return 0, fmt.Errorf("parsing 1-minute load average in %s: %w", path, err)
	}

	return load1, nil
}

// NormalizeLoad returns load1 normalised per CPU core, for display only —
// thresholds always compare the absolute value (CLAUDE.md: "normalise by
// runtime.NumCPU() only for display").
func NormalizeLoad(load1 float64, numCPU int) float64 {
	if numCPU <= 0 {
		numCPU = 1
	}
	return load1 / float64(numCPU)
}
