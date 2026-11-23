//go:build lxproduction && cgo

package desktop

import (
	"context"
	"testing"
)

func TestProductionLXScriptRuntimeAvailabilityRequiresImportedScripts(t *testing.T) {
	runtime := newLXScriptRuntime()
	if runtime == nil {
		t.Fatal("production LX runtime should be constructed in lxproduction builds")
	}
	if runtime.Available() {
		t.Fatal("production runtime without an imported script must not report resolver availability")
	}
	if err := runtime.Start(context.Background()); err != nil {
		t.Fatalf("start empty production runtime: %v", err)
	}
	if runtime.Available() {
		t.Fatal("starting an empty source manager must not report resolver availability")
	}
	if err := runtime.Close(); err != nil {
		t.Fatalf("close production runtime: %v", err)
	}
}
