package desktop

import (
	"context"
	"testing"

	"github.com/dongguo4812/My-LiveHub/app/internal/config"
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
	loaded *music.Track
	state music.PlaybackState
	volume int
}
func (e *serviceTestEngine) Load(_ context.Context, _ music.PlaybackResource, track music.Track) error {
	copy := track
	e.loaded = &copy
	e.state = music.PlaybackPlaying
	return nil
}
func (e *serviceTestEngine) Pause(context.Context) error { e.state = music.PlaybackPaused; return nil }
func (e *serviceTestEngine) Resume(context.Context) error { e.state = music.PlaybackPlaying; return nil }
func (*serviceTestEngine) Seek(context.Context, int64) error { return nil }
func (e *serviceTestEngine) SetVolume(_ context.Context, value int) error { e.volume = value; return nil }
func (e *serviceTestEngine) Stop(context.Context) error { e.loaded = nil; e.state = music.PlaybackStopped; return nil }
func (e *serviceTestEngine) Snapshot(context.Context) (music.PlaybackSnapshot, error) {
	snapshot := music.PlaybackSnapshot{State:e.state, Volume:e.volume}
	if e.loaded != nil { copy := *e.loaded; snapshot.Track = &copy }
	return snapshot, nil
}

func newTestMusicService(t *testing.T, enabled bool) (*MusicService, *serviceTestEngine) {
	t.Helper()
	sources := music.NewSourceRegistry()
	if err := sources.Register(serviceTestSource{}); err != nil { t.Fatal(err) }
	cfg := config.DefaultMusicConfig()
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
	if engine.loaded == nil || engine.loaded.ID != "track-1" {
		t.Fatalf("track was not loaded by the shared engine: %#v", engine.loaded)
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
