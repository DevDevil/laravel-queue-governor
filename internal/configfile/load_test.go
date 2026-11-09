package configfile

import (
	"testing"

	"github.com/DevDevil/laravel-queue-governor/internal/config"
)

func TestLoad(t *testing.T) {
	cfg, err := Load("testdata/sample.yaml")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if cfg.Global.SocketPath != "/run/lqg/lqg.sock" {
		t.Errorf("Global.SocketPath = %q, want /run/lqg/lqg.sock", cfg.Global.SocketPath)
	}
	if cfg.Global.MaxConcurrentJobs != 2 {
		t.Errorf("Global.MaxConcurrentJobs = %d, want 2", cfg.Global.MaxConcurrentJobs)
	}

	if len(cfg.Apps) != 2 {
		t.Fatalf("len(Apps) = %d, want 2", len(cfg.Apps))
	}

	app := cfg.Apps[0]
	if app.Name != "wonen31" {
		t.Errorf("Apps[0].Name = %q, want wonen31", app.Name)
	}
	if app.Mode != config.ModePerJob {
		t.Errorf("Apps[0].Mode = %q, want %q", app.Mode, config.ModePerJob)
	}
	if len(app.Queues) != 2 || app.Queues[0] != "default" || app.Queues[1] != "emails" {
		t.Errorf("Apps[0].Queues = %v, want [default emails]", app.Queues)
	}

	if cfg.Apps[1].Mode != config.ModeBatch {
		t.Errorf("Apps[1].Mode = %q, want %q", cfg.Apps[1].Mode, config.ModeBatch)
	}
}

func TestLoad_MissingFile(t *testing.T) {
	if _, err := Load("testdata/does-not-exist.yaml"); err == nil {
		t.Fatal("Load: expected error for missing file, got nil")
	}
}
