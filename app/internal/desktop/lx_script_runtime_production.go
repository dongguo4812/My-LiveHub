//go:build lxproduction && cgo

package desktop

import (
	"context"
	"errors"
	"net/http"

	"github.com/dongguo4812/My-LiveHub/app/internal/music"
	"github.com/dongguo4812/My-LiveHub/app/internal/music/lxruntime"
)

type productionMusicLXRuntime struct {
	manager *lxruntime.SourceManager
}

func newLXScriptRuntime() lxScriptRuntime {
	return &productionMusicLXRuntime{manager: lxruntime.NewSourceManager(&http.Client{})}
}

func (r *productionMusicLXRuntime) Start(ctx context.Context) error {
	if r == nil || r.manager == nil {
		return errors.New("production LX source runtime is unavailable")
	}
	return r.manager.Start(ctx)
}

func (r *productionMusicLXRuntime) ResolvePlaybackURL(ctx context.Context, source string, info any) (string, error) {
	if r == nil || r.manager == nil {
		return "", errors.New("production LX source runtime is unavailable")
	}
	value, err := r.manager.ResolvePlaybackURL(ctx, source, info)
	if err != nil {
		return "", err
	}
	url, ok := value.(string)
	if !ok || url == "" {
		return "", errors.New("LX source returned an invalid playback URL")
	}
	return url, nil
}

func (r *productionMusicLXRuntime) Close() error {
	if r == nil || r.manager == nil {
		return nil
	}
	return r.manager.Close()
}

var _ music.PlaybackEngineLifecycle = (*LXPlaybackEngine)(nil)
