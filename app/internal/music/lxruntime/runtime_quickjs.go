//go:build lxquickjs && cgo

package lxruntime

/*
#cgo CFLAGS: -I${SRCDIR}/poc/native/include
#include <stdlib.h>
#include "lxqjs.h"
*/
import "C"

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"strings"
	"sync"
	"time"
	"unsafe"
)

const (
	defaultMemoryLimit = 32 << 20
	defaultStackLimit  = 512 << 10
	defaultEvalTimeout = 3 * time.Second
)

type Options struct {
	MemoryLimitBytes int
	StackLimitBytes  int
	EvalTimeout      time.Duration
}

type evalResult struct {
	value string
	err   error
}

type command struct {
	ctx      context.Context
	source   string
	filename string
	result   chan evalResult
	close    bool
	installCrypto bool
}

type Runtime struct {
	commands chan command
	done     chan struct{}
	close    sync.Once
	lifecycle context.Context
	shutdown context.CancelFunc
}

func New(opts Options) (*Runtime, error) {
	if opts.MemoryLimitBytes <= 0 {
		opts.MemoryLimitBytes = defaultMemoryLimit
	}
	if opts.StackLimitBytes <= 0 {
		opts.StackLimitBytes = defaultStackLimit
	}
	if opts.EvalTimeout <= 0 {
		opts.EvalTimeout = defaultEvalTimeout
	}

	lifecycle, shutdown := context.WithCancel(context.Background())
	r := &Runtime{
		commands: make(chan command),
		done:     make(chan struct{}),
		lifecycle: lifecycle,
		shutdown: shutdown,
	}
	ready := make(chan error, 1)
	go r.loop(opts, ready)
	if err := <-ready; err != nil {
		return nil, err
	}
	return r, nil
}

func (r *Runtime) loop(opts Options, ready chan<- error) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	defer close(r.done)

	var cError *C.char
	state := C.lxqjs_new(C.size_t(opts.MemoryLimitBytes), C.size_t(opts.StackLimitBytes), &cError)
	if state == nil {
		ready <- takeError(cError, "QuickJS runtime initialization failed")
		return
	}
	defer C.lxqjs_free(state)
	ready <- nil

	for cmd := range r.commands {
		if cmd.close {
			return
		}
		if cmd.installCrypto {
			var installError *C.char
			if C.lxqjs_install_crypto(state, &installError) != 0 {
				cmd.result <- evalResult{err: takeError(installError, "QuickJS crypto installation failed")}
			} else {
				cmd.result <- evalResult{}
			}
			continue
		}
		if err := cmd.ctx.Err(); err != nil {
			cmd.result <- evalResult{err: err}
			continue
		}

		timeout := opts.EvalTimeout
		if deadline, ok := cmd.ctx.Deadline(); ok {
			if remaining := time.Until(deadline); remaining < timeout {
				timeout = remaining
			}
		}
		if timeout <= 0 {
			cmd.result <- evalResult{err: context.DeadlineExceeded}
			continue
		}

		C.lxqjs_set_interrupted(state, 0)
		stopWatch := make(chan struct{})
		watchDone := make(chan struct{})
		go func() {
			defer close(watchDone)
			select {
			case <-cmd.ctx.Done():
				C.lxqjs_set_interrupted(state, 1)
			case <-stopWatch:
			}
		}()

		timerDone := make(chan struct{})
		timer := time.AfterFunc(timeout, func() {
			C.lxqjs_set_interrupted(state, 1)
			close(timerDone)
		})

		cSource := C.CString(cmd.source)
		cFilename := C.CString(cmd.filename)
		var cEvalError *C.char
		value := C.lxqjs_eval(state, cSource, cFilename, &cEvalError)
		C.free(unsafe.Pointer(cSource))
		C.free(unsafe.Pointer(cFilename))

		close(stopWatch)
		<-watchDone
		if !timer.Stop() {
			<-timerDone
		}
		interrupted := cmd.ctx.Err()
		C.lxqjs_set_interrupted(state, 0)

		if value == nil {
			err := takeError(cEvalError, "JavaScript evaluation failed")
			if interrupted != nil {
				err = interrupted
			} else if strings.Contains(strings.ToLower(err.Error()), "interrupted") {
				err = fmt.Errorf("JavaScript execution exceeded %s: %w", timeout, err)
			}
			cmd.result <- evalResult{err: err}
			continue
		}
		resultText := C.GoString(value)
		C.lxqjs_string_free(value)
		cmd.result <- evalResult{value: resultText}
	}
}

func takeError(value *C.char, fallback string) error {
	if value == nil {
		return errors.New(fallback)
	}
	defer C.lxqjs_string_free(value)
	return errors.New(C.GoString(value))
}

func (r *Runtime) Eval(ctx context.Context, source, filename string) (string, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	evalCtx, cancelEval := context.WithCancel(ctx)
	stopShutdown := context.AfterFunc(r.lifecycle, cancelEval)
	defer func() {
		stopShutdown()
		cancelEval()
	}()
	result := make(chan evalResult, 1)
	cmd := command{ctx: evalCtx, source: source, filename: filename, result: result}
	select {
	case <-r.done:
		return "", errors.New("QuickJS runtime is closed")
	case <-ctx.Done():
		return "", ctx.Err()
	case r.commands <- cmd:
	}
	select {
	case <-ctx.Done():
		return "", ctx.Err()
	case <-r.done:
		return "", errors.New("QuickJS runtime is closed")
	case value := <-result:
		return value.value, value.err
	}
}

func (r *Runtime) Close() error {
	if r == nil {
		return nil
	}
	r.close.Do(func() {
		if r.shutdown != nil {
			r.shutdown()
		}
		select {
		case <-r.done:
		case r.commands <- command{close: true}:
			<-r.done
		}
	})
	return nil
}


// installLXCryptoNative installs synchronous native functions while executing
// on the QuickJS owner goroutine. It must not be called from a C callback.
func (r *Runtime) installLXCryptoNative(ctx context.Context) error {
	if ctx == nil { ctx = context.Background() }
	result := make(chan evalResult, 1)
	cmd := command{ctx: ctx, installCrypto: true, result: result}
	select {
	case <-r.done: return errors.New("QuickJS runtime is closed")
	case <-ctx.Done(): return ctx.Err()
	case r.commands <- cmd:
	}
	select {
	case <-ctx.Done(): return ctx.Err()
	case <-r.done: return errors.New("QuickJS runtime is closed")
	case value := <-result: return value.err
	}
}
