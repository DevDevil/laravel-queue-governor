package cli

import (
	"path/filepath"
	"testing"

	"github.com/DevDevil/laravel-queue-governor/internal/ipc"
	"github.com/DevDevil/laravel-queue-governor/internal/monitor"
)

func TestFetchStatus_RoundTripsAgainstRealServer(t *testing.T) {
	path := filepath.Join(t.TempDir(), "lqg.sock")

	srv := &monitor.Server{Snapshots: monitor.NewSnapshotStore(), Bus: monitor.NewBus()}
	if err := srv.Listen(path); err != nil {
		t.Fatalf("Listen: %v", err)
	}
	t.Cleanup(func() { srv.Close() })
	go srv.Serve()

	reason := "app_load_high"
	srv.Snapshots.Update(ipc.StatusSnapshot{
		Apps: []ipc.AppStatus{{Name: "wonen31", State: "throttled", Load: 5.1, IOWait: 9, Reason: &reason}},
	})

	snap, err := FetchStatus(path)
	if err != nil {
		t.Fatalf("FetchStatus: %v", err)
	}
	if len(snap.Apps) != 1 || snap.Apps[0].Name != "wonen31" || snap.Apps[0].Reason == nil || *snap.Apps[0].Reason != "app_load_high" {
		t.Errorf("FetchStatus result = %+v, want the server's snapshot", snap)
	}
}

func TestFetchStatus_NoServerListening(t *testing.T) {
	path := filepath.Join(t.TempDir(), "does-not-exist.sock")

	if _, err := FetchStatus(path); err == nil {
		t.Fatal("FetchStatus: expected an error when nothing is listening, got nil")
	}
}

func TestSendSet_RoundTripsAgainstRealServer(t *testing.T) {
	path := filepath.Join(t.TempDir(), "lqg.sock")

	srv := &monitor.Server{Snapshots: monitor.NewSnapshotStore(), Bus: monitor.NewBus()}
	var gotReq ipc.SetRequest
	srv.Tune = func(scope, app, field, value string) ([]string, error) {
		gotReq = ipc.SetRequest{Scope: scope, App: app, Field: field, Value: value}
		return nil, nil
	}
	if err := srv.Listen(path); err != nil {
		t.Fatalf("Listen: %v", err)
	}
	t.Cleanup(func() { srv.Close() })
	go srv.Serve()

	resp, err := SendSet(path, ipc.SetRequest{Scope: "app", App: "wonen31", Field: ipc.FieldMaxLoad, Value: "5.0"})
	if err != nil {
		t.Fatalf("SendSet: %v", err)
	}
	if !resp.OK {
		t.Errorf("SetResponse.OK = false, want true (error=%q)", resp.Error)
	}
	if gotReq.Scope != "app" || gotReq.App != "wonen31" || gotReq.Field != ipc.FieldMaxLoad || gotReq.Value != "5.0" {
		t.Errorf("server received %+v, want scope=app app=wonen31 field=max_load value=5.0", gotReq)
	}
}

func TestSendSet_NoServerListening(t *testing.T) {
	path := filepath.Join(t.TempDir(), "does-not-exist.sock")

	if _, err := SendSet(path, ipc.SetRequest{Scope: "global", Field: ipc.FieldNice, Value: "0"}); err == nil {
		t.Fatal("SendSet: expected an error when nothing is listening, got nil")
	}
}
