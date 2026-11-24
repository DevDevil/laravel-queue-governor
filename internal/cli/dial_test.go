package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDialDaemon_MissingSocketSaysDaemonNotRunning(t *testing.T) {
	_, err := dialDaemon(filepath.Join(t.TempDir(), "lqg.sock"))
	if err == nil || !strings.Contains(err.Error(), "does not seem to be running") {
		t.Errorf("err = %v, want daemon-not-running hint", err)
	}
}

func TestDialDaemon_PermissionDeniedExplainsGroup(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root bypasses directory permissions")
	}
	dir := filepath.Join(t.TempDir(), "run")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(dir, 0o700) })

	_, err := dialDaemon(filepath.Join(dir, "lqg.sock"))
	if err == nil || !strings.Contains(err.Error(), "no permission") || !strings.Contains(err.Error(), "group") {
		t.Errorf("err = %v, want permission hint mentioning the group", err)
	}
}
