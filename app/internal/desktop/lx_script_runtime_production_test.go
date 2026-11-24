//go:build lxproduction

package desktop

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"os"
	"reflect"
	"testing"
	"testing/fstest"
 "time"
	"strings"

	"github.com/dongguo4812/My-LiveHub/app/internal/config"
	"github.com/dongguo4812/My-LiveHub/app/internal/music"
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

func TestProductionLXScriptImportRejectsScriptsWithoutCommunityActions(t *testing.T) {
	manager := testLXConfigManager(t)
	runtime := newLXScriptRuntime(manager).(*productionMusicLXRuntime)
	source := `lx.on(lx.EVENT_NAMES.request, () => 'https://1.1.1.1/song.mp3'); lx.send(lx.EVENT_NAMES.inited, {sources:{kw:{type:'music',actions:['musicUrl']}}});`
	_, err := runtime.ImportScript(context.Background(), "native-only fixture", source)
	if err == nil || !strings.Contains(err.Error(), "没有声明 My-LiveHub 支持的社区扩展 action") {
		t.Fatalf("native-only script import error = %v, want explicit unsupported capability error", err)
	}
	if got := runtime.ListScripts(); len(got) != 0 {
		t.Fatalf("unsupported script was silently persisted: %#v", got)
	}
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
	probeRequest.Header.Set("Range", "bytes=0-1")
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
