package logsink

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func fixedNow() time.Time { return time.Date(2026, 11, 24, 12, 0, 0, 0, time.UTC) }

func newTestManager(fallback *bytes.Buffer) *Manager {
	m := New(fallback)
	m.now = fixedNow
	return m
}

func read(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	return string(b)
}

func TestResolve(t *testing.T) {
	got := Resolve("/var/log/lqg", map[string]string{"a": "", "b": "/srv/b.log"})
	if got.DaemonPath != "/var/log/lqg/lqg.log" {
		t.Errorf("DaemonPath = %q", got.DaemonPath)
	}
	if got.Apps["a"] != "/var/log/lqg/a.log" || got.Apps["b"] != "/srv/b.log" {
		t.Errorf("Apps = %v", got.Apps)
	}

	got = Resolve("", map[string]string{"a": "", "b": "/srv/b.log"})
	if got.DaemonPath != "" {
		t.Errorf("DaemonPath without log_dir = %q, want empty", got.DaemonPath)
	}
	if _, ok := got.Apps["a"]; ok || got.Apps["b"] != "/srv/b.log" {
		t.Errorf("Apps without log_dir = %v, want only b", got.Apps)
	}
}

func TestUnconfiguredIsPassThrough(t *testing.T) {
	var fallback bytes.Buffer
	m := newTestManager(&fallback)

	fmt.Fprintf(m.Daemon(), "hello\n")
	if fallback.String() != "hello\n" {
		t.Errorf("fallback = %q, want unprefixed passthrough", fallback.String())
	}
	if m.HasApp("a") {
		t.Error("HasApp true with nothing configured")
	}
	fmt.Fprintf(m.App("a", ""), "dropped\n")
	if strings.Contains(fallback.String(), "dropped") {
		t.Error("app write without a log reached the fallback")
	}
}

func TestDaemonAndAppFiles(t *testing.T) {
	dir := t.TempDir()
	var fallback bytes.Buffer
	m := newTestManager(&fallback)
	if err := m.Configure(Resolve(dir, map[string]string{"shop": ""})); err != nil {
		t.Fatal(err)
	}
	defer m.Close()

	fmt.Fprintf(m.Daemon(), "config reloaded\n")
	fmt.Fprintf(m.App("shop", "[pid 7 err] "), "line one\nline two")

	if fallback.Len() != 0 {
		t.Errorf("fallback got %q, want nothing once lqg.log is configured", fallback.String())
	}
	if got := read(t, filepath.Join(dir, "lqg.log")); got != "2026-11-24T12:00:00.000Z config reloaded\n" {
		t.Errorf("lqg.log = %q", got)
	}
	want := "2026-11-24T12:00:00.000Z [pid 7 err] line one\n2026-11-24T12:00:00.000Z [pid 7 err] line two\n"
	if got := read(t, filepath.Join(dir, "shop.log")); got != want {
		t.Errorf("shop.log = %q, want %q", got, want)
	}

	info, err := os.Stat(filepath.Join(dir, "shop.log"))
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm&^FileMode != 0 {
		t.Errorf("shop.log mode = %v, want at most %v", perm, FileMode)
	}
}

func TestConfigureAddsAndRemovesApps(t *testing.T) {
	dir := t.TempDir()
	m := newTestManager(&bytes.Buffer{})
	defer m.Close()
	if err := m.Configure(Resolve(dir, map[string]string{"a": ""})); err != nil {
		t.Fatal(err)
	}
	aw := m.App("a", "")

	if err := m.Configure(Resolve(dir, map[string]string{"b": ""})); err != nil {
		t.Fatal(err)
	}
	if !m.HasApp("b") || m.HasApp("a") {
		t.Fatalf("after reconfigure HasApp a=%v b=%v, want a=false b=true", m.HasApp("a"), m.HasApp("b"))
	}
	fmt.Fprintf(aw, "late line from a removed app\n")
	fmt.Fprintf(m.App("b", ""), "b line\n")

	if got := read(t, filepath.Join(dir, "a.log")); got != "" {
		t.Errorf("a.log after removal = %q, want nothing written", got)
	}
	if got := read(t, filepath.Join(dir, "b.log")); !strings.Contains(got, "b line") {
		t.Errorf("b.log = %q", got)
	}
}

func TestConfigureErrorKeepsPrevious(t *testing.T) {
	dir := t.TempDir()
	m := newTestManager(&bytes.Buffer{})
	defer m.Close()
	if err := m.Configure(Resolve(dir, map[string]string{"a": ""})); err != nil {
		t.Fatal(err)
	}

	err := m.Configure(Resolve(filepath.Join(dir, "missing"), map[string]string{"a": ""}))
	if err == nil {
		t.Fatal("Configure into a missing dir succeeded")
	}
	fmt.Fprintf(m.App("a", ""), "still here\n")
	if got := read(t, filepath.Join(dir, "a.log")); !strings.Contains(got, "still here") {
		t.Errorf("a.log = %q, want previous config still writing", got)
	}
}

func TestReopenAfterRotate(t *testing.T) {
	dir := t.TempDir()
	m := newTestManager(&bytes.Buffer{})
	defer m.Close()
	if err := m.Configure(Resolve(dir, map[string]string{"a": ""})); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "a.log")
	fmt.Fprintf(m.App("a", ""), "before\n")

	if err := os.Rename(path, path+".1"); err != nil {
		t.Fatal(err)
	}
	fmt.Fprintf(m.App("a", ""), "still old file\n")
	if err := m.Reopen(); err != nil {
		t.Fatal(err)
	}
	fmt.Fprintf(m.App("a", ""), "after\n")

	if got := read(t, path+".1"); !strings.Contains(got, "before") || !strings.Contains(got, "still old file") || strings.Contains(got, "after") {
		t.Errorf("rotated file = %q", got)
	}
	if got := read(t, path); !strings.Contains(got, "after") || strings.Contains(got, "before") {
		t.Errorf("new file = %q", got)
	}
}

func TestSharedPathOneHandle(t *testing.T) {
	dir := t.TempDir()
	shared := filepath.Join(dir, "shared.log")
	m := newTestManager(&bytes.Buffer{})
	defer m.Close()
	if err := m.Configure(Targets{Apps: map[string]string{"a": shared, "b": shared}}); err != nil {
		t.Fatal(err)
	}
	fmt.Fprintf(m.App("a", "a "), "1\n")
	fmt.Fprintf(m.App("b", "b "), "2\n")
	got := read(t, shared)
	if !strings.Contains(got, "a 1\n") || !strings.Contains(got, "b 2\n") {
		t.Errorf("shared.log = %q", got)
	}
}

func TestDaemonLogNamePinnedToConfig(t *testing.T) {
	// config.validateLogging reserves this name for collision checks
	// without importing logsink; keep the two in step.
	if DaemonLogName != "lqg.log" {
		t.Errorf("DaemonLogName = %q — update config.daemonLogName too", DaemonLogName)
	}
}
