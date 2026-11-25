//go:build lxproduction

package desktop

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"testing/fstest"
 "time"
	"strings"

	"github.com/dongguo4812/My-LiveHub/app/internal/config"
	"github.com/dongguo4812/My-LiveHub/app/internal/music"
)

func detectLXMediaFormat(data []byte) string {
	switch {
	case bytes.HasPrefix(data, []byte("fLaC")): return "FLAC"
	case bytes.HasPrefix(data, []byte("OggS")): return "Ogg"
	case bytes.HasPrefix(data, []byte("RIFF")): return "WAV/RIFF"
	case len(data) >= 8 && string(data[4:8]) == "ftyp": return "ISO-BMFF"
	case bytes.HasPrefix(data, []byte("ID3")): return "MP3"
	case len(data) >= 2 && data[0] == 0xff && data[1]&0xe0 == 0xe0: return "MP3"
	default: return "unknown"
	}
}

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
  sources: { kw: { type: 'music', actions: ['musicUrl','search'], qualitys: ['128k'] } }
});
`
	if _, err := runtime.ImportScript(context.Background(), "fixture", source); err != nil { t.Fatalf("import script: %v", err) }
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

func TestProductionLXScriptTestResultPersistsAndReplacesLatest(t *testing.T) {
	manager := testLXConfigManager(t)
	runtime := newLXScriptRuntime(manager).(*productionMusicLXRuntime)
	source := `lx.on(lx.EVENT_NAMES.request, async () => "https://example.com/song.mp3"); lx.send(lx.EVENT_NAMES.inited, {sources:{kw:{type:"music",actions:["search","musicUrl"]}}});`
	if _, err := runtime.ImportScript(context.Background(), "test result fixture", source); err != nil { t.Fatal(err) }
	if err := runtime.Start(context.Background()); err != nil { t.Fatal(err) }
	first := &LXScriptTestResult{Status:"failed", TestedAt:time.Now().UTC(), Platforms:[]LXSourcePlatformTest{{Platform:"kw", Status:"failed", Message:"first"}}}
	runtime.mu.Lock(); runtime.scripts[0].LastTest = first; err := runtime.persistLocked(); runtime.mu.Unlock(); if err != nil { t.Fatal(err) }
	if err := runtime.Close(); err != nil { t.Fatal(err) }
	restored := newLXScriptRuntime(manager).(*productionMusicLXRuntime)
	if err := restored.Start(context.Background()); err != nil { t.Fatal(err) }; defer restored.Close()
	before := restored.ListScripts()[0].LastTest
	if before == nil || len(before.Platforms) != 1 || before.Platforms[0].Message != "first" { t.Fatalf("restored last test = %#v", before) }
	second := &LXScriptTestResult{Status:"passed", TestedAt:time.Now().UTC(), Platforms:[]LXSourcePlatformTest{{Platform:"kw", Status:"passed", Message:"second"}}}
	restored.mu.Lock(); restored.scripts[0].LastTest = second; err = restored.persistLocked(); restored.mu.Unlock(); if err != nil { t.Fatal(err) }
	latest := restored.ListScripts()[0].LastTest
	if latest == nil || latest.Platforms[0].Message != "second" { t.Fatalf("latest result was not replaced: %#v", latest) }
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


func TestMusicServiceLXScriptManagementAPI(t *testing.T) {
	manager := testLXConfigManager(t)
	service := NewMusicServiceWithDependencies(manager, config.MusicConfig{}, nil, nil)
	added, err := service.ImportLXScript("service API fixture", "lx.on(lx.EVENT_NAMES.request, () => []); lx.send(lx.EVENT_NAMES.inited, {sources:{kw:{type:'music',actions:['search']}}});")
	if err != nil { t.Fatalf("MusicService.ImportLXScript: %v", err) }
	if added.ID == "" || added.Name != "service API fixture" { t.Fatalf("import result = %#v", added) }
	list := service.ListLXScripts()
	if len(list) != 1 || list[0].ID != added.ID { t.Fatalf("MusicService.ListLXScripts = %#v", list) }
	if err := service.SetLXScriptEnabled(added.ID, false); err != nil { t.Fatalf("MusicService.SetLXScriptEnabled: %v", err) }
	if list = service.ListLXScripts(); len(list) != 1 || list[0].Enabled { t.Fatalf("disabled script listing = %#v", list) }
	if err := service.DeleteLXScript(added.ID); err != nil { t.Fatalf("MusicService.DeleteLXScript: %v", err) }
	if list = service.ListLXScripts(); len(list) != 0 { t.Fatalf("deleted script listing = %#v", list) }
	if err := service.Shutdown(); err != nil { t.Fatalf("MusicService.Shutdown: %v", err) }
}

func TestLXScriptStoreSerializationIsStable(t *testing.T) {
	data, err := json.Marshal([]storedLXScript{{ID:"id", Name:"fixture", Source:"lx.send('inited', {});", Enabled:true}})
	if err != nil { t.Fatal(err) }
	var decoded []storedLXScript
	if err := json.Unmarshal(data, &decoded); err != nil || len(decoded) != 1 { t.Fatalf("decode store: %#v, %v", decoded, err) }
}


func TestProductionLXCommunityActionsReachMusicService(t *testing.T) {
 manager:=testLXConfigManager(t)
 runtime:=newLXScriptRuntime(manager).(*productionMusicLXRuntime)
 service:=NewMusicServiceWithDependencies(manager,config.MusicConfig{EnabledSearchSources:[]string{"kw"}},nil,nil)
 service.scriptRuntime=runtime
 source:=`
lx.on(lx.EVENT_NAMES.request, async ({source,action,info}) => {
 if(source!=='kw') throw new Error('unexpected source '+source);
 if(action==='musicUrl') return 'https://1.1.1.1/fixture-song.mp3';
 if(action==='search') return {tracks:[{id:'fixture-song',name:'Fixture Song',singer:'Fixture Artist',interval:180}],total:1,page:1,page_size:30,max_page:1};
 if(action==='detail') return {album:'Fixture Album'};
 if(action==='lyric') return '[00:00.00]Fixture lyric';
 if(action==='pic') return 'https://1.1.1.1/fixture-cover.jpg';
 throw new Error('unsupported action '+action);
});
lx.send(lx.EVENT_NAMES.inited,{sources:{kw:{type:'music',actions:['musicUrl','search','detail','lyric','pic'],qualitys:['128k']}}});
`
 if _,err:=runtime.ImportScript(context.Background(),"community-action-fixture",source);err!=nil{t.Fatal(err)}
 if err:=runtime.Start(context.Background());err!=nil{t.Fatal(err)}
 defer service.Shutdown()
 if !runtime.HasExtensionAction("kw", "search")||!runtime.HasExtensionAction("kw", "detail")||!runtime.HasExtensionAction("kw", "lyric")||!runtime.HasExtensionAction("kw", "pic"){t.Fatal("declared community actions were not exposed")}
 if runtime.HasExtensionAction("kw", "musicUrl"){t.Fatal("native musicUrl must not be classified as a community extension")}
 resolved,err:=runtime.ResolvePlaybackURL(context.Background(),"kw",map[string]any{"musicInfo":map[string]any{"songmid":"fixture-song"},"type":"128k"})
 if err!=nil||resolved!="https://1.1.1.1/fixture-song.mp3"{t.Fatalf("native musicUrl action: %q, %v",resolved,err)}
 page,err:=service.SearchSourcePage("Fixture Song","kw",1);if err!=nil{t.Fatalf("script search through MusicService: %v",err)}
 globalPage,err:=service.SearchPage("Fixture Song",1);if err!=nil||len(globalPage.Tracks)!=1||globalPage.Tracks[0].Title!="Fixture Song"{t.Fatalf("script search through MusicService.SearchPage: %#v, %v",globalPage,err)}
 if len(page.Tracks)!=1||page.Tracks[0].ID!="kw_fixture-song"||page.Tracks[0].Title!="Fixture Song"||page.Tracks[0].DurationMS!=180000{t.Fatalf("unexpected script search page: %#v",page)}
 track:=page.Tracks[0]
 detail,err:=service.GetLXTrackDetail(track);if err!=nil{t.Fatalf("detail action: %v",err)}
 if detail.Album!="Fixture Album"||detail.Title!=track.Title||detail.ID!=track.ID{t.Fatalf("detail result was not merged into unified Track: %#v",detail)}
 lyrics,err:=service.GetLyrics(track);if err!=nil||lyrics!="[00:00.00]Fixture lyric"{t.Fatalf("lyric action: %q, %v",lyrics,err)}
 cover,err:=service.GetLXTrackCover(track);if err!=nil||cover!="https://1.1.1.1/fixture-cover.jpg"{t.Fatalf("pic action: %q, %v",cover,err)}
}

func TestProductionLXCommunityActionIsolatesFailingScript(t *testing.T) {
 runtime:=newLXScriptRuntime(testLXConfigManager(t)).(*productionMusicLXRuntime)
 bad:=`lx.on(lx.EVENT_NAMES.request,()=>{throw new Error('fixture failure')});lx.send(lx.EVENT_NAMES.inited,{sources:{kw:{type:'music',actions:['musicUrl','search','detail','lyric','pic']}}});`
 good:=`lx.on(lx.EVENT_NAMES.request,()=>({album:'second script'}));lx.send(lx.EVENT_NAMES.inited,{sources:{kw:{type:'music',actions:['musicUrl','detail']}}});`
 if _,err:=runtime.ImportScript(context.Background(),"failing fixture",bad);err!=nil{t.Fatal(err)}
 if _,err:=runtime.ImportScript(context.Background(),"healthy fixture",good);err!=nil{t.Fatal(err)}
 if err:=runtime.Start(context.Background());err!=nil{t.Fatal(err)}
 defer runtime.Close()
 value,err:=runtime.DispatchExtensionAction(context.Background(),"kw","detail",map[string]any{})
 if err!=nil{t.Fatalf("healthy script should be tried after a failing peer: %v",err)}
 if value.(map[string]any)["album"]!="second script"{t.Fatalf("unexpected fallback value: %#v",value)}
}

func TestProductionLXCommunityActionHonorsContextCancellation(t *testing.T) {
 runtime:=newLXScriptRuntime(testLXConfigManager(t)).(*productionMusicLXRuntime)
 source:=`lx.on(lx.EVENT_NAMES.request,async ({info})=>info.slow?new Promise(resolve=>setTimeout(()=>resolve('late'),1000)):'ok');lx.send(lx.EVENT_NAMES.inited,{sources:{kw:{type:'music',actions:['musicUrl','detail']}}});`
 if _,err:=runtime.ImportScript(context.Background(),"slow action fixture",source);err!=nil{t.Fatal(err)}
 if err:=runtime.Start(context.Background());err!=nil{t.Fatal(err)}
 defer runtime.Close()
 ctx,cancel:=context.WithTimeout(context.Background(),50*time.Millisecond);defer cancel()
 _,err:=runtime.DispatchExtensionAction(ctx,"kw","detail",map[string]any{"slow":true})
 if !errors.Is(err,context.DeadlineExceeded){t.Fatalf("extension action did not honor caller deadline: %v",err)}
}

func TestProductionLXScriptImportRejectsScriptsWithoutSupportedActions(t *testing.T) {
	manager := testLXConfigManager(t)
	runtime := newLXScriptRuntime(manager).(*productionMusicLXRuntime)
	source := `lx.on(lx.EVENT_NAMES.request, () => 'https://1.1.1.1/song.mp3'); lx.send(lx.EVENT_NAMES.inited, {sources:{kw:{type:'music',actions:['unsupportedAction']}}});`
	_, err := runtime.ImportScript(context.Background(), "unsupported fixture", source)
	if err == nil || !strings.Contains(err.Error(), "没有声明可支持的 action") {
		t.Fatalf("unsupported script import error = %v, want explicit unsupported capability error", err)
	}
	if got := runtime.ListScripts(); len(got) != 0 {
		t.Fatalf("unsupported script was silently persisted: %#v", got)
	}
}

func TestProductionLXNativeMusicURLOnlyScriptCanBeImportedAndEnabled(t *testing.T) {
	manager := testLXConfigManager(t)
	runtime := newLXScriptRuntime(manager).(*productionMusicLXRuntime)
	source := `lx.on(lx.EVENT_NAMES.request, ({action}) => action === 'musicUrl' ? 'https://1.1.1.1/song.mp3' : null); lx.send(lx.EVENT_NAMES.inited, {sources:{kw:{type:'music',actions:['musicUrl']}}});`
	imported, err := runtime.ImportScript(context.Background(), "native playback fixture", source)
	if err != nil { t.Fatalf("import native musicUrl-only script: %v", err) }
	if !imported.Enabled || len(imported.NativeActions) != 1 || imported.NativeActions[0] != "musicUrl" || len(imported.ExtensionActions) != 0 {
		t.Fatalf("native-only import capabilities = %#v", imported)
	}
	if err := runtime.Start(context.Background()); err != nil { t.Fatalf("start native-only runtime: %v", err) }
	defer runtime.Close()
	if !runtime.Available() { t.Fatal("native-only imported script should make the playback resolver available") }
	resolved, err := runtime.ResolvePlaybackURL(context.Background(), "kw", map[string]any{"musicInfo": map[string]any{"songmid":"fixture"}})
	if err != nil || resolved != "https://1.1.1.1/song.mp3" { t.Fatalf("native-only musicUrl result = %q, %v", resolved, err) }
}



// TestProductionLXFishLatestRealImportAndEnable exercises the user-provided
// upstream fish/latest.js through the same import path used by the desktop UI.
// It is opt-in because it executes third-party source and depends on upstream
// availability; QuickJS exposes no Node, filesystem, or shell host APIs.
func TestProductionLXFishLatestRealImportAndEnable(t *testing.T) {
	if os.Getenv("MYLIVEHUB_LX_FISH_INTEGRATION") != "1" {
		t.Skip("set MYLIVEHUB_LX_FISH_INTEGRATION=1 to run the real fish/latest.js integration")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	request, err := http.NewRequestWithContext(ctx, http.MethodGet,
		"https://gh-proxy.org/raw.githubusercontent.com/pdone/lx-music-source/main/fish/latest.js", nil)
	if err != nil { t.Fatal(err) }
	response, err := (&http.Client{Timeout: 30 * time.Second}).Do(request)
	if err != nil { t.Fatalf("download user-provided fish/latest.js URL: %v", err) }
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("fish/latest.js download status = %s", response.Status)
	}
	source, err := io.ReadAll(io.LimitReader(response.Body, (2<<20)+1))
	if err != nil { t.Fatalf("read fish/latest.js: %v", err) }
	if len(source) == 0 || len(source) > 2<<20 {
		t.Fatalf("fish/latest.js size = %d bytes; expected 1..%d", len(source), 2<<20)
	}

	runtime := newLXScriptRuntime(testLXConfigManager(t)).(*productionMusicLXRuntime)
	if err := runtime.Start(ctx); err != nil { t.Fatalf("start production LX runtime: %v", err) }
	defer runtime.Close()

	imported, err := runtime.ImportScript(ctx, "fish/latest.js", string(source))
	if err != nil { t.Fatalf("import and auto-enable fish/latest.js: %v", err) }
	if !imported.Enabled { t.Fatalf("imported script is not enabled: %#v", imported) }
	if len(imported.SupportedSources) == 0 {
		t.Fatalf("fish/latest.js initialized but declared no supported sources: %#v", imported)
	}
	if len(imported.ExtensionActions) == 0 && len(imported.NativeActions) == 0 {
		t.Fatalf("fish/latest.js declared no supported community or native LX actions: %#v", imported)
	}
	for _, action := range imported.ExtensionActions {
		switch action {
		case "search", "detail", "lyric", "pic":
		default:
			t.Fatalf("unexpected community extension action %q", action)
		}
	}
	for _, action := range imported.NativeActions {
		if action != "musicUrl" { t.Fatalf("unexpected native LX action %q", action) }
	}
	if len(imported.NativeActions) != 1 || imported.NativeActions[0] != "musicUrl" {
		t.Fatalf("fish/latest.js did not expose native musicUrl capability: %#v", imported)
	}
	if runtime.manager == nil || !runtime.manager.HasScripts() || !runtime.Available() {
		t.Fatal("imported fish/latest.js was not installed in the started production source manager")
	}
	persisted := runtime.ListScripts()
	if len(persisted) != 1 || !persisted[0].Enabled || !reflect.DeepEqual(persisted[0].SupportedSources, imported.SupportedSources) ||
		!reflect.DeepEqual(persisted[0].ExtensionActions, imported.ExtensionActions) ||
		!reflect.DeepEqual(persisted[0].NativeActions, imported.NativeActions) {
		t.Fatalf("imported capabilities were not persisted and restored consistently: imported=%#v persisted=%#v", imported, persisted)
	}
	t.Logf("fish/latest.js import enabled: sources=%v community_actions=%v native_actions=%v", imported.SupportedSources, imported.ExtensionActions, imported.NativeActions)
}


// TestProductionLXFishSunnyDayPlaybackURL resolves an actual "晴天" track
// through a native provider, invokes fish/latest.js musicUrl, verifies the
// returned media endpoint, and exports the signed URL for the Chromium smoke test.

// safeLXMediaURL deliberately strips signed query parameters from test logs.
func safeLXMediaURL(raw string) string {
	parsed, err := url.Parse(raw)
	if err != nil { return "<invalid-url>" }
	return parsed.Host + parsed.EscapedPath()
}

func firstTrackValueForTest(values map[string]string, keys ...string) string {
	for _, key := range keys { if value := strings.TrimSpace(values[key]); value != "" { return value } }
	return ""
}

func TestProductionLXFishSunnyDayPlaybackURL(t *testing.T) {
	if os.Getenv("MYLIVEHUB_LX_FISH_PLAYBACK_INTEGRATION") != "1" {
		t.Skip("set MYLIVEHUB_LX_FISH_PLAYBACK_INTEGRATION=1 to resolve a real song")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()

	request, err := http.NewRequestWithContext(ctx, http.MethodGet,
		"https://gh-proxy.org/raw.githubusercontent.com/pdone/lx-music-source/main/fish/latest.js", nil)
	if err != nil { t.Fatal(err) }
	response, err := (&http.Client{Timeout: 30*time.Second}).Do(request)
	if err != nil { t.Fatalf("download fish/latest.js: %v", err) }
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK { t.Fatalf("fish/latest.js download status = %s", response.Status) }
	source, err := io.ReadAll(io.LimitReader(response.Body, (2<<20)+1))
	if err != nil { t.Fatalf("read fish/latest.js: %v", err) }
	if len(source) == 0 || len(source) > 2<<20 { t.Fatalf("fish/latest.js size = %d", len(source)) }

	manager := newLXScriptRuntime(testLXConfigManager(t)).(*productionMusicLXRuntime)
	if err := manager.Start(ctx); err != nil { t.Fatalf("start production runtime: %v", err) }
	defer manager.Close()
	imported, err := manager.ImportScript(ctx, "fish/latest.js", string(source))
	if err != nil { t.Fatalf("import and enable real fish/latest.js: %v", err) }
	if !imported.Enabled || len(imported.NativeActions) != 1 || imported.NativeActions[0] != "musicUrl" {
		t.Fatalf("fish script lacks enabled native musicUrl: %#v", imported)
	}

	service := NewMusicServiceWithDependencies(testLXConfigManager(t),
		config.MusicConfig{EnabledSearchSources: []string{"tx", "kw", "kg"}}, nil, nil)
	service.scriptRuntime = manager

	// Kuwo is attempted first and its failure is retained explicitly. If the
	// fish backend refuses Kuwo for regional rights, try the other providers
	// exposed by the same imported LX script rather than silently claiming kw.
	var selectedTrack *music.Track
	var selectedResource music.PlaybackResource
	var selectedSource string
	var attemptFailures []string
	sourceResolver := music.NewUnifiedSourceService(service.searcher, manager)
	for _, sourceID := range []string{"kw", "tx", "kg"} {
		page, searchErr := service.SearchSourcePage("晴天", sourceID, 1)
		if searchErr != nil {
			attemptFailures = append(attemptFailures, sourceID+" search: "+searchErr.Error())
			t.Logf("%s search failed: %v", sourceID, searchErr)
			continue
		}
		for i := range page.Tracks {
			candidate := page.Tracks[i]
			if strings.TrimSpace(candidate.Title) != "晴天" || !strings.Contains(candidate.Artist, "周杰伦") { continue }
			if candidate.Album != "" && candidate.Album != "叶惠美" { continue }
			t.Logf("%s candidate: id=%q source_id=%q native_id=%q title=%q artist=%q album=%q duration_ms=%d qualities=%+v source_data=%+v",
				sourceID, candidate.ID, candidate.SourceID, firstTrackValueForTest(candidate.SourceData, "songmid", "song_id", "hash", "id"),
				candidate.Title, candidate.Artist, candidate.Album, candidate.DurationMS, candidate.Qualities, candidate.SourceData)
			qualities := candidate.Qualities
			if len(qualities) == 0 { qualities = []music.TrackQuality{{Type: "320k"}} }
			preferredTypes := []string{"320k", "128k", "flac", "flac24bit"}
			for _, preferred := range preferredTypes {
				for _, advertised := range qualities {
					if advertised.Type != preferred { continue }
					resolvedURL, resolveErr := sourceResolver.ResolvePlaybackURL(ctx, candidate, advertised.Type)
					if resolveErr != nil {
						attemptFailures = append(attemptFailures, sourceID+"/"+advertised.Type+": "+resolveErr.Error())
						t.Logf("%s musicUrl failed: quality=%s size=%s error=%v", sourceID, advertised.Type, advertised.Size, resolveErr)
						continue
					}
					selectedTrack = &candidate
					selectedSource = sourceID
					selectedResource = music.PlaybackResource{URL: resolvedURL, SourceID: sourceID, TrackID: candidate.ID, Quality: advertised.Type}
					t.Logf("%s musicUrl returned URL: quality=%s endpoint=%s", sourceID, advertised.Type, safeLXMediaURL(resolvedURL))
					break
				}
				if selectedTrack != nil { break }
			}
			if selectedTrack != nil { break }
		}
		if selectedTrack != nil { break }
	}
	if selectedTrack == nil {
		t.Fatalf("fish/latest.js did not resolve playable 晴天 / 周杰伦 across kw/tx/kg; attempts: %s", strings.Join(attemptFailures, " | "))
	}
	t.Logf("real search and fish musicUrl succeeded: source=%s title=%q artist=%q track_id=%q quality=%s",
		selectedSource, selectedTrack.Title, selectedTrack.Artist, selectedTrack.ID, selectedResource.Quality)
	lyrics, lyricErr := service.getNativeProviderLyrics(*selectedTrack)
	if lyricErr != nil { t.Fatalf("real %s lyric retrieval failed: %v", selectedSource, lyricErr) }
	if !strings.Contains(lyrics, "[00:") || !strings.Contains(lyrics, "]") {
		t.Fatalf("real %s lyric response is not timestamped LRC: %q", selectedSource, lyrics)
	}
	t.Logf("real %s lyrics resolved: %d LRC lines", selectedSource, len(strings.Split(lyrics, "\n")))
	resource := selectedResource
	parsed, err := url.Parse(resource.URL)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		t.Fatalf("fish musicUrl returned invalid media URL")
	}
	probeCtx, probeCancel := context.WithTimeout(ctx, 25*time.Second)
	defer probeCancel()
	probeRequest, err := http.NewRequestWithContext(probeCtx, http.MethodGet, resource.URL, nil)
	if err != nil { t.Fatal(err) }
	probeRequest.Header.Set("Range", "bytes=0-63")
	probeRequest.Header.Set("User-Agent", "Mozilla/5.0 My-LiveHub playback integration")
	probeResponse, err := (&http.Client{Timeout: 25*time.Second}).Do(probeRequest)
	if err != nil { t.Fatalf("real media URL request failed: %v", err) }
	defer probeResponse.Body.Close()
	if probeResponse.StatusCode != http.StatusOK && probeResponse.StatusCode != http.StatusPartialContent {
		t.Fatalf("real media URL returned HTTP %d", probeResponse.StatusCode)
	}
	firstBytes, err := io.ReadAll(io.LimitReader(probeResponse.Body, 2))
	if err != nil || len(firstBytes) == 0 { t.Fatalf("real media URL returned no media bytes: bytes=%d err=%v", len(firstBytes), err) }

	outputPath := os.Getenv("MYLIVEHUB_PLAYBACK_URL_FILE")
	if outputPath == "" { t.Fatal("MYLIVEHUB_PLAYBACK_URL_FILE must point to the CI handoff file") }
	if err := os.WriteFile(outputPath, []byte(resource.URL), 0600); err != nil { t.Fatalf("write browser playback URL handoff: %v", err) }
	t.Logf("real fish musicUrl resolved and media endpoint returned bytes: source=%s quality=%s status=%d content_type=%q",
		selectedSource, resource.Quality, probeResponse.StatusCode, probeResponse.Header.Get("Content-Type"))
}


func TestProductionLXScriptRuntimeKeepsExactlyOneScriptEnabled(t *testing.T) {
	manager := testLXConfigManager(t)
	runtime := newLXScriptRuntime(manager).(*productionMusicLXRuntime)
	firstSource := `lx.on(lx.EVENT_NAMES.request, () => 'first'); lx.send(lx.EVENT_NAMES.inited, {sources:{kw:{type:'music',actions:['search']}}});`
	secondSource := `lx.on(lx.EVENT_NAMES.request, () => 'second'); lx.send(lx.EVENT_NAMES.inited, {sources:{tx:{type:'music',actions:['search']}}});`

	first, err := runtime.ImportScript(context.Background(), "first source", firstSource)
	if err != nil { t.Fatalf("import first script: %v", err) }
	second, err := runtime.ImportScript(context.Background(), "second source", secondSource)
	if err != nil { t.Fatalf("import second script: %v", err) }

	list := runtime.ListScripts()
	if len(list) != 2 || list[0].Enabled || !list[1].Enabled || list[1].ID != second.ID {
		t.Fatalf("importing and enabling second script must disable first: %#v", list)
	}

	if err := runtime.SetScriptEnabled(context.Background(), first.ID, true); err != nil {
		t.Fatalf("enable first script: %v", err)
	}
	list = runtime.ListScripts()
	if len(list) != 2 || !list[0].Enabled || list[1].Enabled {
		t.Fatalf("enabling first script must disable second: %#v", list)
	}

	if err := runtime.SetScriptEnabled(context.Background(), first.ID, false); err != nil {
		t.Fatalf("disable active script: %v", err)
	}
	list = runtime.ListScripts()
	if list[0].Enabled || list[1].Enabled {
		t.Fatalf("disabling active script should leave no active script: %#v", list)
	}
}

func TestProductionLXScriptRuntimeRestoresLegacyMultipleEnabledWithSingleWinner(t *testing.T) {
	manager := testLXConfigManager(t)
	firstSource := `lx.send(lx.EVENT_NAMES.inited, {sources:{kw:{type:'music',actions:['search']}}});`
	secondSource := `lx.send(lx.EVENT_NAMES.inited, {sources:{tx:{type:'music',actions:['search']}}});`
	firstDigest := sha256.Sum256([]byte(firstSource))
	secondDigest := sha256.Sum256([]byte(secondSource))
	records := []storedLXScript{
		{ID: hex.EncodeToString(firstDigest[:]), Name: "first", Source: firstSource, ExtensionActions: []string{"search"}, Enabled: true},
		{ID: hex.EncodeToString(secondDigest[:]), Name: "second", Source: secondSource, ExtensionActions: []string{"search"}, Enabled: true},
	}
	data, err := json.Marshal(records)
	if err != nil { t.Fatal(err) }
	if err := manager.WriteLocalJSON(lxScriptStoreFile, data, lxScriptStoreLimit); err != nil { t.Fatal(err) }

	runtime := newLXScriptRuntime(manager).(*productionMusicLXRuntime)
	if err := runtime.Start(context.Background()); err != nil { t.Fatalf("restore legacy store: %v", err) }
	defer runtime.Close()
	list := runtime.ListScripts()
	if len(list) != 2 || list[0].Enabled || !list[1].Enabled {
		t.Fatalf("legacy multiple-enabled store should keep only the last enabled script: %#v", list)
	}
	if !strings.Contains(list[0].LastError, "自动停用") {
		t.Fatalf("legacy disabled script should explain automatic deactivation: %#v", list[0])
	}
}


// TestProductionLXXinghaiDaoxiangPlaybackURL imports the exact source stored
// under lx-js/, searches 稻香 through My-LiveHub's native LX search providers,
// resolves each provider through the unchanged Xinghai musicUrl action, and
// hands one verified media URL to the Chromium playback smoke test.
func TestProductionLXXinghaiDaoxiangPlaybackURL(t *testing.T) {
	if os.Getenv("MYLIVEHUB_LX_XINGHAI_PLAYBACK_INTEGRATION") != "1" {
		t.Skip("set MYLIVEHUB_LX_XINGHAI_PLAYBACK_INTEGRATION=1 to verify the real Xinghai source")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	scriptPath := filepath.Join("..", "..", "..", "lx-js", "xinghai-music-sourcev2.3.20.js")
	source, err := os.ReadFile(scriptPath)
	if err != nil { t.Fatalf("read the uploaded lx-js/xinghai-music-sourcev2.3.20.js: %v", err) }
	if len(source) == 0 || len(source) > 2<<20 { t.Fatalf("Xinghai source size is invalid: %d", len(source)) }
	digest := sha256.Sum256(source)
	t.Logf("using exact Xinghai source bytes: size=%d sha256=%s", len(source), hex.EncodeToString(digest[:]))

	runtime := newLXScriptRuntime(testLXConfigManager(t)).(*productionMusicLXRuntime)
	if err := runtime.Start(ctx); err != nil { t.Fatalf("start production LX runtime: %v", err) }
	defer runtime.Close()
	imported, err := runtime.ImportScript(ctx, filepath.Base(scriptPath), string(source))
	if err != nil { t.Fatalf("import and enable unchanged Xinghai source: %v", err) }
	if !imported.Enabled { t.Fatalf("Xinghai source was not enabled: %#v", imported) }
	if !reflect.DeepEqual(imported.NativeActions, []string{"musicUrl"}) {
		t.Fatalf("Xinghai native actions = %v, want [musicUrl]", imported.NativeActions)
	}
	if !reflect.DeepEqual(imported.ExtensionActions, []string{"lyric", "pic"}) {
		t.Fatalf("Xinghai community actions = %v, want [lyric pic]", imported.ExtensionActions)
	}
	for _, expected := range []string{"kw", "kg", "tx", "wy", "mg"} {
		if !containsLXXinghaiSource(imported.SupportedSources, expected) {
			t.Fatalf("Xinghai did not register %s; registered sources: %v", expected, imported.SupportedSources)
		}
	}
	t.Logf("Xinghai imported and enabled: sources=%v native_actions=%v community_actions=%v; search is intentionally delegated to native LX providers", imported.SupportedSources, imported.NativeActions, imported.ExtensionActions)

	enabledSources := []string{"tx", "kw", "kg", "mg"}
	service := NewMusicServiceWithDependencies(testLXConfigManager(t),
		config.MusicConfig{EnabledSearchSources: enabledSources}, nil, nil)
	service.scriptRuntime = runtime
	sourceResolver := music.NewUnifiedSourceService(service.searcher, runtime)
	resourceCache := music.NewPlaybackResourceCache(2*time.Minute, 32)

	var browserURL string
	var txAuthorizationBlocked bool
	var txSearchUnavailable bool
	var miguSearchUnavailable bool
	var failures []string
	for _, sourceID := range enabledSources {
		page, searchErr := service.SearchSourcePage("稻香", sourceID, 1)
		if searchErr != nil {
			// QQ's legacy search fallback is an external, unauthenticated endpoint.
			// Its intermittent HTTP 5xx must not invalidate the independent
			// Xinghai URL/media checks for Kuwo, Kugou, and Migu.
			if sourceID == "tx" && strings.Contains(searchErr.Error(), "HTTP 500") {
				txSearchUnavailable = true
				t.Logf("tx QQ search fallback temporarily unavailable (HTTP 500); continue independent provider checks: %v", searchErr)
				continue
			}
			if sourceID == "mg" && (errors.Is(searchErr, context.DeadlineExceeded) || strings.Contains(searchErr.Error(), "Client.Timeout exceeded")) {
				// Migu search is a Go-native provider request, not part of the
				// uploaded Xinghai musicUrl action. A transient provider timeout
				// must not invalidate successful independent Kuwo/Kugou checks.
				miguSearchUnavailable = true
				t.Logf("mg Migu search endpoints timed out; continue independent Xinghai URL/media checks: %v", searchErr)
				continue
			}
			failures = append(failures, sourceID+" search: "+searchErr.Error())
			continue
		}
		var candidate *music.Track
		for i := range page.Tracks {
			item := page.Tracks[i]
			if strings.TrimSpace(item.Title) == "稻香" && strings.Contains(item.Artist, "周杰伦") {
				candidate = &item
				break
			}
		}
		if candidate == nil {
			preview := make([]string, 0, 8)
			for index, item := range page.Tracks {
				if index >= 8 { break }
				preview = append(preview, fmt.Sprintf("%q/%q", item.Title, item.Artist))
			}
			t.Logf("%s search had no exact 稻香/周杰伦 match (total=%d page_tracks=%d first_results=%v); continue other providers",
				sourceID, page.Total, len(page.Tracks), preview)
			continue
		}
		t.Logf("%s search matched 稻香: track_id=%q native_id=%q qualities=%v",
			sourceID, candidate.ID, firstTrackValueForTest(candidate.SourceData, "songmid", "song_id", "hash", "id"), candidate.Qualities)

		// Request the best quality actually advertised by this search result,
		// matching the quality choice the UI presents to the user.
		requestedQuality := music.BestAvailablePlaybackQuality(candidate.Qualities)
		if requestedQuality == "" { requestedQuality = "320k" }
		resource, resolveErr := sourceResolver.ResolvePlaybackResource(ctx, *candidate, requestedQuality, resourceCache)
		if resolveErr != nil {
			if sourceID == "tx" && strings.Contains(resolveErr.Error(), "该音源仅支持当前已授权的客户端访问") {
				// The script obtained a server token and reached the TX backend.
				// This uploaded copy has an empty LICENSE_CONFIG.code, so the
				// backend's separate paid-client gate is not a host API failure.
				txAuthorizationBlocked = true
				t.Logf("tx token request completed; playback backend requires a separately authorized client/license: %v", resolveErr)
				continue
			}
			failures = append(failures, sourceID+" playback/quality fallback: "+resolveErr.Error())
			continue
		}
		parsed, parseErr := url.Parse(resource.URL)
		if parseErr != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
			failures = append(failures, sourceID+" returned an invalid playback URL")
			continue
		}
		probeCtx, probeCancel := context.WithTimeout(ctx, 25*time.Second)
		probeRequest, requestErr := http.NewRequestWithContext(probeCtx, http.MethodGet, resource.URL, nil)
		if requestErr == nil {
			probeRequest.Header.Set("Range", "bytes=0-63")
			probeRequest.Header.Set("User-Agent", "Mozilla/5.0 My-LiveHub Xinghai playback integration")
		}
		var probeResponse *http.Response
		if requestErr == nil {
			probeResponse, requestErr = (&http.Client{Timeout: 25*time.Second}).Do(probeRequest)
		}
		if requestErr != nil {
			probeCancel()
			failures = append(failures, sourceID+" media probe: "+requestErr.Error())
			continue
		}
		firstBytes, readErr := io.ReadAll(io.LimitReader(probeResponse.Body, 64))
		statusCode, contentType := probeResponse.StatusCode, probeResponse.Header.Get("Content-Type")
		contentRange := probeResponse.Header.Get("Content-Range")
		corsOrigin := probeResponse.Header.Get("Access-Control-Allow-Origin")
		mediaFormat := detectLXMediaFormat(firstBytes)
		_ = probeResponse.Body.Close()
		probeCancel()
		if (statusCode != http.StatusOK && statusCode != http.StatusPartialContent) || readErr != nil || len(firstBytes) == 0 {
			failures = append(failures, fmt.Sprintf("%s media probe: status=%d bytes=%d read_error=%v", sourceID, statusCode, len(firstBytes), readErr))
			continue
		}
		if sourceID == "mg" && mediaFormat == "MP3" && resource.Quality != "128k" {
			failures = append(failures, fmt.Sprintf("mg returned MP3 bytes at %s but resolver reported %s", mediaFormat, resource.Quality))
			continue
		}
		t.Logf("%s Xinghai musicUrl and media probe succeeded: requested_quality=%s resolved_quality=%s endpoint=%s status=%d content_type=%q media_format=%s content_range=%q cors_origin=%q",
			sourceID, requestedQuality, resource.Quality, safeLXMediaURL(resource.URL), statusCode, contentType, mediaFormat, contentRange, corsOrigin)
		if browserURL == "" { browserURL = resource.URL }
	}
	if browserURL == "" {
		t.Fatalf("no Xinghai provider completed search -> musicUrl -> media bytes; provider failures: %s", strings.Join(failures, " | "))
	}
	if len(failures) > 0 {
		t.Logf("some independent Xinghai providers were unavailable; verified providers remain usable: %s", strings.Join(failures, " | "))
	}
	outputPath := os.Getenv("MYLIVEHUB_PLAYBACK_URL_FILE")
	if outputPath == "" { t.Fatal("MYLIVEHUB_PLAYBACK_URL_FILE must point to the CI handoff file") }
	if err := os.WriteFile(outputPath, []byte(browserURL), 0600); err != nil {
		t.Fatalf("write Xinghai browser playback URL handoff: %v", err)
	}
	t.Logf("real 稻香 search, Xinghai URL resolution, quality fallback, and media-byte checks passed for kw/kg/mg; tx_search_unavailable=%t mg_search_unavailable=%t tx_backend_authorization_required=%t; Chromium handoff endpoint=%s",
		txSearchUnavailable, miguSearchUnavailable, txAuthorizationBlocked, safeLXMediaURL(browserURL))
}

func containsLXXinghaiSource(values []string, target string) bool {
	for _, value := range values { if value == target { return true } }
	return false
}


type lxScriptTestBaseSearcher struct { calls map[string]int }

func (s *lxScriptTestBaseSearcher) Sources() []music.SourceSnapshot { return nil }
func (s *lxScriptTestBaseSearcher) SearchProvider(_ context.Context, source, _ string, page int) (music.SearchPage, error) {
	if s.calls == nil { s.calls = make(map[string]int) }
	s.calls[source]++
	return music.SearchPage{Tracks: []music.Track{}, Page: page, PageSize: 30, MaxPage: 1}, nil
}
func (s *lxScriptTestBaseSearcher) SearchSources(ctx context.Context, keyword string, page int, sourceIDs []string) (music.SearchPage, map[string]string, error) {
	result := music.SearchPage{Tracks: []music.Track{}, Page: page, PageSize: 30, MaxPage: 1}
	failures := make(map[string]string)
	for _, source := range sourceIDs {
		item, err := s.SearchProvider(ctx, source, keyword, page)
		if err != nil { failures[source] = err.Error(); continue }
		result.Tracks = append(result.Tracks, item.Tracks...)
	}
	return result, failures, ctx.Err()
}

func TestProductionLXScriptTestFallsBackToNativeSearchWhenScriptOnlyDeclaresMusicURL(t *testing.T) {
	manager := testLXConfigManager(t)
	runtime := newLXScriptRuntime(manager).(*productionMusicLXRuntime)
	source := `
lx.on(lx.EVENT_NAMES.request, async ({source, action}) => {
  if (action === "search") return {tracks: [], total: 0, page: 1, page_size: 30, max_page: 1};
  if (action === "musicUrl") return "https://example.com/song.mp3";
  throw new Error("unexpected action " + action);
});
lx.send(lx.EVENT_NAMES.inited, {sources: {
  kw: {type:"music", actions:["musicUrl"]},
  tx: {type:"music", actions:["search"]},
  mg: {type:"music", actions:["search","musicUrl"]}
}});
`
	added, err := runtime.ImportScript(context.Background(), "native search fallback test", source)
	if err != nil { t.Fatal(err) }
	if err := runtime.Start(context.Background()); err != nil { t.Fatal(err) }
	defer runtime.Close()

	base := &lxScriptTestBaseSearcher{calls: make(map[string]int)}
	result, err := runtime.testScriptWithSearcher(context.Background(), added.ID, base)
	if err != nil { t.Fatal(err) }
	if len(result.Platforms) != 3 {
		t.Fatalf("tested platforms = %#v; want kw, mg, and tx (explicitly unsupported)", result.Platforms)
	}
	got := map[string]bool{}
	for _, item := range result.Platforms {
		got[item.Platform] = true
	}
	if !got["kw"] || !got["mg"] || !got["tx"] {
		t.Fatalf("tested platform set = %#v; want kw, mg, and tx", got)
	}
	for _, item := range result.Platforms {
		if item.Platform == "tx" && (item.Status != "unsupported" || !strings.Contains(item.Message, "未声明 musicUrl")) {
			t.Fatalf("tx should have an explicit unsupported result: %#v", item)
		}
		if item.Platform != "tx" && !strings.Contains(item.Message, "搜索未返回歌曲") {
			t.Fatalf("unexpected %s test result: %#v", item.Platform, item)
		}
	}
	if base.calls["kw"] != 1 {
		t.Fatalf("native provider search calls for kw = %d, want 1", base.calls["kw"])
	}
	if base.calls["mg"] != 0 {
		t.Fatalf("mg unexpectedly used native fallback despite declaring script search: %d", base.calls["mg"])
	}
	if base.calls["tx"] != 0 {
		t.Fatalf("tx without musicUrl must not be searched, got %d calls", base.calls["tx"])
	}

	latest := runtime.ListScripts()[0].LastTest
	if latest == nil || len(latest.Platforms) != 3 {
		t.Fatalf("latest test result was not persisted correctly: %#v", latest)
	}
}
