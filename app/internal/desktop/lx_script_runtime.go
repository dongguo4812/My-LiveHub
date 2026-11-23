package desktop

import (
	"context"

	"github.com/dongguo4812/My-LiveHub/app/internal/music"
)

type lxScriptRuntime interface {
	Start(context.Context) error
	ResolvePlaybackURL(context.Context, string, any) (string, error)
	Close() error
}

var _ music.PlaybackEngineLifecycle = (*LXPlaybackEngine)(nil)
