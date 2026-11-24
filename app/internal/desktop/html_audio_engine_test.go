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

func TestHTMLAudioPlaybackEngineKeepsWatcherLoadingDuringRefresh(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	engine := newHTMLAudioPlaybackEngine(func(track music.Track, failed music.PlaybackResource) (music.PlaybackResource, error) {
		close(started)
		<-release
		return music.PlaybackResource{URL:"https://media.example/new.mp3", SourceID:failed.SourceID, TrackID:failed.TrackID, Quality:failed.Quality}, nil
	})
	var commands []htmlAudioCommand
	engine.setEmitter(func(_ string, payload any) { commands = append(commands, payload.(htmlAudioCommand)) })
	track := music.Track{ID:"track-1", SourceID:"tx"}
	resource := music.PlaybackResource{URL:"https://media.example/old.mp3", SourceID:"tx", TrackID:"track-1", Quality:"128k"}
	if err := engine.LoadResource(context.Background(), track, resource); err != nil { t.Fatal(err) }
	session := commands[0].SessionID
	done := make(chan error, 1)
	go func() { done <- engine.Report(session, "error", 0, 10000, "expired") }()
	<-started
	snapshot, _ := engine.Snapshot(context.Background())
	if snapshot.State != music.PlaybackLoading { t.Fatalf("watcher must not see terminal error during refresh: %s", snapshot.State) }
	if err := engine.Report(session, "error", 0, 10000, "duplicate"); err != nil { t.Fatal(err) }
	close(release)
	if err := <-done; err != nil { t.Fatal(err) }
	if len(commands) != 2 || commands[1].Resource.URL != "https://media.example/new.mp3" { t.Fatalf("refreshed URL was not loaded: %#v", commands) }
}

func TestMusicServiceBeginPlaybackResolutionCancelsPreviousRequest(t *testing.T) {
	service := &MusicService{}
	first, firstGeneration, firstCancel := service.beginPlaybackResolution(context.Background())
	defer firstCancel()
	second, secondGeneration, secondCancel := service.beginPlaybackResolution(context.Background())
	defer secondCancel()
	if firstGeneration == secondGeneration { t.Fatal("playback resolution generation did not advance") }
	select {
	case <-first.Done():
	default: t.Fatal("starting a new track did not cancel the previous URL resolution")
	}
	if err := second.Err(); err != nil { t.Fatalf("new playback resolution was cancelled: %v", err) }
	service.finishPlaybackResolution(firstGeneration)
	if !service.playbackResolutionCurrent(secondGeneration) { t.Fatal("finishing stale generation invalidated the active resolution") }
}

func TestHTMLAudioPlaybackEngineRejectsResourcesRequiringMediaHeadersOrProxy(t *testing.T) {
	engine := newHTMLAudioPlaybackEngine(nil)
	track := music.Track{ID:"track-1", SourceID:"tx"}
	cases := []music.PlaybackResource{
		{URL:"https://media.example/a.mp3", SourceID:"tx", TrackID:"track-1", Headers:map[string]string{"Referer":"https://source.example/"}},
		{URL:"https://media.example/a.mp3", SourceID:"tx", TrackID:"track-1", RequiresProxy:true},
	}
	for _, resource := range cases {
		if err := engine.LoadResource(context.Background(), track, resource); err == nil { t.Fatal("expected unsupported media request context to fail closed") }
	}
}

func TestHTMLAudioQueueEndDoesNotFallBackToLegacyPlayer(t *testing.T) {
	service := &MusicService{engine:newHTMLAudioPlaybackEngine(nil), queue:music.NewQueue(1)}
	if err := service.advanceAfterPlaybackEnd(music.PlaybackSnapshot{State:music.PlaybackStopped}, nil); err != nil { t.Fatalf("empty HTMLAudio queue should end normally: %v", err) }
}


func TestHTMLAudioPlaybackEnginePreservesVolumeAcrossTrackLoads(t *testing.T) {
	var commands []htmlAudioCommand
	engine := newHTMLAudioPlaybackEngine(nil)
	engine.setEmitter(func(_ string, payload any) { commands = append(commands, payload.(htmlAudioCommand)) })
	if err := engine.SetVolume(0.35); err != nil { t.Fatal(err) }
	track1 := music.Track{ID:"track-1", SourceID:"tx"}
	resource1 := music.PlaybackResource{URL:"https://media.example/one.mp3", SourceID:"tx", TrackID:"track-1"}
	if err := engine.LoadResource(context.Background(), track1, resource1); err != nil { t.Fatal(err) }
	if got := commands[len(commands)-1].Volume; got != 0.35 { t.Fatalf("first load volume = %v, want 0.35", got) }
	track2 := music.Track{ID:"track-2", SourceID:"kw"}
	resource2 := music.PlaybackResource{URL:"https://media.example/two.mp3", SourceID:"kw", TrackID:"track-2"}
	if err := engine.LoadResource(context.Background(), track2, resource2); err != nil { t.Fatal(err) }
	if got := commands[len(commands)-1].Volume; got != 0.35 { t.Fatalf("volume was not preserved across source switch: %v", got) }
}

func TestHTMLAudioPlaybackEngineClampsAndAppliesVolumeToActiveSession(t *testing.T) {
	var commands []htmlAudioCommand
	engine := newHTMLAudioPlaybackEngine(nil)
	engine.setEmitter(func(_ string, payload any) { commands = append(commands, payload.(htmlAudioCommand)) })
	track := music.Track{ID:"track-1", SourceID:"tx"}
	resource := music.PlaybackResource{URL:"https://media.example/song.mp3", SourceID:"tx", TrackID:"track-1"}
	if err := engine.LoadResource(context.Background(), track, resource); err != nil { t.Fatal(err) }
	if err := engine.SetVolume(1.5); err != nil { t.Fatal(err) }
	last := commands[len(commands)-1]
	if last.Command != "volume" || last.Volume != 1 { t.Fatalf("volume command = %#v, want clamped 1", last) }
	if err := engine.SetVolume(-0.5); err != nil { t.Fatal(err) }
	last = commands[len(commands)-1]
	if last.Command != "volume" || last.Volume != 0 { t.Fatalf("mute command = %#v, want 0", last) }
}


func TestHTMLAudioPlaybackEngineVolumePersistsAcrossSourceSwitchAndMuteRestore(t *testing.T) {
	var commands []htmlAudioCommand
	engine := newHTMLAudioPlaybackEngine(nil)
	engine.setEmitter(func(_ string, payload any) { commands = append(commands, payload.(htmlAudioCommand)) })

	if err := engine.SetVolume(0.42); err != nil { t.Fatal(err) }
	track1 := music.Track{ID:"track-1", SourceID:"tx"}
	resource1 := music.PlaybackResource{URL:"https://media.example/one.mp3", SourceID:"tx", TrackID:"track-1"}
	if err := engine.LoadResource(context.Background(), track1, resource1); err != nil { t.Fatal(err) }
	if got := commands[len(commands)-1].Volume; got != 0.42 { t.Fatalf("initial load volume = %v, want 0.42", got) }

	if err := engine.SetVolume(0); err != nil { t.Fatal(err) }
	mute := commands[len(commands)-1]
	if mute.Command != "volume" || mute.Volume != 0 { t.Fatalf("mute command = %#v", mute) }
	if err := engine.SetVolume(0.42); err != nil { t.Fatal(err) }
	unmute := commands[len(commands)-1]
	if unmute.Command != "volume" || unmute.Volume != 0.42 { t.Fatalf("restore command = %#v", unmute) }

	track2 := music.Track{ID:"track-2", SourceID:"kw"}
	resource2 := music.PlaybackResource{URL:"https://media.example/two.mp3", SourceID:"kw", TrackID:"track-2"}
	if err := engine.LoadResource(context.Background(), track2, resource2); err != nil { t.Fatal(err) }
	if got := commands[len(commands)-1].Volume; got != 0.42 { t.Fatalf("volume after source switch = %v, want 0.42", got) }
}

func TestHTMLAudioPlaybackEngineVolumeIsRetainedWhenMediaURLRefreshes(t *testing.T) {
	var commands []htmlAudioCommand
	engine := newHTMLAudioPlaybackEngine(func(_ music.Track, failed music.PlaybackResource) (music.PlaybackResource, error) {
		return music.PlaybackResource{URL:"https://media.example/refreshed.mp3", SourceID:failed.SourceID, TrackID:failed.TrackID, Quality:failed.Quality}, nil
	})
	engine.setEmitter(func(_ string, payload any) { commands = append(commands, payload.(htmlAudioCommand)) })
	track := music.Track{ID:"track-1", SourceID:"tx"}
	resource := music.PlaybackResource{URL:"https://media.example/expired.mp3", SourceID:"tx", TrackID:"track-1", Quality:"128k"}
	if err := engine.SetVolume(0.27); err != nil { t.Fatal(err) }
	if err := engine.LoadResource(context.Background(), track, resource); err != nil { t.Fatal(err) }
	session := commands[0].SessionID
	if err := engine.Report(session, "error", 0, 10000, "expired"); err != nil { t.Fatal(err) }
	if len(commands) != 2 || commands[1].Command != "load" || commands[1].Volume != 0.27 {
		t.Fatalf("refreshed load did not retain volume: %#v", commands)
	}
}


func TestHTMLAudioPlaybackEngineSeekAndStopCommands(t *testing.T) {
	var commands []htmlAudioCommand
	engine := newHTMLAudioPlaybackEngine(nil)
	engine.setEmitter(func(_ string, payload any) { commands = append(commands, payload.(htmlAudioCommand)) })
	track := music.Track{ID:"track-seek", SourceID:"tx"}
	resource := music.PlaybackResource{URL:"https://media.example/seek.mp3", SourceID:"tx", TrackID:"track-seek"}
	if err := engine.LoadResource(context.Background(), track, resource); err != nil { t.Fatal(err) }
	session := commands[0].SessionID
	if err := engine.Seek(42000); err != nil { t.Fatal(err) }
	seek := commands[len(commands)-1]
	if seek.Command != "seek" || seek.SessionID != session || seek.PositionMS != 42000 { t.Fatalf("seek command = %#v", seek) }
	if err := engine.Stop(context.Background()); err != nil { t.Fatal(err) }
	stop := commands[len(commands)-1]
	if stop.Command != "stop" || stop.SessionID != session { t.Fatalf("stop command = %#v", stop) }
	snapshot, _ := engine.Snapshot(context.Background())
	if snapshot.State != music.PlaybackStopped || snapshot.PositionMS != 0 { t.Fatalf("stopped snapshot = %#v", snapshot) }
	if err := engine.Report(session, "pause", 42000, 90000, ""); err != nil { t.Fatal(err) }
	snapshot, _ = engine.Snapshot(context.Background())
	if snapshot.State != music.PlaybackStopped { t.Fatalf("late pause event overwrote stopped state: %s", snapshot.State) }
}


func TestHTMLAudioPlaybackEngineAppliesPersistedVolumeBeforeFirstTrack(t *testing.T) {
	var commands []htmlAudioCommand
	engine := newHTMLAudioPlaybackEngine(nil)
	engine.setEmitter(func(_ string, payload any) { commands = append(commands, payload.(htmlAudioCommand)) })
	if err := engine.SetVolume(0.23); err != nil { t.Fatal(err) }
	if len(commands) != 1 || commands[0].Command != "volume" || commands[0].SessionID != 0 || commands[0].Volume != 0.23 {
		t.Fatalf("initial volume command = %#v", commands)
	}
}
