package runner

import (
	"testing"
	"time"

	"github.com/DevDevil/laravel-queue-governor/internal/config"
)

func TestBuildSpec_PerJob(t *testing.T) {
	app := config.AppConfig{
		Name:        "wonen31",
		Path:        "/var/www/wonen31",
		PHPBinary:   "/usr/bin/php8.3",
		Connection:  "redis",
		Queues:      []string{"default", "emails"},
		Mode:        config.ModePerJob,
		ResolvedUID: 33,
		ResolvedGID: 33,
	}

	spec := BuildSpec(config.GlobalConfig{}, app)

	if spec.PHPBinary != "/usr/bin/php8.3" || spec.Dir != "/var/www/wonen31" {
		t.Fatalf("PHPBinary/Dir = %q/%q, want /usr/bin/php8.3 //var/www/wonen31", spec.PHPBinary, spec.Dir)
	}
	if spec.UID != 33 || spec.GID != 33 {
		t.Errorf("UID/GID = %d/%d, want 33/33", spec.UID, spec.GID)
	}

	want := []string{"artisan", "queue:work", "redis", "--queue=default,emails", "--once"}
	if !equalArgs(spec.Args, want) {
		t.Errorf("Args = %v, want %v", spec.Args, want)
	}
}

func TestBuildSpec_Batch(t *testing.T) {
	app := config.AppConfig{
		Name:       "squaremanager",
		Path:       "/var/www/squaremanager",
		PHPBinary:  "/usr/bin/php8.4",
		Connection: "database",
		Queues:     []string{"default"},
		Mode:       config.ModeBatch,
		BatchSize:  25,
	}

	spec := BuildSpec(config.GlobalConfig{}, app)

	want := []string{"artisan", "queue:work", "database", "--queue=default", "--max-jobs=25", "--stop-when-empty"}
	if !equalArgs(spec.Args, want) {
		t.Errorf("Args = %v, want %v", spec.Args, want)
	}
}

func TestBuildSpec_NiceUsesGlobalDefaultWhenAppUnset(t *testing.T) {
	app := config.AppConfig{PHPBinary: "/usr/bin/php8.3", Path: "/var/www/wonen31", Queues: []string{"default"}}
	spec := BuildSpec(config.GlobalConfig{Nice: 10}, app)
	if spec.Nice != 10 {
		t.Errorf("Nice = %d, want 10 (inherited from global)", spec.Nice)
	}
}

func TestBuildSpec_NiceAppOverrideWinsOverGlobal(t *testing.T) {
	appNice := 3
	app := config.AppConfig{PHPBinary: "/usr/bin/php8.3", Path: "/var/www/wonen31", Queues: []string{"default"}, Nice: &appNice}
	spec := BuildSpec(config.GlobalConfig{Nice: 10}, app)
	if spec.Nice != 3 {
		t.Errorf("Nice = %d, want 3 (app override)", spec.Nice)
	}
}

func TestBuildSpec_NiceAppExplicitZeroOverridesNonZeroGlobal(t *testing.T) {
	// The whole reason Nice is a pointer: an app explicitly opting back
	// into normal priority, even though global.nice is set, must not be
	// indistinguishable from "app didn't configure nice at all".
	appNice := 0
	app := config.AppConfig{PHPBinary: "/usr/bin/php8.3", Path: "/var/www/wonen31", Queues: []string{"default"}, Nice: &appNice}
	spec := BuildSpec(config.GlobalConfig{Nice: 10}, app)
	if spec.Nice != 0 {
		t.Errorf("Nice = %d, want 0 (explicit app override, not the global default)", spec.Nice)
	}
}

func equalArgs(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestDidWork(t *testing.T) {
	cases := []struct {
		duration time.Duration
		want     bool
	}{
		{duration: 50 * time.Millisecond, want: false},
		{duration: 299 * time.Millisecond, want: false},
		{duration: 300 * time.Millisecond, want: true},
		{duration: 2 * time.Second, want: true},
	}

	for _, c := range cases {
		got := DidWork(RunResult{Duration: c.duration})
		if got != c.want {
			t.Errorf("DidWork(%v) = %v, want %v", c.duration, got, c.want)
		}
	}
}

func TestBuildScheduleSpec(t *testing.T) {
	nice := 7
	app := config.AppConfig{
		Name: "shop-a", Path: "/var/www/shop-a", PHPBinary: "/usr/bin/php8.3",
		Connection: "redis", Queues: []string{"default"}, Mode: config.ModeBatch, BatchSize: 5,
		JSONOutput: true, Nice: &nice, ResolvedUID: 33, ResolvedGID: 34,
	}
	spec := BuildScheduleSpec(config.GlobalConfig{Nice: 3}, app)

	want := []string{"artisan", "schedule:run"}
	if !equalArgs(spec.Args, want) {
		t.Errorf("Args = %v, want %v (no queue/json flags)", spec.Args, want)
	}
	if spec.PHPBinary != "/usr/bin/php8.3" || spec.Dir != "/var/www/shop-a" || spec.UID != 33 || spec.GID != 34 || spec.Nice != 7 {
		t.Errorf("spec = %+v", spec)
	}
}
