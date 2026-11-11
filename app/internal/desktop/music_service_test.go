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
