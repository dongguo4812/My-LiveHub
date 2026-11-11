package music

import (
 "os"
 "path/filepath"
 "testing"
 "net/url"
)

func TestLXUserSourceStoreImportsListsAndDeletes(t *testing.T) {
 store, err := NewLXUserSourceStore(filepath.Join(t.TempDir(), "sources"))
 if err != nil { t.Fatal(err) }
 script := "/*\n * @name Store Test\n * @author Test Author\n * @version 1.0\n */\nlx.send(lx.EVENT_NAMES.inited, { sources: {} });\n"
	metadata, err := store.ImportScript(script, "https://example.com/source.js")
 if err != nil { t.Fatal(err) }
 if metadata.Name != "Store Test" || metadata.SourceURL != "https://example.com/source.js" { t.Fatalf("unexpected imported metadata: %#v", metadata) }
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
  "http://localhost/source.js",
  "http://127.0.0.1/source.js",
  "http://192.168.1.1/source.js",
  "https://user:pass@example.com/source.js",
 }
 for _, raw := range invalid {
  t.Run(raw, func(t *testing.T) {
   parsed, err := url.Parse(raw)
   if err != nil { t.Fatal(err) }
   if err := validateLXSourceURL(parsed); err == nil { t.Fatalf("expected URL to be rejected: %s", raw) }
  })
 }
}
