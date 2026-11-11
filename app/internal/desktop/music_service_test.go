package desktop

import (
	"context"
	"testing"
	"time"
	"sync"

	"github.com/dongguo4812/My-LiveHub/app/internal/config"
	"github.com/dongguo4812/My-LiveHub/app/internal/events"
	"github.com/dongguo4812/My-LiveHub/app/internal/music"
)

type serviceTestSource struct{}
func (serviceTestSource) ID() string { return "test" }
func (serviceTestSource) Name() string { return "Test source" }
func (serviceTestSource) Capabilities() music.SourceCapabilities { return music.SourceCapabilities{Search:true, Resolve:true} }
func (serviceTestSource) Search(context.Context, string, int) ([]music.Track, error) {
	return []music.Track{{ID:"track-1", SourceID:"test", Title:"Test song", Artist:"Test artist"}}, nil
}
func (serviceTestSource) Resolve(context.Context, music.Track) (music.PlaybackResource, error) {
	return music.PlaybackResource{URL:"https://media.invalid/test.mp3"}, nil
}

type serviceTestEngine struct {
	mu sync.Mutex
	loaded *music.Track
	state music.PlaybackState
	volume int
	loadedCh chan music.Track
}
func (e *serviceTestEngine) Load(_ context.Context, _ music.PlaybackResource, track music.Track) error {
	copy := track
	e.mu.Lock()
	e.loaded = &copy
	e.state = music.PlaybackPlaying
	loadedCh := e.loadedCh
	e.mu.Unlock()
	if loadedCh != nil { select { case loadedCh <- copy: default: } }
	return nil
}
func (e *serviceTestEngine) Pause(context.Context) error { e.mu.Lock(); e.state = music.PlaybackPaused; e.mu.Unlock(); return nil }
func (e *serviceTestEngine) Resume(context.Context) error { e.mu.Lock(); e.state = music.PlaybackPlaying; e.mu.Unlock(); return nil }
func (*serviceTestEngine) Seek(context.Context, int64) error { return nil }
func (e *serviceTestEngine) SetVolume(_ context.Context, value int) error { e.mu.Lock(); e.volume = value; e.mu.Unlock(); return nil }
func (e *serviceTestEngine) Stop(context.Context) error { e.mu.Lock(); e.loaded = nil; e.state = music.PlaybackStopped; e.mu.Unlock(); return nil }
func (e *serviceTestEngine) Snapshot(context.Context) (music.PlaybackSnapshot, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	snapshot := music.PlaybackSnapshot{State:e.state, Volume:e.volume}
	if e.loaded != nil { copy := *e.loaded; snapshot.Track = &copy }
	return snapshot, nil
}
func (e *serviceTestEngine) CurrentLoaded() *music.Track {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.loaded == nil { return nil }
	copy := *e.loaded
	return &copy
}

func newTestMusicService(t *testing.T, enabled bool) (*MusicService, *serviceTestEngine) {
	t.Helper()
	sources := music.NewSourceRegistry()
	if err := sources.Register(serviceTestSource{}); err != nil { t.Fatal(err) }
	cfg := config.DefaultMusicConfig()
	cfg.Enabled = true
	cfg.Sources = map[string]bool{"test":enabled}
	engine := &serviceTestEngine{state:music.PlaybackIdle, volume:80}
	return NewMusicServiceWithDependencies(nil, cfg, sources, engine), engine
}

func TestMusicServiceSearchEnqueueAndPlay(t *testing.T) {
	service, engine := newTestMusicService(t, true)
	defer service.Shutdown()
	response, err := service.Search("test song")
	if err != nil { t.Fatal(err) }
	if len(response.Tracks) != 1 || response.Tracks[0].SourceID != "test" {
		t.Fatalf("unexpected search response: %#v", response)
	}
	if _, err := service.Enqueue(response.Tracks[0], "viewer"); err != nil { t.Fatal(err) }
	if err := service.PlayNext(); err != nil { t.Fatal(err) }
	if loaded := engine.CurrentLoaded(); loaded == nil || loaded.ID != "track-1" {
		t.Fatalf("track was not loaded by the shared engine: %#v", loaded)
	}
	snapshot := service.GetQueue()
	if snapshot.Current == nil || snapshot.Current.Track.SourceID != "test" {
		t.Fatalf("unexpected current queue item: %#v", snapshot.Current)
	}
}

type serviceSearchOnlySource struct{ serviceTestSource }
func (serviceSearchOnlySource) Capabilities() music.SourceCapabilities { return music.SourceCapabilities{Search:true, Resolve:false} }

func TestMusicServiceCanEnqueueTrackWithoutPlaybackResolver(t *testing.T) {
	sources := music.NewSourceRegistry()
	if err := sources.Register(serviceSearchOnlySource{}); err != nil { t.Fatal(err) }
	cfg := config.DefaultMusicConfig()
	cfg.Enabled = true
	cfg.Sources = map[string]bool{"test":true}
	service := NewMusicServiceWithDependencies(nil, cfg, sources, &serviceTestEngine{state:music.PlaybackIdle, volume:80})
	defer service.Shutdown()
	track := music.Track{ID:"track-1", SourceID:"test", Title:"Search-only track"}
	if _, err := service.Enqueue(track, "viewer"); err != nil { t.Fatalf("search-only track should be queueable before URL resolution: %v", err) }
	if got := len(service.GetQueue().Items); got != 1 { t.Fatalf("expected one queued item, got %d", got) }
}

func TestMusicServiceRejectsDisabledSource(t *testing.T) {
	service, _ := newTestMusicService(t, false)
	_, err := service.Enqueue(music.Track{ID:"track-1", SourceID:"test", Title:"Test song"}, "")
	if err == nil { t.Fatal("expected disabled source to be rejected") }
}

func TestParseSongRequest(t *testing.T) {
	tests := []struct {
		content string
		want string
		ok bool
	}{
		{content:"点歌 稻香 周杰伦", want:"稻香 周杰伦", ok:true},
		{content:"点歌\u3000稻香", want:"稻香", ok:true},
		{content:"点歌", ok:false},
		{content:"点歌稻香", ok:false},
		{content:"请点歌 稻香", ok:false},
	}
	for _, test := range tests {
		got, ok := parseSongRequest(test.content, "点歌")
		if got != test.want || ok != test.ok {
			t.Errorf("parseSongRequest(%q) = (%q, %v), want (%q, %v)", test.content, got, ok, test.want, test.ok)
		}
	}
}

func TestMusicModuleTurnsDanmakuIntoQueuedPlayback(t *testing.T) {
	buffer := events.NewBuffer(events.NewBus())
	sources := music.NewSourceRegistry()
	if err := sources.Register(serviceTestSource{}); err != nil { t.Fatal(err) }
	cfg := config.DefaultMusicConfig()
	cfg.Enabled = true
	cfg.CommandEnabled = true
	cfg.Sources = map[string]bool{"test":true}
	engine := &serviceTestEngine{state:music.PlaybackIdle, volume:80, loadedCh:make(chan music.Track, 1)}
	service := NewMusicServiceWithDependencies(nil, cfg, sources, engine, buffer)
	module := NewMusicModule(service)
	if err := module.Start(context.Background()); err != nil { t.Fatal(err) }
	defer module.Stop(context.Background())

	buffer.Add(events.Event{PlatformID:"bilibili", Type:"danmaku", UserID:42, UserName:"viewer", Content:"点歌 Test song"})
	select {
	case track := <-engine.loadedCh:
		if track.Title != "Test song" || track.SourceID != "test" { t.Fatalf("unexpected auto-played track: %#v", track) }
	case <-time.After(3 * time.Second):
		t.Fatal("danmaku point request was not processed")
	}
	snapshot := service.GetQueue()
	if snapshot.Current == nil || snapshot.Current.RequestedBy != "viewer" {
		t.Fatalf("expected viewer request to be current: %#v", snapshot.Current)
	}
}

func TestMusicServicePreviousRestoresHistoryAndKeepsCurrentAsNext(t *testing.T) {
	service, engine := newTestMusicService(t, true)
	defer service.Shutdown()
	first := music.Track{ID:"first", SourceID:"test", Title:"First song"}
	second := music.Track{ID:"second", SourceID:"test", Title:"Second song"}
	if _, err := service.Enqueue(first, ""); err != nil { t.Fatal(err) }
	if err := service.PlayNext(); err != nil { t.Fatal(err) }
	if _, err := service.Enqueue(second, ""); err != nil { t.Fatal(err) }
	if err := service.Next(); err != nil { t.Fatal(err) }
	if loaded := engine.CurrentLoaded(); loaded == nil || loaded.ID != "second" { t.Fatalf("expected second track, got %#v", loaded) }
	if err := service.Previous(); err != nil { t.Fatal(err) }
	if loaded := engine.CurrentLoaded(); loaded == nil || loaded.ID != "first" { t.Fatalf("previous did not restore first track: %#v", loaded) }
	if err := service.Next(); err != nil { t.Fatal(err) }
	if loaded := engine.CurrentLoaded(); loaded == nil || loaded.ID != "second" { t.Fatalf("next did not restore second track: %#v", loaded) }
}

func TestMusicServiceSearchPageReturnsPaginationMetadata(t *testing.T) {
	service, _ := newTestMusicService(t, true)
	defer service.Shutdown()

	response, err := service.SearchPage("test song", 1)
	if err != nil {
		t.Fatal(err)
	}
	if response.Page != 1 || response.PageSize <= 0 || response.MaxPage != 1 || response.Total != 1 {
		t.Fatalf("unexpected search pagination metadata: %#v", response)
	}
	if len(response.Tracks) != 1 || response.Tracks[0].ID != "track-1" {
		t.Fatalf("unexpected paged search tracks: %#v", response.Tracks)
	}
}

func TestNewMusicServiceRegistersFiveSearchSources(t *testing.T) {
	service := NewMusicService(nil, config.DefaultMusicConfig())
	defer service.Shutdown()

	sources := service.GetSources()
	if len(sources) != 5 {
		t.Fatalf("expected five registered music sources, got %#v", sources)
	}
	want := map[string]bool{"qq_music": true, "netease": true, "kugou": true, "kuwo": true, "migu": true}
	for _, source := range sources {
		if !want[source.ID] {
			t.Errorf("unexpected registered music source %q", source.ID)
		}
		delete(want, source.ID)
	}
	for id := range want {
		t.Errorf("expected music source %q was not registered", id)
	}
	for _, source := range sources {
		if !source.Available || !source.Capabilities.Search {
			t.Errorf("source %q is not available for search: %#v", source.ID, source)
		}
	}
}


func assertLXMusicInfoKeys(t *testing.T, info map[string]any, want []string) {
	t.Helper()
	if len(info) != len(want) {
		t.Fatalf("LX musicInfo contains unexpected/missing fields: got %d keys, want %d (%#v)", len(info), len(want), info)
	}
	for _, key := range want {
		if _, exists := info[key]; !exists {
			t.Errorf("LX musicInfo is missing contract field %q", key)
		}
	}
}

func TestLXQQMusicInfoMatchesLXMusicFlatContract(t *testing.T) {
	track := music.Track{
		ID: "song-mid", SourceID: "qq_music", LXSourceKey: "tx",
		Title: "稻香", Artist: "周杰伦", Album: "魔杰座",
		DurationMS: 223000, CoverURL: "https://img.example/cover.jpg",
		SourceData: map[string]string{
			"id": "123456", "media_mid": "media-mid", "albummid": "album-mid", "albumid": "789",
			"interval": "223", // QQ API seconds must not overwrite LX's mm:ss value.
			"size_128mp3": "1048576", "size_320mp3": "2097152",
			"size_flac": "0", "size_hires": "4194304",
		},
	}
	info := buildLXMusicInfo(track, "320k")
	if info["source"] != "tx" || info["songmid"] != "song-mid" || info["songId"] != int64(123456) ||
		info["strMediaMid"] != "media-mid" || info["albumMid"] != "album-mid" ||
		info["albumId"] != "album-mid" || info["albumName"] != "魔杰座" ||
		info["img"] != track.CoverURL || info["interval"] != "03:43" {
		t.Fatalf("QQ musicInfo does not match LX Music flat fields: %#v", info)
	}
	types, ok := info["types"].([]any)
	if !ok || len(types) != 3 || types[0].(map[string]any)["type"] != "128k" ||
		types[0].(map[string]any)["size"] != "1.00 MiB" ||
		types[1].(map[string]any)["type"] != "320k" ||
		types[1].(map[string]any)["size"] != "2.00 MiB" ||
		types[2].(map[string]any)["type"] != "flac24bit" {
		t.Fatalf("unexpected QQ types: %#v", info["types"])
	}
	available, ok := info["_types"].(map[string]any)
	if !ok || len(available) != 3 || available["320k"].(map[string]any)["size"] != "2.00 MiB" {
		t.Fatalf("unexpected QQ _types: %#v", info["_types"])
	}
	assertLXMusicInfoKeys(t, info, []string{"name", "singer", "source", "songmid", "interval", "albumName", "img", "typeUrl", "types", "_types", "songId", "strMediaMid", "albumMid", "albumId"})
}

func TestLXQQMusicInfoDoesNotFabricateMediaMid(t *testing.T) {
	track := music.Track{
		ID: "song-mid", LXSourceKey: "tx", Title: "稻香", Artist: "周杰伦",
		SourceData: map[string]string{"id": "123456", "albummid": "album-mid"},
	}
	info := buildLXMusicInfo(track, "128k")
	if _, exists := info["strMediaMid"]; exists {
		t.Fatalf("missing strMediaMid must be omitted like LX's undefined property, got %#v", info["strMediaMid"])
	}
	if info["albumId"] != "album-mid" { t.Fatalf("QQ albumId must mirror LX album.mid, got %#v",info["albumId"]) }
	missingAlbum:=buildLXMusicInfo(music.Track{ID:"song-mid",LXSourceKey:"tx",Title:"稻香",SourceData:map[string]string{"id":"123456"}},"128k")
	if _, exists := missingAlbum["albumId"]; exists { t.Fatalf("QQ albumId must be omitted when album.mid is missing, got %#v", missingAlbum["albumId"]) }
}

func TestLXQQQualitySelectionUsesPerTrackAvailability(t *testing.T) {
	track := music.Track{
		LXSourceKey: "tx",
		SourceData: map[string]string{
			"size_128mp3": "1000", "size_320mp3": "0",
			"size_flac": "2000", "size_hires": "0",
		},
	}
	if got := selectLXQuality([]string{"320k", "flac", "128k"}, false, track); got != "128k" {
		t.Fatalf("expected LX default quality 128k, got %#v", got)
	}
	if got := selectLXQuality([]string{"320k"}, false, track); got != "128k" {
		t.Fatalf("LX default remains 128k when track provides it, got %#v", got)
	}
}

func TestLXMiguMusicInfoMatchesLXFields(t *testing.T){
 tr:=music.Track{ID:"mg-song-id",LXSourceKey:"mg",Title:"找自己",Artist:"陶喆",Album:"I'm OK",DurationMS:304000,CoverURL:"https://img.example/cover.jpg",SourceData:map[string]string{
 "songmid":"mg-song-id","copyrightId":"mg-copy-id","albumId":"album-9","lrcUrl":"https://lyrics.example/lrc",
 "audio_formats_json":`[{"formatType":"PQ","asize":1048576,"isize":0},{"formatType":"HQ","asize":2097152,"isize":0},{"formatType":"ZQ24","asize":4194304,"isize":0}]`,
 }}
 info:=buildLXMusicInfo(tr,"320k")
 if info["source"]!="mg"||info["songmid"]!="mg-song-id"||info["copyrightId"]!="mg-copy-id"||info["albumId"]!="album-9"||info["albumName"]!="I'm OK"||info["interval"]!="05:04"||info["img"]!=tr.CoverURL||info["lrcUrl"]!="https://lyrics.example/lrc"{t.Fatalf("Migu musicInfo mismatch: %#v",info)}
 types,ok:=info["types"].([]any);if !ok||len(types)!=3||types[0].(map[string]any)["type"]!="128k"||types[1].(map[string]any)["type"]!="320k"||types[2].(map[string]any)["type"]!="flac24bit"{t.Fatalf("unexpected Migu qualities: %#v",info["types"])}
}

func TestLXKugouMusicInfoPreservesPerQualityHashes(t *testing.T) {
	track := music.Track{ID:"123",LXSourceKey:"kg",Title:"找自己",Artist:"陶喆",Album:"I'm OK",DurationMS:304000,SourceData:map[string]string{
		"Audioid":"123","MixSongID":"456","AlbumID":"album-9","FileHash":"base-hash","FileSize":"1048576",
		"HQFileHash":"hq-hash","HQFileSize":"2097152","SQFileHash":"sq-hash","SQFileSize":"4194304",
		"ResFileHash":"res-hash","ResFileSize":"8388608",
	}}
	info:=buildLXMusicInfo(track,"320k")
	if info["source"]!="kg"||info["songmid"]!=int64(123)||info["albumAudioId"]!=int64(456)||info["albumId"]!="album-9"||info["hash"]!="base-hash"||info["interval"]!="05:04"{t.Fatalf("Kugou LX musicInfo mismatch: %#v",info)}
	types,ok:=info["types"].([]any)
	if !ok||len(types)!=4||types[0].(map[string]any)["hash"]!="base-hash"||types[3].(map[string]any)["hash"]!="res-hash"{t.Fatalf("Kugou quality hashes missing: %#v",info["types"])}
	assertLXMusicInfoKeys(t, info, []string{"name", "singer", "source", "songmid", "interval", "albumName", "img", "typeUrl", "types", "_types", "albumId", "albumAudioId", "hash"})
	if got:=selectLXQuality([]string{"flac24bit","flac","320k"},false,track);got!="128k"{t.Fatalf("expected LX default 128k, got %#v",got)}
	if got:=selectLXQuality([]string{"flac24bit"},false,track);got!="128k"{t.Fatalf("expected LX default 128k, got %#v",got)}
	track.SourceData["ResFileSize"]="0"
	if got:=selectLXQuality([]string{"flac24bit"},false,track);got!="128k"{t.Fatalf("LX default remains 128k while base MP3 exists, got %#v",got)}
}

func TestLXNetEaseMusicInfoUsesPrivilegeAndQualityFiles(t *testing.T) {
	track:=music.Track{ID:"321",LXSourceKey:"wy",Title:"第三页歌曲",Artist:"歌手",Album:"专辑",DurationMS:180000,CoverURL:"https://img.invalid/cover.jpg",SourceData:map[string]string{
		"albumId":"12","wy_privilege":`{"maxbr":999000,"maxBrLevel":"hires"}`,
		"wy_l":`{"size":1000}`,"wy_h":`{"size":3000}`,"wy_sq":`{"size":6000}`,"wy_hr":`{"size":12000}`,
	}}
	info:=buildLXMusicInfo(track,"flac")
	if info["source"]!="wy"||info["songmid"]!=int64(321)||info["albumId"]!=int64(12)||info["interval"]!="03:00"||info["img"]!=track.CoverURL{t.Fatalf("NetEase LX musicInfo mismatch: %#v",info)}
	assertLXMusicInfoKeys(t, info, []string{"name", "singer", "source", "songmid", "interval", "albumName", "img", "typeUrl", "types", "_types", "albumId"})
	types,ok:=info["types"].([]any)
	if !ok||len(types)!=4||types[0].(map[string]any)["type"]!="128k"||types[1].(map[string]any)["type"]!="320k"||types[3].(map[string]any)["type"]!="flac24bit"{t.Fatalf("unexpected NetEase qualities: %#v",info["types"])}
	if got:=selectLXQuality([]string{"flac24bit","flac","320k","128k"},false,track);got!="128k"{t.Fatalf("expected LX default 128k, got %#v",got)}
	track.SourceData["wy_privilege"]=`{"maxbr":128000,"maxBrLevel":"standard"}`
	if got:=selectLXQuality([]string{"flac","320k","128k"},false,track);got!="128k"{t.Fatalf("must not request unavailable NetEase quality, got %#v",got)}
}

func TestLXKuwoMusicInfoUsesNInfoQualityCapabilities(t *testing.T) {
	track := music.Track{
		ID: "456", LXSourceKey: "kw", Title: "找自己", Artist: "陶喆",
		Album: "I'm OK", DurationMS: 304000,
		SourceData: map[string]string{
			"albumId": "album-9",
			"N_MINFO": "level:standard,bitrate:128,format:mp3,size:4.2;level:high,bitrate:320,format:mp3,size:8.6;level:sq,bitrate:2000,format:flac,size:25.4",
		},
	}
	info := buildLXMusicInfo(track, "flac")
	if info["source"] != "kw" || info["songmid"] != "456" || info["albumId"] != "album-9" ||
		info["albumName"] != "I'm OK" || info["interval"] != "05:04" || info["img"] != "" {
		t.Fatalf("Kuwo musicInfo mismatch: %#v", info)
	}
	types, ok := info["types"].([]any)
	assertLXMusicInfoKeys(t, info, []string{"name", "singer", "source", "songmid", "interval", "albumName", "img", "typeUrl", "types", "_types", "albumId"})
	if !ok || len(types) != 3 || types[0].(map[string]any)["type"] != "flac" ||
		types[1].(map[string]any)["type"] != "320k" || types[2].(map[string]any)["type"] != "128k" {
		t.Fatalf("unexpected Kuwo qualities: %#v", info["types"])
	}
	if got := selectLXQuality([]string{"320k", "flac", "128k"}, false, track); got != "128k" {
		t.Fatalf("expected LX default 128k, got %#v", got)
	}
	if got := selectLXQuality([]string{"flac24bit"}, false, track); got != "128k" {
		t.Fatalf("LX default remains 128k when track provides it, got %#v", got)
	}
}

func TestLXMiguQualitySelectionUsesPerTrackAvailability(t *testing.T) {
	track:=music.Track{LXSourceKey:"mg",SourceData:map[string]string{"audio_formats_json":`[{"formatType":"PQ","asize":1000},{"formatType":"ZQ24","asize":2000}]`}}
	if got:=selectLXQuality([]string{"320k","flac24bit","128k"},false,track);got!="128k"{t.Fatalf("expected LX default 128k, got %#v",got)}
	if got:=selectLXQuality([]string{"320k"},false,track);got!="128k"{t.Fatalf("LX default remains 128k when track provides it, got %#v",got)}
}


func TestLXMusicFallbackMatchesFiveSecondTolerance(t *testing.T) {
	reference := music.Track{SourceID: "tx", Title: "稻香", Artist: "周杰伦", DurationMS: 180000}
	candidates := []music.Track{
		{SourceID: "wy", LXSourceKey: "wy", ID: "too-far", Title: "稻香", Artist: "周杰伦", DurationMS: 186000},
		{SourceID: "kg", LXSourceKey: "kg", ID: "within", Title: "稻香", Artist: "周杰伦", DurationMS: 185000},
	}
	got := findLXMusicMatches(reference, candidates)
	if len(got) != 1 || got[0].ID != "within" {
		t.Fatalf("LX matcher must accept a 5-second difference and reject 6 seconds: %#v", got)
	}
}

func TestLXMusicFallbackNormalizesPunctuationAndSingerOrder(t *testing.T) {
	reference := music.Track{SourceID: "tx", Title: "稻香！", Artist: "周杰伦/方文山", DurationMS: 180000}
	candidate := music.Track{SourceID: "wy", LXSourceKey: "wy", ID: "normalized", Title: "稻香", Artist: "方文山、周杰伦", DurationMS: 180000}
	got := findLXMusicMatches(reference, []music.Track{candidate})
	if len(got) != 1 || got[0].ID != "normalized" {
		t.Fatalf("LX matcher should normalize punctuation and multi-singer order: %#v", got)
	}
}

func TestLXMusicFallbackRejectsDifferentVersionMarkers(t *testing.T) {
	reference := music.Track{SourceID: "tx", Title: "稻香", Artist: "周杰伦", DurationMS: 180000}
	candidate := music.Track{SourceID: "wy", LXSourceKey: "wy", ID: "live", Title: "稻香 Live", Artist: "周杰伦", DurationMS: 180000}
	if got := findLXMusicMatches(reference, []music.Track{candidate}); len(got) != 0 {
		t.Fatalf("LX matcher must not substitute a different version: %#v", got)
	}
}

func TestLXMusicFallbackUsesLXSourceOrderAndTenRankingTiers(t *testing.T) {
	reference := music.Track{SourceID: "tx", Title: "稻香", Artist: "周杰伦", Album: "魔杰座", DurationMS: 180000}
	candidates := []music.Track{
		{SourceID: "mg", LXSourceKey: "mg", ID: "third-tier", Title: "稻香", Artist: "周杰伦", Album: "其他专辑", DurationMS: 181000},
		{SourceID: "kg", LXSourceKey: "kg", ID: "second-source", Title: "稻香", Artist: "周杰伦", Album: "魔杰座", DurationMS: 180000},
		{SourceID: "kw", LXSourceKey: "kw", ID: "first-source", Title: "稻香", Artist: "周杰伦", Album: "魔杰座", DurationMS: 180000},
	}
	got := findLXMusicMatches(reference, candidates)
	if len(got) != 3 || got[0].ID != "first-source" || got[1].ID != "second-source" || got[2].ID != "third-tier" {
		t.Fatalf("LX matcher should preserve source order within the highest matching tier and rank exact duration first: %#v", got)
	}
}


func TestFlowerLXMusicURLLookupIdentifiersMatchAllFivePlatforms(t *testing.T) {
	cases := []struct {
		name string
		track music.Track
		lookupKey string
		want any
	}{
		{
			name: "QQ Music",
			track: music.Track{ID: "qq-song-mid", SourceID: "qq_music", LXSourceKey: "tx", Title: "song", SourceData: map[string]string{"id": "12345"}},
			lookupKey: "songmid", want: "qq-song-mid",
		},
		{
			name: "NetEase",
			track: music.Track{ID: "150617", SourceID: "netease", LXSourceKey: "wy", Title: "song"},
			lookupKey: "songmid", want: int64(150617),
		},
		{
			name: "Kuwo",
			track: music.Track{ID: "62355680", SourceID: "kuwo", LXSourceKey: "kw", Title: "song"},
			lookupKey: "songmid", want: "62355680",
		},
		{
			name: "Kugou",
			track: music.Track{ID: "123", SourceID: "kugou", LXSourceKey: "kg", Title: "song", SourceData: map[string]string{"Audioid": "123", "FileHash": "kugou-file-hash"}},
			lookupKey: "hash", want: "kugou-file-hash",
		},
		{
			name: "Migu",
			track: music.Track{ID: "migu-song-id", SourceID: "migu", LXSourceKey: "mg", Title: "song", SourceData: map[string]string{"copyrightId": "migu-copyright-id"}},
			lookupKey: "copyrightId", want: "migu-copyright-id",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			info := buildLXMusicInfo(tc.track, "128k")
			if info["source"] != tc.track.LXSourceKey {
				t.Fatalf("source key = %#v, want %q", info["source"], tc.track.LXSourceKey)
			}
			if info[tc.lookupKey] != tc.want {
				t.Fatalf("Flower musicUrl lookup field %q = %#v, want %#v; musicInfo: %#v", tc.lookupKey, info[tc.lookupKey], tc.want, info)
			}
		})
	}
}


