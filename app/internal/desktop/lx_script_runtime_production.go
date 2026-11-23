//go:build lxproduction && cgo

package desktop

import (
	"context"
	"errors"
	"sync"

	"github.com/dongguo4812/My-LiveHub/app/internal/music/lxruntime"
)

type productionMusicLXRuntime struct {
	mu sync.Mutex
	manager *lxruntime.SourceManager
}

func newLXScriptRuntime() lxScriptRuntime {
	return &productionMusicLXRuntime{}
}

func (r *productionMusicLXRuntime) Start(ctx context.Context) error {
	if r == nil {
		return errors.New("production LX source runtime is unavailable")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.manager == nil {
		r.manager = lxruntime.NewSourceManager(nil)
	}
	return r.manager.Start(ctx)
}

func (r *productionMusicLXRuntime) ResolvePlaybackURL(ctx context.Context, source string, info any) (string, error) {
	if r == nil {
		return "", errors.New("production LX source runtime is unavailable")
	}
	r.mu.Lock()
	manager := r.manager
	r.mu.Unlock()
	if manager == nil {
		return "", errors.New("production LX source runtime has not started")
	}
	value, err := manager.ResolvePlaybackURL(ctx, source, info)
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
	if r == nil {
		return nil
	}
	r.mu.Lock()
	manager := r.manager
	r.manager = nil
	r.mu.Unlock()
	if manager == nil {
		return nil
	}
	return manager.Close()
}
