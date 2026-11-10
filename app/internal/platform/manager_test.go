package platform

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
)

type simulatedAdapter struct {
	id           string
	liveStatus   string
	calls        *[]string
	connectError error
	stopError    error
	failureHandler func(error)
}

func (a *simulatedAdapter) ID() string { return a.id }
func (a *simulatedAdapter) LiveStatus() string { return a.liveStatus }
func (a *simulatedAdapter) SetConnectionFailureHandler(handler func(error)) { a.failureHandler = handler }
func (a *simulatedAdapter) fail(err error) { if a.failureHandler != nil { a.failureHandler(err) } }

func (a *simulatedAdapter) Connect(context.Context) error {
	*a.calls = append(*a.calls, "connect:"+a.id)
	return a.connectError
}

func (a *simulatedAdapter) Disconnect(context.Context) error {
	*a.calls = append(*a.calls, "disconnect:"+a.id)
	return a.stopError
}

func TestManagerConnectsAndDisconnectsAdapter(t *testing.T) {
	manager := NewManager()
	var calls []string
	if err := manager.Register(&simulatedAdapter{id: "sample", calls: &calls}); err != nil {
		t.Fatal(err)
	}
	if err := manager.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := manager.Connect(context.Background(), "sample"); err != nil {
		t.Fatal(err)
	}
	if err := manager.Connect(context.Background(), "sample"); err != nil {
		t.Fatalf("repeated connect should be idempotent: %v", err)
	}
	if got := manager.Snapshot()[0].Status; got != StatusConnected {
		t.Fatalf("status = %q, want %q", got, StatusConnected)
	}
	if err := manager.Disconnect(context.Background(), "sample"); err != nil {
		t.Fatal(err)
	}
	if err := manager.Disconnect(context.Background(), "sample"); err != nil {
		t.Fatalf("repeated disconnect should be idempotent: %v", err)
	}
	if got, want := calls, []string{"connect:sample", "disconnect:sample"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("calls = %v, want %v", got, want)
	}
	if got := manager.Snapshot()[0].Status; got != StatusDisconnected {
		t.Fatalf("final status = %q, want %q", got, StatusDisconnected)
	}
}

func TestManagerReportsConnectionFailureAndAllowsRetry(t *testing.T) {
	manager := NewManager()
	var calls []string
	failure := errors.New("simulated connection failure")
	adapter := &simulatedAdapter{id: "sample", calls: &calls, connectError: failure}
	if err := manager.Register(adapter); err != nil {
		t.Fatal(err)
	}
	if err := manager.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := manager.Connect(context.Background(), "sample"); !errors.Is(err, failure) {
		t.Fatalf("Connect error = %v, want wrapped failure", err)
	}
	snapshot := manager.Snapshot()[0]
	if snapshot.Status != StatusFailed || snapshot.Error == "" {
		t.Fatalf("unexpected failed snapshot: %+v", snapshot)
	}
	adapter.connectError = nil
	if err := manager.Connect(context.Background(), "sample"); err != nil {
		t.Fatal(err)
	}
	if got := manager.Snapshot()[0].Status; got != StatusConnected {
		t.Fatalf("status after retry = %q, want %q", got, StatusConnected)
	}
}

func TestManagerLifecycleDisconnectsAdaptersInReverseOrder(t *testing.T) {
	manager := NewManager()
	var calls []string
	for _, id := range []string{"first", "second"} {
		if err := manager.Register(&simulatedAdapter{id: id, calls: &calls}); err != nil {
			t.Fatal(err)
		}
	}
	if err := manager.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"first", "second"} {
		if err := manager.Connect(context.Background(), id); err != nil {
			t.Fatal(err)
		}
	}
	if err := manager.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	want := []string{"connect:first", "connect:second", "disconnect:second", "disconnect:first"}
	if !reflect.DeepEqual(calls, want) {
		t.Fatalf("calls = %v, want %v", calls, want)
	}
	for _, snapshot := range manager.Snapshot() {
		if snapshot.Status != StatusDisconnected {
			t.Errorf("platform %q status = %q, want disconnected", snapshot.PlatformID, snapshot.Status)
		}
	}
}

func TestManagerRejectsUnknownAdapterAndLateRegistration(t *testing.T) {
	manager := NewManager()
	var calls []string
	adapter := &simulatedAdapter{id: "sample", calls: &calls}
	if err := manager.Register(adapter); err != nil {
		t.Fatal(err)
	}
	if err := manager.Register(adapter); err == nil {
		t.Fatal("expected duplicate adapter registration to fail")
	}
	if err := manager.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := manager.Connect(context.Background(), "missing"); err == nil {
		t.Fatal("expected unknown adapter to fail")
	}
	if err := manager.Register(&simulatedAdapter{id: "late", calls: &calls}); err == nil {
		t.Fatal("expected registration after startup to fail")
	}
}

func TestManagerContinuesShutdownAfterDisconnectFailure(t *testing.T) {
	manager := NewManager()
	var calls []string
	failure := errors.New("simulated disconnect failure")
	_ = manager.Register(&simulatedAdapter{id: "first", calls: &calls})
	_ = manager.Register(&simulatedAdapter{id: "second", calls: &calls, stopError: failure})
	if err := manager.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := manager.Connect(context.Background(), "first"); err != nil {
		t.Fatal(err)
	}
	if err := manager.Connect(context.Background(), "second"); err != nil {
		t.Fatal(err)
	}
	if err := manager.Stop(context.Background()); !errors.Is(err, failure) {
		t.Fatalf("Stop error = %v, want wrapped disconnect failure", err)
	}
	if got, want := calls, []string{"connect:first", "connect:second", "disconnect:second", "disconnect:first"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("calls = %v, want %v", got, want)
	}
	snapshots := manager.Snapshot()
	if snapshots[0].Status != StatusDisconnected || snapshots[1].Status != StatusFailed {
		t.Fatalf("unexpected shutdown snapshots: %+v", snapshots)
	}
}


func TestManagerSnapshotIncludesOptionalLiveStatus(t *testing.T) {
	manager := NewManager()
	var calls []string
	if err := manager.Register(&simulatedAdapter{id: "bilibili", calls: &calls, liveStatus: "live"}); err != nil { t.Fatal(err) }
	if err := manager.Start(context.Background()); err != nil { t.Fatal(err) }
	snapshot := manager.Snapshot()[0]
	if snapshot.LiveStatus != "live" { t.Fatalf("live status = %q, want live", snapshot.LiveStatus) }
}

func TestManagerMarksUnexpectedAdapterDisconnectAsFailed(t *testing.T) {
	manager := NewManager()
	var calls []string
	adapter := &simulatedAdapter{id: "sample", calls: &calls}
	if err := manager.Register(adapter); err != nil { t.Fatal(err) }
	if err := manager.Start(context.Background()); err != nil { t.Fatal(err) }
	if err := manager.Connect(context.Background(), "sample"); err != nil { t.Fatal(err) }
	adapter.fail(errors.New("stream stopped"))
	snapshot := manager.Snapshot()[0]
	if snapshot.Status != StatusFailed || !strings.Contains(snapshot.Error, "stream stopped") {
		t.Fatalf("unexpected connection failure snapshot: %+v", snapshot)
	}
}
