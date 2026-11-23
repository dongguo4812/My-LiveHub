//go:build lxproduction && cgo

package desktop

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
 "errors"
	"testing"
	"testing/fstest"
 "time"
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
 source:=`lx.on(lx.EVENT_NAMES.request,async ({info})=>info.slow?new Promise(resolve=>setTimeout(()=>resolve('late'),1000)):'ok');lx.send(lx.EVENT_NAMES.inited,{sources:{kw:{type:'music',actions:['musicUrl']}}});`
 if _,err:=runtime.ImportScript(context.Background(),"slow action fixture",source);err!=nil{t.Fatal(err)}
 if err:=runtime.Start(context.Background());err!=nil{t.Fatal(err)}
 defer runtime.Close()
 ctx,cancel:=context.WithTimeout(context.Background(),50*time.Millisecond);defer cancel()
 _,err:=runtime.DispatchExtensionAction(ctx,"kw","detail",map[string]any{"slow":true})
 if !errors.Is(err,context.DeadlineExceeded){t.Fatalf("extension action did not honor caller deadline: %v",err)}
}
