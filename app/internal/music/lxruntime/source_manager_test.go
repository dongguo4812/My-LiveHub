//go:build lxproduction && cgo

package lxruntime

import (
	"context"
	"testing"
)

func TestProductionSourceManagerLifecycleGuards(t *testing.T) {
	manager := NewSourceManager(nil)
	if len(manager.scripts) != 3 {
		t.Fatalf("production script count = %d, want 3", len(manager.scripts))
	}
	for _, script := range manager.scripts {
		if script.info.RawScript != script.source || script.info.RawScript == "" {
			t.Fatalf("%s does not expose the exact embedded source bytes as RawScript", script.info.Name)
		}
	}
	if _, err := manager.ResolvePlaybackURL(context.Background(), "kw", map[string]any{}); err == nil {
		t.Fatal("resolve before Start must fail")
	}
	if err := manager.Start(context.Background()); err != nil {
		t.Fatalf("start production source manager: %v", err)
	}
	if err := manager.Start(context.Background()); err != nil {
		t.Fatalf("Start must be idempotent: %v", err)
	}
	if err := manager.Close(); err != nil {
		t.Fatalf("close production source manager: %v", err)
	}
	if err := manager.Close(); err != nil {
		t.Fatalf("Close must be idempotent: %v", err)
	}
	if _, err := manager.ResolvePlaybackURL(context.Background(), "kw", map[string]any{}); err == nil {
		t.Fatal("resolve after Close must fail")
	}
	if err := manager.Start(context.Background()); err == nil {
		t.Fatal("Start after Close must fail")
	}
}
