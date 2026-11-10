//go:build !windows

package music

import (
	"context"
	"errors"
)

type MpvEngine struct{}

func NewMpvEngine(string) *MpvEngine { return &MpvEngine{} }
func (*MpvEngine) Start(context.Context) error { return errors.New("mpv IPC engine is currently supported on Windows only") }
func (*MpvEngine) Load(context.Context, PlaybackResource, Track) error { return ErrPlaybackEngineUnavailable }
func (*MpvEngine) Pause(context.Context) error { return ErrPlaybackEngineUnavailable }
func (*MpvEngine) Resume(context.Context) error { return ErrPlaybackEngineUnavailable }
func (*MpvEngine) Seek(context.Context, int64) error { return ErrPlaybackEngineUnavailable }
func (*MpvEngine) SetVolume(context.Context, int) error { return ErrPlaybackEngineUnavailable }
func (*MpvEngine) Stop(context.Context) error { return ErrPlaybackEngineUnavailable }
func (*MpvEngine) Snapshot(context.Context) (PlaybackSnapshot, error) { return PlaybackSnapshot{}, ErrPlaybackEngineUnavailable }
func (*MpvEngine) Close() error { return nil }

var _ PlaybackEngine = (*MpvEngine)(nil)
