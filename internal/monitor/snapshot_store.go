package monitor

import (
	"sync"

	"github.com/DevDevil/laravel-queue-governor/internal/config"
	"github.com/DevDevil/laravel-queue-governor/internal/ipc"
)

// SnapshotStore holds the latest StatusSnapshot: the tick loop calls
// Update after every tick, and socket connections handling a `status`
// request (possibly concurrently, on their own goroutines) call Current.
// Zero value is a valid empty snapshot for a daemon that hasn't ticked
// yet.
type SnapshotStore struct {
	mu      sync.RWMutex
	current ipc.StatusSnapshot
}

func NewSnapshotStore() *SnapshotStore {
	return &SnapshotStore{}
}

func (s *SnapshotStore) Update(snap ipc.StatusSnapshot) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.current = snap
}

func (s *SnapshotStore) Current() ipc.StatusSnapshot {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.current
}

// PatchAppConfig updates just the static, config-derived fields of each
// already-known app's AppStatus (by name) in place — Mode, the ceilings,
// BatchSize, JSONOutput, MinInterval, SpikeCooldown, Priority, Nice —
// leaving the dynamic per-tick fields (State/Load/IOWait/Reason/
// JobsSession/LastDispatch/ActiveRuns) untouched.
//
// Update only runs once per scheduler tick (up to check_interval seconds
// apart), so a config change applied between ticks would otherwise show a
// stale value to anyone reading the snapshot in that window — the config
// fields self-correct on the next tick regardless, this just closes the
// gap immediately. An app name in apps that isn't yet in the stored
// snapshot (a brand-new app from a config reload that hasn't ticked yet)
// is silently skipped; it appears once the next real tick runs, same as
// before this method existed.
func (s *SnapshotStore) PatchAppConfig(apps []config.AppConfig) {
	s.mu.Lock()
	defer s.mu.Unlock()

	byName := make(map[string]config.AppConfig, len(apps))
	for _, a := range apps {
		byName[a.Name] = a
	}

	for i := range s.current.Apps {
		a, ok := byName[s.current.Apps[i].Name]
		if !ok {
			continue
		}
		st := &s.current.Apps[i]
		st.Mode = string(a.Mode)
		st.MaxLoadCeiling = a.MaxLoad
		st.MaxIOWaitCeiling = a.MaxIOWait
		st.MaxConcurrent = a.MaxConcurrent
		st.BatchSize = a.BatchSize
		st.JSONOutput = a.JSONOutput
		st.MinInterval = a.MinInterval
		st.SpikeCooldown = a.SpikeCooldown
		st.Priority = a.Priority
		st.Nice = a.Nice
	}
}

// PatchSchedule replaces one app's schedule:run status in place, so a
// schedule start/skip/completion shows up in `status` immediately rather
// than at the next queue tick. Unknown names are skipped (the app appears
// with its schedule status on the next real tick).
func (s *SnapshotStore) PatchSchedule(name string, st *ipc.ScheduleStatus) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.current.Apps {
		if s.current.Apps[i].Name == name {
			// Copy-on-write: a snapshot handed out by Current() shares
			// this backing array and may be being encoded right now.
			apps := make([]ipc.AppStatus, len(s.current.Apps))
			copy(apps, s.current.Apps)
			apps[i].Schedule = st
			s.current.Apps = apps
			return
		}
	}
}
