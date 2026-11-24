package config

import (
	"os"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// validAppFixture returns a Config with one app pointing at a real temp
// dir (with an executable "artisan" and "php" file) and the current
// process's own user — so Validate's filesystem/user-lookup checks are
// exercised against real state without depending on anything outside the
// test's own temp dir.
func validAppFixture(t *testing.T) *Config {
	t.Helper()

	dir := t.TempDir()
	artisan := filepath.Join(dir, "artisan")
	if err := os.WriteFile(artisan, []byte("#!/usr/bin/env php\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	php := filepath.Join(dir, "php")
	if err := os.WriteFile(php, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	me, err := user.Current()
	if err != nil {
		t.Fatalf("user.Current: %v", err)
	}

	return &Config{
		Global: GlobalConfig{
			SocketPath:        "/run/lqg/lqg.sock",
			CheckInterval:     5,
			MaxSystemLoad:     6.0,
			MaxIOWait:         25,
			MaxConcurrentJobs: 2,
			LogLevel:          "info",
		},
		Apps: []AppConfig{
			{
				Name:          "wonen31",
				Path:          dir,
				User:          me.Username,
				PHPBinary:     php,
				Connection:    "redis",
				Queues:        []string{"default"},
				Mode:          ModePerJob,
				MaxLoad:       4.0,
				MaxIOWait:     20,
				MaxConcurrent: 1,
				MinInterval:   2,
				SpikeCooldown: 30,
			},
		},
	}
}

func TestValidate_Valid(t *testing.T) {
	cfg := validAppFixture(t)

	warnings, err := Validate(cfg)
	if err != nil {
		t.Fatalf("Validate: unexpected error: %v", err)
	}
	if len(warnings) != 0 {
		t.Errorf("warnings = %v, want none", warnings)
	}

	me, _ := user.Current()
	uid, _ := strconv.Atoi(me.Uid)
	if cfg.Apps[0].ResolvedUID != uint32(uid) {
		t.Errorf("ResolvedUID = %d, want %d", cfg.Apps[0].ResolvedUID, uid)
	}
}

func TestValidate_MissingRequiredFields(t *testing.T) {
	cfg := &Config{
		Global: GlobalConfig{CheckInterval: 5, MaxIOWait: 25, LogLevel: "info"},
		Apps:   []AppConfig{{}},
	}

	_, err := Validate(cfg)
	if err == nil {
		t.Fatal("Validate: expected error for empty app, got nil")
	}

	ve, ok := err.(*ValidationError)
	if !ok {
		t.Fatalf("err type = %T, want *ValidationError", err)
	}

	wantSubstrings := []string{"name is required", "path is required", "user is required", "php_binary is required", "connection is required", "queues must have at least one entry"}
	for _, want := range wantSubstrings {
		if !containsAny(ve.Errors, want) {
			t.Errorf("errors %v missing %q", ve.Errors, want)
		}
	}
}

func TestValidate_DuplicateNames(t *testing.T) {
	cfg := validAppFixture(t)
	cfg.Apps = append(cfg.Apps, cfg.Apps[0])

	_, err := Validate(cfg)
	if err == nil || !strings.Contains(err.Error(), "duplicate app name") {
		t.Fatalf("Validate: expected duplicate-name error, got %v", err)
	}
}

func TestValidate_BatchSizeRequired(t *testing.T) {
	cfg := validAppFixture(t)
	cfg.Apps[0].Mode = ModeBatch
	cfg.Apps[0].BatchSize = 0

	_, err := Validate(cfg)
	if err == nil || !strings.Contains(err.Error(), "batch_size must be > 0") {
		t.Fatalf("Validate: expected batch_size error, got %v", err)
	}
}

func TestValidate_InvalidMode(t *testing.T) {
	cfg := validAppFixture(t)
	cfg.Apps[0].Mode = "bogus"

	_, err := Validate(cfg)
	if err == nil || !strings.Contains(err.Error(), `mode "bogus"`) {
		t.Fatalf("Validate: expected mode error, got %v", err)
	}
}

func TestValidate_UnknownUser(t *testing.T) {
	cfg := validAppFixture(t)
	cfg.Apps[0].User = "no-such-user-lqg-test"

	_, err := Validate(cfg)
	if err == nil || !strings.Contains(err.Error(), "not resolvable") {
		t.Fatalf("Validate: expected unresolvable-user error, got %v", err)
	}
}

func TestValidate_PathWithoutArtisan(t *testing.T) {
	cfg := validAppFixture(t)
	cfg.Apps[0].Path = t.TempDir() // no artisan file here

	_, err := Validate(cfg)
	if err == nil || !strings.Contains(err.Error(), "does not contain an artisan file") {
		t.Fatalf("Validate: expected artisan error, got %v", err)
	}
}

func TestValidate_PHPBinaryNotExecutable(t *testing.T) {
	cfg := validAppFixture(t)

	nonExec := filepath.Join(t.TempDir(), "php")
	if err := os.WriteFile(nonExec, []byte("nope"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg.Apps[0].PHPBinary = nonExec

	_, err := Validate(cfg)
	if err == nil || !strings.Contains(err.Error(), "is not executable") {
		t.Fatalf("Validate: expected not-executable error, got %v", err)
	}
}

func TestValidate_IOWaitBounds(t *testing.T) {
	cfg := validAppFixture(t)
	cfg.Global.MaxIOWait = 150

	_, err := Validate(cfg)
	if err == nil || !strings.Contains(err.Error(), "global.max_iowait must be between 0 and 100") {
		t.Fatalf("Validate: expected global iowait bounds error, got %v", err)
	}
}

func TestValidate_CheckIntervalMustBePositive(t *testing.T) {
	cfg := validAppFixture(t)
	cfg.Global.CheckInterval = 0

	_, err := Validate(cfg)
	if err == nil || !strings.Contains(err.Error(), "global.check_interval must be > 0") {
		t.Fatalf("Validate: expected check_interval error, got %v", err)
	}
}

func TestValidate_LogLevelEnum(t *testing.T) {
	cfg := validAppFixture(t)
	cfg.Global.LogLevel = "verbose"

	_, err := Validate(cfg)
	if err == nil || !strings.Contains(err.Error(), "global.log_level") {
		t.Fatalf("Validate: expected log_level error, got %v", err)
	}
}

func TestValidate_GlobalNiceBounds(t *testing.T) {
	for _, bad := range []int{-21, 20} {
		cfg := validAppFixture(t)
		cfg.Global.Nice = bad

		_, err := Validate(cfg)
		if err == nil || !strings.Contains(err.Error(), "global.nice must be between -20 and 19") {
			t.Fatalf("Validate with global.nice=%d: expected a nice-bounds error, got %v", bad, err)
		}
	}
}

func TestValidate_AppNiceBounds(t *testing.T) {
	for _, bad := range []int{-21, 20} {
		cfg := validAppFixture(t)
		cfg.Apps[0].Nice = &bad

		_, err := Validate(cfg)
		if err == nil || !strings.Contains(err.Error(), "nice must be between -20 and 19") {
			t.Fatalf("Validate with apps[0].nice=%d: expected a nice-bounds error, got %v", bad, err)
		}
	}
}

func TestValidate_NiceAtBoundsIsValid(t *testing.T) {
	lo, hi := -20, 19
	cfg := validAppFixture(t)
	cfg.Global.Nice = lo
	cfg.Apps[0].Nice = &hi

	if _, err := Validate(cfg); err != nil {
		t.Errorf("Validate with nice at the exact -20/19 bounds: unexpected error: %v", err)
	}
}

func TestValidate_AppNiceUnsetIsValid(t *testing.T) {
	cfg := validAppFixture(t) // Apps[0].Nice left nil
	if _, err := Validate(cfg); err != nil {
		t.Errorf("Validate with apps[0].nice unset: unexpected error: %v", err)
	}
}

func TestValidate_ZeroCeilingsAreRejected(t *testing.T) {
	// A zero max_load/max_concurrent/max_system_load/max_concurrent_jobs
	// isn't a stricter ceiling, it's a permanently shut gate — nothing
	// would ever dispatch, silently. All four must be rejected.
	cases := []struct {
		name    string
		mutate  func(*Config)
		wantMsg string
	}{
		{"global.max_system_load", func(c *Config) { c.Global.MaxSystemLoad = 0 }, "global.max_system_load must be > 0"},
		{"global.max_concurrent_jobs", func(c *Config) { c.Global.MaxConcurrentJobs = 0 }, "global.max_concurrent_jobs must be > 0"},
		{"apps[0].max_load", func(c *Config) { c.Apps[0].MaxLoad = 0 }, "max_load must be > 0"},
		{"apps[0].max_concurrent", func(c *Config) { c.Apps[0].MaxConcurrent = 0 }, "max_concurrent must be > 0"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			cfg := validAppFixture(t)
			c.mutate(cfg)

			_, err := Validate(cfg)
			if err == nil || !strings.Contains(err.Error(), c.wantMsg) {
				t.Fatalf("Validate with %s=0: expected error containing %q, got %v", c.name, c.wantMsg, err)
			}
		})
	}
}

func TestValidate_WarnRules(t *testing.T) {
	cfg := validAppFixture(t)
	cfg.Apps[0].MaxLoad = 100
	cfg.Apps[0].MaxIOWait = 99
	cfg.Apps[0].MaxConcurrent = 99

	warnings, err := Validate(cfg)
	if err != nil {
		t.Fatalf("Validate: unexpected error: %v", err)
	}
	if len(warnings) != 3 {
		t.Errorf("warnings = %v, want 3", warnings)
	}
}

func TestValidate_SchedulerOptionsWithoutEnabledWarn(t *testing.T) {
	cfg := validAppFixture(t)
	cfg.Apps[0].Scheduler = SchedulerConfig{Stagger: true}

	warnings, err := Validate(cfg)
	if err != nil {
		t.Fatalf("Validate: unexpected error: %v", err)
	}
	if !containsAny(warnings, "scheduler.enabled is false") {
		t.Errorf("warnings = %v, want scheduler-not-enabled warning", warnings)
	}

	cfg.Apps[0].Scheduler.Enabled = true
	if warnings, _ := Validate(cfg); len(warnings) != 0 {
		t.Errorf("enabled scheduler: warnings = %v, want none", warnings)
	}
}

func containsAny(haystack []string, substr string) bool {
	for _, s := range haystack {
		if strings.Contains(s, substr) {
			return true
		}
	}
	return false
}
