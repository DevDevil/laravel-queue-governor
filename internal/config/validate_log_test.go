package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func validateErrs(t *testing.T, cfg *Config) (string, []string) {
	t.Helper()
	warnings, err := Validate(cfg)
	if err == nil {
		return "", warnings
	}
	return err.Error(), warnings
}

func TestValidate_LogDirValid(t *testing.T) {
	cfg := validAppFixture(t)
	cfg.Global.LogDir = t.TempDir()
	if msg, _ := validateErrs(t, cfg); msg != "" {
		t.Fatalf("Validate: %s", msg)
	}
}

func TestValidate_LogDirMustBeAbsoluteAndExist(t *testing.T) {
	cfg := validAppFixture(t)
	cfg.Global.LogDir = "relative/logs"
	if msg, _ := validateErrs(t, cfg); !strings.Contains(msg, "global.log_dir") || !strings.Contains(msg, "absolute") {
		t.Errorf("relative log_dir: err = %q", msg)
	}

	cfg.Global.LogDir = filepath.Join(t.TempDir(), "missing")
	if msg, _ := validateErrs(t, cfg); !strings.Contains(msg, "does not exist") {
		t.Errorf("missing log_dir: err = %q", msg)
	}

	file := filepath.Join(t.TempDir(), "f")
	os.WriteFile(file, nil, 0o644)
	cfg.Global.LogDir = file
	if msg, _ := validateErrs(t, cfg); !strings.Contains(msg, "not a directory") {
		t.Errorf("file as log_dir: err = %q", msg)
	}
}

func TestValidate_LogDirNotWritable(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root can write anywhere")
	}
	cfg := validAppFixture(t)
	dir := t.TempDir()
	os.Chmod(dir, 0o555)
	t.Cleanup(func() { os.Chmod(dir, 0o755) })
	cfg.Global.LogDir = dir

	msg, warnings := validateErrs(t, cfg)
	if msg != "" {
		t.Fatalf("non-root unwritable log_dir should only warn, got error %q", msg)
	}
	if !strings.Contains(strings.Join(warnings, "\n"), "not writable") {
		t.Errorf("warnings = %v, want a not-writable warning", warnings)
	}
}

func TestValidate_AppNameUnsafeForLogDir(t *testing.T) {
	cfg := validAppFixture(t)
	cfg.Global.LogDir = t.TempDir()
	cfg.Apps[0].Name = "../etc/x"
	if msg, _ := validateErrs(t, cfg); !strings.Contains(msg, "can't be used as a log file name") {
		t.Errorf("err = %q", msg)
	}

	// An explicit log_file makes any name fine.
	cfg.Apps[0].LogFile = filepath.Join(cfg.Global.LogDir, "x.log")
	if msg, _ := validateErrs(t, cfg); msg != "" {
		t.Errorf("with log_file: err = %q", msg)
	}
}

func TestValidate_AppNamedLqgCollidesWithDaemonLog(t *testing.T) {
	cfg := validAppFixture(t)
	cfg.Global.LogDir = t.TempDir()
	cfg.Apps[0].Name = "lqg"
	if msg, _ := validateErrs(t, cfg); !strings.Contains(msg, "already used by the daemon log") {
		t.Errorf("err = %q", msg)
	}
}

func TestValidate_LogFile(t *testing.T) {
	cfg := validAppFixture(t)
	cfg.Apps[0].LogFile = "shop.log"
	if msg, _ := validateErrs(t, cfg); !strings.Contains(msg, "log_file") || !strings.Contains(msg, "absolute") {
		t.Errorf("relative log_file: err = %q", msg)
	}

	cfg.Apps[0].LogFile = filepath.Join(t.TempDir(), "missing", "shop.log")
	if msg, _ := validateErrs(t, cfg); !strings.Contains(msg, "does not exist") {
		t.Errorf("log_file in missing dir: err = %q", msg)
	}

	// log_file alone (no log_dir) is valid.
	cfg.Apps[0].LogFile = filepath.Join(t.TempDir(), "shop.log")
	if msg, _ := validateErrs(t, cfg); msg != "" {
		t.Errorf("valid log_file: err = %q", msg)
	}
}

func TestValidate_TwoAppsSameLogFile(t *testing.T) {
	cfg := validAppFixture(t)
	second := cfg.Apps[0]
	second.Name = "other"
	cfg.Apps = append(cfg.Apps, second)
	shared := filepath.Join(t.TempDir(), "shared.log")
	cfg.Apps[0].LogFile = shared
	cfg.Apps[1].LogFile = shared
	if msg, _ := validateErrs(t, cfg); !strings.Contains(msg, "already used by apps[0]") {
		t.Errorf("err = %q", msg)
	}
}
