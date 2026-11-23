package desktop

import (
	"context"
	"testing"

	"github.com/dongguo4812/My-LiveHub/app/internal/music"
)

func TestHTMLAudioPlaybackEngineUsesStructuredResourceAndSession(t *testing.T) {
	var commands []htmlAudioCommand
	engine := newHTMLAudioPlaybackEngine(nil)
	engine.setEmitter(func(name string, payload any) {
		if name != musicHTMLAudioCommandEvent { t.Fatalf("unexpected event %q", name) }
		commands = append(commands, payload.(htmlAudioCommand))
	})
	track := music.Track{ID:"track-1", SourceID:"tx"}
	resource := music.PlaybackResource{URL:"https://media.example/song.mp3", SourceID:"tx", TrackID:"track-1", Quality:"128k"}
	if err := engine.LoadResource(context.Background(), track, resource); err != nil { t.Fatal(err) }
	if len(commands) != 1 || commands[0].Command != "load" || commands[0].Resource.URL != resource.URL { t.Fatalf("resource was not bridged: %#v", commands) }
	session := commands[0].SessionID
	if err := engine.Report(session+1, "playing", 0, 10000, ""); err != nil { t.Fatal(err) }
	snapshot, _ := engine.Snapshot(context.Background())
	if snapshot.State != music.PlaybackLoading { t.Fatalf("stale session changed state: %s", snapshot.State) }
	if err := engine.Report(session, "playing", 0, 10000, ""); err != nil { t.Fatal(err) }
	snapshot, _ = engine.Snapshot(context.Background())
	if snapshot.State != music.PlaybackPlaying { t.Fatalf("playing event not applied: %s", snapshot.State) }
}

func TestHTMLAudioPlaybackEngineRejectsTrackResourceMismatch(t *testing.T) {
	engine := newHTMLAudioPlaybackEngine(nil)
	err := engine.LoadResource(context.Background(), music.Track{ID:"track-1", SourceID:"tx"}, music.PlaybackResource{URL:"https://media.example/a.mp3", SourceID:"kw", TrackID:"track-1"})
	if err == nil { t.Fatal("expected mismatched resource to be rejected") }
}

func TestHTMLAudioPlaybackEngineRefreshesFailedMediaAtMostOnce(t *testing.T) {
	refreshCalls := 0
	var commands []htmlAudioCommand
	engine := newHTMLAudioPlaybackEngine(func(track music.Track, failed music.PlaybackResource) (music.PlaybackResource, error) {
		refreshCalls++
		return music.PlaybackResource{URL:"https://media.example/refreshed.mp3", SourceID:failed.SourceID, TrackID:failed.TrackID, Quality:failed.Quality}, nil
	})
	engine.setEmitter(func(_ string, payload any) { commands = append(commands, payload.(htmlAudioCommand)) })
	track := music.Track{ID:"track-1", SourceID:"tx"}
	resource := music.PlaybackResource{URL:"https://media.example/expired.mp3", SourceID:"tx", TrackID:"track-1", Quality:"128k"}
	if err := engine.LoadResource(context.Background(), track, resource); err != nil { t.Fatal(err) }
	session := commands[0].SessionID
	if err := engine.Report(session, "error", 0, 10000, "network error"); err != nil { t.Fatal(err) }
	if refreshCalls != 1 || len(commands) != 2 || commands[1].Resource.URL != "https://media.example/refreshed.mp3" { t.Fatalf("refresh was not bridged once: calls=%d commands=%#v", refreshCalls, commands) }
	if err := engine.Report(session, "error", 0, 10000, "retry failed"); err != nil { t.Fatal(err) }
	if refreshCalls != 1 { t.Fatalf("refresh repeated after retry: %d", refreshCalls) }
}
