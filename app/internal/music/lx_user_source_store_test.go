package music

import (
 "os"
 "path/filepath"
 "testing"
 "strings"
 "net/url"
)

func TestLXUserSourceValidationPersistsAcrossStoreInstances(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "sources")
	store, err := NewLXUserSourceStore(dir)
	if err != nil { t.Fatal(err) }
	script := "/*\n * @name Persistent Validation\n */\nlx.send(lx.EVENT_NAMES.inited, { sources: {} });"
	metadata, err := store.ImportScript(script, "")
	if err != nil { t.Fatal(err) }
	result := LXSourceRuntimeResult{
		Initialized: true,
		Platforms: []string{"kw"},
		DeclaredSources: []LXDeclaredSource{{Key: "kw", Type: "music", Actions: []string{"musicUrl"}}},
		Handlers: []string{"request"},
	}
	if err := store.SetValidation(metadata.ID, result); err != nil { t.Fatal(err) }

	restartedStore, err := NewLXUserSourceStore(dir)
	if err != nil { t.Fatal(err) }
	reloaded, err := restartedStore.Get(metadata.ID)
	if err != nil { t.Fatal(err) }
	if reloaded.Validation == nil || !reloaded.Validation.Initialized {
		t.Fatalf("successful validation was not persisted: %#v", reloaded.Validation)
	}
	if reloaded.ValidatedAt == nil || reloaded.ValidatedAt.IsZero() {
		t.Fatalf("validation timestamp was not persisted: %#v", reloaded.ValidatedAt)
	}
	if len(reloaded.Validation.Platforms) != 1 || reloaded.Validation.Platforms[0] != "kw" {
		t.Fatalf("validation details were not persisted: %#v", reloaded.Validation)
	}
}

func TestLXUserSourceStoreImportsListsAndDeletes(t *testing.T) {
 store, err := NewLXUserSourceStore(filepath.Join(t.TempDir(), "sources"))
 if err != nil { t.Fatal(err) }
 script := "/*\n * @name Store Test\n * @author Test Author\n * @version 1.0\n */\nlx.send(lx.EVENT_NAMES.inited, { sources: {} });\n"
	metadata, err := store.ImportScript(script, "https://example.com/source.js")
 if err != nil { t.Fatal(err) }
 if metadata.Name != "Store Test" || metadata.SourceURL != "https://example.com/source.js" || !metadata.AllowShowUpdateAlert { t.Fatalf("unexpected imported metadata: %#v", metadata) }
 listed, err := store.List()
 if err != nil { t.Fatal(err) }
 if len(listed) != 1 || listed[0].ID != metadata.ID { t.Fatalf("unexpected source list: %#v", listed) }
 if _, err := os.Stat(filepath.Join(store.dir, metadata.ID+".js")); err != nil { t.Fatal(err) }
 if err := store.Delete(metadata.ID); err != nil { t.Fatal(err) }
 listed, err = store.List()
 if err != nil { t.Fatal(err) }
 if len(listed) != 0 { t.Fatalf("source still exists after deletion: %#v", listed) }
}

func TestValidateLXSourceURL(t *testing.T) {
	invalid := []string{
		"file:///C:/Windows/win.ini",
		"ftp://example.com/source.js",
		"http:///source.js",
		"/relative/source.js",
	}
	for _, raw := range invalid {
		t.Run(raw, func(t *testing.T) {
			parsed, err := url.Parse(raw)
			if err != nil { t.Fatal(err) }
			if err := validateLXSourceURL(parsed); err == nil { t.Fatalf("expected URL to be rejected: %s", raw) }
		})
	}
}

func TestValidateLXSourceURLAllowsLANDestinationsLikeElectron(t *testing.T) {
	for _, raw := range []string{"http://localhost/source.js", "http://127.0.0.1/source.js", "http://192.168.1.10/source.js"} {
		parsed, err := url.Parse(raw)
		if err != nil { t.Fatal(err) }
		if err := validateLXSourceURL(parsed); err != nil {
			t.Fatalf("LX-compatible HTTP destination %q was rejected: %v", raw, err)
		}
	}
}

func TestLXUserSourceStoreRejectsDuplicateScriptLikeLX(t *testing.T) {
	store, err := NewLXUserSourceStore(filepath.Join(t.TempDir(), "sources"))
	if err != nil { t.Fatal(err) }
	script := "/*\n * @name Duplicate Test\n */\nlx.send(lx.EVENT_NAMES.inited, { sources: {} });\n"
	if _, err := store.ImportScript(script, ""); err != nil { t.Fatal(err) }
	if _, err := store.ImportScript(script, ""); err == nil || !strings.Contains(err.Error(), "脚本内容与已有的源") {
		t.Fatalf("duplicate LX source script was not rejected: %v", err)
	}
}

func TestLXUserSourceStorePersistsActiveIDAndUpdateAlertPreference(t *testing.T) {
	store, err := NewLXUserSourceStore(filepath.Join(t.TempDir(), "sources"))
	if err != nil { t.Fatal(err) }
	script := "/*\n * @name State Test\n */\nlx.send(lx.EVENT_NAMES.inited, { sources: {} });"
	metadata, err := store.ImportScript(script, "")
	if err != nil { t.Fatal(err) }
	if err := store.SetActiveID(metadata.ID); err != nil { t.Fatal(err) }
	active, err := store.GetActiveID()
	if err != nil || active != metadata.ID { t.Fatalf("active ID = %q, err=%v", active, err) }
	if err := store.SetAllowShowUpdateAlert(metadata.ID, false); err != nil { t.Fatal(err) }
	updated, err := store.Get(metadata.ID)
	if err != nil || updated.AllowShowUpdateAlert { t.Fatalf("update alert setting = %v, err=%v", updated.AllowShowUpdateAlert, err) }
}

func TestLXUserSourceEnabledSearchSourcesPersistAndRejectUnsupportedKeys(t *testing.T) {
	store, err := NewLXUserSourceStore(filepath.Join(t.TempDir(), "sources"))
	if err != nil { t.Fatal(err) }
	script := "/*\\n * @name Search Selection\\n */\\nlx.send(lx.EVENT_NAMES.inited, { sources: {} });"
	metadata, err := store.ImportScript(script, "")
	if err != nil { t.Fatal(err) }
	allowed := []string{"kw", "kg", "tx"}
	if err := store.SetEnabledSearchSources(metadata.ID, allowed, []string{"kg", "tx"}); err != nil { t.Fatal(err) }
	restarted, err := NewLXUserSourceStore(store.dir)
	if err != nil { t.Fatal(err) }
	got, err := restarted.Get(metadata.ID)
	if err != nil { t.Fatal(err) }
	if len(got.EnabledSearchSources) != 2 || got.EnabledSearchSources[0] != "kg" || got.EnabledSearchSources[1] != "tx" { t.Fatalf("enabled search sources = %#v", got.EnabledSearchSources) }
	if err := restarted.SetEnabledSearchSources(metadata.ID, allowed, []string{"wy"}); err == nil { t.Fatal("unsupported search source was accepted") }
	if err := restarted.SetEnabledSearchSources(metadata.ID, allowed, []string{}); err != nil { t.Fatal(err) }
	got, err = restarted.Get(metadata.ID)
	if err != nil { t.Fatal(err) }
	if got.EnabledSearchSources == nil || len(got.EnabledSearchSources) != 0 { t.Fatalf("empty selection was not persisted distinctly from an unset selection: %#v", got.EnabledSearchSources) }
}

func TestSetEnabledSearchSourcesPreservesPriorityOrder(t *testing.T) {
	store, err := NewLXUserSourceStore(filepath.Join(t.TempDir(), "sources"))
	if err != nil { t.Fatal(err) }
	metadata, err := store.ImportScript("/*\n * @name Priority Test\n */\nvoid 0;", "")
	if err != nil { t.Fatal(err) }
	if err := store.SetEnabledSearchSources(metadata.ID, []string{"kw", "kg", "tx"}, []string{"tx", "kw", "kg"}); err != nil { t.Fatal(err) }
	got, err := store.Get(metadata.ID)
	if err != nil { t.Fatal(err) }
	want := []string{"tx", "kw", "kg"}
	if len(got.EnabledSearchSources) != len(want) { t.Fatalf("priority length = %v, want %v", got.EnabledSearchSources, want) }
	for i := range want { if got.EnabledSearchSources[i] != want[i] { t.Fatalf("priority = %v, want %v", got.EnabledSearchSources, want) } }
}
