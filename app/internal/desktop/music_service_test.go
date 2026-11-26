package desktop

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"encoding/json"
	"sync/atomic"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/dongguo4812/My-LiveHub/app/internal/config"
	"github.com/dongguo4812/My-LiveHub/app/internal/events"
	"github.com/dongguo4812/My-LiveHub/app/internal/music"
)
func TestSongRequestMatchesTrackRejectsUnrelatedSearchResult(t *testing.T) {
	requested := "太空基米"
	if songRequestMatchesTrack(requested, music.Track{Title: "光年之外", Artist: "邓紫棋"}) {
		t.Fatal("unrelated first search result must not be treated as the requested song")
	}
	if !songRequestMatchesTrack(requested, music.Track{Title: "太空基米", Artist: "测试歌手"}) {
		t.Fatal("exact title should match")
	}
	if !songRequestMatchesTrack("测试歌手 太空基米", music.Track{Title: "太空基米", Artist: "测试歌手"}) {
		t.Fatal("query containing artist and title should match")
	}
	if !songRequestMatchesTrack("太空基米", music.Track{Title: "太空基米（DJ版）", Artist: "测试歌手"}) {
		t.Fatal("title with edition suffix should match")
	}
}

func TestParseSongRequest(t *testing.T){
 tests:=[]struct{content,prefix,want string;ok bool}{{"点歌 稻香","点歌","稻香",true},{"点歌稻香","点歌","",false},{"点歌   稻香 ","点歌","稻香",true},{"闲聊点歌 稻香","点歌","",false},{"点歌","点歌","",false}}
 for _,tt:=range tests{got,ok:=parseSongRequest(tt.content,tt.prefix);if got!=tt.want||ok!=tt.ok{t.Errorf("parseSongRequest(%q)=(%q,%v), want (%q,%v)",tt.content,got,ok,tt.want,tt.ok)}}
}
func TestNewMusicServiceDefaultsToHTMLAudioPlayback(t *testing.T) {
	service := NewMusicService(nil, config.DefaultMusicConfig())
	defer service.Shutdown()
	if _, ok := service.engine.(*htmlAudioPlaybackEngine); !ok {
		t.Fatalf("default playback engine = %T, want *htmlAudioPlaybackEngine", service.engine)
	}
}

func TestNewMusicServiceRegistersFiveLXSearchPlatforms(t *testing.T){
 s:=NewMusicService(nil, config.DefaultMusicConfig());defer s.Shutdown()
 got:=s.GetSources()
 want:=[]string{"tx","kw","kg","wy","mg"}
 if len(got)!=len(want){t.Fatalf("got %d sources, want %d: %#v",len(got),len(want),got)}
 for i,id:=range want { if got[i].ID!=id { t.Errorf("source[%d]=%q, want %q",i,got[i].ID,id) }; if !got[i].Capabilities.Search { t.Errorf("source %q is not marked searchable",id) } }
}

func TestSkipCommandMatchingIsExact(t *testing.T) {
	if !isSkipCommand(" 切歌 ", "切歌") { t.Fatal("trimmed exact command should match") }
	if isSkipCommand("切歌吧", "切歌") { t.Fatal("command must not match ordinary messages") }
	if isSkipCommand("切歌", "") { t.Fatal("empty command must not match") }
}
func TestOnlyRoomOwnerAndModeratorsCanSkip(t *testing.T) {
	if !canSkipMusic(events.Event{UserID: 100, IsRoomOwner: true}) { t.Fatal("room owner must be allowed") }
	if !canSkipMusic(events.Event{UserID: 200, IsModerator: true}) { t.Fatal("moderator must be allowed") }
	if canSkipMusic(events.Event{UserID: 300}) { t.Fatal("ordinary viewer must not be allowed") }
}


func TestIndependentSearchSourcePriorityPersistsThroughServiceReload(t *testing.T) {
	defaultJSON, err := json.Marshal(config.DefaultMusicConfig())
	if err != nil { t.Fatal(err) }
	manager, err := config.NewManager(fstest.MapFS{"music.json": &fstest.MapFile{Data: defaultJSON}}, t.TempDir())
	if err != nil { t.Fatal(err) }
	value := config.DefaultMusicConfig()
	value.EnabledSearchSources = []string{"mg", "tx", "kw"}
	service := NewMusicService(manager, value)
	if err := service.SaveConfig(value); err != nil { t.Fatal(err) }
	if got := service.enabledSearchSourceIDs(); len(got) != 3 || got[0] != "mg" || got[1] != "tx" || got[2] != "kw" {
		t.Fatalf("configured search order = %v", got)
	}
	reloaded, err := config.LoadMusicConfig(manager)
	if err != nil { t.Fatal(err) }
	restarted := NewMusicService(manager, reloaded)
	defer restarted.Shutdown()
	if got := restarted.enabledSearchSourceIDs(); len(got) != 3 || got[0] != "mg" || got[1] != "tx" || got[2] != "kw" {
		t.Fatalf("search order after restart = %v", got)
	}
}

func TestEmptySearchSourceSelectionDisablesAllPlatforms(t *testing.T) {
	value := config.DefaultMusicConfig()
	value.EnabledSearchSources = []string{}
	service := NewMusicService(nil, value)
	defer service.Shutdown()
	if got := service.enabledSearchSourceIDs(); len(got) != 0 {
		t.Fatalf("explicitly empty source selection should stay empty, got %v", got)
	}
}


type nextTrackTestEngine struct {
	stopCalls int
	pauseCalls int
}
func (e *nextTrackTestEngine) Load(context.Context, music.Track) error { return nil }
func (e *nextTrackTestEngine) Pause(context.Context) error { e.pauseCalls++; return nil }
func (e *nextTrackTestEngine) Resume(context.Context) error { return nil }
func (e *nextTrackTestEngine) Stop(context.Context) error { e.stopCalls++; return nil }
func (e *nextTrackTestEngine) Snapshot(context.Context) (music.PlaybackSnapshot, error) {
	return music.PlaybackSnapshot{State: music.PlaybackPlaying}, nil
}

func TestNextWithoutPendingTrackDoesNotPauseCurrentPlayback(t *testing.T) {
	engine := &nextTrackTestEngine{}
	service := NewMusicServiceWithDependencies(nil, config.DefaultMusicConfig(), nil, engine)
	current, err := service.queue.SetCurrentTrack(music.Track{
		ID: "tx_current", SourceID: "tx", Title: "当前歌曲", Artist: "歌手",
	}, searchPlayMarker)
	if err != nil { t.Fatal(err) }

	if err := service.Next(); err == nil {
		t.Fatal("Next should report that there is no pending track")
	}
	snapshot := service.queue.Snapshot()
	if snapshot.Current == nil || snapshot.Current.ID != current.ID {
		t.Fatalf("current track changed despite empty queue: %#v", snapshot.Current)
	}
	if engine.stopCalls != 0 || engine.pauseCalls != 0 {
		t.Fatalf("Next unexpectedly controlled playback: stop=%d pause=%d", engine.stopCalls, engine.pauseCalls)
	}
}

func TestPlaybackFallbackIsClaimedOnlyOncePerQueueItem(t *testing.T) {
	service := NewMusicServiceWithDependencies(nil, config.DefaultMusicConfig(), nil, &nextTrackTestEngine{})
	item := &music.QueueItem{ID: "queue-item-1", Track: music.Track{Title: "哈哈哈", Artist: "测试歌手"}}
	service.mu.Lock()
	service.playGeneration = 7
	service.mu.Unlock()
	if !service.claimPlaybackFallback(item, 7) {
		t.Fatal("first asynchronous failure should be allowed one cross-source fallback")
	}
	if service.claimPlaybackFallback(item, 7) {
		t.Fatal("same queue item must not repeatedly fall back and loop between providers")
	}
	if service.claimPlaybackFallback(item, 6) {
		t.Fatal("stale playback watcher must not claim a fallback")
	}
}

func TestPlaybackSnapshotEnded(t *testing.T) {
	tests := []struct {
		name string
		snapshot music.PlaybackSnapshot
		sawPlaying bool
		want bool
	}{
		{name: "stopped before playback observed", snapshot: music.PlaybackSnapshot{State: music.PlaybackStopped}, sawPlaying: false, want: false},
		{name: "stopped after playback", snapshot: music.PlaybackSnapshot{State: music.PlaybackStopped}, sawPlaying: true, want: true},
		{name: "playing at final two seconds", snapshot: music.PlaybackSnapshot{State: music.PlaybackPlaying, PositionMS: 98000, DurationMS: 100000}, sawPlaying: true, want: true},
		{name: "playing before end threshold", snapshot: music.PlaybackSnapshot{State: music.PlaybackPlaying, PositionMS: 97000, DurationMS: 100000}, sawPlaying: true, want: false},
		{name: "paused near end", snapshot: music.PlaybackSnapshot{State: music.PlaybackPaused, PositionMS: 99000, DurationMS: 100000}, sawPlaying: true, want: false},
		{name: "playing without duration", snapshot: music.PlaybackSnapshot{State: music.PlaybackPlaying, PositionMS: 99000}, sawPlaying: true, want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := playbackSnapshotEnded(tt.sawPlaying, tt.snapshot); got != tt.want {
				t.Fatalf("playbackSnapshotEnded() = %v, want %v", got, tt.want)
			}
		})
	}
}

type queueAdvanceTestEngine struct {
	loadCalls atomic.Int32
}

func (e *queueAdvanceTestEngine) Load(context.Context, music.Track) error {
	e.loadCalls.Add(1)
	return nil
}
func (e *queueAdvanceTestEngine) Pause(context.Context) error { return nil }
func (e *queueAdvanceTestEngine) Resume(context.Context) error { return nil }
func (e *queueAdvanceTestEngine) Stop(context.Context) error { return nil }
func (e *queueAdvanceTestEngine) Snapshot(context.Context) (music.PlaybackSnapshot, error) {
	return music.PlaybackSnapshot{State: music.PlaybackIdle}, nil
}

func TestAdvanceAfterPlaybackEndPrioritizesMyLiveHubQueue(t *testing.T) {
	value := config.DefaultMusicConfig()
	value.Enabled = true
	engine := &queueAdvanceTestEngine{}
	service := NewMusicServiceWithDependencies(nil, value, nil, engine)
	defer service.cancelPlaybackWatcher()

	track := music.Track{ID: "tx_next", SourceID: "tx", LXSourceKey: "tx", Title: "待播歌曲", Artist: "测试歌手"}
	if _, err := service.queue.Enqueue(track, "viewer"); err != nil { t.Fatal(err) }
	finished := &music.QueueItem{Track: music.Track{Title: "当前歌曲", Artist: "测试歌手"}}
	snapshot := music.PlaybackSnapshot{State: music.PlaybackPlaying, Track: &finished.Track}

	if err := service.advanceAfterPlaybackEnd(snapshot, finished); err != nil {
		t.Fatalf("advanceAfterPlaybackEnd returned error: %v", err)
	}
	current := service.queue.Snapshot().Current
	if current == nil || current.Track.ID != track.ID {
		t.Fatalf("My-LiveHub queue was not promoted to current: %#v", current)
	}
	if got := engine.loadCalls.Load(); got != 1 {
		t.Fatalf("playback engine Load calls = %d, want 1", got)
	}
}

func TestAdvanceAfterPlaybackEndLeavesExternalPlaylistsUntouched(t *testing.T) {
	engine := &queueAdvanceTestEngine{}
	service := NewMusicServiceWithDependencies(nil, config.DefaultMusicConfig(), nil, engine)
	finished := &music.QueueItem{Track: music.Track{Title: "当前歌曲", Artist: "测试歌手"}}
	snapshot := music.PlaybackSnapshot{State: music.PlaybackStopped}

	if err := service.advanceAfterPlaybackEnd(snapshot, finished); err != nil {
		t.Fatalf("advanceAfterPlaybackEnd returned error: %v", err)
	}
}

func TestGetPlaybackStatusReportsActualResolvedProvider(t *testing.T) {
	engine := &nextTrackTestEngine{}
	service := NewMusicServiceWithDependencies(nil, config.DefaultMusicConfig(), nil, engine)
	_, err := service.queue.SetCurrentTrack(music.Track{
		ID: "kg_hash123", SourceID: "kg", LXSourceKey: "kg", Title: "稻香", Artist: "周杰伦",
	}, searchPlayMarker)
	if err != nil { t.Fatal(err) }
	snapshot, err := service.GetPlaybackStatus()
	if err != nil { t.Fatal(err) }
	if snapshot.Track == nil { t.Fatal("current queue track was not attached to playback status") }
	if snapshot.Track.SourceID != "kg" || snapshot.Track.LXSourceKey != "kg" {
		t.Fatalf("actual resolved source was not preserved: %#v", snapshot.Track)
	}
}


type queueRecoveryTestEngine struct {
	state music.PlaybackState
	loadCalls atomic.Int32
}
func (e *queueRecoveryTestEngine) Load(context.Context, music.Track) error {
	e.loadCalls.Add(1)
	return nil
}
func (e *queueRecoveryTestEngine) Pause(context.Context) error { return nil }
func (e *queueRecoveryTestEngine) Resume(context.Context) error { return nil }
func (e *queueRecoveryTestEngine) Stop(context.Context) error { return nil }
func (e *queueRecoveryTestEngine) Snapshot(context.Context) (music.PlaybackSnapshot, error) {
	return music.PlaybackSnapshot{State: e.state}, nil
}

func TestEnqueueStartsNextWhenQueueCurrentIsStale(t *testing.T) {
	value := config.DefaultMusicConfig()
	engine := &queueRecoveryTestEngine{state: music.PlaybackStopped}
	service := NewMusicServiceWithDependencies(nil, value, nil, engine)
	defer service.Shutdown()

	priority := service.enabledSearchSourceIDs()
	if len(priority) == 0 { t.Fatal("expected at least one enabled search source") }
	source := priority[0]
	oldTrack := music.Track{ID: source + "_old", SourceID: source, LXSourceKey: source, Title: "旧歌曲", Artist: "测试歌手"}
	if _, err := service.queue.SetCurrentTrack(oldTrack, "previous"); err != nil { t.Fatal(err) }

	newTrack := music.Track{ID: source + "_new", SourceID: source, LXSourceKey: source, Title: "新歌曲", Artist: "测试歌手"}
	item, err := service.Enqueue(newTrack, "viewer")
	if err != nil { t.Fatalf("Enqueue returned error: %v", err) }

	snapshot := service.queue.Snapshot()
	if snapshot.Current == nil || snapshot.Current.ID != item.ID {
		t.Fatalf("new request was not promoted to current after stale playback: %#v", snapshot)
	}
	if len(snapshot.Items) != 0 { t.Fatalf("new request remained pending: %#v", snapshot.Items) }
	if got := engine.loadCalls.Load(); got != 1 { t.Fatalf("playback Load calls = %d, want 1", got) }
}

func TestEnqueueDoesNotInterruptPausedCurrentTrack(t *testing.T) {
	value := config.DefaultMusicConfig()
	engine := &queueRecoveryTestEngine{state: music.PlaybackPaused}
	service := NewMusicServiceWithDependencies(nil, value, nil, engine)
	defer service.Shutdown()

	priority := service.enabledSearchSourceIDs()
	if len(priority) == 0 { t.Fatal("expected at least one enabled search source") }
	source := priority[0]
	oldTrack := music.Track{ID: source + "_old", SourceID: source, LXSourceKey: source, Title: "暂停中的歌曲", Artist: "测试歌手"}
	current, err := service.queue.SetCurrentTrack(oldTrack, "previous")
	if err != nil { t.Fatal(err) }

	newTrack := music.Track{ID: source + "_new", SourceID: source, LXSourceKey: source, Title: "待播歌曲", Artist: "测试歌手"}
	if _, err := service.Enqueue(newTrack, "viewer"); err != nil { t.Fatalf("Enqueue returned error: %v", err) }

	snapshot := service.queue.Snapshot()
	if snapshot.Current == nil || snapshot.Current.ID != current.ID {
		t.Fatalf("paused current track was interrupted: %#v", snapshot.Current)
	}
	if len(snapshot.Items) != 1 { t.Fatalf("pending queue length = %d, want 1", len(snapshot.Items)) }
	if got := engine.loadCalls.Load(); got != 0 { t.Fatalf("playback Load calls = %d, want 0", got) }
}


type verifiedLyricsTestEngine struct {
	playback music.PlaybackSnapshot
	lyrics string
	lyricCalls int
}
func (e *verifiedLyricsTestEngine) Load(context.Context, music.Track) error { return nil }
func (e *verifiedLyricsTestEngine) Pause(context.Context) error { return nil }
func (e *verifiedLyricsTestEngine) Resume(context.Context) error { return nil }
func (e *verifiedLyricsTestEngine) Stop(context.Context) error { return nil }
func (e *verifiedLyricsTestEngine) Snapshot(context.Context) (music.PlaybackSnapshot, error) { return e.playback, nil }
func (e *verifiedLyricsTestEngine) GetCurrentLyrics(context.Context) (string, error) {
	e.lyricCalls++
	return e.lyrics, nil
}

func TestGetLyricsHTMLAudioUsesNativeProviderAdapter(t *testing.T) {
	service := NewMusicServiceWithDependencies(nil, config.DefaultMusicConfig(), nil, &htmlAudioPlaybackEngine{})
	_, err := service.GetLyrics(music.Track{ID: "tx_without_metadata", SourceID: "tx", Title: "测试歌曲", Artist: "测试歌手"})
	if err == nil || !strings.Contains(err.Error(), "QQ 歌词请求缺少 song_id/songmid") {
		t.Fatalf("HTMLAudio should use the QQ provider lyric adapter, got error %v", err)
	}
}

func TestGetLyricsHTMLAudioRejectsUnknownProviderExplicitly(t *testing.T) {
	service := NewMusicServiceWithDependencies(nil, config.DefaultMusicConfig(), nil, &htmlAudioPlaybackEngine{})
	_, err := service.GetLyrics(music.Track{ID: "unknown_1", SourceID: "unknown", Title: "测试歌曲", Artist: "测试歌手"})
	if err == nil || !strings.Contains(err.Error(), "暂不支持独立歌词解析") {
		t.Fatalf("unsupported provider should return an explicit lyric capability error, got %v", err)
	}
}

func TestGetLyricsRejectsLyricsWhenLXIsPlayingAnotherTrack(t *testing.T) {
	engine := &verifiedLyricsTestEngine{
		playback: music.PlaybackSnapshot{State: music.PlaybackPlaying, Track: &music.Track{Title:"晴天", Artist:"周杰伦"}},
		lyrics: "[00:01.00]不应显示",
	}
	service := NewMusicServiceWithDependencies(nil, config.DefaultMusicConfig(), nil, engine)
	requested := music.Track{ID:"tx_1", SourceID:"tx", Title:"稻香", Artist:"周杰伦"}
	lyrics, err := service.GetLyrics(requested)
	if err != nil { t.Fatal(err) }
	if lyrics != "" { t.Fatalf("lyrics for another currently playing track were returned: %q", lyrics) }
	if engine.lyricCalls != 0 { t.Fatalf("洛雪歌词 API called %d times before track identity was verified", engine.lyricCalls) }
	if len(service.lyricsCache) != 0 { t.Fatalf("unverified lyrics were cached: %#v", service.lyricsCache) }
}

func TestGetLyricsCachesOnlyAfterLXTrackIdentityMatches(t *testing.T) {
	engine := &verifiedLyricsTestEngine{
		playback: music.PlaybackSnapshot{State: music.PlaybackPlaying, Track: &music.Track{Title:"稻香", Artist:"周杰伦"}},
		lyrics: "[00:01.00]这是洛雪当前歌曲歌词",
	}
	service := NewMusicServiceWithDependencies(nil, config.DefaultMusicConfig(), nil, engine)
	requested := music.Track{ID:"tx_1", SourceID:"tx", Title:"稻香", Artist:"周杰伦"}
	lyrics, err := service.GetLyrics(requested)
	if err != nil { t.Fatal(err) }
	if lyrics != engine.lyrics { t.Fatalf("lyrics = %q, want %q", lyrics, engine.lyrics) }
	if len(service.lyricsCache) != 1 { t.Fatalf("verified lyrics cache size = %d, want 1", len(service.lyricsCache)) }
}


func TestMusicCommandsPublishFailureNoticesWhenDisabledOrUnauthorized(t *testing.T) {
	tests := []struct {
		name string
		configure func(*config.MusicConfig)
		content string
		event events.Event
		wantKind string
		wantReason string
	}{
		{
			name: "skip command disabled",
			configure: func(value *config.MusicConfig) { value.SkipCommandEnabled = false },
			content: "切歌",
			event: events.Event{Type: "danmaku", UserName: "房管", IsModerator: true},
			wantKind: "music_skip_failed",
			wantReason: "音乐切歌指令当前已关闭",
		},
		{
			name: "ordinary viewer cannot skip",
			configure: func(value *config.MusicConfig) { value.SkipCommandEnabled = true },
			content: "切歌",
			event: events.Event{Type: "danmaku", UserName: "观众"},
			wantKind: "music_skip_failed",
			wantReason: "仅主播或房管可以切歌",
		},
		{
			name: "song request command disabled",
			configure: func(value *config.MusicConfig) { value.CommandEnabled = false },
			content: "点歌 稻香",
			event: events.Event{Type: "danmaku", UserName: "观众"},
			wantKind: "music_request_failed",
			wantReason: "音乐点歌指令当前已关闭",
		},
		{
			name: "song name missing",
			configure: func(value *config.MusicConfig) { value.CommandEnabled = true },
			content: "点歌",
			event: events.Event{Type: "danmaku", UserName: "观众"},
			wantKind: "music_request_failed",
			wantReason: "请在点歌指令后输入歌曲名称",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			value := config.DefaultMusicConfig()
			value.Enabled = true
			tt.configure(&value)
			service := NewMusicServiceWithDependencies(nil, value, nil, &nextTrackTestEngine{})
			service.started = true
			service.requests = make(chan events.Event, 1)
			broker := NewRequestNoticeBroker()
			service.SetRequestNoticeBroker(broker)
			notices, cancel := broker.Subscribe()
			defer cancel()

			tt.event.Content = tt.content
			service.handleEvent(tt.event)
			select {
			case queued := <-service.requests:
				service.processSongRequest(context.Background(), queued)
			default:
				t.Fatal("configured command was not routed to the music worker")
			}
			select {
			case notice := <-notices:
				if notice.Kind != tt.wantKind { t.Fatalf("notice kind = %q, want %q", notice.Kind, tt.wantKind) }
				if notice.Reason != tt.wantReason { t.Fatalf("notice reason = %q, want %q", notice.Reason, tt.wantReason) }
			case <-time.After(time.Second):
				t.Fatal("expected request notice was not published")
			}
		})
	}
}

func TestMusicCommandsAreRoutedUsingConfiguredValuesWhenTogglesAreOff(t *testing.T) {
	value := config.DefaultMusicConfig()
	value.Enabled = true
	value.CommandPrefix = "歌曲"
	value.CommandEnabled = false
	value.SkipCommand = "换一首"
	value.SkipCommandEnabled = false
	service := NewMusicServiceWithDependencies(nil, value, nil, &nextTrackTestEngine{})
	service.started = true
	service.requests = make(chan events.Event, 2)

	service.handleEvent(events.Event{Type: "danmaku", Content: "歌曲"})
	service.handleEvent(events.Event{Type: "danmaku", Content: "换一首"})
	if got := len(service.requests); got != 2 {
		t.Fatalf("configured disabled commands routed = %d, want 2", got)
	}
	if isSongRequestCommand("歌曲列表", "歌曲") {
		t.Fatal("request command must respect the configured prefix boundary")
	}
}


func TestStoppedMusicModuleStillReportsConfiguredCommands(t *testing.T) {
	value := config.DefaultMusicConfig()
	value.Enabled = true
	buffer := events.NewBuffer(events.NewBus())
	service := NewMusicServiceWithDependencies(nil, value, nil, &nextTrackTestEngine{}, buffer)
	broker := NewRequestNoticeBroker()
	service.SetRequestNoticeBroker(broker)
	notices, cancel := broker.Subscribe()
	defer cancel()

	buffer.Add(events.Event{Type: "danmaku", Content: value.SkipCommand, UserName: "主播"})
	select {
	case notice := <-notices:
		if notice.Kind != "music_skip_failed" || notice.Reason != "音乐模块未启动" {
			t.Fatalf("stopped module skip notice = %#v", notice)
		}
	case <-time.After(time.Second):
		t.Fatal("stopped music module did not report its configured skip command")
	}
}


// Song-request matching must reject a shorter, unrelated prefix result.
func TestSelectSongRequestTrackRejectsReverseContainmentAndPrefersExact(t *testing.T) {
	tracks := []music.Track{
		{ID: "tx_short", Title: "太空", Artist: "歌手甲"},
		{ID: "kg_extended", Title: "太空基米（完整版）", Artist: "歌手乙"},
		{ID: "mg_exact", Title: "太空基米", Artist: "歌手丙"},
	}
	got, ok := selectSongRequestTrack(tracks, "太空基米", "")
	if !ok || got.ID != "mg_exact" { t.Fatalf("selected %#v, matched=%v; want exact title", got, ok) }
	if _, ok := selectSongRequestTrack(tracks[:1], "太空基米", ""); ok { t.Fatal("reverse containment must be rejected") }
	got, ok = selectSongRequestTrack(tracks[1:2], "太空基米", "")
	if !ok || got.ID != "kg_extended" { t.Fatalf("one-way containment should match, got %#v", got) }
}

func TestSelectSongRequestTrackChecksRequestedArtist(t *testing.T) {
	tracks := []music.Track{
		{ID: "wrong_artist", Title: "稻香", Artist: "其他歌手"},
		{ID: "right_artist", Title: "稻香", Artist: "周杰伦"},
	}
	got, ok := selectSongRequestTrack(tracks, "稻香", "周杰伦")
	if !ok || got.ID != "right_artist" { t.Fatalf("artist-constrained selection = %#v, matched=%v", got, ok) }
	if _, ok := selectSongRequestTrack(tracks[:1], "稻香", "周杰伦"); ok { t.Fatal("different artist must be rejected") }
}

func TestSplitSongRequestArtist(t *testing.T) {
	cases := []struct{ input, title, artist string }{
		{"太空基米", "太空基米", ""},
		{"稻香 - 周杰伦", "稻香", "周杰伦"},
		{"稻香－周杰伦", "稻香", "周杰伦"},
		{"晴天 周杰伦", "晴天", "周杰伦"},
		{"晴天  周杰伦", "晴天", "周杰伦"},
		{"晴天　周杰伦", "晴天", "周杰伦"},
	}
	for _, tt := range cases {
		title, artist := splitSongRequestArtist(tt.input)
		if title != tt.title || artist != tt.artist { t.Errorf("splitSongRequestArtist(%q)=(%q,%q), want (%q,%q)", tt.input, title, artist, tt.title, tt.artist) }
	}
}

func TestSearchSongRequestAcrossSourcesFallsBackUntilValidMatch(t *testing.T) {
	var attempted []string
	track, matched, failures := searchSongRequestAcrossSources(context.Background(), []string{"tx", "kw", "kg", "wy"},
		func(source string) ([]music.Track, error) {
			attempted = append(attempted, source)
			switch source {
			case "tx": return nil, errors.New("provider unavailable")
			case "kw": return []music.Track{}, nil
			case "kg": return []music.Track{{ID: "kg_short", Title: "太空", Artist: "歌手"}}, nil
			case "wy": return []music.Track{{ID: "wy_match", Title: "太空基米", Artist: "歌手"}}, nil
			default: return nil, nil
			}
		}, "太空基米", "")
	if !matched || track.ID != "wy_match" { t.Fatalf("track=%#v matched=%v; want wy_match", track, matched) }
	if len(attempted) != 4 || attempted[0] != "tx" || attempted[1] != "kw" || attempted[2] != "kg" || attempted[3] != "wy" { t.Fatalf("attempted sources = %v", attempted) }
	if len(failures) != 3 { t.Fatalf("failures = %v; want three diagnostics", failures) }
}

func TestSearchSongRequestAcrossSourcesStopsAfterFirstValidMatch(t *testing.T) {
	var attempted []string
	track, matched, _ := searchSongRequestAcrossSources(context.Background(), []string{"tx", "kw", "kg"},
		func(source string) ([]music.Track, error) {
			attempted = append(attempted, source)
			if source == "tx" { return []music.Track{{ID: "tx_match", Title: "稻香", Artist: "周杰伦"}}, nil }
			return nil, errors.New("should not be called")
		}, "稻香", "")
	if !matched || track.ID != "tx_match" { t.Fatalf("track=%#v matched=%v", track, matched) }
	if len(attempted) != 1 || attempted[0] != "tx" { t.Fatalf("search continued after valid match: %v", attempted) }
}

func TestSelectSongRequestTrackByCombinedText(t *testing.T) {
	tracks := []music.Track{
		{ID: "wrong", Title: "晴天", Artist: "其他歌手"},
		{ID: "right", Title: "晴天", Artist: "周杰伦"},
	}
	got, ok := selectSongRequestTrackByCombinedText(tracks, "晴天 周杰伦")
	if !ok || got.ID != "right" {
		t.Fatalf("combined query selected %#v, matched=%v; want right", got, ok)
	}
	if _, ok := selectSongRequestTrackByCombinedText(tracks, "晴天 陈奕迅"); ok {
		t.Fatal("combined query must not match an unrelated artist")
	}
}


type failFirstLoadTestEngine struct {
	loadCalls atomic.Int32
}
func (e *failFirstLoadTestEngine) Load(context.Context, music.Track) error {
	if e.loadCalls.Add(1) == 1 { return errors.New("模拟首首歌曲播放失败") }
	return nil
}
func (e *failFirstLoadTestEngine) Pause(context.Context) error { return nil }
func (e *failFirstLoadTestEngine) Resume(context.Context) error { return nil }
func (e *failFirstLoadTestEngine) Stop(context.Context) error { return nil }
func (e *failFirstLoadTestEngine) Snapshot(context.Context) (music.PlaybackSnapshot, error) {
	return music.PlaybackSnapshot{State: music.PlaybackPlaying}, nil
}

func TestPlayNextSkipsFailedTrackAndStartsNextQueuedTrack(t *testing.T) {
	value := config.DefaultMusicConfig()
	value.Enabled = true
	value.EnabledSearchSources = []string{"tx"}
	engine := &failFirstLoadTestEngine{}
	service := NewMusicServiceWithDependencies(nil, value, nil, engine)
	defer service.cancelPlaybackWatcher()

	first := music.Track{ID: "tx_first", SourceID: "tx", LXSourceKey: "tx", Title: "首首歌曲", Artist: "测试歌手"}
	second := music.Track{ID: "tx_second", SourceID: "tx", LXSourceKey: "tx", Title: "第二首歌曲", Artist: "测试歌手"}
	if _, err := service.queue.Enqueue(first, "viewer"); err != nil { t.Fatal(err) }
	if _, err := service.queue.Enqueue(second, "viewer"); err != nil { t.Fatal(err) }

	if err := service.PlayNext(); err != nil { t.Fatalf("PlayNext should continue to the next track after the first fails: %v", err) }
	snapshot := service.queue.Snapshot()
	if snapshot.Current == nil || snapshot.Current.Track.ID != second.ID {
		t.Fatalf("current track = %#v, want second queued track", snapshot.Current)
	}
	if len(snapshot.Items) != 0 { t.Fatalf("pending queue = %#v, want empty", snapshot.Items) }
	if got := engine.loadCalls.Load(); got != 2 { t.Fatalf("Load calls = %d, want 2", got) }
	if service.GetStatus().LastError == "" { t.Fatal("failed first track should leave a diagnostic error") }
}

func TestMusicPlaybackStartTimeoutIsTenSeconds(t *testing.T) {
	if musicPlaybackStartTimeout != 10*time.Second {
		t.Fatalf("music playback start timeout = %s, want 10s", musicPlaybackStartTimeout)
	}
}


func TestMusicRequestCooldownIsPerViewerAndExemptsOwnerAndModerator(t *testing.T) {
	value := config.DefaultMusicConfig()
	value.RequestCooldownSeconds = 30
	service := NewMusicServiceWithDependencies(nil, value, nil, &nextTrackTestEngine{})
	viewer := events.Event{UserID: 101, UserName: "观众"}
	if got := service.requestCooldownRemaining(viewer); got != 0 { t.Fatalf("new viewer should not be cooling down, got %v", got) }
	service.recordRequestCooldown(viewer)
	if got := service.requestCooldownRemaining(viewer); got <= 0 || got > 30*time.Second { t.Fatalf("viewer should be cooling down, got %v", got) }
	otherViewer := events.Event{UserID: 102, UserName: "另一位观众"}
	if got := service.requestCooldownRemaining(otherViewer); got != 0 { t.Fatalf("cooldown must be per viewer, got %v", got) }
	if got := service.requestCooldownRemaining(events.Event{UserID: 101, IsRoomOwner: true}); got != 0 { t.Fatalf("room owner must be exempt, got %v", got) }
	if got := service.requestCooldownRemaining(events.Event{UserID: 101, IsModerator: true}); got != 0 { t.Fatalf("moderator must be exempt, got %v", got) }
}

func TestMusicRequestCooldownDisabledWhenZero(t *testing.T) {
	value := config.DefaultMusicConfig()
	service := NewMusicServiceWithDependencies(nil, value, nil, &nextTrackTestEngine{})
	viewer := events.Event{UserID: 101, UserName: "观众"}
	service.recordRequestCooldown(viewer)
	if got := service.requestCooldownRemaining(viewer); got != 0 { t.Fatalf("zero cooldown should be disabled, got %v", got) }
}

func TestMusicRequestCooldownUsesNameWhenUserIDUnavailable(t *testing.T) {
	value := config.DefaultMusicConfig()
	value.RequestCooldownSeconds = 30
	service := NewMusicServiceWithDependencies(nil, value, nil, &nextTrackTestEngine{})
	viewer := events.Event{UserName: " Viewer "}
	service.recordRequestCooldown(viewer)
	if got := service.requestCooldownRemaining(events.Event{UserName: "viewer"}); got <= 0 { t.Fatalf("username fallback should identify the same viewer, got %v", got) }
}

func TestSearchAndEnqueueUsesUnifiedSearchAndPlaybackQueue(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"code":0,"music.search.SearchCgiService":{"code":0,"data":{"body":{"song":{"list":[{"id":101,"mid":"TX101","title":"稻香","interval":223,"singer":[{"name":"周杰伦"}],"album":{"mid":"ALB101","name":"魔杰座"},"file":{"media_mid":"MEDIA101","size_128mp3":1000,"size_320mp3":2000}}]}},"meta":{"sum":1}}}}`))
	}))
	defer server.Close()

	value := config.DefaultMusicConfig()
	value.Enabled = true
	value.EnabledSearchSources = []string{"tx"}
	engine := &queueAdvanceTestEngine{}
	service := NewMusicServiceWithDependencies(nil, value, nil, engine)
	defer service.Shutdown()
	service.searcher = music.NewLXSearchClientWithEndpoints(server.Client(), music.LXSearchEndpoints{QQ: server.URL})

	item, matched, failures, err := service.SearchAndEnqueue(context.Background(), "稻香", "viewer")
	if err != nil { t.Fatalf("SearchAndEnqueue returned error: %v", err) }
	if !matched { t.Fatalf("song was not matched; provider failures: %#v", failures) }
	if item.Track.SourceID != "tx" || item.Track.SourceData["songmid"] != "TX101" {
		t.Fatalf("normalized native track was not preserved: %#v", item.Track)
	}
	snapshot := service.GetQueue()
	if snapshot.Current == nil || snapshot.Current.ID != item.ID {
		t.Fatalf("unified request did not schedule playback: %#v", snapshot)
	}
	if got := engine.loadCalls.Load(); got != 1 { t.Fatalf("playback engine Load calls = %d, want 1", got) }
}

func TestMusicServiceSourceCapabilitiesShareSearchAndPlaybackResolver(t *testing.T) {
	service := NewMusicServiceWithDependencies(nil, config.DefaultMusicConfig(), nil, nil)
	service.scriptRuntime = &fakeLXScriptRuntime{available: true}
	sources := service.GetSources()
	if len(sources) != 5 { t.Fatalf("source count = %d, want 5", len(sources)) }
	for _, source := range sources {
		if !source.Capabilities.Search || !source.Capabilities.Resolve {
			t.Errorf("source %s does not expose both unified capabilities: %#v", source.ID, source.Capabilities)
		}
	}
}


func TestMusicServiceStopResetsPlaybackToIdleSnapshot(t *testing.T) {
	engine := newHTMLAudioPlaybackEngine(nil)
	engine.setEmitter(func(string, any) {})
	service := NewMusicServiceWithDependencies(nil, config.DefaultMusicConfig(), nil, engine)
	track := music.Track{ID:"stop-track", SourceID:"tx", Title:"停止测试", Artist:"测试"}
	resource := music.PlaybackResource{URL:"https://media.example/stop.mp3", SourceID:"tx", TrackID:track.ID}
	if err := engine.LoadResource(context.Background(), track, resource); err != nil { t.Fatal(err) }
	if err := engine.Report(1, "playing", 1000, 10000, ""); err != nil { t.Fatal(err) }
	service.setPlaybackSnapshot(music.PlaybackSnapshot{State:music.PlaybackPlaying, Track:&track, PositionMS:1000, DurationMS:10000})
	if err := service.Stop(); err != nil { t.Fatal(err) }
	snapshot, err := service.GetCachedPlaybackStatus()
	if err != nil { t.Fatal(err) }
	if snapshot.State != music.PlaybackIdle || snapshot.Track != nil || snapshot.PositionMS != 0 || snapshot.DurationMS != 0 {
		t.Fatalf("cached stop snapshot did not return to idle: %#v", snapshot)
	}
}


func TestMusicServiceCanPreviousTracksPlaybackHistory(t *testing.T) {
	service := NewMusicServiceWithDependencies(nil, config.DefaultMusicConfig(), nil, newHTMLAudioPlaybackEngine(nil))
	if service.CanPrevious() { t.Fatal("previous should be unavailable without playback history") }
	item := music.QueueItem{ID:"history-1", Track:music.Track{ID:"track-1", SourceID:"tx", Title:"上一首", Artist:"测试"}}
	service.pushHistory(item)
	if !service.CanPrevious() { t.Fatal("previous should be available after a track enters playback history") }
}


func TestMusicServiceNextAndPreviousNavigateHTMLAudioQueueHistory(t *testing.T) {
	value := config.DefaultMusicConfig()
	value.Enabled = true
	value.EnabledSearchSources = []string{"tx"}
	engine := &nextTrackTestEngine{}
	service := NewMusicServiceWithDependencies(nil, value, nil, engine)
	service.scriptRuntime = nil // Exercise the deterministic provider/engine path without network access.
	defer service.cancelPlaybackWatcher()

	previous := music.Track{ID:"tx_previous", SourceID:"tx", LXSourceKey:"tx", Title:"上一首歌曲", Artist:"测试歌手"}
	next := music.Track{ID:"tx_next", SourceID:"tx", LXSourceKey:"tx", Title:"下一首歌曲", Artist:"测试歌手"}
	if _, err := service.queue.SetCurrentTrack(previous, searchPlayMarker); err != nil { t.Fatal(err) }
	if _, err := service.queue.Enqueue(next, "viewer"); err != nil { t.Fatal(err) }

	if err := service.Next(); err != nil { t.Fatalf("Next failed to start queued track: %v", err) }
	snapshot := service.queue.Snapshot()
	if snapshot.Current == nil || snapshot.Current.Track.ID != next.ID { t.Fatalf("Next current = %#v, want %s", snapshot.Current, next.ID) }
	if !service.CanPrevious() { t.Fatal("previous should become available after advancing") }

	if err := service.Previous(); err != nil { t.Fatalf("Previous failed to restore history: %v", err) }
	snapshot = service.queue.Snapshot()
	if snapshot.Current == nil || snapshot.Current.Track.ID != previous.ID { t.Fatalf("Previous current = %#v, want %s", snapshot.Current, previous.ID) }
	if len(snapshot.Items) != 1 || snapshot.Items[0].Track.ID != next.ID { t.Fatalf("forward track was not preserved after going back: %#v", snapshot.Items) }
	if service.CanPrevious() { t.Fatal("previous history entry should be consumed after going back") }
}


type canceledLoadTestEngine struct{}
func (*canceledLoadTestEngine) Load(context.Context, music.Track) error { return context.Canceled }
func (*canceledLoadTestEngine) Pause(context.Context) error { return nil }
func (*canceledLoadTestEngine) Resume(context.Context) error { return nil }
func (*canceledLoadTestEngine) Stop(context.Context) error { return nil }
func (*canceledLoadTestEngine) Snapshot(context.Context) (music.PlaybackSnapshot, error) {
	return music.PlaybackSnapshot{State: music.PlaybackIdle}, nil
}

func TestPlayNextDoesNotRetireTrackWhenResolutionIsSuperseded(t *testing.T) {
	value := config.DefaultMusicConfig()
	value.Enabled = true
	value.EnabledSearchSources = []string{"tx"}
	service := NewMusicServiceWithDependencies(nil, value, nil, &canceledLoadTestEngine{})
	service.scriptRuntime = nil
	track := music.Track{ID:"tx_cancelled", SourceID:"tx", LXSourceKey:"tx", Title:"稻香", Artist:"周杰伦"}
	if _, err := service.queue.SetCurrentTrack(track, searchPlayMarker); err != nil { t.Fatal(err) }
	if err := service.PlayNext(); err != nil { t.Fatalf("expected superseded cancellation to be silent, got %v", err) }
	current := service.queue.Snapshot().Current
	if current == nil || current.Track.ID != track.ID { t.Fatalf("cancelled resolution retired current track: %#v", current) }
	if got := service.GetStatus().LastError; got != "" { t.Fatalf("cancellation was reported as a playback error: %q", got) }
}

func TestCachedPlaybackStatusExposesActiveResolution(t *testing.T) {
	value := config.DefaultMusicConfig()
	value.Enabled = true
	service := NewMusicServiceWithDependencies(nil, value, nil, &nextTrackTestEngine{})
	track := music.Track{ID:"tx_resolving", SourceID:"tx", Title:"稻香", Artist:"周杰伦"}
	if _, err := service.queue.SetCurrentTrack(track, searchPlayMarker); err != nil { t.Fatal(err) }
	oldTrack := music.Track{ID:"old_track", SourceID:"wy", Title:"晴天", Artist:"周杰伦"}
	service.setPlaybackSnapshot(music.PlaybackSnapshot{State:music.PlaybackPlaying, Track:&oldTrack})
	ctx, generation, cancel := service.beginPlaybackResolution(context.Background())
	defer cancel()
	defer service.finishPlaybackResolution(generation)
	if ctx.Err() != nil { t.Fatalf("resolution context unexpectedly cancelled: %v", ctx.Err()) }
	snapshot, err := service.GetCachedPlaybackStatus()
	if err != nil { t.Fatal(err) }
	if snapshot.ResolutionStatus != "resolving" { t.Fatalf("resolution status = %q, want resolving", snapshot.ResolutionStatus) }
	if snapshot.Track == nil || snapshot.Track.ID != track.ID { t.Fatalf("resolving track not exposed in playback snapshot: %#v", snapshot.Track) }
}

type blockingLXScriptRuntime struct {
	started chan struct{}
	release chan struct{}
	startErr error
	startCalls atomic.Int32
	closeCalls atomic.Int32
}
func (r *blockingLXScriptRuntime) Available() bool { return true }
func (r *blockingLXScriptRuntime) Start(ctx context.Context) error {
	r.startCalls.Add(1)
	close(r.started)
	select {
	case <-r.release:
		return r.startErr
	case <-ctx.Done():
		return ctx.Err()
	}
}
func (r *blockingLXScriptRuntime) ResolvePlaybackURL(context.Context, string, any) (string, error) {
	return "", errors.New("not implemented")
}
func (r *blockingLXScriptRuntime) Close() error { r.closeCalls.Add(1); return nil }

func TestMusicStartDoesNotWaitForLXRuntimeInitialization(t *testing.T) {
	runtime := &blockingLXScriptRuntime{started: make(chan struct{}), release: make(chan struct{})}
	service := NewMusicServiceWithDependencies(nil, config.DefaultMusicConfig(), nil, &nextTrackTestEngine{}, events.NewBuffer())
	service.scriptRuntime = runtime
	startedAt := time.Now()
	startResult := make(chan error, 1)
	go func() { startResult <- service.start(context.Background()) }()
	select {
	case err := <-startResult:
		if err != nil { t.Fatal(err) }
	case <-time.After(time.Second):
		close(runtime.release)
		if err := <-startResult; err != nil { t.Fatal(err) }
		stopCtx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = service.stop(stopCtx)
		t.Fatal("music service startup waited for the blocked LX runtime initialization")
	}
	if elapsed := time.Since(startedAt); elapsed > time.Second {
		t.Fatalf("music service startup waited %s for LX runtime initialization", elapsed)
	}
	select {
	case <-runtime.started:
	case <-time.After(time.Second):
		t.Fatal("background LX runtime initialization did not start")
	}
	if got := service.GetStatus().LXRuntimeStatus; got != "initializing" {
		t.Fatalf("LX runtime status = %q while initialization is blocked, want initializing", got)
	}
	waitResult := make(chan error, 1)
	go func() { waitResult <- service.waitForLXRuntime(context.Background()) }()
	select {
	case err := <-waitResult:
		t.Fatalf("first-use wait returned before initialization completed: %v", err)
	case <-time.After(30 * time.Millisecond):
	}
	close(runtime.release)
	select {
	case err := <-waitResult:
		if err != nil { t.Fatalf("wait for successful runtime initialization: %v", err) }
	case <-time.After(time.Second):
		t.Fatal("first-use wait did not finish after runtime initialization")
	}
	if got := service.GetStatus().LXRuntimeStatus; got != "ready" {
		t.Fatalf("LX runtime status = %q, want ready", got)
	}
	if got := runtime.startCalls.Load(); got != 1 { t.Fatalf("runtime Start called %d times, want exactly once", got) }
	stopCtx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := service.stop(stopCtx); err != nil { t.Fatal(err) }
}

func TestMusicFirstUseReceivesLXRuntimeInitializationFailure(t *testing.T) {
	wantErr := errors.New("墨澜聚合音源 初始化失败（阶段：initialize_script）：网络超时")
	runtime := &blockingLXScriptRuntime{started: make(chan struct{}), release: make(chan struct{}), startErr: wantErr}
	close(runtime.release)
	service := NewMusicServiceWithDependencies(nil, config.DefaultMusicConfig(), nil, &nextTrackTestEngine{}, events.NewBuffer())
	service.scriptRuntime = runtime
	if err := service.start(context.Background()); err != nil { t.Fatal(err) }
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := service.waitForLXRuntime(ctx); err != nil {
		t.Fatalf("completed script initialization failure must not block native provider fallback: %v", err)
	}
	status := service.GetStatus()
	if status.LXRuntimeStatus != "failed" || !strings.Contains(status.LXRuntimeError, "墨澜聚合音源") {
		t.Fatalf("failed runtime status did not preserve script details: %#v", status)
	}
	if got := runtime.startCalls.Load(); got != 1 { t.Fatalf("runtime Start called %d times, want exactly once", got) }
	stopCtx, stopCancel := context.WithTimeout(context.Background(), time.Second)
	defer stopCancel()
	if err := service.stop(stopCtx); err != nil { t.Fatal(err) }
}

func TestStoppingMusicModuleCancelsBackgroundLXInitialization(t *testing.T) {
	runtime := &blockingLXScriptRuntime{started: make(chan struct{}), release: make(chan struct{})}
	service := NewMusicServiceWithDependencies(nil, config.DefaultMusicConfig(), nil, &nextTrackTestEngine{}, events.NewBuffer())
	service.scriptRuntime = runtime
	if err := service.start(context.Background()); err != nil { t.Fatal(err) }
	select {
	case <-runtime.started:
	case <-time.After(time.Second):
		t.Fatal("background LX runtime initialization did not start")
	}
	stopCtx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := service.stop(stopCtx); err != nil { t.Fatalf("stop did not cancel and join LX initialization: %v", err) }
	if got := runtime.closeCalls.Load(); got != 1 { t.Fatalf("runtime Close called %d times, want once after initialization stopped", got) }
	if got := runtime.startCalls.Load(); got != 1 { t.Fatalf("runtime Start called %d times, want once", got) }
}
