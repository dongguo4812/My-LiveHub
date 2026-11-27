//go:build lxquickjs || lxproduction

package lxruntime

import (
	"context"
	"errors"
	"fmt"
	"os"
	"net/http"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
	"unsafe"

	"github.com/ebitengine/purego"
)

const (
	defaultMemoryLimit = 32 << 20
	defaultStackLimit = 512 << 10
	defaultEvalTimeout = 3 * time.Second
	maxInitializationEvalTimeout = 60 * time.Second
)

type Options struct {
	MemoryLimitBytes int
	StackLimitBytes int
	EvalTimeout time.Duration
}
type evalResult struct { value string; err error }
type command struct {
	ctx context.Context
	evalTimeout time.Duration
	source, filename string
	result chan evalResult
	close bool
	installCrypto bool
}
type Runtime struct {
	commands chan command
	done chan struct{}
	close sync.Once
	dispatchMu sync.Mutex
	initializeMu sync.Mutex
	httpClientMu sync.Mutex
	safeHTTPClients map[lxSafeHTTPClientKey]*http.Client
	lifecycle context.Context
	shutdown context.CancelFunc
	allowPrivateNetworkForTests bool
	api *quickJSAPI
}

type quickJSAPI struct {
	newRuntime func(uintptr, uintptr, *unsafe.Pointer) unsafe.Pointer
	freeRuntime func(unsafe.Pointer)
	setInterrupted func(unsafe.Pointer, int32)
	evalTimed func(unsafe.Pointer, unsafe.Pointer, unsafe.Pointer, *unsafe.Pointer, *float64, *float64, *float64) unsafe.Pointer
	evalLegacy func(unsafe.Pointer, unsafe.Pointer, unsafe.Pointer, *unsafe.Pointer) unsafe.Pointer
	installCrypto func(unsafe.Pointer, *unsafe.Pointer) int32
	freeString func(unsafe.Pointer)
	alloc func(uintptr) unsafe.Pointer
	dealloc func(unsafe.Pointer)
	setCallbacks func(uintptr, uintptr, uintptr, uintptr, uintptr, uintptr, uintptr)
}

var (
	quickJSLoadOnce sync.Once
	quickJSLoaded *quickJSAPI
	quickJSLoadErr error
)

func loadQuickJSAPI() (*quickJSAPI, error) {
	quickJSLoadOnce.Do(func() {
		quickJSLoaded, quickJSLoadErr = openQuickJSAPI()
	})
	return quickJSLoaded, quickJSLoadErr
}

func openQuickJSAPI() (*quickJSAPI, error) {
	path, err := findQuickJSLibrary()
	if err != nil { return nil, err }
	lib, err := openNativeLibrary(path)
	if err != nil { return nil, fmt.Errorf("load QuickJS native library %q: %w", path, err) }
	api := &quickJSAPI{}
	bind := func(target any, symbol string) error { return lib.bind(target, symbol) }
	for _, item := range []struct{ target any; name string }{
		{&api.newRuntime, "lxqjs_new"}, {&api.freeRuntime, "lxqjs_free"},
		{&api.setInterrupted, "lxqjs_set_interrupted"},
		{&api.installCrypto, "lxqjs_install_crypto"}, {&api.freeString, "lxqjs_string_free"},
		{&api.alloc, "lxqjs_alloc"}, {&api.dealloc, "lxqjs_dealloc"},
		{&api.setCallbacks, "lxqjs_set_host_callbacks"},
	} {
		if err := bind(item.target, item.name); err != nil { return nil, fmt.Errorf("resolve QuickJS symbol %s: %w", item.name, err) }
	}
	if err := bind(&api.evalTimed, "lxqjs_eval_timed"); err != nil {
		if legacyErr := bind(&api.evalLegacy, "lxqjs_eval"); legacyErr != nil {
			return nil, fmt.Errorf("resolve QuickJS eval symbols: timed API unavailable (%v), legacy API unavailable (%w)", err, legacyErr)
		}
	}
	setNativeAllocator(api.alloc)
	api.setCallbacks(
		purego.NewCallback(lxqjsGoMD5),
		purego.NewCallback(lxqjsGoRandomBytes),
		purego.NewCallback(lxqjsGoBufferFrom),
		purego.NewCallback(lxqjsGoBufferToString),
		purego.NewCallback(lxqjsGoRSAEncrypt),
		purego.NewCallback(lxqjsGoAESEncrypt),
		purego.NewCallback(lxqjsGoZlib),
	)
	return api, nil
}

func findQuickJSLibrary() (string, error) {
	if configured := strings.TrimSpace(os.Getenv("MYLIVEHUB_QUICKJS_LIB")); configured != "" {
		if _, err := os.Stat(configured); err == nil { return configured, nil }
		return "", fmt.Errorf("MYLIVEHUB_QUICKJS_LIB points to a missing file: %s", configured)
	}
	name := "liblxqjs.so"
	switch runtime.GOOS {
	case "windows": name = "lxqjs.dll"
	case "darwin": name = "liblxqjs.dylib"
	}
	executable, _ := os.Executable()
	exeDir := filepath.Dir(executable)
	cwd, _ := os.Getwd()
	candidates := []string{
		filepath.Join(exeDir, name),
		filepath.Join(exeDir, "bin", name),
		filepath.Join(cwd, name),
		filepath.Join(cwd, "bin", name),
		filepath.Join(cwd, "internal", "music", "lxruntime", "native", name),
	}
	for _, candidate := range candidates {
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() { return candidate, nil }
	}
	return "", fmt.Errorf("QuickJS native runtime %s is missing; run task dev/build to provision the pinned runtime", name)
}

func New(opts Options) (*Runtime, error) {
	if opts.MemoryLimitBytes <= 0 { opts.MemoryLimitBytes = defaultMemoryLimit }
	if opts.StackLimitBytes <= 0 { opts.StackLimitBytes = defaultStackLimit }
	if opts.EvalTimeout <= 0 { opts.EvalTimeout = defaultEvalTimeout }
	api, err := loadQuickJSAPI()
	if err != nil { return nil, err }
	lifecycle, shutdown := context.WithCancel(context.Background())
	r := &Runtime{commands: make(chan command), done: make(chan struct{}), lifecycle: lifecycle, shutdown: shutdown, api: api}
	ready := make(chan error, 1)
	go r.loop(opts, ready)
	if err := <-ready; err != nil { return nil, err }
	return r, nil
}

func (r *Runtime) loop(opts Options, ready chan<- error) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	defer close(r.done)
	var nativeError unsafe.Pointer
	state := r.api.newRuntime(uintptr(opts.MemoryLimitBytes), uintptr(opts.StackLimitBytes), &nativeError)
	if state == nil { ready <- r.takeError(nativeError, "QuickJS runtime initialization failed"); return }
	defer r.api.freeRuntime(state)
	ready <- nil
	for cmd := range r.commands {
		if cmd.close { return }
		if cmd.installCrypto {
			var installError unsafe.Pointer
			if r.api.installCrypto(state, &installError) != 0 {
				cmd.result <- evalResult{err:r.takeError(installError, "QuickJS crypto installation failed")}
			} else { cmd.result <- evalResult{} }
			continue
		}
		if err := cmd.ctx.Err(); err != nil { cmd.result <- evalResult{err:err}; continue }
		timeout := opts.EvalTimeout
		if cmd.evalTimeout > 0 { timeout = cmd.evalTimeout }
		if deadline, ok := cmd.ctx.Deadline(); ok { if remaining := time.Until(deadline); remaining < timeout { timeout = remaining } }
		if timeout <= 0 { cmd.result <- evalResult{err:context.DeadlineExceeded}; continue }
		r.api.setInterrupted(state, 0)
		stopWatch := make(chan struct{})
		watchDone := make(chan struct{})
		go func() {
			defer close(watchDone)
			select { case <-cmd.ctx.Done(): r.api.setInterrupted(state, 1); case <-stopWatch: }
		}()
		timerDone := make(chan struct{})
		timer := time.AfterFunc(timeout, func() { r.api.setInterrupted(state, 1); close(timerDone) })
		sourcePtr := r.allocCString(cmd.source)
		filenamePtr := r.allocCString(cmd.filename)
		var evalError unsafe.Pointer
		var compileMS, executeMS, jobsMS float64
		var value unsafe.Pointer
		if r.api.evalTimed != nil {
			value = r.api.evalTimed(state, sourcePtr, filenamePtr, &evalError, &compileMS, &executeMS, &jobsMS)
		} else {
			value = r.api.evalLegacy(state, sourcePtr, filenamePtr, &evalError)
		}
		r.api.dealloc(sourcePtr)
		r.api.dealloc(filenamePtr)
		close(stopWatch)
		<-watchDone
		if !timer.Stop() { <-timerDone }
		interrupted := cmd.ctx.Err()
		r.api.setInterrupted(state, 0)
		if value == nil {
			err := r.takeError(evalError, "JavaScript evaluation failed")
			if interrupted != nil { err = interrupted } else if strings.Contains(strings.ToLower(err.Error()), "interrupted") { err = fmt.Errorf("JavaScript execution exceeded %s: %w", timeout, err) }
			cmd.result <- evalResult{err:err}
			continue
		}
		resultText := nativeCString(value, 8<<20)
		r.api.freeString(value)
		cmd.result <- evalResult{value:resultText}
	}
}

func (r *Runtime) allocCString(value string) unsafe.Pointer {
	ptr := r.api.alloc(uintptr(len(value)+1))
	if ptr == nil { return nil }
	dst := unsafe.Slice((*byte)(ptr), len(value)+1)
	copy(dst, value); dst[len(value)] = 0
	return ptr
}
func (r *Runtime) takeError(ptr unsafe.Pointer, fallback string) error {
	if ptr == nil { return errors.New(fallback) }
	value := nativeCString(ptr, 1<<20)
	r.api.freeString(ptr)
	return errors.New(value)
}
func nativeCString(ptr unsafe.Pointer, limit int) string {
	if ptr == nil { return "" }
	data := make([]byte, 0, 256)
	for i := 0; i < limit; i++ {
		b := *(*byte)(unsafe.Add(ptr, uintptr(i)))
		if b == 0 { return string(data) }
		data = append(data, b)
	}
	return string(data)
}

func (r *Runtime) Eval(ctx context.Context, source, filename string) (string, error) {
	return r.EvalWithTimeout(ctx, source, filename, 0)
}

// EvalWithTimeout overrides the runtime's ordinary per-evaluation watchdog for
// one bounded operation. The caller context remains the hard upper bound.
func (r *Runtime) EvalWithTimeout(ctx context.Context, source, filename string, timeout time.Duration) (string, error) {
	if r == nil { return "", errors.New("QuickJS runtime is nil") }
	if ctx == nil { ctx = context.Background() }
	evalCtx, cancelEval := context.WithCancel(ctx)
	stopShutdown := context.AfterFunc(r.lifecycle, cancelEval)
	defer func(){ stopShutdown(); cancelEval() }()
	result := make(chan evalResult, 1)
	cmd := command{ctx:evalCtx, evalTimeout:timeout, source:source, filename:filename, result:result}
	select {
	case <-r.done: return "", errors.New("QuickJS runtime is closed")
	case <-ctx.Done(): return "", ctx.Err()
	case r.commands <- cmd:
	}
	select {
	case <-ctx.Done(): return "", ctx.Err()
	case <-r.done: return "", errors.New("QuickJS runtime is closed")
	case value := <-result:
		return value.value, value.err
	}
}
func (r *Runtime) Close() error {
	if r == nil { return nil }
	r.close.Do(func() {
		if r.shutdown != nil { r.shutdown() }
		select { case <-r.done: case r.commands <- command{close:true}: <-r.done }
		r.closeSafeHTTPClients()
	})
	return nil
}
func (r *Runtime) installLXCryptoNative(ctx context.Context) error {
	if ctx == nil { ctx = context.Background() }
	result := make(chan evalResult, 1)
	cmd := command{ctx:ctx, installCrypto:true, result:result}
	select { case <-r.done: return errors.New("QuickJS runtime is closed"); case <-ctx.Done(): return ctx.Err(); case r.commands <- cmd: }
	select { case <-ctx.Done(): return ctx.Err(); case <-r.done: return errors.New("QuickJS runtime is closed"); case value := <-result: return value.err }
}
