package metrics

var _ Source = (*ProcSource)(nil)

// ProcSource is the production metrics.Source, combining the loadavg
// reader (ticket 3) and the iowait delta-sampler (ticket 4). It is called
// once per tick from the scheduler's single-threaded poll loop, so it
// keeps no locking — concurrent Sample() calls are not a supported use.
//
// IOWaitPct is delta-sampled between successive calls: the first Sample()
// after construction has no prior /proc/stat reading to diff against, so
// it reports 0 rather than a bogus value.
type ProcSource struct {
	LoadAvgPath string
	StatPath    string

	prev    CPUStat
	hasPrev bool
}

// NewProcSource returns a ProcSource reading the real /proc files.
func NewProcSource() *ProcSource {
	return &ProcSource{LoadAvgPath: "/proc/loadavg", StatPath: "/proc/stat"}
}

func (s *ProcSource) Sample() (Metrics, error) {
	load1, err := ReadLoadAvg(s.LoadAvgPath)
	if err != nil {
		return Metrics{}, err
	}

	curr, err := ReadCPUStat(s.StatPath)
	if err != nil {
		return Metrics{}, err
	}

	var iowaitPct float64
	if s.hasPrev {
		iowaitPct = IOWaitPercent(s.prev, curr)
	}
	s.prev = curr
	s.hasPrev = true

	return Metrics{Load1: load1, IOWaitPct: iowaitPct}, nil
}
