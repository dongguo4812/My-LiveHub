//go:build windows

package music

import (
	"context"
	"encoding/binary"
	"fmt"
	"math"
	"net/url"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestMpvPlaybackIntegration exercises the real bundled mpv binary. CI routes
// decoded PCM to mpv's null audio output so it validates decoding and IPC
// without depending on a hosted runner's physical audio device.
func TestMpvPlaybackIntegration(t *testing.T) {
	executable := os.Getenv("MPV_EXECUTABLE")
	if executable == "" {
		t.Skip("MPV_EXECUTABLE is not set; integration test runs in the pinned-runtime CI job")
	}

	audioPath := filepath.Join(t.TempDir(), "mpv-integration-tone.wav")
	if err := writeIntegrationWAV(audioPath, 12*time.Second); err != nil {
		t.Fatalf("generate WAV fixture: %v", err)
	}
	absolutePath, err := filepath.Abs(audioPath)
	if err != nil {
		t.Fatal(err)
	}
	slashPath := filepath.ToSlash(absolutePath)
	if len(slashPath) >= 2 && slashPath[1] == ':' {
		slashPath = "/" + slashPath
	}
	mediaURL := (&url.URL{Scheme: "file", Path: slashPath}).String()

	engine := NewMpvEngine(executable)
	if os.Getenv("MPV_TEST_REAL_AUDIO") == "1" {
		// Opt-in local hardware test: use Windows WASAPI instead of the null sink.
		// This is intentionally disabled in hosted CI to avoid requiring speakers.
		engine.audioOutput = "wasapi"
		t.Log("real audio mode enabled: mpv will output the generated tone through Windows WASAPI")
	} else {
		engine.audioOutput = "null"
		t.Log("null audio mode enabled: validating decode and IPC without physical audio output")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := engine.Start(ctx); err != nil {
		t.Fatalf("start mpv and establish JSON IPC: %v", err)
	}
	defer func() {
		if err := engine.Close(); err != nil {
			t.Errorf("close mpv process: %v", err)
		}
	}()

	track := Track{ID: "integration-tone", SourceID: "test", Title: "My-LiveHub integration tone", DurationMS: 12000}
	if err := engine.Load(ctx, PlaybackResource{URL: mediaURL}, track); err != nil {
		t.Fatalf("load generated WAV: %v", err)
	}

	playing := waitForMpvSnapshot(t, ctx, engine, func(s PlaybackSnapshot) bool {
		return s.State == PlaybackPlaying && s.DurationMS >= 11000
	}, "mpv did not decode and start the WAV fixture")
	if playing.Track == nil || playing.Track.ID != track.ID {
		t.Fatalf("unexpected active track: %#v", playing.Track)
	}

	if err := engine.Pause(ctx); err != nil {
		t.Fatalf("pause via JSON IPC: %v", err)
	}
	waitForMpvSnapshot(t, ctx, engine, func(s PlaybackSnapshot) bool {
		return s.State == PlaybackPaused
	}, "mpv did not enter paused state")

	if err := engine.Resume(ctx); err != nil {
		t.Fatalf("resume via JSON IPC: %v", err)
	}
	waitForMpvSnapshot(t, ctx, engine, func(s PlaybackSnapshot) bool {
		return s.State == PlaybackPlaying
	}, "mpv did not resume playback")

	if err := engine.Seek(ctx, 2500); err != nil {
		t.Fatalf("seek via JSON IPC: %v", err)
	}
	waitForMpvSnapshot(t, ctx, engine, func(s PlaybackSnapshot) bool {
		return s.PositionMS >= 2000 && s.PositionMS <= 6000
	}, "mpv did not seek to the requested position")

	if err := engine.SetVolume(ctx, 37); err != nil {
		t.Fatalf("set volume via JSON IPC: %v", err)
	}
	volume := waitForMpvSnapshot(t, ctx, engine, func(s PlaybackSnapshot) bool {
		return s.Volume == 37
	}, "mpv did not apply the requested volume")

	if err := engine.Stop(ctx); err != nil {
		t.Fatalf("stop via JSON IPC: %v", err)
	}
	stopped := waitForMpvSnapshot(t, ctx, engine, func(s PlaybackSnapshot) bool {
		return s.Track == nil
	}, "mpv did not clear the active track after stop")
	if stopped.Track != nil || volume.Volume != 37 {
		t.Fatalf("unexpected final playback snapshots: stopped=%#v volume=%#v", stopped, volume)
	}

	missingPath := filepath.Join(t.TempDir(), "missing-audio-file.mp3")
	missingSlashPath := filepath.ToSlash(missingPath)
	if len(missingSlashPath) >= 2 && missingSlashPath[1] == ':' {
		missingSlashPath = "/" + missingSlashPath
	}
	missingURL := (&url.URL{Scheme: "file", Path: missingSlashPath}).String()
	failedTrack := Track{ID: "missing-fixture", SourceID: "test", Title: "Missing audio fixture"}
	if err := engine.Load(ctx, PlaybackResource{URL: missingURL}, failedTrack); err != nil {
		t.Fatalf("submit missing media fixture: %v", err)
	}
	// The real failure is asynchronous in mpv. Move the pending-load clock
	// forward so the test can verify the terminal error state without waiting.
	engine.mu.Lock()
	engine.loadStartedAt = time.Now().Add(-31 * time.Second)
	engine.mu.Unlock()
	failed, err := engine.Snapshot(ctx)
	if err != nil {
		t.Fatalf("snapshot failed media load: %v", err)
	}
	if failed.State != PlaybackError || failed.Error == "" {
		t.Fatalf("failed media load was not exposed as an error: %#v", failed)
	}
}

func waitForMpvSnapshot(t *testing.T, ctx context.Context, engine *MpvEngine, predicate func(PlaybackSnapshot) bool, failure string) PlaybackSnapshot {
	t.Helper()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		snapshot, err := engine.Snapshot(ctx)
		if err == nil && predicate(snapshot) {
			return snapshot
		}
		select {
		case <-ctx.Done():
			t.Fatalf("%s (last snapshot: %#v; last error: %v)", failure, snapshot, err)
		case <-ticker.C:
		}
	}
}

func writeIntegrationWAV(path string, duration time.Duration) error {
	const sampleRate = 44100
	const channels = 1
	const bitsPerSample = 16
	samples := int(float64(sampleRate) * duration.Seconds())
	dataSize := samples * channels * bitsPerSample / 8
	file, err := os.Create(path)
	if err != nil {
		return err
	}
	defer file.Close()

	if _, err := file.Write([]byte("RIFF")); err != nil { return err }
	if err := binary.Write(file, binary.LittleEndian, uint32(36+dataSize)); err != nil { return err }
	if _, err := file.Write([]byte("WAVEfmt ")); err != nil { return err }
	if err := binary.Write(file, binary.LittleEndian, uint32(16)); err != nil { return err }
	if err := binary.Write(file, binary.LittleEndian, uint16(1)); err != nil { return err }
	if err := binary.Write(file, binary.LittleEndian, uint16(channels)); err != nil { return err }
	if err := binary.Write(file, binary.LittleEndian, uint32(sampleRate)); err != nil { return err }
	if err := binary.Write(file, binary.LittleEndian, uint32(sampleRate*channels*bitsPerSample/8)); err != nil { return err }
	if err := binary.Write(file, binary.LittleEndian, uint16(channels*bitsPerSample/8)); err != nil { return err }
	if err := binary.Write(file, binary.LittleEndian, uint16(bitsPerSample)); err != nil { return err }
	if _, err := file.Write([]byte("data")); err != nil { return err }
	if err := binary.Write(file, binary.LittleEndian, uint32(dataSize)); err != nil { return err }

	buffer := make([]byte, samples*2)
	for i := 0; i < samples; i++ {
		sample := int16(math.Sin(2*math.Pi*440*float64(i)/sampleRate) * 12000)
		binary.LittleEndian.PutUint16(buffer[i*2:], uint16(sample))
	}
	if _, err := file.Write(buffer); err != nil {
		return fmt.Errorf("write PCM samples: %w", err)
	}
	return nil
}
