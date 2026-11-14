package core

import (
	"context"
	"errors"
	"reflect"
	"testing"
)

type testModule struct {
	name      string
	calls     *[]string
	startFail error
	stopFail  error
}

func (m testModule) Start(context.Context) error {
	*m.calls = append(*m.calls, "start:"+m.name)
	return m.startFail
}

func (m testModule) Stop(context.Context) error {
	*m.calls = append(*m.calls, "stop:"+m.name)
	return m.stopFail
}

func TestModuleManagerStartsInOrderAndStopsInReverse(t *testing.T) {
	manager := NewModuleManager()
	var calls []string
	for _, name := range []string{"first", "second", "third"} {
		if err := manager.Register(name, testModule{name: name, calls: &calls}); err != nil {
			t.Fatal(err)
		}
	}
	if err := manager.StartAll(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := manager.StopAll(context.Background()); err != nil {
		t.Fatal(err)
	}
	want := []string{"start:first", "start:second", "start:third", "stop:third", "stop:second", "stop:first"}
	if !reflect.DeepEqual(calls, want) {
		t.Fatalf("calls = %v, want %v", calls, want)
	}
	for _, item := range manager.Snapshot() {
		if item.Status != ModuleStopped {
			t.Errorf("module %q status = %q, want stopped", item.Name, item.Status)
		}
	}
}

func TestModuleManagerRollsBackAfterStartupFailure(t *testing.T) {
	manager := NewModuleManager()
	var calls []string
	startFailure := errors.New("startup failed")
	_ = manager.Register("first", testModule{name: "first", calls: &calls})
	_ = manager.Register("second", testModule{name: "second", calls: &calls, startFail: startFailure})
	_ = manager.Register("third", testModule{name: "third", calls: &calls})

	err := manager.StartAll(context.Background())
	if !errors.Is(err, startFailure) {
		t.Fatalf("StartAll error = %v, want wrapped startup failure", err)
	}
	want := []string{"start:first", "start:second", "stop:first"}
	if !reflect.DeepEqual(calls, want) {
		t.Fatalf("calls = %v, want %v", calls, want)
	}
	statuses := manager.Snapshot()
	if statuses[0].Status != ModuleStopped || statuses[1].Status != ModuleFailed || statuses[2].Status != ModuleRegistered {
		t.Fatalf("unexpected module statuses after rollback: %+v", statuses)
	}
}

func TestModuleManagerRejectsDuplicateAndLateRegistration(t *testing.T) {
	manager := NewModuleManager()
	var calls []string
	module := testModule{name: "one", calls: &calls}
	if err := manager.Register("one", module); err != nil {
		t.Fatal(err)
	}
	if err := manager.Register("one", module); err == nil {
		t.Fatal("expected duplicate registration to fail")
	}
	if err := manager.StartAll(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := manager.Register("two", module); err == nil {
		t.Fatal("expected registration after startup to fail")
	}
}

func TestModuleManagerContinuesStoppingAfterError(t *testing.T) {
	manager := NewModuleManager()
	var calls []string
	stopFailure := errors.New("stop failed")
	_ = manager.Register("first", testModule{name: "first", calls: &calls})
	_ = manager.Register("second", testModule{name: "second", calls: &calls, stopFail: stopFailure})
	if err := manager.StartAll(context.Background()); err != nil {
		t.Fatal(err)
	}
	err := manager.StopAll(context.Background())
	if !errors.Is(err, stopFailure) {
		t.Fatalf("StopAll error = %v, want wrapped stop failure", err)
	}
	want := []string{"start:first", "start:second", "stop:second", "stop:first"}
	if !reflect.DeepEqual(calls, want) {
		t.Fatalf("calls = %v, want %v", calls, want)
	}
	statuses := manager.Snapshot()
	if statuses[0].Status != ModuleStopped || statuses[1].Status != ModuleFailed {
		t.Fatalf("unexpected module statuses after shutdown: %+v", statuses)
	}
}

func TestModuleManagerControlsModulesIndependentlyAndAllowsRestart(t *testing.T) {
	manager := NewModuleManager()
	var calls []string
	_ = manager.Register("voice", testModule{name: "voice", calls: &calls})
	_ = manager.Register("music", testModule{name: "music", calls: &calls})
	if err := manager.StartAll(context.Background()); err != nil { t.Fatal(err) }
	if err := manager.StopModule(context.Background(), "voice"); err != nil { t.Fatal(err) }
	statuses := manager.Snapshot()
	if statuses[0].Status != ModuleStopped || statuses[1].Status != ModuleRunning {
		t.Fatalf("stopping voice changed unexpected module states: %+v", statuses)
	}
	if err := manager.StartModule(context.Background(), "voice"); err != nil { t.Fatal(err) }
	if got := manager.Snapshot()[0].Status; got != ModuleRunning { t.Fatalf("restarted voice status = %q, want running", got) }
	if err := manager.StopAll(context.Background()); err != nil { t.Fatal(err) }
	want := []string{"start:voice", "start:music", "stop:voice", "start:voice", "stop:music", "stop:voice"}
	if !reflect.DeepEqual(calls, want) { t.Fatalf("calls = %v, want %v", calls, want) }
}


type manualStartTestModule struct { testModule }

func (manualStartTestModule) StartOnApplicationLaunch() bool { return false }

func TestModuleManagerKeepsManualModulesStoppedUntilRequested(t *testing.T) {
	manager := NewModuleManager()
	var calls []string
	if err := manager.Register("manual", manualStartTestModule{testModule{name: "manual", calls: &calls}}); err != nil { t.Fatal(err) }
	if err := manager.Register("automatic", testModule{name: "automatic", calls: &calls}); err != nil { t.Fatal(err) }
	if err := manager.StartAll(context.Background()); err != nil { t.Fatal(err) }
	statuses := manager.Snapshot()
	if statuses[0].Status != ModuleStopped || statuses[1].Status != ModuleRunning { t.Fatalf("unexpected startup statuses: %+v", statuses) }
	if !reflect.DeepEqual(calls, []string{"start:automatic"}) { t.Fatalf("startup calls = %v", calls) }
	if err := manager.StartModule(context.Background(), "manual"); err != nil { t.Fatal(err) }
	if err := manager.StopModule(context.Background(), "manual"); err != nil { t.Fatal(err) }
	if !reflect.DeepEqual(calls, []string{"start:automatic", "start:manual", "stop:manual"}) { t.Fatalf("manual lifecycle calls = %v", calls) }
	if err := manager.StopAll(context.Background()); err != nil { t.Fatal(err) }
}
