package metrics

import (
	"bufio"
	"fmt"
	"os"
	"strconv"
	"strings"
)

// cpuStatFieldCount is CPUStat's field count — the number of
// whitespace-separated values expected after "cpu" on /proc/stat's
// aggregate line.
const cpuStatFieldCount = 8

// CPUStat is the aggregate "cpu" line from /proc/stat — cumulative jiffies
// since boot. Guest/guest_nice are omitted: they're already included in
// User/Nice on Linux, so summing them into Total would double-count.
type CPUStat struct {
	User, Nice, System, Idle, IOWait, IRQ, SoftIRQ, Steal uint64
}

// Total is the sum of jiffies across all counted states.
func (c CPUStat) Total() uint64 {
	return c.User + c.Nice + c.System + c.Idle + c.IOWait + c.IRQ + c.SoftIRQ + c.Steal
}

// ReadCPUStat reads the aggregate "cpu" line (not the per-core "cpu0",
// "cpu1", ... lines) from a /proc/stat-formatted file. Production calls
// this with "/proc/stat"; tests point it at fixtures.
func ReadCPUStat(path string) (CPUStat, error) {
	f, err := os.Open(path)
	if err != nil {
		return CPUStat{}, fmt.Errorf("reading %s: %w", path, err)
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) > 0 && fields[0] == "cpu" {
			return parseCPUStatFields(fields[1:], path)
		}
	}
	if err := scanner.Err(); err != nil {
		return CPUStat{}, fmt.Errorf("reading %s: %w", path, err)
	}

	return CPUStat{}, fmt.Errorf("no aggregate cpu line found in %s", path)
}

func parseCPUStatFields(fields []string, path string) (CPUStat, error) {
	if len(fields) < cpuStatFieldCount {
		return CPUStat{}, fmt.Errorf("unexpected cpu line format in %s: want at least %d fields, got %d", path, cpuStatFieldCount, len(fields))
	}

	vals := make([]uint64, cpuStatFieldCount)
	for i := 0; i < cpuStatFieldCount; i++ {
		v, err := strconv.ParseUint(fields[i], 10, 64)
		if err != nil {
			return CPUStat{}, fmt.Errorf("parsing cpu field %d in %s: %w", i, path, err)
		}
		vals[i] = v
	}

	return CPUStat{
		User: vals[0], Nice: vals[1], System: vals[2], Idle: vals[3],
		IOWait: vals[4], IRQ: vals[5], SoftIRQ: vals[6], Steal: vals[7],
	}, nil
}

// IOWaitPercent computes the iowait percentage as the delta of the iowait
// jiffy counter over the delta of total jiffies between two samples
// (PLAN.md §3). Returns 0 if the samples are equal (no time has passed) or
// non-monotonic (curr older than prev — e.g. samples passed in the wrong
// order, or /proc/stat counters reset), rather than underflowing.
func IOWaitPercent(prev, curr CPUStat) float64 {
	prevTotal, currTotal := prev.Total(), curr.Total()
	if currTotal <= prevTotal {
		return 0
	}

	if curr.IOWait < prev.IOWait {
		return 0
	}

	deltaTotal := currTotal - prevTotal
	deltaIOWait := curr.IOWait - prev.IOWait
	return float64(deltaIOWait) / float64(deltaTotal) * 100
}
