// Package logsink routes the daemon's own log lines and each app's log
// lines (worker stdout/stderr plus the daemon's per-app decisions) to
// files under global.log_dir / apps[].log_file (PLAN.md §8.1), with
// reopen-on-signal so logrotate can rotate without copytruncate.
//
// Stdlib-only: the daemon loop writes through it.
//
// With nothing configured it is a no-op router: Daemon() is the fallback
// writer (stderr) unchanged, and App() reports no per-app log — exactly
// the behaviour from before log_dir existed.
package logsink

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

// DaemonLogName is the daemon's own log file inside log_dir.
const DaemonLogName = "lqg.log"

// FileMode is what new log files are created with: root writes, the
// unit's group (lqg) reads.
const FileMode os.FileMode = 0o640

// timeLayout prefixes every line written to a file. Lines that fall back
// to stderr get no prefix: journald already timestamps them.
const timeLayout = "2006-01-02T15:04:05.000Z07:00"

// Targets is the resolved set of log files: an empty DaemonPath means the
// daemon log stays on the fallback writer; an app missing from Apps has
// no log of its own.
type Targets struct {
	DaemonPath string
	Apps       map[string]string // app name -> path
}

// Resolve computes Targets from config values: log_dir gives lqg.log and
// <app>.log for every app, and a per-app log_file overrides (or, without
// log_dir, alone enables) that app's file. appFiles maps app name to its
// log_file ("" when unset).
func Resolve(logDir string, appFiles map[string]string) Targets {
	t := Targets{Apps: make(map[string]string, len(appFiles))}
	if logDir != "" {
		t.DaemonPath = filepath.Join(logDir, DaemonLogName)
	}
	for name, file := range appFiles {
		switch {
		case file != "":
			t.Apps[name] = file
		case logDir != "":
			t.Apps[name] = filepath.Join(logDir, name+".log")
		}
	}
	return t
}

// Manager owns the open log files. All methods are safe for concurrent
// use: workers' output goroutines, the daemon loop and the signal
// handlers all write or reconfigure through it.
type Manager struct {
	fallback io.Writer
	now      func() time.Time

	mu     sync.Mutex
	files  map[string]*os.File // path -> open file (shared if two targets name one path)
	daemon string              // path, "" = fallback
	apps   map[string]string   // app name -> path
}

// New returns a Manager with nothing configured, writing the daemon log
// to fallback.
func New(fallback io.Writer) *Manager {
	return &Manager{
		fallback: fallback,
		now:      time.Now,
		files:    map[string]*os.File{},
		apps:     map[string]string{},
	}
}

// Configure switches to t: files for new paths are opened (created with
// FileMode, appended to), files no longer referenced are closed. On any
// open error nothing changes and the error is returned — the caller keeps
// running on the previous set, same rule as an invalid config.
func (m *Manager) Configure(t Targets) error {
	wanted := map[string]bool{}
	if t.DaemonPath != "" {
		wanted[t.DaemonPath] = true
	}
	for _, p := range t.Apps {
		wanted[p] = true
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	opened := map[string]*os.File{}
	for p := range wanted {
		if _, ok := m.files[p]; ok {
			continue
		}
		f, err := openLog(p)
		if err != nil {
			for _, f := range opened {
				f.Close()
			}
			return err
		}
		opened[p] = f
	}

	for p, f := range m.files {
		if !wanted[p] {
			f.Close()
			delete(m.files, p)
		}
	}
	for p, f := range opened {
		m.files[p] = f
	}
	m.daemon = t.DaemonPath
	m.apps = make(map[string]string, len(t.Apps))
	for name, p := range t.Apps {
		m.apps[name] = p
	}
	return nil
}

// Reopen closes and reopens every log file by path — what logrotate's
// postrotate (SIGUSR1) needs after it renamed them away. A file that
// fails to reopen keeps its old handle, and the first error is returned.
func (m *Manager) Reopen() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	var first error
	paths := make([]string, 0, len(m.files))
	for p := range m.files {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	for _, p := range paths {
		f, err := openLog(p)
		if err != nil {
			if first == nil {
				first = err
			}
			continue
		}
		m.files[p].Close()
		m.files[p] = f
	}
	return first
}

// Close closes every open file; later writes fall back as if unconfigured.
func (m *Manager) Close() {
	m.mu.Lock()
	defer m.mu.Unlock()
	for p, f := range m.files {
		f.Close()
		delete(m.files, p)
	}
	m.daemon = ""
	m.apps = map[string]string{}
}

func openLog(path string) (*os.File, error) {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND|os.O_CREATE, FileMode)
	if err != nil {
		return nil, fmt.Errorf("opening log file: %w", err)
	}
	return f, nil
}

// Daemon is the daemon's own log: lqg.log when configured, otherwise the
// fallback writer, unprefixed. Each Write should be one or more whole
// lines (every caller uses Fprintf with a trailing \n).
func (m *Manager) Daemon() io.Writer { return daemonWriter{m} }

type daemonWriter struct{ m *Manager }

func (w daemonWriter) Write(p []byte) (int, error) {
	w.m.mu.Lock()
	defer w.m.mu.Unlock()
	if f := w.m.files[w.m.daemon]; w.m.daemon != "" && f != nil {
		w.m.writeLines(f, "", p)
		return len(p), nil
	}
	return w.m.fallback.Write(p)
}

// HasDaemonFile reports whether the daemon log goes to lqg.log (rather
// than the fallback writer).
func (m *Manager) HasDaemonFile() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.daemon != ""
}

// HasApp reports whether app name has a log file of its own.
func (m *Manager) HasApp(name string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	_, ok := m.apps[name]
	return ok
}

// App returns a writer for app name's log, resolved on every write (so a
// reload or reopen in between is picked up, and writes for an app that has
// no log — or no longer has one — are silently dropped). prefix is put
// after the timestamp on every line, e.g. "[pid 123] ".
func (m *Manager) App(name, prefix string) io.Writer {
	return appWriter{m: m, name: name, prefix: prefix}
}

type appWriter struct {
	m      *Manager
	name   string
	prefix string
}

func (w appWriter) Write(p []byte) (int, error) {
	w.m.mu.Lock()
	defer w.m.mu.Unlock()
	if path, ok := w.m.apps[w.name]; ok {
		if f := w.m.files[path]; f != nil {
			w.m.writeLines(f, w.prefix, p)
		}
	}
	return len(p), nil
}

// writeLines writes p (one or more lines; a missing final newline is
// added) with a timestamp + prefix on each line, as a single write so
// lines from concurrent writers to a shared file never interleave
// mid-line. Write errors are dropped: logging must never stall or crash
// the daemon. Caller holds m.mu.
func (m *Manager) writeLines(f *os.File, prefix string, p []byte) {
	ts := m.now().Format(timeLayout)
	var buf bytes.Buffer
	for len(p) > 0 {
		line := p
		if i := bytes.IndexByte(p, '\n'); i >= 0 {
			line, p = p[:i], p[i+1:]
		} else {
			p = nil
		}
		buf.WriteString(ts)
		buf.WriteByte(' ')
		buf.WriteString(prefix)
		buf.Write(line)
		buf.WriteByte('\n')
	}
	f.Write(buf.Bytes())
}
