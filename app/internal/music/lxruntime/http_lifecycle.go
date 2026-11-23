//go:build (lxquickjs || lxproduction) && cgo

package lxruntime

import (
	"context"
	"fmt"
	"net/http"
	"time"
)

type lxHTTPCompletion struct {
	id       uint64
	response *lxHTTPResponse
	err      error
}

// lxHTTPTasks runs network I/O away from the QuickJS owner thread. This lets
// QuickJS timers execute while a request is in flight, so a source can invoke
// the cancellation function returned by lx.request().
type lxHTTPTasks struct {
	client    *http.Client
	active    map[uint64]context.CancelFunc
	completed chan lxHTTPCompletion
}

func newLXHTTPTasks(client *http.Client) *lxHTTPTasks {
	if client == nil {
		client = http.DefaultClient
	}
	return &lxHTTPTasks{
		client: client,
		active: make(map[uint64]context.CancelFunc),
		completed: make(chan lxHTTPCompletion, 256),
	}
}

func (t *lxHTTPTasks) start(parent context.Context, requests []lxRequestDescriptor) error {
	for _, request := range requests {
		if _, exists := t.active[request.ID]; exists {
			return fmt.Errorf("lx.request: duplicate active request id %d", request.ID)
		}
		requestCtx, cancel := context.WithCancel(parent)
		t.active[request.ID] = cancel
		go func(request lxRequestDescriptor) {
			response, err := executeLXHTTPRequest(requestCtx, request, t.client)
			t.completed <- lxHTTPCompletion{id: request.ID, response: response, err: err}
		}(request)
	}
	return nil
}

func (t *lxHTTPTasks) abort(ids []uint64) {
	for _, id := range ids {
		if cancel := t.active[id]; cancel != nil {
			cancel()
		}
	}
}

func (t *lxHTTPTasks) finish(ctx context.Context, runtime *Runtime, result lxHTTPCompletion) error {
	if cancel := t.active[result.id]; cancel != nil {
		delete(t.active, result.id)
		cancel()
	}
	return runtime.completeLXRequest(ctx, result.id, result.response, result.err)
}

func (t *lxHTTPTasks) cancelAll() {
	for id, cancel := range t.active {
		cancel()
		delete(t.active, id)
	}
}

func (t *lxHTTPTasks) wait(ctx context.Context, nextTimerDelay int64) (*lxHTTPCompletion, error) {
	if nextTimerDelay < 0 {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case result := <-t.completed:
			return &result, nil
		}
	}
	wait := time.Duration(nextTimerDelay) * time.Millisecond
	if wait > 10*time.Millisecond {
		wait = 10 * time.Millisecond
	}
	if wait < time.Millisecond {
		wait = time.Millisecond
	}
	timer := time.NewTimer(wait)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case result := <-t.completed:
		return &result, nil
	case <-timer.C:
		return nil, nil
	}
}
