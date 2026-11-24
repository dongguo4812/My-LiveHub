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
	Volume float64 `json:"volume"`
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
}

func newHTMLAudioPlaybackEngine(refresh func(music.Track, music.PlaybackResource) (music.PlaybackResource, error)) *htmlAudioPlaybackEngine {
	return &htmlAudioPlaybackEngine{refresh: refresh, volume: 1, snapshot: music.PlaybackSnapshot{State: music.PlaybackIdle}}
}

func (e *htmlAudioPlaybackEngine) setEmitter(emit func(string, any)) {
	e.mu.Lock()
	e.emit = emit
	e.mu.Unlock()
}

func (e *htmlAudioPlaybackEngine) commandLocked(command string, position int64, volume float64) error {
	if e.emit == nil { return errors.New("HTMLAudio bridge is not attached to the Wails window") }
	payload := htmlAudioCommand{Command:command, SessionID:e.sessionID, PositionMS:position, Volume:volume}
	if command == "load" { payload.Volume = e.volume }
	if command == "load" { payload.Track, payload.Resource = e.track, e.resource }
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
	if resource.RequiresProxy || len(resource.Headers) > 0 {
		return errors.New("HTMLAudio direct playback cannot satisfy media headers or proxy requirements; media transport adaptation is required")
	}
	e.mu.Lock()
	e.sessionID++
	e.track, e.resource, e.retried, e.refreshing = track, resource, false, false
	e.snapshot = music.PlaybackSnapshot{State:music.PlaybackLoading, Track:&track}
	err := e.commandLocked("load", 0, 0)
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
	e.mu.Lock(); defer e.mu.Unlock()
	if e.snapshot.State == music.PlaybackIdle { return nil }
	err := e.commandLocked("stop", 0, 0)
	e.snapshot.State = music.PlaybackStopped
	e.snapshot.PositionMS = 0
	return err
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
		e.snapshot.State = music.PlaybackLoading
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
			emitErr := e.commandLocked("load", 0, 0)
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
