//go:build lxquickjs || lxproduction

package lxruntime

import (
	"context"
	"fmt"
	"net/http"
	"sync"
	"time"
)

type lxHTTPCompletion struct {
	id          uint64
	response    *lxHTTPResponse
	err         error
	completedAt time.Time
}

// lxHTTPTasks runs network I/O away from the QuickJS owner thread. This lets
// QuickJS timers execute while a request is in flight, so a source can invoke
// the cancellation function returned by lx.request().
type lxHTTPTasks struct {
	client    *http.Client
	active    map[uint64]context.CancelFunc
	completed chan lxHTTPCompletion
	wg        sync.WaitGroup
	allowPrivateNetwork bool
}

func newLXHTTPTasks(client *http.Client) *lxHTTPTasks {
	return newLXHTTPTasksWithPolicy(client, false)
}

func newLXHTTPTasksWithPolicy(client *http.Client, allowPrivate bool) *lxHTTPTasks {
	if client == nil {
		client = http.DefaultClient
	}
	return &lxHTTPTasks{
		client: client,
		active: make(map[uint64]context.CancelFunc),
		completed: make(chan lxHTTPCompletion, 256),
		allowPrivateNetwork: allowPrivate,
	}
}

func (t *lxHTTPTasks) start(parent context.Context, requests []lxRequestDescriptor) error {
	for _, request := range requests {
		if _, exists := t.active[request.ID]; exists {
			return fmt.Errorf("lx.request: duplicate active request id %d", request.ID)
		}
		requestCtx, cancel := context.WithCancel(parent)
		t.active[request.ID] = cancel
		t.wg.Add(1)
		go func(request lxRequestDescriptor, requestCtx context.Context) {
			defer t.wg.Done()
			response, err := executeLXHTTPRequestWithPolicy(requestCtx, request, t.client, t.allowPrivateNetwork)
			completedAt := time.Now()
			select {
			case t.completed <- lxHTTPCompletion{id: request.ID, response: response, err: err, completedAt: completedAt}:
			case <-requestCtx.Done():
				// An aborted request no longer needs to enqueue a completion.
			}
		}(request, requestCtx)
	}
	return nil
}

func (t *lxHTTPTasks) abort(ids []uint64) {
	for _, id := range ids {
		if cancel := t.active[id]; cancel != nil {
			cancel()
			delete(t.active, id)
		}
	}
}

func (t *lxHTTPTasks) finish(ctx context.Context, runtime *Runtime, result lxHTTPCompletion) error {
	if !result.completedAt.IsZero() {
		logLXTiming("http_callback_delivery_wait", result.completedAt, fmt.Sprintf("request_id=%d result=%s", result.id, timingResult(result.err)))
	}
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
	// Do not let a closed/expired script leave HTTP goroutines behind.
	t.wg.Wait()
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
