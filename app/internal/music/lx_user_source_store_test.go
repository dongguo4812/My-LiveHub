package music

import (
 "os"
 "path/filepath"
 "testing"
 "strings"
 "net/url"
)

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
	script := "/*\\n * @name State Test\\n */\\nlx.send(lx.EVENT_NAMES.inited, { sources: {} });"
	metadata, err := store.ImportScript(script, "")
	if err != nil { t.Fatal(err) }
	if err := store.SetActiveID(metadata.ID); err != nil { t.Fatal(err) }
	active, err := store.GetActiveID()
	if err != nil || active != metadata.ID { t.Fatalf("active ID = %q, err=%v", active, err) }
	if err := store.SetAllowShowUpdateAlert(metadata.ID, false); err != nil { t.Fatal(err) }
	updated, err := store.Get(metadata.ID)
	if err != nil || updated.AllowShowUpdateAlert { t.Fatalf("update alert setting = %v, err=%v", updated.AllowShowUpdateAlert, err) }
}
