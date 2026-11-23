package desktop

import (
	"context"
	"testing"
	"time"

	"github.com/dongguo4812/My-LiveHub/app/internal/config"
	"github.com/dongguo4812/My-LiveHub/app/internal/events"
	"github.com/dongguo4812/My-LiveHub/app/internal/music"
)

type fakeLXScriptRuntime struct {
	started bool
	closed bool
	source string
	info any
}

func (r *fakeLXScriptRuntime) Start(context.Context) error { r.started = true; return nil }
func (r *fakeLXScriptRuntime) Close() error { r.closed = true; return nil }
func (r *fakeLXScriptRuntime) ResolvePlaybackURL(_ context.Context, source string, info any) (string, error) {
	r.source, r.info = source, info
	return "https://cdn.example.test/song.mp3", nil
}

func TestMusicServiceResolvesPlaybackURLThroughOwnedLXRuntime(t *testing.T) {
	service := NewMusicServiceWithDependencies(nil, config.DefaultMusicConfig(), nil, nil)
	runtime := &fakeLXScriptRuntime{}
	service.scriptRuntime = runtime
	track := music.Track{
		ID: "kw_song-123",
		SourceID: "kw",
		LXSourceKey: "kw",
		Title: "测试歌曲",
		Artist: "测试歌手",
		SourceData: map[string]string{"songmid": "song-123"},
		Qualities: []music.TrackQuality{{Type: "320k", Size: "8 MiB"}},
	}
	got, err := service.ResolvePlaybackURL(track, "")
	if err != nil {
		t.Fatal(err)
	}
	if got != "https://cdn.example.test/song.mp3" {
		t.Fatalf("resolved URL = %q", got)
	}
	if runtime.source != "kw" {
		t.Fatalf("runtime source = %q, want kw", runtime.source)
	}
	payload, ok := runtime.info.(map[string]any)
	if !ok {
		t.Fatalf("runtime info type = %T", runtime.info)
	}
	musicInfo, ok := payload["musicInfo"].(map[string]any)
	if !ok || musicInfo["songmid"] != "song-123" || payload["type"] != "320k" {
		t.Fatalf("runtime payload = %#v", payload)
	}
}

func TestMusicServiceRejectsPlaybackURLWithoutProductionRuntime(t *testing.T) {
	service := NewMusicServiceWithDependencies(nil, config.DefaultMusicConfig(), nil, nil)
	service.scriptRuntime = nil
	_, err := service.ResolvePlaybackURL(music.Track{SourceID: "kw"}, "320k")
	if err == nil {
		t.Fatal("expected a clear error when production QuickJS is not enabled")
	}
}


func TestMusicServiceOwnsLXRuntimeStartAndShutdown(t *testing.T) {
	value := config.DefaultMusicConfig()
	value.Enabled = false
	service := NewMusicServiceWithDependencies(nil, value, nil, nil, events.NewBuffer())
	runtime := &fakeLXScriptRuntime{}
	service.scriptRuntime = runtime
	if err := service.start(context.Background()); err != nil {
		t.Fatalf("start music service: %v", err)
	}
	if !runtime.started {
		t.Fatal("music service did not start the LX runtime")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := service.stop(ctx); err != nil {
		t.Fatalf("stop music service: %v", err)
	}
	if !runtime.closed {
		t.Fatal("music service did not close the LX runtime")
	}
}
