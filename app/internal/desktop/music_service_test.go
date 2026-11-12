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
 want:=[]string{"kg","kw","mg","tx","wy"}
 if len(got)!=len(want){t.Fatalf("got %d sources, want %d: %#v",len(got),len(want),got)}
 for i,id:=range want { if got[i].ID!=id { t.Errorf("source[%d]=%q, want %q",i,got[i].ID,id) }; if !got[i].Capabilities.Search { t.Errorf("source %q is not marked searchable",id) } }
}

func TestShouldAutoStartAcceptedSongRequest(t *testing.T) {
	if !shouldAutoStartSongRequest(nil) { t.Fatal("an idle player must start the first accepted request") }
	if shouldAutoStartSongRequest(&music.QueueItem{RequestedBy: "viewer"}) { t.Fatal("a request must not interrupt another viewer request") }
	if !shouldAutoStartSongRequest(&music.QueueItem{RequestedBy: hostQueueMarker}) { t.Fatal("a viewer request must interrupt the host playlist") }
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


func TestMoveHostTrackBetweenPlaylists(t *testing.T) {
	st := &hostPlaylistState{
		loaded: true,
		activeID: "a",
		index: 1,
		playlists: []HostPlaylistSnapshot{
			{ID: "a", Name: "A", Tracks: []music.Track{
				{ID: "one", SourceID: "tx", Title: "第一首"},
				{ID: "two", SourceID: "tx", Title: "第二首"},
				{ID: "three", SourceID: "tx", Title: "第三首"},
			}},
			{ID: "b", Name: "B", Tracks: []music.Track{{ID: "two", SourceID: "tx", Title: "第二首"}}},
		},
	}
	if err := moveHostTrackBetweenPlaylists(st, "a", "b", "tx", "one"); err != nil { t.Fatal(err) }
	if len(st.playlists[0].Tracks) != 2 || st.playlists[0].Tracks[0].ID != "two" { t.Fatalf("source playlist not updated: %#v", st.playlists[0].Tracks) }
	if st.index != 0 { t.Fatalf("playback cursor should shift with removal before current track, got %d", st.index) }
	if got := st.playlists[1].Tracks[1]; got.ID != "one" || got.Title != "第一首" { t.Fatalf("track metadata was not preserved: %#v", got) }
	if err := moveHostTrackBetweenPlaylists(st, "a", "b", "tx", "two"); err == nil { t.Fatal("expected duplicate destination track error") }
	if err := moveHostTrackBetweenPlaylists(st, "a", "missing", "tx", "two"); err == nil { t.Fatal("expected missing playlist error") }
}

func TestMovingCurrentlyPlayingHostTrackKeepsCursor(t *testing.T) {
	st := &hostPlaylistState{
		loaded: true, activeID: "a", index: 1,
		playlists: []HostPlaylistSnapshot{
			{ID: "a", Tracks: []music.Track{{ID:"one",SourceID:"tx"},{ID:"two",SourceID:"tx"},{ID:"three",SourceID:"tx"}}},
			{ID: "b", Tracks: []music.Track{}},
		},
	}
	if err := moveHostTrackBetweenPlaylists(st,"a","b","tx","two"); err != nil { t.Fatal(err) }
	if st.index != 0 { t.Fatalf("moving current track must position cursor before the next queued track, got %d",st.index) }
	if st.playlists[1].Tracks[0].ID != "two" { t.Fatal("current track was not moved to destination") }
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


func TestLXSearchSourceSelectionPersistsThroughServiceReload(t *testing.T) {
	dir := t.TempDir()
	newService := func() *MusicService {
		service := NewMusicService(nil, config.DefaultMusicConfig())
		service.lxStoreOnce.Do(func() {})
		store, err := music.NewLXUserSourceStore(dir)
		if err != nil {
			t.Fatal(err)
		}
		service.lxStore = store
		return service
	}

	service := newService()
	defer service.Shutdown()

	script := "/*\n * @name Huibq search source fixture\n * @version 1.2.0\n */\nvoid 0;"
	metadata, err := service.lxStore.ImportScript(script, "")
	if err != nil {
		t.Fatal(err)
	}
	keys := []string{"kw", "kg", "tx", "wy", "mg"}
	declared := make([]music.LXDeclaredSource, 0, len(keys))
	for _, key := range keys {
		declared = append(declared, music.LXDeclaredSource{
			Key: key, Type: "music", Actions: []string{"musicUrl"},
		})
	}
	result := music.LXSourceRuntimeResult{
		Initialized: true,
		Platforms: keys,
		DeclaredSources: declared,
	}
	if err := service.lxStore.SetValidation(metadata.ID, result); err != nil {
		t.Fatal(err)
	}

	// Save a non-default selection and a deliberately non-default priority.
	wantEnabled := []string{"tx", "kw", "mg"}
	if err := service.SetLXSearchSources(metadata.ID, wantEnabled); err != nil {
		t.Fatal(err)
	}
	assertOptions := func(got []LXSearchSourceOption, want []string) {
		t.Helper()
		gotEnabled := make([]string, 0)
		for _, option := range got {
			if option.Enabled {
				gotEnabled = append(gotEnabled, option.ID)
			}
		}
		if len(gotEnabled) != len(want) {
			t.Fatalf("enabled sources after reload = %v, want %v (all options: %#v)", gotEnabled, want, got)
		}
		for i := range want {
			if gotEnabled[i] != want[i] {
				t.Fatalf("enabled source priority after reload = %v, want %v", gotEnabled, want)
			}
		}
	}

	got, err := service.GetLXSearchSourceOptions(metadata.ID)
	if err != nil {
		t.Fatal(err)
	}
	assertOptions(got, wantEnabled)

	// A fresh service/store instance simulates closing and reopening the app.
	restarted := newService()
	defer restarted.Shutdown()
	got, err = restarted.GetLXSearchSourceOptions(metadata.ID)
	if err != nil {
		t.Fatal(err)
	}
	assertOptions(got, wantEnabled)

	// An explicit empty selection must remain empty after restart, not trigger
	// the "nil means use all defaults" compatibility path.
	if err := restarted.SetLXSearchSources(metadata.ID, []string{}); err != nil {
		t.Fatal(err)
	}
	restartedAgain := newService()
	defer restartedAgain.Shutdown()
	got, err = restartedAgain.GetLXSearchSourceOptions(metadata.ID)
	if err != nil {
		t.Fatal(err)
	}
	assertOptions(got, []string{})
}
