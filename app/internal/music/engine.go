package music

import (
	"context"
	"errors"
)

type PlaybackState string

const (
	PlaybackIdle PlaybackState = "idle"
	PlaybackLoading PlaybackState = "loading"
	PlaybackPlaying PlaybackState = "playing"
	PlaybackPaused PlaybackState = "paused"
	PlaybackStopped PlaybackState = "stopped"
	PlaybackError PlaybackState = "error"
)

type PlaybackSnapshot struct {
	State PlaybackState `json:"state"`
	Track *Track `json:"track,omitempty"`
	PositionMS int64 `json:"position_ms"`
	DurationMS int64 `json:"duration_ms"`
	Volume int `json:"volume"`
	Error string `json:"error,omitempty"`
}

type PlaybackEngine interface {
	Load(context.Context, PlaybackResource, Track) error
	Pause(context.Context) error
	Resume(context.Context) error
	Seek(context.Context, int64) error
	SetVolume(context.Context, int) error
	Stop(context.Context) error
	Snapshot(context.Context) (PlaybackSnapshot, error)
}

var ErrPlaybackEngineUnavailable = errors.New("music playback engine is unavailable")
