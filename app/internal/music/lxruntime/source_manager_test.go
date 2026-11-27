//go:build lxproduction

package lxruntime

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
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

func TestDetectLXScriptExtensionActionsAllowsSlowSynchronousBootstrap(t *testing.T) {
	source := `
const startedAt = Date.now();
while (Date.now() - startedAt < 3200) {}
lx.on(lx.EVENT_NAMES.request, async () => 'ok');
lx.send(lx.EVENT_NAMES.inited, {
  sources: { kw: { type: 'music', actions: ['search', 'musicUrl'], qualitys: ['128k'] } }
});
`
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	actions, sources, err := DetectLXScriptExtensionActions(ctx, ScriptInfo{Name: "slow-bootstrap"}, source, nil)
	if err != nil {
		t.Fatalf("capability detection rejected a script whose synchronous bootstrap takes slightly over 3s: %v", err)
	}
	if len(actions) != 1 || actions[0] != "search" {
		t.Fatalf("detected extension actions = %#v, want [search]", actions)
	}
	if len(sources) != 1 || sources[0] != "kw" {
		t.Fatalf("detected sources = %#v, want [kw]", sources)
	}
}
func TestDetectLXScriptCapabilitiesAcceptsNativeMusicURLOnly(t *testing.T) {
	source := `
lx.on(lx.EVENT_NAMES.request, async () => 'https://example.com/song.mp3');
lx.send(lx.EVENT_NAMES.inited, {
  sources: { kw: { type: 'music', actions: ['musicUrl'], qualitys: ['128k'] } }
});
`
	capabilities, err := DetectLXScriptCapabilities(context.Background(), ScriptInfo{Name: "native-music-url-only"}, source, nil)
	if err != nil { t.Fatalf("native musicUrl-only source should be recognized: %v", err) }
	if len(capabilities.SupportedSources) != 1 || capabilities.SupportedSources[0] != "kw" {
		t.Fatalf("supported sources = %#v, want [kw]", capabilities.SupportedSources)
	}
	if len(capabilities.NativeActions) != 1 || capabilities.NativeActions[0] != "musicUrl" {
		t.Fatalf("native actions = %#v, want [musicUrl]", capabilities.NativeActions)
	}
	if len(capabilities.ExtensionActions) != 0 {
		t.Fatalf("native musicUrl must not be misclassified as a community action: %#v", capabilities.ExtensionActions)
	}
}

func TestDetectLXScriptCapabilitiesRetainsActionsPerProvider(t *testing.T) {
	source := `
lx.on(lx.EVENT_NAMES.request, async () => 'https://example.com/song.mp3');
lx.send(lx.EVENT_NAMES.inited, {
  sources: {
    provider_with_playback: { type: 'music', actions: ['search', 'musicUrl'], qualitys: ['128k'] },
    search_only_provider: { type: 'music', actions: ['search'] }
  }
});
`
	capabilities, err := DetectLXScriptCapabilities(context.Background(), ScriptInfo{Name: "per-provider-actions"}, source, nil)
	if err != nil { t.Fatalf("detect provider actions: %v", err) }
	if got := strings.Join(capabilities.SourceActions["provider_with_playback"], ","); got != "musicUrl,search" {
		t.Fatalf("playback provider actions = %q, want musicUrl,search", got)
	}
	if got := strings.Join(capabilities.SourceActions["search_only_provider"], ","); got != "search" {
		t.Fatalf("search-only provider actions = %q, want search", got)
	}
	if len(capabilities.SourceActions) != 2 { t.Fatalf("per-provider action map = %#v", capabilities.SourceActions) }
}

func TestDetectLXScriptCapabilitiesRejectsUnknownActions(t *testing.T) {
	source := `
lx.on(lx.EVENT_NAMES.request, async () => 'ok');
lx.send(lx.EVENT_NAMES.inited, {
  sources: { kw: { type: 'music', actions: ['unsupportedAction'] } }
});
`
	_, err := DetectLXScriptCapabilities(context.Background(), ScriptInfo{Name: "unknown-action"}, source, nil)
	if err == nil || !strings.Contains(err.Error(), "没有声明可支持的 action") || !strings.Contains(err.Error(), "kw=[]") {
		t.Fatalf("unknown-only action declaration should fail with explicit post-handshake reason, got %v", err)
	}
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

func TestProductionSourceManagerStartCancelsInitialization(t *testing.T) {
	source := `
lx.on(lx.EVENT_NAMES.request, async () => 'https://cdn.example.test/song.mp3');
setTimeout(() => lx.send(lx.EVENT_NAMES.inited, {
  sources: { kw: { type: 'music', actions: ['musicUrl'], qualitys: ['128k'] } }
}), 30000);
`
	manager := newTestSourceManager(t, []LXSourceScript{{Info: ScriptInfo{Name: "startup-cancel-fixture", RawScript: source}, Source: source}})
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	started := time.Now()
	if err := manager.Start(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Start should stop waiting when its startup context expires, got %v", err)
	}
	if time.Since(started) > 2*time.Second {
		t.Fatalf("Start did not respect its startup context: %s", time.Since(started))
	}
	if err := manager.Close(); err != nil { t.Fatal(err) }
}

func TestProductionLXSourceManagerRetainsStartupInitializationFailureAndNeverRetriesOnPlayback(t *testing.T) {
	source := `throw new Error("fixture startup exploded");`
	manager := newTestSourceManager(t, []LXSourceScript{{Info:ScriptInfo{Name:"broken-startup"}, Source:source}})
	if err := manager.Start(context.Background()); err != nil { t.Fatalf("per-script failure should not abort music module startup: %v", err) }
	results := manager.InitializationResults()
	if len(results) != 1 || results[0].Status != "failed" || results[0].Stage != "initialize_script" || !strings.Contains(results[0].Error, "fixture startup exploded") {
		t.Fatalf("startup initialization result = %#v", results)
	}
	if _, err := manager.ResolvePlaybackURL(context.Background(), "kw", map[string]any{}); err == nil || !strings.Contains(err.Error(), "fixture startup exploded") {
		t.Fatalf("playback should report retained startup failure, not retry initialization: %v", err)
	}
	_ = manager.Close()
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


func TestProductionSourceManagerScopesCommunityActionsPerProvider(t *testing.T) {
	source := `
lx.on(lx.EVENT_NAMES.request, async ({ action }) => action === 'lyric' ? '[00:01.00]fixture' : 'https://cdn.example.test/song.mp3');
lx.send(lx.EVENT_NAMES.inited, {
  sources: {
    kw: { type: 'music', actions: ['musicUrl'], qualitys: ['128k'] },
    kg: { type: 'music', actions: ['musicUrl', 'lyric'], qualitys: ['128k'] }
  }
});
`
	manager := newTestSourceManager(t, []LXSourceScript{{
		Info: ScriptInfo{Name: "provider-scoped-actions"},
		Source: source,
		ExtensionActions: []string{"lyric"},
	}})
	if err := manager.Start(context.Background()); err != nil { t.Fatal(err) }
	defer manager.Close()

	// First dispatch initializes the script and discovers the per-provider
	// action table. The script must not receive kw/lyric because only kg
	// declared that community action.
	_, err := manager.DispatchExtensionAction(context.Background(), "kw", "lyric", nil)
	var unsupported interface{ LXExtensionSourceUnsupported() bool }
	if !errors.As(err, &unsupported) || !unsupported.LXExtensionSourceUnsupported() {
		t.Fatalf("kw lyric should be treated as provider-unsupported, got %v", err)
	}
	if manager.HasExtensionAction("kw", "lyric") {
		t.Fatal("kw must not advertise lyric merely because kg declares it")
	}
	if !manager.HasExtensionAction("kg", "lyric") {
		t.Fatal("kg should advertise its explicitly declared lyric action")
	}
	got, err := manager.DispatchExtensionAction(context.Background(), "kg", "lyric", nil)
	if err != nil { t.Fatal(err) }
	if got != "[00:01.00]fixture" { t.Fatalf("kg lyric = %#v", got) }
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
lx.on(lx.EVENT_NAMES.request, async ({ source, action, info }) => action === 'musicUrl' ? 'https://1.1.1.1/' + source + '.mp3' : source + ':' + action + ':' + String(info.value));
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


func TestProductionSourceManagerRequiresNativeMusicURLAdvertisement(t *testing.T) {
	source := `
lx.on(lx.EVENT_NAMES.request, async ({ action }) => action === 'search' ? 'community-search-ok' : 'https://1.1.1.1/should-not-run.mp3');
lx.send(lx.EVENT_NAMES.inited, { sources: { kw: { type: 'music', actions: [], qualitys: [] } } });
`
	manager := newTestSourceManager(t, []LXSourceScript{{Info: ScriptInfo{Name: "community-only"}, Source: source, ExtensionActions: []string{"search"}}})
	if err := manager.Start(context.Background()); err != nil { t.Fatal(err) }
	defer manager.Close()
	got, err := manager.DispatchExtensionAction(context.Background(), "kw", "search", nil)
	if err != nil || got != "community-search-ok" { t.Fatalf("community search action = %#v, %v", got, err) }
	if _, err := manager.ResolvePlaybackURL(context.Background(), "kw", nil); err == nil || !strings.Contains(err.Error(), "no LX scripts available") {
		t.Fatalf("native musicUrl must be rejected when not advertised, got %v", err)
	}
}


func TestSourceManagerAllowsAsyncHTTPResponseCallbackBeyondDefaultEvalTimeout(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"url":"https://1.1.1.1/async-callback.mp3"}`))
	}))
	defer server.Close()

	source := `
lx.on(lx.EVENT_NAMES.request, async ({ info }) => await new Promise((resolve, reject) => {
  lx.request(` + string(mustJSONQuote(server.URL)) + `, {}, (error, response) => {
    if (error) return reject(error);
    const started = Date.now();
    while (Date.now() - started < 3500) {}
    resolve(response.body.url);
  });
}));
lx.send(lx.EVENT_NAMES.inited, {
  sources: { kw: { type: 'music', actions: ['musicUrl'] } }
});
`
	manager, err := NewSourceManagerWithScripts(server.Client(), []LXSourceScript{{
		Info: ScriptInfo{Name: "slow async response callback"},
		Source: source,
	}})
	if err != nil { t.Fatal(err) }
	defer manager.Close()
	if err := manager.Start(context.Background()); err != nil { t.Fatal(err) }

	manager.mu.Lock()
	if len(manager.scripts) != 1 || manager.scripts[0].runtime == nil {
		manager.mu.Unlock()
		t.Fatal("source manager did not initialize its QuickJS runtime")
	}
	manager.scripts[0].runtime.allowPrivateNetworkForTests = true
	manager.mu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	value, err := manager.ResolvePlaybackURL(ctx, "kw", map[string]any{
		"musicInfo": map[string]any{"songmid": "fixture-song"},
		"type": "320k",
	})
	if err != nil { t.Fatalf("async response callback was interrupted by the default 3s eval watchdog: %v", err) }
	if value != "https://1.1.1.1/async-callback.mp3" {
		t.Fatalf("musicUrl result = %#v", value)
	}
}
