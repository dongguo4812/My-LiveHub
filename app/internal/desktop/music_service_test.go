package desktop
import ("context"; "errors"; "testing"; "testing/fstest"; "github.com/dongguo4812/My-LiveHub/app/internal/config"
 "github.com/dongguo4812/My-LiveHub/app/internal/events"
 "github.com/dongguo4812/My-LiveHub/app/internal/music")
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

type playbackStartTestEngine struct {
	calls []string
	loadErr error
	resumeErr error
}
func (e *playbackStartTestEngine) Load(context.Context, music.PlaybackResource, music.Track) error {
	e.calls = append(e.calls, "load")
	return e.loadErr
}
func (e *playbackStartTestEngine) Pause(context.Context) error { return nil }
func (e *playbackStartTestEngine) Resume(context.Context) error {
	e.calls = append(e.calls, "resume")
	return e.resumeErr
}
func (e *playbackStartTestEngine) Seek(context.Context, int64) error { return nil }
func (e *playbackStartTestEngine) SetVolume(context.Context, int) error { return nil }
func (e *playbackStartTestEngine) Stop(context.Context) error { return nil }
func (e *playbackStartTestEngine) Snapshot(context.Context) (music.PlaybackSnapshot, error) {
	return music.PlaybackSnapshot{}, nil
}

type audioProcessingTestEngine struct {
	playbackStartTestEngine
	normalization []bool
	normalizationErr error
}
func (e *audioProcessingTestEngine) SetLoudnessNormalization(_ context.Context, enabled bool) error {
	e.normalization = append(e.normalization, enabled)
	return e.normalizationErr
}

func TestSaveConfigAppliesAndPersistsLoudnessNormalization(t *testing.T) {
	manager, err := config.NewManager(fstest.MapFS{
		"music.json": {Data: []byte(`{"config_version":8,"enabled":true,"command_enabled":true,"command_prefix":"点歌","skip_command_enabled":true,"skip_command":"切歌","queue_capacity":100,"volume":80,"loudness_normalization":false}`)},
	}, t.TempDir())
	if err != nil { t.Fatal(err) }
	engine := &audioProcessingTestEngine{}
	service := NewMusicServiceWithDependencies(manager, config.DefaultMusicConfig(), nil, engine)
	next := service.GetConfig()
	next.LoudnessNormalization = true
	if err := service.SaveConfig(next); err != nil { t.Fatalf("SaveConfig: %v", err) }
	if len(engine.normalization) != 1 || !engine.normalization[0] {
		t.Fatalf("normalization was not applied to the engine: %#v", engine.normalization)
	}
	saved, err := config.LoadMusicConfig(manager)
	if err != nil { t.Fatalf("reload saved music config: %v", err) }
	if !saved.LoudnessNormalization { t.Fatalf("normalization preference was not persisted: %+v", saved) }
}

func TestLoadAndStartPlaybackAlwaysResumesAfterReplacingTrack(t *testing.T) {
	engine := &playbackStartTestEngine{}
	track := music.Track{ID: "tx_123", SourceID: "tx", LXSourceKey: "tx", Title: "晴天"}
	if err := loadAndStartPlayback(context.Background(), engine, music.PlaybackResource{URL: "https://example.com/audio.mp3"}, track); err != nil {
		t.Fatalf("loadAndStartPlayback returned error: %v", err)
	}
	if len(engine.calls) != 2 || engine.calls[0] != "load" || engine.calls[1] != "resume" {
		t.Fatalf("expected load then resume, got %#v", engine.calls)
	}
}

func TestLoadAndStartPlaybackDoesNotResumeWhenLoadFails(t *testing.T) {
	engine := &playbackStartTestEngine{loadErr: errors.New("load failed")}
	track := music.Track{ID: "tx_123", SourceID: "tx", LXSourceKey: "tx", Title: "晴天"}
	if err := loadAndStartPlayback(context.Background(), engine, music.PlaybackResource{URL: "https://example.com/audio.mp3"}, track); err == nil {
		t.Fatal("expected load error")
	}
	if len(engine.calls) != 1 || engine.calls[0] != "load" {
		t.Fatalf("resume must not run after load failure, got %#v", engine.calls)
	}
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
	manager, err := config.NewManager(fstest.MapFS{}, t.TempDir())
	if err != nil { t.Fatal(err) }
	value := config.DefaultMusicConfig()
	value.EnabledSearchSources = []string{"mg", "tx", "kw"}
	service := NewMusicService(manager, value)
	if err := service.SaveConfig(value); err != nil { t.Fatal(err) }
	if got := service.activeLXSearchSourceIDs(); len(got) != 3 || got[0] != "mg" || got[1] != "tx" || got[2] != "kw" {
		t.Fatalf("configured search order = %v", got)
	}
	reloaded, err := config.LoadMusicConfig(manager)
	if err != nil { t.Fatal(err) }
	restarted := NewMusicService(manager, reloaded)
	defer restarted.Shutdown()
	if got := restarted.activeLXSearchSourceIDs(); len(got) != 3 || got[0] != "mg" || got[1] != "tx" || got[2] != "kw" {
		t.Fatalf("search order after restart = %v", got)
	}
}
