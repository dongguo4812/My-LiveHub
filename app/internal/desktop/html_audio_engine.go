package desktop

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/dongguo4812/My-LiveHub/app/internal/music"
)

const musicHTMLAudioCommandEvent = "music:html-audio:command"

type htmlAudioCommand struct {
	Command string `json:"command"`
	SessionID uint64 `json:"session_id"`
	Track music.Track `json:"track"`
	Resource music.PlaybackResource `json:"resource"`
	PositionMS int64 `json:"position_ms,omitempty"`
	Autoplay bool `json:"autoplay"`
	Volume float64 `json:"volume"`
	LoudnessNormalization bool `json:"loudness_normalization"`
}

type htmlAudioPlaybackEngine struct {
	mu sync.RWMutex
	emit func(string, any)
	refresh func(music.Track, music.PlaybackResource) (music.PlaybackResource, error)
	snapshot music.PlaybackSnapshot
	track music.Track
	resource music.PlaybackResource
	sessionID uint64
	retried bool
	refreshing bool
	volume float64
	loudnessNormalization bool
	proxy *mediaProxy
}

func newHTMLAudioPlaybackEngine(refresh func(music.Track, music.PlaybackResource) (music.PlaybackResource, error)) *htmlAudioPlaybackEngine {
	return &htmlAudioPlaybackEngine{refresh: refresh, volume: 1, snapshot: music.PlaybackSnapshot{State: music.PlaybackIdle}}
}

// Start creates the loopback transport used by Web Audio normalization. It is
// started with the music module and remains alive for the lifetime of the player.
func (e *htmlAudioPlaybackEngine) Start(context.Context) error {
	proxy, err := newMediaProxy()
	if err != nil { return err }
	e.mu.Lock()
	if e.proxy != nil {
		e.mu.Unlock()
		_ = proxy.Close()
		return nil
	}
	e.proxy = proxy
	e.mu.Unlock()
	return nil
}

// Close stops playback and releases the loopback media transport.
func (e *htmlAudioPlaybackEngine) Close() error {
	stopErr := e.Stop(context.Background())
	e.mu.Lock()
	proxy := e.proxy
	e.proxy = nil
	e.mu.Unlock()
	if proxy != nil {
		if err := proxy.Close(); err != nil { return errors.Join(stopErr, err) }
	}
	return stopErr
}

func (e *htmlAudioPlaybackEngine) setEmitter(emit func(string, any)) {
	e.mu.Lock()
	e.emit = emit
	e.mu.Unlock()
}

func (e *htmlAudioPlaybackEngine) commandLoadLocked(position int64, autoplay bool) error {
	if e.emit == nil { return errors.New("HTMLAudio bridge is not attached to the Wails window") }
	resource := e.resource
	if e.loudnessNormalization || resource.RequiresProxy || len(resource.Headers) > 0 {
		if e.proxy == nil { return errors.New("music media proxy is not running") }
		proxyURL, err := e.proxy.Register(resource)
		if err != nil { return err }
		resource.URL = proxyURL
	}
	payload := htmlAudioCommand{
		Command: "load", SessionID: e.sessionID, Track: e.track, Resource: resource,
		PositionMS: position, Autoplay: autoplay, Volume: e.volume,
		LoudnessNormalization: e.loudnessNormalization,
	}
	e.emit(musicHTMLAudioCommandEvent, payload)
	return nil
}

func (e *htmlAudioPlaybackEngine) commandLocked(command string, position int64, volume float64) error {
	if command == "load" { return e.commandLoadLocked(position, true) }
	if e.emit == nil { return errors.New("HTMLAudio bridge is not attached to the Wails window") }
	payload := htmlAudioCommand{Command:command, SessionID:e.sessionID, PositionMS:position, Volume:volume, LoudnessNormalization:e.loudnessNormalization}
	e.emit(musicHTMLAudioCommandEvent, payload)
	return nil
}

func (e *htmlAudioPlaybackEngine) Load(ctx context.Context, track music.Track) error {
	return errors.New("HTMLAudio playback requires a validated PlaybackResource")
}

func (e *htmlAudioPlaybackEngine) LoadResource(ctx context.Context, track music.Track, resource music.PlaybackResource) error {
	if err := ctx.Err(); err != nil { return err }
	if strings.TrimSpace(resource.URL) == "" || resource.SourceID == "" || resource.TrackID == "" ||
		resource.SourceID != firstNonEmpty(track.LXSourceKey, track.SourceID) || resource.TrackID != track.ID {
		return errors.New("HTMLAudio received a playback resource that does not match the track")
	}
	e.mu.Lock()
	e.sessionID++
	e.track, e.resource, e.retried, e.refreshing = track, resource, false, false
	e.snapshot = music.PlaybackSnapshot{State:music.PlaybackLoading, Track:&track, DurationMS:track.DurationMS}
	err := e.commandLoadLocked(0, true)
	if err != nil {
		e.snapshot.State = music.PlaybackError
		e.snapshot.Error = err.Error()
	}
	e.mu.Unlock()
	return err
}

func (e *htmlAudioPlaybackEngine) Pause(context.Context) error {
	e.mu.Lock(); defer e.mu.Unlock()
	if e.snapshot.State == music.PlaybackIdle || e.snapshot.State == music.PlaybackStopped { return errors.New("no active HTMLAudio session") }
	return e.commandLocked("pause", 0, 0)
}
func (e *htmlAudioPlaybackEngine) Resume(context.Context) error {
	e.mu.Lock(); defer e.mu.Unlock()
	if e.snapshot.State == music.PlaybackIdle || e.snapshot.State == music.PlaybackStopped { return errors.New("no active HTMLAudio session") }
	return e.commandLocked("play", 0, 0)
}
func (e *htmlAudioPlaybackEngine) Stop(context.Context) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.snapshot.State != music.PlaybackIdle {
		if err := e.commandLocked("stop", 0, 0); err != nil {
			return err
		}
		// Invalidate the stopped session so delayed media events cannot
		// repopulate the idle snapshot with the previous track's metadata.
		e.sessionID++
	}
	e.track = music.Track{}
	e.resource = music.PlaybackResource{}
	e.retried = false
	e.refreshing = false
	e.snapshot = music.PlaybackSnapshot{State: music.PlaybackIdle}
	return nil
}
func (e *htmlAudioPlaybackEngine) Snapshot(context.Context) (music.PlaybackSnapshot, error) {
	e.mu.RLock(); defer e.mu.RUnlock()
	result := e.snapshot
	if result.Track != nil { track := *result.Track; result.Track = &track }
	return result, nil
}
func (e *htmlAudioPlaybackEngine) Seek(positionMS int64) error {
	if positionMS < 0 { positionMS = 0 }
	e.mu.Lock(); defer e.mu.Unlock()
	return e.commandLocked("seek", positionMS, 0)
}
func (e *htmlAudioPlaybackEngine) SetLoudnessNormalization(enabled bool) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.loudnessNormalization == enabled { return nil }
	previousEnabled, previousSession, previousState, previousRetried := e.loudnessNormalization, e.sessionID, e.snapshot.State, e.retried
	e.loudnessNormalization = enabled
	if e.emit == nil || e.snapshot.Track == nil || e.snapshot.State == music.PlaybackIdle || e.snapshot.State == music.PlaybackStopped {
		return nil
	}
	position := e.snapshot.PositionMS
	autoplay := e.snapshot.State == music.PlaybackPlaying || e.snapshot.State == music.PlaybackLoading
	e.sessionID++
	e.retried = false
	if autoplay { e.snapshot.State = music.PlaybackLoading } else { e.snapshot.State = music.PlaybackPaused }
	if err := e.commandLoadLocked(position, autoplay); err != nil {
		e.loudnessNormalization, e.sessionID, e.snapshot.State, e.retried = previousEnabled, previousSession, previousState, previousRetried
		return err
	}
	return nil
}

func (e *htmlAudioPlaybackEngine) SetVolume(volume float64) error {
	if volume < 0 { volume = 0 }; if volume > 1 { volume = 1 }
	e.mu.Lock(); defer e.mu.Unlock()
	e.volume = volume
	return e.commandLocked("volume", 0, volume)
}
func (e *htmlAudioPlaybackEngine) Report(sessionID uint64, event string, positionMS, durationMS int64, message string) error {
	e.mu.Lock()
	if sessionID == 0 || sessionID != e.sessionID || e.refreshing {
		e.mu.Unlock()
		return nil
	}
	if positionMS < 0 { positionMS = 0 }
	if durationMS < 0 { durationMS = 0 }
	switch event {
	case "loadstart":
		if e.snapshot.State != music.PlaybackPaused {
			e.snapshot.State = music.PlaybackLoading
		}
	case "autoplayblocked":
		e.snapshot.State = music.PlaybackPaused
		e.snapshot.Error = strings.TrimSpace(message)
	case "playing":
		e.snapshot.State = music.PlaybackPlaying
		e.snapshot.Error = ""
	case "pause":
		if e.snapshot.State != music.PlaybackStopped { e.snapshot.State = music.PlaybackPaused }
	case "timeupdate", "durationchange", "waiting", "stalled":
		e.snapshot.PositionMS, e.snapshot.DurationMS = positionMS, durationMS
	case "ended":
		e.snapshot.State = music.PlaybackStopped
		e.snapshot.PositionMS, e.snapshot.DurationMS = durationMS, durationMS
	case "error":
		track, failed, refresh := e.track, e.resource, e.refresh
		e.snapshot.Error = strings.TrimSpace(message)
		if !e.retried && refresh != nil {
			// Keep the watcher in Loading while the single bounded URL refresh
			// is running; otherwise it could advance the queue before retry.
			e.retried, e.refreshing = true, true
			e.snapshot.State = music.PlaybackLoading
			e.mu.Unlock()
			refreshed, err := refresh(track, failed)
			e.mu.Lock()
			if sessionID != e.sessionID {
				e.mu.Unlock()
				return nil
			}
			e.refreshing = false
			if err != nil {
				e.snapshot.State = music.PlaybackError
				e.snapshot.Error = err.Error()
				e.mu.Unlock()
				return fmt.Errorf("HTMLAudio media failed and URL refresh failed: %w", err)
			}
			if refreshed.URL == "" || refreshed.URL == failed.URL {
				e.snapshot.State = music.PlaybackError
				e.mu.Unlock()
				return nil
			}
			e.resource = refreshed
			e.snapshot = music.PlaybackSnapshot{State:music.PlaybackLoading, Track:&track}
			emitErr := e.commandLoadLocked(0, true)
			if emitErr != nil {
				e.snapshot.State = music.PlaybackError
				e.snapshot.Error = emitErr.Error()
			}
			e.mu.Unlock()
			return emitErr
		}
		e.snapshot.State = music.PlaybackError
	default:
		e.mu.Unlock()
		return fmt.Errorf("unsupported HTMLAudio event %q", event)
	}
	e.mu.Unlock()
	return nil
}
