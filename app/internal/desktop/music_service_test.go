package desktop
import ("context"; "errors"; "testing"; "github.com/dongguo4812/My-LiveHub/app/internal/config"
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
