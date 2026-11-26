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
	service := NewMusicServiceWithDependencies(nil, config.DefaultMusicConfig(), engine)
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
	service := NewMusicServiceWithDependencies(nil, config.DefaultMusicConfig(), &nextTrackTestEngine{})
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
	service := NewMusicServiceWithDependencies(nil, value, engine)
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
	service := NewMusicServiceWithDependencies(nil, config.DefaultMusicConfig(), engine)
	finished := &music.QueueItem{Track: music.Track{Title: "当前歌曲", Artist: "测试歌手"}}
	snapshot := music.PlaybackSnapshot{State: music.PlaybackStopped}

	if err := service.advanceAfterPlaybackEnd(snapshot, finished); err != nil {
		t.Fatalf("advanceAfterPlaybackEnd returned error: %v", err)
	}
}

func TestGetPlaybackStatusReportsActualResolvedProvider(t *testing.T) {
	engine := &nextTrackTestEngine{}
	service := NewMusicServiceWithDependencies(nil, config.DefaultMusicConfig(), engine)
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
	service := NewMusicServiceWithDependencies(nil, value, engine)
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
	service := NewMusicServiceWithDependencies(nil, value, engine)
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
			service := NewMusicServiceWithDependencies(nil, value, &nextTrackTestEngine{})
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
	service := NewMusicServiceWithDependencies(nil, value, &nextTrackTestEngine{})
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
	service := NewMusicServiceWithDependencies(nil, value, &nextTrackTestEngine{}, buffer)
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

func TestPlayNextSkipsFailedPlaylistTrackAndStartsNextPlaylistTrack(t *testing.T) {
	value := config.DefaultMusicConfig()
	value.Enabled = true
	value.EnabledSearchSources = []string{"tx"}
	engine := &failFirstLoadTestEngine{}
	service := NewMusicServiceWithDependencies(nil, value, engine)
	service.scriptRuntime = nil
	defer service.cancelPlaybackWatcher()

	first := music.Track{ID:"tx_playlist_first", SourceID:"tx", LXSourceKey:"tx", Title:"歌单失败歌曲", Artist:"测试歌手"}
	second := music.Track{ID:"tx_playlist_second", SourceID:"tx", LXSourceKey:"tx", Title:"歌单下一首", Artist:"测试歌手"}
	service.mu.Lock()
	service.playlistPlaybackActive = true
	service.playlistPlaybackID = "playlist-recovery"
	service.playlistPlaybackTracks = []music.Track{first, second}
	service.playlistPlaybackIndex = 0
	service.playlistPlaybackMode = "sequential"
	service.mu.Unlock()

	if err := service.PlayNext(); err != nil {
		t.Fatalf("PlayNext should skip the failed playlist track and continue: %v", err)
	}
	snapshot := service.queue.Snapshot()
	if snapshot.Current == nil || snapshot.Current.Track.ID != second.ID {
		t.Fatalf("current playlist track = %#v, want %q", snapshot.Current, second.ID)
	}
	if snapshot.Current.RequestedBy != playlistPlayMarker {
		t.Fatalf("current playlist item requester = %q, want playlist marker", snapshot.Current.RequestedBy)
	}
	if got := engine.loadCalls.Load(); got != 2 {
		t.Fatalf("engine Load calls = %d, want failed first and successful second", got)
	}
	if got := service.GetStatus().LastError; got == "" || !strings.Contains(got, first.Title) {
		t.Fatalf("failed playlist track diagnostic = %q, want failure for %q", got, first.Title)
	}
}

func TestPlayNextSkipsFailedTrackAndStartsNextQueuedTrack(t *testing.T) {
	value := config.DefaultMusicConfig()
	value.Enabled = true
	value.EnabledSearchSources = []string{"tx"}
	engine := &failFirstLoadTestEngine{}
	service := NewMusicServiceWithDependencies(nil, value, engine)
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

type serializedPlaybackStartTestEngine struct {
	loadCalls atomic.Int32
	started chan struct{}
	release chan struct{}
}
func (e *serializedPlaybackStartTestEngine) Load(ctx context.Context, _ music.Track) error {
	if e.loadCalls.Add(1) == 1 {
		close(e.started)
		select {
		case <-e.release:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return nil
}
func (e *serializedPlaybackStartTestEngine) Pause(context.Context) error { return nil }
func (e *serializedPlaybackStartTestEngine) Resume(context.Context) error { return nil }
func (e *serializedPlaybackStartTestEngine) Stop(context.Context) error { return nil }
func (e *serializedPlaybackStartTestEngine) Snapshot(context.Context) (music.PlaybackSnapshot, error) {
	return music.PlaybackSnapshot{State: music.PlaybackPlaying}, nil
}

func TestConcurrentPlayNextLoadsCurrentQueueItemOnlyOnce(t *testing.T) {
	value := config.DefaultMusicConfig()
	value.EnabledSearchSources = []string{"tx"}
	engine := &serializedPlaybackStartTestEngine{started: make(chan struct{}), release: make(chan struct{})}
	service := NewMusicServiceWithDependencies(nil, value, engine)
	defer service.cancelPlaybackWatcher()

	track := music.Track{ID: "tx_concurrent", SourceID: "tx", LXSourceKey: "tx", Title: "并发播放测试", Artist: "测试歌手"}
	if _, err := service.queue.Enqueue(track, "viewer"); err != nil { t.Fatal(err) }

	first := make(chan error, 1)
	second := make(chan error, 1)
	go func() { first <- service.PlayNext() }()
	select {
	case <-engine.started:
	case <-time.After(2 * time.Second):
		t.Fatal("first playback did not reach the engine")
	}
	go func() { second <- service.PlayNext() }()
	close(engine.release)

	for name, result := range map[string]<-chan error{"first": first, "second": second} {
		select {
		case err := <-result:
			if err != nil { t.Fatalf("%s PlayNext() error = %v", name, err) }
		case <-time.After(2 * time.Second):
			t.Fatalf("%s PlayNext() did not finish", name)
		}
	}
	if got := engine.loadCalls.Load(); got != 1 {
		t.Fatalf("playback engine Load calls = %d, want exactly one for concurrent PlayNext calls", got)
	}
	current := service.queue.Snapshot().Current
	if current == nil || current.Track.ID != track.ID {
		t.Fatalf("current queue item = %#v, want %q", current, track.ID)
	}
}

type stopDuringPlaybackLoadTestEngine struct {
	loadStarted chan struct{}
	loadCalls atomic.Int32
	stopCalls atomic.Int32
}

func (e *stopDuringPlaybackLoadTestEngine) Load(ctx context.Context, _ music.Track) error {
	if e.loadCalls.Add(1) == 1 { close(e.loadStarted) }
	<-ctx.Done()
	return ctx.Err()
}
func (e *stopDuringPlaybackLoadTestEngine) Pause(context.Context) error { return nil }
func (e *stopDuringPlaybackLoadTestEngine) Resume(context.Context) error { return nil }
func (e *stopDuringPlaybackLoadTestEngine) Stop(context.Context) error {
	e.stopCalls.Add(1)
	return nil
}
func (e *stopDuringPlaybackLoadTestEngine) Snapshot(context.Context) (music.PlaybackSnapshot, error) {
	return music.PlaybackSnapshot{State: music.PlaybackStopped}, nil
}

func TestMusicServiceStopCancelsInFlightLoadBeforeStoppingEngine(t *testing.T) {
	value := config.DefaultMusicConfig()
	value.Enabled = true
	value.EnabledSearchSources = []string{"tx"}
	engine := &stopDuringPlaybackLoadTestEngine{loadStarted: make(chan struct{})}
	service := NewMusicServiceWithDependencies(nil, value, engine)
	track := music.Track{ID: "tx_stop_race", SourceID: "tx", LXSourceKey: "tx", Title: "停止竞态测试", Artist: "测试歌手"}
	if _, err := service.queue.Enqueue(track, "viewer"); err != nil { t.Fatal(err) }

	playDone := make(chan error, 1)
	go func() { playDone <- service.PlayNext() }()
	select {
	case <-engine.loadStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("playback did not enter engine Load")
	}

	stopDone := make(chan error, 1)
	go func() { stopDone <- service.Stop() }()
	select {
	case err := <-stopDone:
		if err != nil { t.Fatalf("Stop returned error: %v", err) }
	case <-time.After(2 * time.Second):
		t.Fatal("Stop blocked instead of canceling the in-flight engine Load")
	}
	select {
	case err := <-playDone:
		if err != nil { t.Fatalf("PlayNext returned error after Stop cancellation: %v", err) }
	case <-time.After(2 * time.Second):
		t.Fatal("in-flight PlayNext did not exit after Stop")
	}
	if got := engine.stopCalls.Load(); got != 1 { t.Fatalf("engine Stop calls = %d, want 1", got) }
	if current := service.queue.Snapshot().Current; current != nil {
		t.Fatalf("queue Current survived Stop: %#v", current)
	}
	if service.playbackStopping { t.Fatal("playbackStopping remained set after Stop completed") }
}

func TestMusicPlaybackStartTimeoutIsTenSeconds(t *testing.T) {
	if musicPlaybackStartTimeout != 10*time.Second {
		t.Fatalf("music playback start timeout = %s, want 10s", musicPlaybackStartTimeout)
	}
}


func TestMusicRequestCooldownIsPerViewerAndExemptsOwnerAndModerator(t *testing.T) {
	value := config.DefaultMusicConfig()
	value.RequestCooldownSeconds = 30
	service := NewMusicServiceWithDependencies(nil, value, &nextTrackTestEngine{})
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
	service := NewMusicServiceWithDependencies(nil, value, &nextTrackTestEngine{})
	viewer := events.Event{UserID: 101, UserName: "观众"}
	service.recordRequestCooldown(viewer)
	if got := service.requestCooldownRemaining(viewer); got != 0 { t.Fatalf("zero cooldown should be disabled, got %v", got) }
}

func TestMusicRequestCooldownUsesNameWhenUserIDUnavailable(t *testing.T) {
	value := config.DefaultMusicConfig()
	value.RequestCooldownSeconds = 30
	service := NewMusicServiceWithDependencies(nil, value, &nextTrackTestEngine{})
	viewer := events.Event{UserName: " Viewer "}
	service.recordRequestCooldown(viewer)
	if got := service.requestCooldownRemaining(events.Event{UserName: "viewer"}); got <= 0 { t.Fatalf("username fallback should identify the same viewer, got %v", got) }
}

func TestSearchAndEnqueueUsesUnifiedSearchAndPlaybackQueue(t *testing.T) {
	value := config.DefaultMusicConfig()
	value.Enabled = true
	value.EnabledSearchSources = []string{"tx"}
	engine := &queueAdvanceTestEngine{}
	service := NewMusicServiceWithDependencies(nil, value, engine)
	defer service.Shutdown()
	// The service-level queue test uses a deterministic QuickJS Action result.
	// Real HTTP execution, QQ request parameters, native IDs and cancellation
	// are exercised by lxruntime's bundled-search integration tests.
	service.scriptRuntime = fakeLXActionRuntime{available: true, value: map[string]any{
		"tracks": []any{map[string]any{
			"id": "tx_TX101", "source_id": "tx", "lx_source_key": "tx",
			"title": "稻香", "artist": "周杰伦", "album": "魔杰座",
			"duration_ms": 223000,
			"source_data": map[string]string{
				"songmid": "TX101", "song_id": "101", "media_mid": "MEDIA101", "album_mid": "ALB101",
			},
			"qualities": []any{map[string]any{"type": "128k", "size": "1000 B"}},
		}},
		"total": 1, "page": 1, "page_size": 30, "max_page": 1, "source": "tx",
	}}

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
	service := NewMusicServiceWithDependencies(nil, config.DefaultMusicConfig(), nil)
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
	service := NewMusicServiceWithDependencies(nil, config.DefaultMusicConfig(), engine)
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
	service := NewMusicServiceWithDependencies(nil, config.DefaultMusicConfig(), newHTMLAudioPlaybackEngine(nil))
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
	service := NewMusicServiceWithDependencies(nil, value, engine)
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

func TestConcurrentNextNavigationsAdvanceEachRequestedStep(t *testing.T) {
	value := config.DefaultMusicConfig()
	value.Enabled = true
	value.EnabledSearchSources = []string{"tx"}
	engine := &queueAdvanceTestEngine{}
	service := NewMusicServiceWithDependencies(nil, value, engine)
	service.scriptRuntime = nil
	defer service.cancelPlaybackWatcher()

	previous := music.Track{ID:"tx_nav_previous", SourceID:"tx", LXSourceKey:"tx", Title:"起始歌曲", Artist:"测试歌手"}
	first := music.Track{ID:"tx_nav_first", SourceID:"tx", LXSourceKey:"tx", Title:"下一首", Artist:"测试歌手"}
	second := music.Track{ID:"tx_nav_second", SourceID:"tx", LXSourceKey:"tx", Title:"再下一首", Artist:"测试歌手"}
	if _, err := service.queue.SetCurrentTrack(previous, searchPlayMarker); err != nil { t.Fatal(err) }
	if _, err := service.queue.Enqueue(first, "viewer-one"); err != nil { t.Fatal(err) }
	if _, err := service.queue.Enqueue(second, "viewer-two"); err != nil { t.Fatal(err) }

	start := make(chan struct{})
	results := make(chan error, 2)
	for i := 0; i < 2; i++ {
		go func() {
			<-start
			results <- service.Next()
		}()
	}
	close(start)
	for i := 0; i < 2; i++ {
		select {
		case err := <-results:
			if err != nil { t.Fatalf("concurrent Next() error = %v", err) }
		case <-time.After(2 * time.Second):
			t.Fatal("concurrent Next() did not finish")
		}
	}

	snapshot := service.queue.Snapshot()
	if snapshot.Current == nil || snapshot.Current.Track.ID != second.ID {
		t.Fatalf("current track = %#v, want second requested navigation target %q", snapshot.Current, second.ID)
	}
	if len(snapshot.Items) != 0 { t.Fatalf("pending queue after two Next() operations = %#v, want empty", snapshot.Items) }
	if got := engine.loadCalls.Load(); got != 2 { t.Fatalf("engine Load calls = %d, want 2", got) }
	service.mu.RLock()
	history := append([]music.QueueItem(nil), service.history...)
	service.mu.RUnlock()
	if len(history) != 2 || history[0].Track.ID != previous.ID || history[1].Track.ID != first.ID {
		t.Fatalf("navigation history = %#v, want [previous, first]", history)
	}
}

func TestPlayNextDoesNotRetireTrackWhenResolutionIsSuperseded(t *testing.T) {
	value := config.DefaultMusicConfig()
	value.Enabled = true
	value.EnabledSearchSources = []string{"tx"}
	service := NewMusicServiceWithDependencies(nil, value, &canceledLoadTestEngine{})
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
	service := NewMusicServiceWithDependencies(nil, value, &nextTrackTestEngine{})
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

func TestStaleLXRuntimeInitializationCannotCloseOrOverwriteNewGeneration(t *testing.T) {
	runtime := &blockingLXScriptRuntime{started: make(chan struct{}), release: make(chan struct{})}
	service := NewMusicServiceWithDependencies(nil, config.DefaultMusicConfig(), &nextTrackTestEngine{}, events.NewBuffer())
	service.scriptRuntime = runtime
	oldDone := make(chan struct{})
	newDone := make(chan struct{})
	service.mu.Lock()
	service.lxRuntimeGeneration = 1
	service.lxRuntimeDone = oldDone
	service.lxRuntimeStatus = "initializing"
	service.mu.Unlock()

	oldFinished := make(chan struct{})
	go func() {
		service.initializeLXRuntime(context.Background(), 1, oldDone)
		close(oldFinished)
	}()
	select {
	case <-runtime.started:
	case <-time.After(time.Second):
		t.Fatal("old runtime initialization did not start")
	}

	service.mu.Lock()
	service.lxRuntimeGeneration = 2
	service.lxRuntimeDone = newDone
	service.lxRuntimeStatus = "initializing"
	service.lxRuntimeError = ""
	service.mu.Unlock()
	close(runtime.release)

	select {
	case <-oldFinished:
	case <-time.After(time.Second):
		t.Fatal("stale runtime initialization did not finish")
	}
	select {
	case <-newDone:
		t.Fatal("stale initialization closed the newer generation completion channel")
	default:
	}
	status := service.GetStatus()
	if status.LXRuntimeStatus != "initializing" {
		t.Fatalf("stale initialization overwrote new runtime status: %#v", status)
	}
	service.mu.RLock()
	currentDone, generation := service.lxRuntimeDone, service.lxRuntimeGeneration
	service.mu.RUnlock()
	if currentDone != newDone || generation != 2 {
		t.Fatalf("new runtime lifecycle was replaced: generation=%d done=%p, want generation=2 done=%p", generation, currentDone, newDone)
	}
}

func TestMusicStartDoesNotWaitForLXRuntimeInitialization(t *testing.T) {
	runtime := &blockingLXScriptRuntime{started: make(chan struct{}), release: make(chan struct{})}
	service := NewMusicServiceWithDependencies(nil, config.DefaultMusicConfig(), &nextTrackTestEngine{}, events.NewBuffer())
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
	service := NewMusicServiceWithDependencies(nil, config.DefaultMusicConfig(), &nextTrackTestEngine{}, events.NewBuffer())
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
	service := NewMusicServiceWithDependencies(nil, config.DefaultMusicConfig(), &nextTrackTestEngine{}, events.NewBuffer())
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


type blockingLyricsLXRuntime struct {
	started chan struct{}
	cancelled chan struct{}
}
func (*blockingLyricsLXRuntime) Available() bool { return true }
func (*blockingLyricsLXRuntime) Start(context.Context) error { return nil }
func (*blockingLyricsLXRuntime) ResolvePlaybackURL(context.Context, string, any) (string, error) { return "", errors.New("not implemented") }
func (*blockingLyricsLXRuntime) Close() error { return nil }
func (*blockingLyricsLXRuntime) HasExtensionAction(source, action string) bool { return source == "tx" && action == "lyric" }
func (r *blockingLyricsLXRuntime) DispatchExtensionAction(ctx context.Context, source, action string, info any) (any, error) {
	select {
	case <-r.started:
	default: close(r.started)
	}
	<-ctx.Done()
	close(r.cancelled)
	return nil, ctx.Err()
}

func TestGetLyricsForRequestCancelsLXScriptAndDoesNotStartNativeFallback(t *testing.T) {
	service := NewMusicServiceWithDependencies(nil, config.DefaultMusicConfig(), &nextTrackTestEngine{})
	runtime := &blockingLyricsLXRuntime{started: make(chan struct{}), cancelled: make(chan struct{})}
	service.scriptRuntime = runtime
	result := make(chan error, 1)
	go func() {
		_, err := service.GetLyricsForRequest(music.Track{ID:"tx_cancel", SourceID:"tx", LXSourceKey:"tx", Title:"取消测试"}, "lyrics-request-1")
		result <- err
	}()
	select {
	case <-runtime.started:
	case <-time.After(time.Second): t.Fatal("LX lyric request did not start")
	}
	service.CancelLyrics("lyrics-request-1")
	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) { t.Fatalf("GetLyricsForRequest error = %v, want context.Canceled", err) }
	case <-time.After(time.Second):
		t.Fatal("lyrics request did not return after cancellation")
	}
	select {
	case <-runtime.cancelled:
	case <-time.After(time.Second): t.Fatal("LX runtime did not observe request cancellation")
	}
	if len(service.lyricsCache) != 0 { t.Fatalf("cancelled request populated lyrics cache: %#v", service.lyricsCache) }
}

func TestShutdownCancelsInFlightLyricsRequests(t *testing.T) {
	service := NewMusicServiceWithDependencies(nil, config.DefaultMusicConfig(), &nextTrackTestEngine{})
	runtime := &blockingLyricsLXRuntime{started: make(chan struct{}), cancelled: make(chan struct{})}
	service.scriptRuntime = runtime
	result := make(chan error, 1)
	go func() {
		_, err := service.GetLyricsForRequest(music.Track{ID:"tx_shutdown", SourceID:"tx", LXSourceKey:"tx", Title:"退出测试"}, "lyrics-request-shutdown")
		result <- err
	}()
	select {
	case <-runtime.started:
	case <-time.After(time.Second): t.Fatal("LX lyric request did not start")
	}
	if err := service.Shutdown(); err != nil { t.Fatal(err) }
	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) { t.Fatalf("request error = %v, want context.Canceled", err) }
	case <-time.After(time.Second): t.Fatal("lyrics request did not stop during shutdown")
	}
}


type staticLyricsLXRuntime struct { calls atomic.Int32; value any; err error }
func (*staticLyricsLXRuntime) Available() bool { return true }
func (*staticLyricsLXRuntime) Start(context.Context) error { return nil }
func (*staticLyricsLXRuntime) ResolvePlaybackURL(context.Context, string, any) (string, error) { return "", errors.New("not implemented") }
func (*staticLyricsLXRuntime) Close() error { return nil }
func (*staticLyricsLXRuntime) HasExtensionAction(source, action string) bool { return (source == "tx" || source == "zz") && action == "lyric" }
func (r *staticLyricsLXRuntime) DispatchExtensionAction(context.Context, string, string, any) (any, error) {
	r.calls.Add(1)
	return r.value, r.err
}

func TestGetLyricsCachesSuccessfulLXScriptLyricsByTrackIdentity(t *testing.T) {
	service := NewMusicServiceWithDependencies(nil, config.DefaultMusicConfig(), &nextTrackTestEngine{})
	runtime := &staticLyricsLXRuntime{value:"[00:01.00]缓存歌词"}
	service.scriptRuntime = runtime
	track := music.Track{ID:"tx_cached", SourceID:"tx", LXSourceKey:"tx", Title:"缓存测试", Artist:"测试歌手"}
	first, err := service.GetLyricsForRequest(track, "cache-request-1")
	if err != nil { t.Fatal(err) }
	second, err := service.GetLyricsForRequest(track, "cache-request-2")
	if err != nil { t.Fatal(err) }
	if first != second || first == "" { t.Fatalf("lyrics results differ or are empty: first=%q second=%q", first, second) }
	if got := runtime.calls.Load(); got != 1 { t.Fatalf("LX lyric dispatch calls = %d, want one cached request", got) }
	other := track
	other.ID = "tx_other"
	if _, ok := service.cachedLyrics(other); ok { t.Fatal("lyrics cache leaked across different track identities") }
}

func TestLyricsCacheDoesNotStoreEmptyLyrics(t *testing.T) {
	service := NewMusicServiceWithDependencies(nil, config.DefaultMusicConfig(), &nextTrackTestEngine{})
	track := music.Track{ID:"tx_empty", SourceID:"tx", Title:"空歌词", Artist:"测试歌手"}
	service.cacheLyrics(track, "  \n ")
	if _, ok := service.cachedLyrics(track); ok { t.Fatal("empty lyrics must not be cached") }
}


func TestGetLyricsDoesNotCacheFailedProviderResults(t *testing.T) {
	service := NewMusicServiceWithDependencies(nil, config.DefaultMusicConfig(), &nextTrackTestEngine{})
	runtime := &staticLyricsLXRuntime{err:errors.New("模拟歌词网络错误")}
	service.scriptRuntime = runtime
	track := music.Track{ID:"tx_failed", SourceID:"tx", LXSourceKey:"tx", Title:"失败缓存测试", Artist:"测试歌手"}
	if _, err := service.GetLyricsForRequest(track, "failed-request-1"); err == nil { t.Fatal("failed script and native fallback should return an error") }
	if _, ok := service.cachedLyrics(track); ok { t.Fatal("failed lyrics request must not be cached") }
}


func TestGetLyricsFallsBackToNativeProviderAfterScriptTimeout(t *testing.T) {
	service := NewMusicServiceWithDependencies(nil, config.DefaultMusicConfig(), &nextTrackTestEngine{})
	service.scriptRuntime = &staticLyricsLXRuntime{err:context.DeadlineExceeded}
	track := music.Track{ID:"zz_timeout", SourceID:"zz", LXSourceKey:"zz", Title:"超时回退测试"}
	_, err := service.GetLyricsForRequest(track, "timeout-fallback-request")
	if err == nil || !strings.Contains(err.Error(), "暂不支持独立歌词解析") {
		t.Fatalf("script timeout should still attempt native fallback; error = %v", err)
	}
}

type failingStopTestEngine struct { stopErr error }
func (*failingStopTestEngine) Load(context.Context, music.Track) error { return nil }
func (*failingStopTestEngine) Pause(context.Context) error { return nil }
func (*failingStopTestEngine) Resume(context.Context) error { return nil }
func (e *failingStopTestEngine) Stop(context.Context) error { return e.stopErr }
func (*failingStopTestEngine) Snapshot(context.Context) (music.PlaybackSnapshot, error) {
	return music.PlaybackSnapshot{State: music.PlaybackIdle}, nil
}

func TestDisablingMusicClearsQueueAndHistoryEvenWhenEngineStopFails(t *testing.T) {
	manager, err := config.NewManager(fstest.MapFS{
		"music.json": &fstest.MapFile{Data: []byte("{}")},
	}, t.TempDir())
	if err != nil { t.Fatal(err) }

	value := config.DefaultMusicConfig()
	value.Enabled = true
	engine := &failingStopTestEngine{stopErr: errors.New("模拟播放器停止失败")}
	service := NewMusicServiceWithDependencies(manager, value, engine)

	current := music.Track{ID:"disable-current", SourceID:"tx", Title:"当前歌曲", Artist:"歌手"}
	pending := music.Track{ID:"disable-pending", SourceID:"tx", Title:"待播歌曲", Artist:"歌手"}
	if _, err := service.queue.SetCurrentTrack(current, searchPlayMarker); err != nil { t.Fatal(err) }
	if _, err := service.queue.Enqueue(pending, "viewer"); err != nil { t.Fatal(err) }
	service.pushHistory(music.QueueItem{ID:"disable-history", Track:music.Track{ID:"history", SourceID:"tx", Title:"历史歌曲", Artist:"歌手"}})
	service.mu.Lock()
	service.playlistPlaybackActive = true
	service.playlistPlaybackID = "playlist-id"
	service.playlistPlaybackName = "测试歌单"
	service.playlistPlaybackTracks = []music.Track{pending}
	service.playlistPlaybackResumeTrack = &current
	service.mu.Unlock()

	disabled := value
	disabled.Enabled = false
	if err := service.SaveConfig(disabled); err == nil || !strings.Contains(err.Error(), "模拟播放器停止失败") {
		t.Fatalf("SaveConfig error = %v, want reported engine stop failure", err)
	}
	if service.GetConfig().Enabled { t.Fatal("music module remained enabled after saving disabled configuration") }
	queue := service.GetQueue()
	if queue.Current != nil || len(queue.Items) != 0 {
		t.Fatalf("disabled music module retained queue state: %#v", queue)
	}
	service.mu.RLock()
	defer service.mu.RUnlock()
	if len(service.history) != 0 { t.Fatalf("disabled music module retained playback history: %#v", service.history) }
	if service.playlistPlaybackActive || len(service.playlistPlaybackTracks) != 0 || service.playlistPlaybackID != "" || service.playlistPlaybackName != "" || service.playlistPlaybackResumeTrack != nil {
		t.Fatalf("disabled music module retained playlist playback state")
	}
	if !service.playbackSnapshotValid || service.playbackSnapshot.State != music.PlaybackIdle || service.playbackSnapshot.Track != nil {
		t.Fatalf("disabled music module playback snapshot is not idle: %#v", service.playbackSnapshot)
	}
}

type cancelFirstNavigationLoadTestEngine struct {
	calls atomic.Int32
	started chan string
}
func (e *cancelFirstNavigationLoadTestEngine) Load(ctx context.Context, track music.Track) error {
	call := e.calls.Add(1)
	e.started <- track.ID
	if call == 1 {
		<-ctx.Done()
		return ctx.Err()
	}
	return nil
}
func (*cancelFirstNavigationLoadTestEngine) Pause(context.Context) error { return nil }
func (*cancelFirstNavigationLoadTestEngine) Resume(context.Context) error { return nil }
func (*cancelFirstNavigationLoadTestEngine) Stop(context.Context) error { return nil }
func (*cancelFirstNavigationLoadTestEngine) Snapshot(context.Context) (music.PlaybackSnapshot, error) {
	return music.PlaybackSnapshot{State:music.PlaybackPlaying}, nil
}

func TestNavigationSupersedesInFlightTrackLoad(t *testing.T) {
	tests := []struct {
		name string
		navigate func(*MusicService) error
		wantCurrent string
		wantPending []string
		withHistory bool
	}{
		{name:"next while loading", navigate:func(s *MusicService) error { return s.Next() }, wantCurrent:"tx_nav_next", wantPending:[]string{}},
		{name:"previous while loading", navigate:func(s *MusicService) error { return s.Previous() }, wantCurrent:"tx_nav_previous", wantPending:[]string{"tx_nav_loading","tx_nav_next"}, withHistory:true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			value := config.DefaultMusicConfig()
			value.Enabled = true
			value.EnabledSearchSources = []string{"tx"}
			engine := &cancelFirstNavigationLoadTestEngine{started:make(chan string, 3)}
			service := NewMusicServiceWithDependencies(nil, value, engine)
			service.scriptRuntime = nil
			defer service.cancelPlaybackWatcher()

			loading := music.Track{ID:"tx_nav_loading", SourceID:"tx", LXSourceKey:"tx", Title:"正在解析", Artist:"测试"}
			next := music.Track{ID:"tx_nav_next", SourceID:"tx", LXSourceKey:"tx", Title:"下一首", Artist:"测试"}
			previous := music.Track{ID:"tx_nav_previous", SourceID:"tx", LXSourceKey:"tx", Title:"上一首", Artist:"测试"}
			if tt.withHistory {
				service.pushHistory(music.QueueItem{ID:"history-previous", Track:previous, RequestedBy:searchPlayMarker})
			}
			if _, err := service.queue.Enqueue(loading, "viewer"); err != nil { t.Fatal(err) }
			if _, err := service.queue.Enqueue(next, "viewer"); err != nil { t.Fatal(err) }

			firstPlay := make(chan error, 1)
			go func() { firstPlay <- service.PlayNext() }()
			select {
			case got := <-engine.started:
				if got != loading.ID { t.Fatalf("first loading track = %q, want %q", got, loading.ID) }
			case <-time.After(2*time.Second):
				t.Fatal("initial track did not enter engine Load")
			}

			navigationResult := make(chan error, 1)
			go func() { navigationResult <- tt.navigate(service) }()
			select {
			case err := <-navigationResult:
				if err != nil { t.Fatalf("navigation returned error: %v", err) }
			case <-time.After(2*time.Second):
				t.Fatal("navigation did not supersede the in-flight load")
			}
			select {
			case err := <-firstPlay:
				if err != nil { t.Fatalf("superseded initial PlayNext returned error: %v", err) }
			case <-time.After(2*time.Second):
				t.Fatal("superseded initial PlayNext did not exit")
			}
			select {
			case got := <-engine.started:
				if got != tt.wantCurrent { t.Fatalf("replacement load track = %q, want %q", got, tt.wantCurrent) }
			case <-time.After(2*time.Second):
				t.Fatal("replacement track did not enter engine Load")
			}

			snapshot := service.queue.Snapshot()
			if snapshot.Current == nil || snapshot.Current.Track.ID != tt.wantCurrent {
				t.Fatalf("current after navigation = %#v, want %q", snapshot.Current, tt.wantCurrent)
			}
			if len(snapshot.Items) != len(tt.wantPending) { t.Fatalf("pending items = %#v, want %v", snapshot.Items, tt.wantPending) }
			for i, want := range tt.wantPending {
				if snapshot.Items[i].Track.ID != want { t.Fatalf("pending item %d = %q, want %q", i, snapshot.Items[i].Track.ID, want) }
			}
			if got := engine.calls.Load(); got != 2 { t.Fatalf("engine Load calls = %d, want 2", got) }
		})
	}
}



type lifecycleRaceTestEngine struct {
	startEntered chan struct{}
	allowStart chan struct{}
	startErr error
	closeErr error
	startCalls atomic.Int32
	closeCalls atomic.Int32
}
func (*lifecycleRaceTestEngine) Load(context.Context, music.Track) error { return nil }
func (*lifecycleRaceTestEngine) Pause(context.Context) error { return nil }
func (*lifecycleRaceTestEngine) Resume(context.Context) error { return nil }
func (*lifecycleRaceTestEngine) Stop(context.Context) error { return nil }
func (*lifecycleRaceTestEngine) Snapshot(context.Context) (music.PlaybackSnapshot, error) { return music.PlaybackSnapshot{State:music.PlaybackIdle}, nil }
func (e *lifecycleRaceTestEngine) Start(ctx context.Context) error {
	e.startCalls.Add(1)
	if e.startEntered != nil { close(e.startEntered) }
	if e.startErr != nil { return e.startErr }
	if e.allowStart == nil { return nil }
	select { case <-e.allowStart: return nil; case <-ctx.Done(): return ctx.Err() }
}
func (e *lifecycleRaceTestEngine) Close() error { e.closeCalls.Add(1); return e.closeErr }

type shutdownBlockingSnapshotEngine struct {
	entered chan struct{}
	release chan struct{}
	closeCalls atomic.Int32
}
func (*shutdownBlockingSnapshotEngine) Load(context.Context, music.Track) error { return nil }
func (*shutdownBlockingSnapshotEngine) Pause(context.Context) error { return nil }
func (*shutdownBlockingSnapshotEngine) Resume(context.Context) error { return nil }
func (*shutdownBlockingSnapshotEngine) Stop(context.Context) error { return nil }
func (e *shutdownBlockingSnapshotEngine) Snapshot(context.Context) (music.PlaybackSnapshot, error) {
	select { case <-e.entered: default: close(e.entered) }
	<-e.release // Deliberately ignore cancellation to verify Shutdown joins the task.
	return music.PlaybackSnapshot{State:music.PlaybackIdle}, nil
}
func (*shutdownBlockingSnapshotEngine) Start(context.Context) error { return nil }
func (e *shutdownBlockingSnapshotEngine) Close() error { e.closeCalls.Add(1); return nil }

func TestMusicShutdownWaitsForPlaybackWatcherBeforeClosingEngine(t *testing.T) {
	engine := &shutdownBlockingSnapshotEngine{entered:make(chan struct{}), release:make(chan struct{})}
	service := NewMusicServiceWithDependencies(nil, config.DefaultMusicConfig(), engine, events.NewBuffer())
	service.startPlaybackWatcher()
	select {
	case <-engine.entered:
	case <-time.After(2*time.Second):
		t.Fatal("playback watcher did not enter Snapshot")
	}
	shutdownDone := make(chan error, 1)
	go func() { shutdownDone <- service.Shutdown() }()
	select {
	case err := <-shutdownDone:
		t.Fatalf("Shutdown returned before playback watcher exited: %v", err)
	case <-time.After(50*time.Millisecond):
	}
	if got := engine.closeCalls.Load(); got != 0 {
		t.Fatalf("engine Close called while watcher still used it: %d", got)
	}
	close(engine.release)
	select {
	case err := <-shutdownDone:
		if err != nil { t.Fatalf("Shutdown failed: %v", err) }
	case <-time.After(time.Second):
		t.Fatal("Shutdown did not finish after playback watcher exited")
	}
	if got := engine.closeCalls.Load(); got != 1 { t.Fatalf("engine Close calls = %d, want 1", got) }
}

type shutdownBlockingFanMedalChecker struct {
	entered chan struct{}
	release chan struct{}
}
func (c *shutdownBlockingFanMedalChecker) Check(context.Context, int, int) (bool, int, error) {
	close(c.entered)
	<-c.release // Deliberately ignore cancellation to verify the worker is joined.
	return false, 0, nil
}

func TestMusicShutdownCancelsAndJoinsRequestWorker(t *testing.T) {
	value := config.DefaultMusicConfig()
	value.Enabled = true
	value.CommandEnabled = true
	value.CommandPrefix = "!点歌"
	value.RequireFanMedal = true
	value.FanMedalLevel = 1
	service := NewMusicServiceWithDependencies(nil, value, &nextTrackTestEngine{}, events.NewBuffer())
	checker := &shutdownBlockingFanMedalChecker{entered:make(chan struct{}), release:make(chan struct{})}
	service.fanMedalChecker = checker
	if err := service.start(context.Background()); err != nil { t.Fatal(err) }
	service.requests <- events.Event{Type:"danmaku", Content:"!点歌 测试歌曲", UserID:123, UserName:"viewer"}
	select {
	case <-checker.entered:
	case <-time.After(time.Second):
		t.Fatal("request worker did not enter fan medal check")
	}
	shutdownDone := make(chan error, 1)
	go func() { shutdownDone <- service.Shutdown() }()
	select {
	case err := <-shutdownDone:
		t.Fatalf("Shutdown returned before request worker exited: %v", err)
	case <-time.After(50*time.Millisecond):
	}
	close(checker.release)
	select {
	case err := <-shutdownDone:
		if err != nil { t.Fatalf("Shutdown failed: %v", err) }
	case <-time.After(time.Second):
		t.Fatal("Shutdown did not finish after request worker exited")
	}
	if service.GetStatus().Running { t.Fatal("music service remains running after Shutdown") }
}

func TestMusicStopTimeoutBlocksRestartUntilShutdownJoinsWorker(t *testing.T) {
	value := config.DefaultMusicConfig()
	value.Enabled = true
	value.CommandEnabled = true
	value.CommandPrefix = "!点歌"
	value.RequireFanMedal = true
	value.FanMedalLevel = 1
	engine := &lifecycleRaceTestEngine{}
	service := NewMusicServiceWithDependencies(nil, value, engine, events.NewBuffer())
	checker := &shutdownBlockingFanMedalChecker{entered:make(chan struct{}), release:make(chan struct{})}
	service.fanMedalChecker = checker
	if err := service.start(context.Background()); err != nil { t.Fatal(err) }
	service.requests <- events.Event{Type:"danmaku", Content:"!点歌 测试歌曲", UserID:123, UserName:"viewer"}
	select {
	case <-checker.entered:
	case <-time.After(time.Second):
		t.Fatal("request worker did not enter fan medal check")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	err := service.stop(ctx)
	cancel()
	if !errors.Is(err, context.DeadlineExceeded) { t.Fatalf("Stop error = %v, want context deadline", err) }
	if err := service.start(context.Background()); err == nil || !strings.Contains(err.Error(), "still shutting down") {
		t.Fatalf("restart should be blocked while previous worker is still active, got %v", err)
	}
	if got := engine.startCalls.Load(); got != 1 { t.Fatalf("engine Start calls during pending shutdown = %d, want 1", got) }

	service.mu.RLock()
	workerDone := service.workerDone
	service.mu.RUnlock()
	close(checker.release)
	select {
	case <-workerDone:
	case <-time.After(time.Second):
		t.Fatal("request worker did not exit after releasing checker")
	}
	if err := service.Shutdown(); err != nil { t.Fatalf("finish timed-out shutdown: %v", err) }
	if err := service.start(context.Background()); err != nil { t.Fatalf("restart after shutdown cleanup: %v", err) }
	if got := engine.startCalls.Load(); got != 2 { t.Fatalf("engine Start calls after cleanup = %d, want 2", got) }
	if err := service.Shutdown(); err != nil { t.Fatalf("shutdown restarted service: %v", err) }
}

func TestMusicStartClosesPlaybackEngineAfterPartialStartFailure(t *testing.T) {
	startErr := errors.New("partial playback engine startup failure")
	closeErr := errors.New("playback engine cleanup failure")
	engine := &lifecycleRaceTestEngine{startErr:startErr, closeErr:closeErr}
	service := NewMusicServiceWithDependencies(nil, config.DefaultMusicConfig(), engine, events.NewBuffer())
	err := service.start(context.Background())
	if !errors.Is(err, startErr) || !errors.Is(err, closeErr) {
		t.Fatalf("startup error should preserve both Start and cleanup failures, got %v", err)
	}
	if got := engine.startCalls.Load(); got != 1 { t.Fatalf("engine Start calls = %d, want 1", got) }
	if got := engine.closeCalls.Load(); got != 1 { t.Fatalf("engine Close calls = %d, want 1 after partial startup failure", got) }
	if service.GetStatus().Running { t.Fatal("service must remain stopped after engine startup failure") }
}

func TestMusicStartWaitsForCleanupRetryAfterPartialCloseFailure(t *testing.T) {
	engine := &lifecycleRaceTestEngine{
		startErr: errors.New("partial startup failure"),
		closeErr: errors.New("first cleanup attempt failed"),
	}
	service := NewMusicServiceWithDependencies(nil, config.DefaultMusicConfig(), engine, events.NewBuffer())
	if err := service.start(context.Background()); err == nil { t.Fatal("partial startup failure was not returned") }
	if err := service.start(context.Background()); err == nil || !strings.Contains(err.Error(), "still shutting down") {
		t.Fatalf("restart should be blocked until cleanup succeeds, got %v", err)
	}
	if got := engine.startCalls.Load(); got != 1 { t.Fatalf("engine Start calls before cleanup retry = %d, want 1", got) }

	engine.closeErr = nil
	if err := service.Shutdown(); err != nil { t.Fatalf("retry partial resource cleanup: %v", err) }
	engine.startErr = nil
	if err := service.start(context.Background()); err != nil { t.Fatalf("start after successful cleanup retry: %v", err) }
	if got := engine.startCalls.Load(); got != 2 { t.Fatalf("engine Start calls after recovery = %d, want 2", got) }
	if err := service.Shutdown(); err != nil { t.Fatalf("shutdown after recovered start: %v", err) }
}

func TestMusicStartAndStopSerializePlaybackEngineLifecycle(t *testing.T) {
	engine := &lifecycleRaceTestEngine{startEntered:make(chan struct{}), allowStart:make(chan struct{})}
	service := NewMusicServiceWithDependencies(nil, config.DefaultMusicConfig(), engine, events.NewBuffer())
	startResult := make(chan error, 1)
	go func(){ startResult <- service.start(context.Background()) }()
	select { case <-engine.startEntered: case <-time.After(time.Second): t.Fatal("engine Start was not called") }

	stopResult := make(chan error, 1)
	go func(){
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		stopResult <- service.stop(ctx)
	}()
	select {
	case err := <-stopResult:
		t.Fatalf("Stop completed while engine Start was still in flight: %v", err)
	case <-time.After(30*time.Millisecond):
	}
	close(engine.allowStart)
	if err := <-startResult; err != nil { t.Fatalf("Start failed: %v", err) }
	if err := <-stopResult; err != nil { t.Fatalf("Stop failed: %v", err) }
	if service.GetStatus().Running { t.Fatal("service remains running after serialized Stop") }
	if got := engine.startCalls.Load(); got != 1 { t.Fatalf("engine Start calls = %d, want 1", got) }
	if got := engine.closeCalls.Load(); got != 1 { t.Fatalf("engine Close calls = %d, want 1", got) }
}

func TestMusicStartDoesNotRestartAlreadyRunningPlaybackEngine(t *testing.T) {
	engine := &lifecycleRaceTestEngine{startEntered:make(chan struct{}), allowStart:make(chan struct{})}
	close(engine.allowStart)
	service := NewMusicServiceWithDependencies(nil, config.DefaultMusicConfig(), engine, events.NewBuffer())
	if err := service.start(context.Background()); err != nil { t.Fatal(err) }
	if err := service.start(context.Background()); err == nil { t.Fatal("second service Start should be rejected") }
	if got := engine.startCalls.Load(); got != 1 { t.Fatalf("engine Start called %d times, want once", got) }
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := service.stop(ctx); err != nil { t.Fatal(err) }
}
