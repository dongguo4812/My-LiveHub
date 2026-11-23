//go:build lxproduction && cgo

package desktop

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"testing"
	"testing/fstest"
	"strings"

	"github.com/dongguo4812/My-LiveHub/app/internal/config"
)

func testLXConfigManager(t *testing.T) *config.Manager {
	t.Helper()
	manager, err := config.NewManager(fstest.MapFS{"empty.json": &fstest.MapFile{Data: []byte("{}")}}, t.TempDir())
	if err != nil { t.Fatal(err) }
	return manager
}

func TestProductionLXScriptRuntimeAvailabilityRequiresImportedScripts(t *testing.T) {
	runtime := newLXScriptRuntime(testLXConfigManager(t))
	if runtime == nil { t.Fatal("production LX runtime should be constructed in lxproduction builds") }
	if runtime.Available() { t.Fatal("runtime without an imported script must not report resolver availability") }
	if err := runtime.Start(context.Background()); err != nil { t.Fatalf("start empty production runtime: %v", err) }
	if runtime.Available() { t.Fatal("starting an empty source manager must not report resolver availability") }
	if err := runtime.Close(); err != nil { t.Fatalf("close production runtime: %v", err) }
}

func TestProductionLXScriptRuntimeImportRestoreEnableDelete(t *testing.T) {
	manager := testLXConfigManager(t)
	runtime := newLXScriptRuntime(manager).(*productionMusicLXRuntime)
	source := `
/*!
 * @name Fixture Source
 * @author My-LiveHub tests
 * @version 1.2.3
 * @description lifecycle fixture
 * @homepage https://example.test/source
 */
lx.on(lx.EVENT_NAMES.request, async () => 'https://1.1.1.1/song.mp3');
lx.send(lx.EVENT_NAMES.inited, {
  sources: { kw: { type: 'music', actions: ['musicUrl'], qualitys: ['128k'] } }
});
`
	if _, err := runtime.ImportScript(context.Background(), "fixture", source, nil); err != nil { t.Fatalf("import script: %v", err) }
	list := runtime.ListScripts()
	if len(list) != 1 || !list[0].Enabled || list[0].Name != "Fixture Source" || list[0].Version != "1.2.3" || list[0].Author != "My-LiveHub tests" || list[0].Homepage != "https://example.test/source" { t.Fatalf("imported script metadata = %#v", list) }
	id := list[0].ID
	if err := runtime.Start(context.Background()); err != nil { t.Fatalf("start and restore scripts: %v", err) }
	if !runtime.Available() { t.Fatal("restored script runtime should be available") }
	if got, err := runtime.ResolvePlaybackURL(context.Background(), "kw", map[string]any{}); err != nil || got != "https://1.1.1.1/song.mp3" { t.Fatalf("restored script URL = %q, %v", got, err) }
	if err := runtime.SetScriptEnabled(context.Background(), id, false); err != nil { t.Fatalf("disable script: %v", err) }
	if runtime.Available() { t.Fatal("disabled script must not be available for dispatch") }
	if err := runtime.SetScriptEnabled(context.Background(), id, true); err != nil { t.Fatalf("re-enable script: %v", err) }
	if !runtime.Available() { t.Fatal("re-enabled script should be available") }
	if err := runtime.Close(); err != nil { t.Fatalf("close runtime: %v", err) }

	restarted := newLXScriptRuntime(manager).(*productionMusicLXRuntime)
	if err := restarted.Start(context.Background()); err != nil { t.Fatalf("restart runtime from persisted store: %v", err) }
	if !restarted.Available() { t.Fatal("script was not restored after runtime restart") }
	if restored := restarted.ListScripts()[0]; restored.Version != "1.2.3" || restored.Author != "My-LiveHub tests" { t.Fatalf("restored metadata = %#v", restored) }
	if err := restarted.DeleteScript(context.Background(), id); err != nil { t.Fatalf("delete script: %v", err) }
	if len(restarted.ListScripts()) != 0 { t.Fatal("deleted script remains in store") }
	if err := restarted.Close(); err != nil { t.Fatalf("close restarted runtime: %v", err) }
}

func TestProductionLXScriptRuntimeQuarantinesMalformedStore(t *testing.T) {
	manager := testLXConfigManager(t)
	if err := manager.WriteLocalJSON(lxScriptStoreFile, []byte("{}"), lxScriptStoreLimit); err != nil { t.Fatal(err) }
	runtime := newLXScriptRuntime(manager).(*productionMusicLXRuntime)
	if err := runtime.Start(context.Background()); err != nil { t.Fatalf("start should recover from a malformed store: %v", err) }
	if len(runtime.ListScripts()) != 0 { t.Fatal("malformed store unexpectedly yielded scripts") }
	if err := runtime.Close(); err != nil { t.Fatal(err) }
}


func TestProductionLXScriptRuntimeIsolatesInvalidPersistedScript(t *testing.T) {
	manager := testLXConfigManager(t)
	validSource := "lx.send('inited', {});"
	validDigest := sha256.Sum256([]byte(validSource))
	records := []storedLXScript{
		{ID:"broken", Name:"broken source", Source:"  ", Enabled:true},
		{ID:"tampered", Name:"tampered source", Source:validSource, Enabled:true},
		{ID:hex.EncodeToString(validDigest[:]), Name:"valid source", Source:validSource, Enabled:true},
	}
	data, err := json.Marshal(records)
	if err != nil { t.Fatal(err) }
	if err := manager.WriteLocalJSON(lxScriptStoreFile, data, lxScriptStoreLimit); err != nil { t.Fatal(err) }
	runtime := newLXScriptRuntime(manager).(*productionMusicLXRuntime)
	if err := runtime.Start(context.Background()); err != nil { t.Fatalf("start with one damaged script: %v", err) }
	defer runtime.Close()
	scripts := runtime.ListScripts()
	if len(scripts) != 3 { t.Fatalf("restored script count = %d, want 3: %#v", len(scripts), scripts) }
	if scripts[0].Enabled || scripts[0].LastError == "" { t.Fatalf("damaged script was not isolated: %#v", scripts[0]) }
	if scripts[1].Enabled || !strings.Contains(scripts[1].LastError, "hash") { t.Fatalf("tampered script was not rejected by hash verification: %#v", scripts[1]) }
	if !scripts[2].Enabled || scripts[2].LastError != "" { t.Fatalf("valid script was affected by damaged peer: %#v", scripts[2]) }
	if !runtime.Available() { t.Fatal("valid script should keep runtime available") }
}

func TestLXScriptStoreSerializationIsStable(t *testing.T) {
	data, err := json.Marshal([]storedLXScript{{ID:"id", Name:"fixture", Source:"lx.send('inited', {});", Enabled:true}})
	if err != nil { t.Fatal(err) }
	var decoded []storedLXScript
	if err := json.Unmarshal(data, &decoded); err != nil || len(decoded) != 1 { t.Fatalf("decode store: %#v, %v", decoded, err) }
}
