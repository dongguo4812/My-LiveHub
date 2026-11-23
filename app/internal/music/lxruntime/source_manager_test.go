//go:build lxproduction && cgo

package lxruntime

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestProductionSourceManagerLifecycleGuards(t *testing.T) {
	manager := NewSourceManagerWithScripts(nil, []LXSourceScript{
		{Info: ScriptInfo{Name: "fixture-one"}, Source: "lx.on('request', () => 'https://1.1.1.1/a.mp3');"},
		{Info: ScriptInfo{Name: "fixture-two"}, Source: "lx.on('request', () => 'https://1.1.1.1/b.mp3');"},
	})
	if len(manager.scripts) != 2 {
		t.Fatalf("registered script count = %d, want 2", len(manager.scripts))
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


func TestProductionSourceManagerUsesLXRequestTimeoutBudget(t *testing.T) {
	manager := NewSourceManager(nil)
	if len(manager.scripts) != 0 { t.Fatalf("default manager unexpectedly bundled %d test/community scripts", len(manager.scripts)) }
	if manager.client.Timeout != lxMaxRequestTimeout {
		t.Fatalf("default source HTTP client timeout = %s, want %s", manager.client.Timeout, lxMaxRequestTimeout)
	}
	_ = manager.Close()
}

func TestProductionSourceManagerCloseCancelsInitialization(t *testing.T) {
	 source := `
lx.on(lx.EVENT_NAMES.request, async () => 'https://cdn.example.test/song.mp3');
setTimeout(() => lx.send(lx.EVENT_NAMES.inited, {
  sources: { kw: { type: 'music', actions: ['musicUrl'], qualitys: ['128k'] } }
}), 30000);
`
	manager := NewSourceManagerWithScripts(nil, []LXSourceScript{{Info: ScriptInfo{Name: "shutdown-cancel-fixture", RawScript: source}, Source: source}})
	if err := manager.Start(context.Background()); err != nil { t.Fatal(err) }

	done := make(chan error, 1)
	go func() {
		_, err := manager.ResolvePlaybackURL(context.Background(), "kw", map[string]any{})
		done <- err
	}()
	time.Sleep(100 * time.Millisecond)
	started := time.Now()
	if err := manager.Close(); err != nil { t.Fatal(err) }
	if time.Since(started) > 2*time.Second { t.Fatalf("Close waited too long for in-flight initialization: %s", time.Since(started)) }
	select {
	case err := <-done:
		if err == nil { t.Fatal("ResolvePlaybackURL unexpectedly succeeded after manager shutdown") }
	case <-time.After(2 * time.Second):
		t.Fatal("ResolvePlaybackURL did not exit after manager shutdown")
	}
}


func TestProductionSourceManagerRejectsLoopbackPlaybackURL(t *testing.T) {
	source := `
lx.on(lx.EVENT_NAMES.request, async () => 'http://127.0.0.1:23330/private-audio');
lx.send(lx.EVENT_NAMES.inited, {
  sources: { kw: { type: 'music', actions: ['musicUrl'], qualitys: ['128k'] } }
});
`
	manager := NewSourceManagerWithScripts(nil, []LXSourceScript{{Info: ScriptInfo{Name: "private-url-fixture"}, Source: source}})
	if err := manager.Start(context.Background()); err != nil { t.Fatal(err) }
	defer manager.Close()
	_, err := manager.ResolvePlaybackURL(context.Background(), "kw", map[string]any{})
	if err == nil || !strings.Contains(err.Error(), "not public") {
		t.Fatalf("loopback playback URL should be rejected, got %v", err)
	}
}
