package desktop
import ("context"; "encoding/json"; "testing"; "testing/fstest"; "sync/atomic"; "github.com/dongguo4812/My-LiveHub/app/internal/config"
 "github.com/dongguo4812/My-LiveHub/app/internal/events"; "github.com/dongguo4812/My-LiveHub/app/internal/music")
func TestParseSongRequest(t *testing.T){
 tests:=[]struct{content,prefix,want string;ok bool}{{"点歌 稻香","点歌","稻香",true},{"点歌稻香","点歌","",false},{"点歌   稻香 ","点歌","稻香",true},{"闲聊点歌 稻香","点歌","",false},{"点歌","点歌","",false}}
 for _,tt:=range tests{got,ok:=parseSongRequest(tt.content,tt.prefix);if got!=tt.want||ok!=tt.ok{t.Errorf("parseSongRequest(%q)=(%q,%v), want (%q,%v)",tt.content,got,ok,tt.want,tt.ok)}}
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
