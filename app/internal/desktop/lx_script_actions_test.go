package desktop

import (
 "testing"
 "context"
 "errors"
 "strings"
 "sync"

 "github.com/dongguo4812/My-LiveHub/app/internal/config"
 "github.com/dongguo4812/My-LiveHub/app/internal/music"
)


func TestNormalizeLXScriptSearchResultSupportsLegacyListEnvelope(t *testing.T) {
	got, err := normalizeLXScriptSearchResult("qs", "稻香", 1, map[string]any{
		"isEnd": true,
		"list": []any{map[string]any{"id":"qs-1", "name":"稻香", "singer":"周杰伦", "duration":240}},
	})
	if err != nil { t.Fatal(err) }
	if len(got.Tracks) != 1 || got.Tracks[0].Title != "稻香" || got.Tracks[0].Artist != "周杰伦" {
		t.Fatalf("legacy list response was not normalized: %#v", got)
	}
	if got.Page != 1 || got.MaxPage != 1 { t.Fatalf("unexpected terminal page metadata: %#v", got) }
}

func TestGetLyricsAcceptsStructuredLXResponse(t *testing.T) {
	service := &MusicService{scriptRuntime: fakeLXActionRuntime{available: true, value: map[string]any{
		"lyric": "[00:01.00]主歌词", "tlyric": "[00:01.00]Translated lyric",
	}}}
	got, err := service.GetLyrics(music.Track{ID: "1", SourceID: "kw", LXSourceKey: "kw"})
	if err != nil { t.Fatal(err) }
	if got != "[00:01.00]主歌词\n[00:01.00]Translated lyric" {
		t.Fatalf("structured lyric = %q", got)
	}
}

func TestNormalizeLXScriptLyricsRejectsUnsupportedValue(t *testing.T) {
	if _, err := normalizeLXScriptLyrics([]any{"not", "lyrics"}); err == nil {
		t.Fatal("expected unsupported lyric response to fail")
	}
}

func TestNormalizeLXScriptSearchResultMapsCommunityTrackShape(t *testing.T){
 got,err:=normalizeLXScriptSearchResult("kw","fixture",1,[]any{map[string]any{"id":"native-1","name":"Fixture Song","singer":"Fixture Artist","album":"Fixture Album","interval":212,"pic":"https://cdn.example.test/cover.jpg"}})
 if err!=nil{t.Fatal(err)}
 if len(got.Tracks)!=1{t.Fatalf("tracks=%d",len(got.Tracks))}
 track:=got.Tracks[0]
 if track.ID!="kw_native-1"||track.SourceID!="kw"||track.LXSourceKey!="kw"||track.Title!="Fixture Song"||track.Artist!="Fixture Artist"||track.DurationMS!=212000||track.CoverURL!="https://cdn.example.test/cover.jpg"{t.Fatalf("unexpected mapped track: %#v",track)}
}
func TestNormalizeLXScriptSearchResultPreservesNativeLyricIdentifiers(t *testing.T) {
	got, err := normalizeLXScriptSearchResult("tx", "fixture", 1, []any{map[string]any{
		"id":"song-1", "name":"Fixture Song", "singer":"Fixture Artist",
		"songmid":"mid-123", "song_id":"12345", "lrc_url":"https://lyrics.example.test/a.lrc",
	}})
	if err != nil { t.Fatal(err) }
	if len(got.Tracks) != 1 { t.Fatalf("tracks=%d", len(got.Tracks)) }
	data := got.Tracks[0].SourceData
	for key, want := range map[string]string{"songmid":"mid-123", "song_id":"12345", "lrc_url":"https://lyrics.example.test/a.lrc"} {
		if data[key] != want { t.Errorf("SourceData[%q]=%q, want %q", key, data[key], want) }
	}
}

func TestGetLyricsDoesNotFallBackAfterLXActionFailure(t *testing.T) {
	service := NewMusicServiceWithDependencies(nil, config.DefaultMusicConfig(), &htmlAudioPlaybackEngine{})
	service.scriptRuntime = fakeLXActionRuntime{available:true, err:errors.New("script lyric action failed")}
	_, err := service.GetLyrics(music.Track{ID:"unknown_1", SourceID:"unknown", LXSourceKey:"unknown", Title:"测试", Artist:"歌手"})
	if err == nil || !strings.Contains(err.Error(), "script lyric action failed") {
		t.Fatalf("expected the QuickJS lyric action error, got %v", err)
	}
	if strings.Contains(err.Error(), "独立歌词解析") {
		t.Fatalf("retired Go lyrics fallback was invoked: %v", err)
	}
}

func TestNormalizeLXScriptSearchResultDerivesProviderLyricIDs(t *testing.T) {
	qq, err := normalizeLXScriptSearchResult("tx", "fixture", 1, []any{map[string]any{"id":"123456", "name":"QQ Song", "singer":"Artist"}})
	if err != nil { t.Fatal(err) }
	if got := qq.Tracks[0].SourceData["song_id"]; got != "123456" { t.Fatalf("QQ song_id=%q, want 123456", got) }

	kugou, err := normalizeLXScriptSearchResult("kg", "fixture", 1, []any{map[string]any{"id":"native-1", "name":"KG Song", "singer":"Artist", "FileHash":"ABCDEF"}})
	if err != nil { t.Fatal(err) }
	if got := kugou.Tracks[0].SourceData["hash"]; got != "ABCDEF" { t.Fatalf("Kugou hash=%q, want ABCDEF", got) }
}

func TestNormalizeLXScriptSearchResultPreservesUnifiedTrack(t *testing.T){
 got,err:=normalizeLXScriptSearchResult("kg","fixture",2,map[string]any{"tracks":[]music.Track{{ID:"track-2",SourceID:"kg",Title:"Song"}},"total":61,"page":2,"page_size":30,"max_page":3})
 if err!=nil{t.Fatal(err)}
 if len(got.Tracks)!=1||got.Page!=2||got.Total!=61||got.MaxPage!=3{t.Fatalf("unexpected page: %#v",got)}
}

type fakeLXActionRuntime struct { available bool; value any; err error; playbackURL string }
func (f fakeLXActionRuntime) Available() bool { return f.available }
func (f fakeLXActionRuntime) Start(context.Context) error { return nil }
func (f fakeLXActionRuntime) ResolvePlaybackURL(context.Context,string,any)(string,error) { return f.playbackURL,nil }
func (f fakeLXActionRuntime) Close() error { return nil }
func (f fakeLXActionRuntime) HasExtensionAction(string, string) bool { return f.available }
func (f fakeLXActionRuntime) DispatchExtensionAction(context.Context,string,string,any)(any,error) { return f.value,f.err }

func TestDispatchLXExtensionClassifiesTimeout(t *testing.T) {
 service:=&MusicService{scriptRuntime:fakeLXActionRuntime{available:true,err:context.DeadlineExceeded}}
 _,handled,err:=service.dispatchLXExtension(context.Background(),"kw","search",map[string]any{"keyword":"x"})
 var actionErr *LXScriptActionError
 if !handled||!errors.As(err,&actionErr)||actionErr.Kind!="timeout"||!errors.Is(err,context.DeadlineExceeded){t.Fatalf("timeout classification = %#v, handled=%v, err=%v",actionErr,handled,err)}
}
func TestDispatchLXExtensionClassifiesCancellation(t *testing.T) {
 service:=&MusicService{scriptRuntime:fakeLXActionRuntime{available:true,err:context.Canceled}}
 _,handled,err:=service.dispatchLXExtension(context.Background(),"kg","lyric",nil)
 var actionErr *LXScriptActionError
 if !handled||!errors.As(err,&actionErr)||actionErr.Kind!="cancelled"||!errors.Is(err,context.Canceled){t.Fatalf("cancel classification = %#v, handled=%v, err=%v",actionErr,handled,err)}
}


// captureLXSearchRuntime records the exact request payload sent to a community search action.
type captureLXSearchRuntime struct { info any }
func (*captureLXSearchRuntime) Available() bool { return true }
func (*captureLXSearchRuntime) Start(context.Context) error { return nil }
func (*captureLXSearchRuntime) ResolvePlaybackURL(context.Context, string, any) (string, error) { return "", nil }
func (*captureLXSearchRuntime) Close() error { return nil }
func (*captureLXSearchRuntime) HasExtensionAction(string, string) bool { return true }
func (r *captureLXSearchRuntime) DispatchExtensionAction(_ context.Context, _, _ string, info any) (any, error) {
 r.info = info
 return map[string]any{"isEnd":true,"list":[]any{map[string]any{"id":"1","name":"稻香","singer":"周杰伦"}}},nil
}

func TestScriptAwareSearcherUsesLXNativeSearchRequestFields(t *testing.T) {
 runtime := &captureLXSearchRuntime{}
 service := &MusicService{scriptRuntime:runtime}
 searcher := scriptAwareSourceSearcher{service:service}
 got,err := searcher.SearchProvider(context.Background(),"wy","稻香",2)
 if err != nil { t.Fatal(err) }
 if len(got.Tracks)!=1 || got.Tracks[0].Title!="稻香" { t.Fatalf("script search result not normalized: %#v",got) }
 payload,ok:=runtime.info.(map[string]any)
 if !ok { t.Fatalf("search payload type=%T",runtime.info) }
 for key,want:=range map[string]any{"text":"稻香","keyword":"稻香","page":2,"limit":30,"pageSize":30} {
  if payload[key]!=want { t.Errorf("search payload[%q]=%v, want %v",key,payload[key],want) }
 }
}


type multiPlatformSearchActionRuntime struct {
	mu sync.Mutex
	calls map[string]int
}

func (r *multiPlatformSearchActionRuntime) Available() bool { return true }
func (r *multiPlatformSearchActionRuntime) Start(context.Context) error { return nil }
func (r *multiPlatformSearchActionRuntime) ResolvePlaybackURL(context.Context, string, any) (string, error) { return "", nil }
func (r *multiPlatformSearchActionRuntime) Close() error { return nil }
func (r *multiPlatformSearchActionRuntime) HasExtensionAction(source, action string) bool {
	return action == "search" && (source == "tx" || source == "kw" || source == "kg" || source == "wy" || source == "mg")
}
func (r *multiPlatformSearchActionRuntime) DispatchExtensionAction(_ context.Context, source, action string, _ any) (any, error) {
	if action != "search" {
		return nil, errors.New("unexpected action: " + action)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.calls == nil {
		r.calls = make(map[string]int)
	}
	r.calls[source]++
	return map[string]any{
		"page": 1,
		"page_size": 30,
		"total": 1,
		"max_page": 1,
		"tracks": []any{map[string]any{
			"id": source + "_native_1",
			"source_id": source,
			"title": "JS Search Fixture",
			"artist": "Fixture Artist",
		}},
	}, nil
}

func TestScriptAwareSearcherDispatchesSearchActionForAllFivePlatforms(t *testing.T) {
	runtime := &multiPlatformSearchActionRuntime{}
	service := &MusicService{scriptRuntime: runtime}
	searcher := scriptAwareSourceSearcher{service: service}
	for _, source := range []string{"tx", "kw", "kg", "wy", "mg"} {
		page, err := searcher.SearchProvider(context.Background(), source, "fixture", 1)
		if err != nil {
			t.Fatalf("%s JS search action failed: %v", source, err)
		}
		if len(page.Tracks) != 1 || page.Tracks[0].SourceID != source || page.Tracks[0].Title != "JS Search Fixture" {
			t.Fatalf("%s JS search result was not normalized: %#v", source, page)
		}
	}
	for _, source := range []string{"tx", "kw", "kg", "wy", "mg"} {
		if runtime.calls[source] != 1 {
			t.Errorf("%s JS search action calls = %d, want 1", source, runtime.calls[source])
		}
	}
}


func TestScriptAwareSearcherPreservesConfiguredPriorityWithoutGoProvider(t *testing.T) {
	runtime := &multiPlatformSearchActionRuntime{}
	searcher := scriptAwareSourceSearcher{service: &MusicService{scriptRuntime: runtime}}
	priority := []string{"mg", "kw", "tx", "wy", "kg"}
	result, failures, err := searcher.SearchSources(context.Background(), "priority fixture", 1, priority)
	if err != nil { t.Fatalf("QuickJS multi-source search failed: %v", err) }
	if len(failures) != 0 { t.Fatalf("unexpected provider failures: %#v", failures) }
	if len(result.Tracks) != len(priority) { t.Fatalf("result count=%d, want %d: %#v", len(result.Tracks), len(priority), result.Tracks) }
	for i, source := range priority {
		if result.Tracks[i].SourceID != source {
			t.Fatalf("configured priority order changed at %d: got %q, want %q; tracks=%#v", i, result.Tracks[i].SourceID, source, result.Tracks)
		}
		if runtime.calls[source] != 1 { t.Errorf("%s QuickJS action calls=%d, want 1", source, runtime.calls[source]) }
	}
}

func TestScriptAwareSearcherBlocksNativeFallbackWhenQuickJSActionIsUnavailable(t *testing.T) {
	searcher := scriptAwareSourceSearcher{
		service: &MusicService{scriptRuntime: fakeLXActionRuntime{available: false}},
	}
	for _, source := range []string{"kw", "kg", "tx", "wy", "mg"} {
		_, err := searcher.SearchProvider(context.Background(), source, "fallback guard", 1)
		if err == nil || !strings.Contains(err.Error(), "已阻止 Go 原生搜索回退") {
			t.Errorf("%s should fail closed when QuickJS search is unavailable, got %v", source, err)
		}
	}
}
