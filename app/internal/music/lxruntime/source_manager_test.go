//go:build lxproduction && cgo

package lxruntime

import (
	"context"
	"strings"
	"testing"
	"time"
)

func newTestSourceManager(t *testing.T, scripts []LXSourceScript) *SourceManager {
	t.Helper()
	manager, err := NewSourceManagerWithScripts(nil, scripts)
	if err != nil { t.Fatal(err) }
	return manager
}

func TestProductionSourceManagerLifecycleGuards(t *testing.T) {
	manager := newTestSourceManager(t, []LXSourceScript{
		{Info: ScriptInfo{Name: "fixture-one"}, Source: "lx.on('request', () => 'https://1.1.1.1/a.mp3');"},
		{Info: ScriptInfo{Name: "fixture-two"}, Source: "lx.on('request', () => 'https://1.1.1.1/b.mp3');"},
	})
	if len(manager.scripts) != 2 {
		t.Fatalf("registered script count = %d, want 2", len(manager.scripts))
	}
	for _, script := range manager.scripts {
		if script.info.RawScript != script.source || script.info.RawScript == "" {
			t.Fatalf("%s does not expose the exact registered source bytes as RawScript", script.info.Name)
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
	if err := manager.Start(context.Background()); err != nil { t.Fatalf("empty manager Start should be safe: %v", err) }
	if _, err := manager.ResolvePlaybackURL(context.Background(), "kw", map[string]any{}); err == nil || !strings.Contains(err.Error(), "no LX scripts available") {
		t.Fatalf("empty manager should report missing imported scripts, got %v", err)
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
	manager := newTestSourceManager(t, []LXSourceScript{{Info: ScriptInfo{Name: "shutdown-cancel-fixture", RawScript: source}, Source: source}})
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
	manager := newTestSourceManager(t, []LXSourceScript{{Info: ScriptInfo{Name: "private-url-fixture"}, Source: source}})
	if err := manager.Start(context.Background()); err != nil { t.Fatal(err) }
	defer manager.Close()
	_, err := manager.ResolvePlaybackURL(context.Background(), "kw", map[string]any{})
	if err == nil || !strings.Contains(err.Error(), "not public") {
		t.Fatalf("loopback playback URL should be rejected, got %v", err)
	}
}


func TestProductionSourceManagerCommunityExtensionsAreExplicit(t *testing.T) {
	source := `
lx.on(lx.EVENT_NAMES.request, async ({ action, info }) => action + ':' + String(info.value));
lx.send(lx.EVENT_NAMES.inited, {
  sources: { kw: { type: 'music', actions: ['musicUrl'], qualitys: ['128k'] } }
});
`
	manager := newTestSourceManager(t, []LXSourceScript{{
		Info: ScriptInfo{Name: "community-extension-fixture"},
		Source: source,
		ExtensionActions: []string{"search", "detail", "lyric", "pic"},
	}})
	if err := manager.Start(context.Background()); err != nil { t.Fatal(err) }
	defer manager.Close()
	for _, action := range []string{"search", "detail", "lyric", "pic"} {
		got, err := manager.DispatchExtensionAction(context.Background(), "kw", action, map[string]any{"value": "fixture"})
		if err != nil { t.Fatalf("%s extension action failed: %v", action, err) }
		if got != action+":fixture" { t.Fatalf("%s extension result = %#v", action, got) }
	}
	if _, err := manager.DispatchExtensionAction(context.Background(), "kw", "musicUrl", map[string]any{}); err == nil {
		t.Fatal("musicUrl must remain on the official native action path, not the community extension dispatcher")
	}
}

func TestProductionSourceManagerDoesNotImplicitlyEnableCommunityExtensions(t *testing.T) {
	source := `
lx.on(lx.EVENT_NAMES.request, async ({ action }) => action);
lx.send(lx.EVENT_NAMES.inited, {
  sources: { kw: { type: 'music', actions: ['musicUrl'], qualitys: ['128k'] } }
});
`
	manager := newTestSourceManager(t, []LXSourceScript{{Info: ScriptInfo{Name: "no-extensions"}, Source: source}})
	if err := manager.Start(context.Background()); err != nil { t.Fatal(err) }
	defer manager.Close()
	if _, err := manager.DispatchExtensionAction(context.Background(), "kw", "search", nil); err == nil || !strings.Contains(err.Error(), "explicitly enabled") {
		t.Fatalf("undeclared community extension should be rejected, got %v", err)
	}
}


func TestProductionSourceManagerEnforcesScriptResourceLimits(t *testing.T) {
	if _, err := NewSourceManagerWithScripts(nil, []LXSourceScript{{Source: strings.Repeat("x", maxLXScriptSourceBytes+1)}}); err == nil || !strings.Contains(err.Error(), "byte limit") {
		t.Fatalf("oversized script should be rejected, got %v", err)
	}
	tooMany := make([]LXSourceScript, maxLXImportedScripts+1)
	for i := range tooMany { tooMany[i].Source = "lx.send('inited', {});" }
	if _, err := NewSourceManagerWithScripts(nil, tooMany); err == nil || !strings.Contains(err.Error(), "too many imported") {
		t.Fatalf("excessive script count should be rejected, got %v", err)
	}

	total := make([]LXSourceScript, 9)
	for i := range total { total[i].Source = strings.Repeat("x", maxLXScriptSourceBytes) }
	if _, err := NewSourceManagerWithScripts(nil, total); err == nil || !strings.Contains(err.Error(), "total imported") {
		t.Fatalf("excessive total source size should be rejected, got %v", err)
	}
	invalidExtension := []LXSourceScript{{Source: "lx.send('inited', {});", ExtensionActions: []string{"shell"}}}
	if _, err := NewSourceManagerWithScripts(nil, invalidExtension); err == nil || !strings.Contains(err.Error(), "unsupported community extension") {
		t.Fatalf("unknown extension capability should be rejected, got %v", err)
	}
}


func TestProductionSourceManagerRejectsMismatchedRawScriptMetadata(t *testing.T) {
	_, err := NewSourceManagerWithScripts(nil, []LXSourceScript{{
		Info: ScriptInfo{Name: "mismatched", RawScript: "different executable bytes"},
		Source: "lx.send('inited', {});",
	}})
	if err == nil || !strings.Contains(err.Error(), "does not match executable source") {
		t.Fatalf("mismatched RawScript metadata must be rejected, got %v", err)
	}
}


func TestProductionSourceManagerAvailabilityReflectsImportedScripts(t *testing.T) {
	empty := NewSourceManager(nil)
	if empty.HasScripts() { t.Fatal("empty source manager reported imported scripts") }
	_ = empty.Close()

	manager := newTestSourceManager(t, []LXSourceScript{{Info: ScriptInfo{Name: "available"}, Source: "lx.send('inited', {});"}})
	if !manager.HasScripts() { t.Fatal("source manager did not report its imported script") }
	if err := manager.Close(); err != nil { t.Fatal(err) }
	if manager.HasScripts() { t.Fatal("closed source manager reported available scripts") }
}


func TestProductionSourceManagerRoutesCommunityActionsToMatchingSourceScript(t *testing.T) {
	makeScript := func(name, source string) LXSourceScript {
		return LXSourceScript{Info: ScriptInfo{Name: name}, Source: source, ExtensionActions: []string{"search", "detail", "lyric", "pic"}}
	}
	kw := `
lx.on(lx.EVENT_NAMES.request, async ({ source, action, info }) => action === 'musicUrl' ? 'https://1.1.1.1/' + source + '.mp3' : source + ':' + action + ':' + String(info.value));
lx.send(lx.EVENT_NAMES.inited, { sources: { kw: { type: 'music', actions: ['musicUrl'], qualitys: ['128k'] } } });
`
	kg := `
lx.on(lx.EVENT_NAMES.request, async ({ source, action, info }) => source + ':' + action + ':' + String(info.value));
lx.send(lx.EVENT_NAMES.inited, { sources: { kg: { type: 'music', actions: ['musicUrl'], qualitys: ['128k'] } } });
`
	manager := newTestSourceManager(t, []LXSourceScript{makeScript("kw-only", kw), makeScript("kg-only", kg)})
	if err := manager.Start(context.Background()); err != nil { t.Fatal(err) }
	defer manager.Close()

	for _, source := range []string{"kw", "kg"} {
		for _, action := range []string{"search", "detail", "lyric", "pic"} {
			if !manager.HasExtensionAction(source, action) { t.Fatalf("%s/%s was not advertised as a candidate", source, action) }
			got, err := manager.DispatchExtensionAction(context.Background(), source, action, map[string]any{"value": "fixture"})
			if err != nil { t.Fatalf("dispatch %s/%s: %v", source, action, err) }
			want := source + ":" + action + ":fixture"
			if got != want { t.Fatalf("%s/%s dispatched to wrong script: got %#v, want %q", source, action, got, want) }
		}
	}
	for _, source := range []string{"kw", "kg"} {
		got, err := manager.ResolvePlaybackURL(context.Background(), source, map[string]any{})
		want := "https://1.1.1.1/" + source + ".mp3"
		if err != nil || got != want { t.Fatalf("native musicUrl dispatch for %s = %#v, %v; want %q", source, got, err, want) }
	}
	if manager.HasExtensionAction("kw", "musicUrl") { t.Fatal("native musicUrl must not be advertised as a community extension") }
	if manager.HasExtensionAction("wy", "search") { t.Fatal("unadvertised source must not claim community search capability after initialization") }
	_, err := manager.DispatchExtensionAction(context.Background(), "wy", "search", map[string]any{"value": "fixture"})
	var unsupported interface { LXExtensionSourceUnsupported() bool }
	if !errors.As(err, &unsupported) || !unsupported.LXExtensionSourceUnsupported() {
		t.Fatalf("unadvertised source error = %v, want source-unsupported classification", err)
	}
}
