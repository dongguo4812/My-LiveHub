package music

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/dop251/goja"
)

// LXUserSourceSession mirrors the lifecycle of the desktop userApi window:
// one selected script is initialized once, keeps its JavaScript state, and
// receives request events until it is closed or replaced.
type LXUserSourceSession struct {
	requests chan lxSessionRequest
	closeCh chan struct{}
	done chan struct{}
	ready chan lxSessionReady
	closeOnce sync.Once
	mu sync.RWMutex
	vm *goja.Runtime
	result LXSourceRuntimeResult
}

type lxSessionRequest struct {
	payload json.RawMessage
	result chan lxSessionResponse
}

type lxSessionResponse struct {
	value json.RawMessage
	err error
}

type lxSessionReady struct {
	result LXSourceRuntimeResult
	err error
}

func StartLXUserSourceSession(ctx context.Context, script string, requestClient *http.Client) (*LXUserSourceSession, error) {
	if ctx == nil { ctx = context.Background() }
	if strings.TrimSpace(script) == "" || len(script) > MaxLXUserSourceBytes {
		return nil, errors.New("LX 音源脚本为空或超过 1 MB 限制")
	}
	session := &LXUserSourceSession{
		requests: make(chan lxSessionRequest),
		closeCh: make(chan struct{}),
		done: make(chan struct{}),
		ready: make(chan lxSessionReady, 1),
	}
	go session.run(script, requestClient)
	select {
	case ready := <-session.ready:
		if ready.err != nil {
			_ = session.Close()
			return nil, ready.err
		}
		session.mu.Lock()
		session.result = ready.result
		session.mu.Unlock()
		return session, nil
	case <-ctx.Done():
		_ = session.Close()
		return nil, ctx.Err()
	case <-session.done:
		return nil, errors.New("LX 音源运行实例已退出")
	}
}

func (s *LXUserSourceSession) Snapshot() LXSourceRuntimeResult {
	if s == nil { return LXSourceRuntimeResult{} }
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.result
}

func (s *LXUserSourceSession) Request(ctx context.Context, payload json.RawMessage) (json.RawMessage, error) {
	if s == nil { return nil, errors.New("LX 音源运行实例不可用") }
	if ctx == nil { ctx = context.Background() }
	if len(payload) == 0 || len(payload) > 64<<10 {
		return nil, errors.New("LX 音源请求数据为空或超过 64 KB 限制")
	}
	call := lxSessionRequest{payload: append(json.RawMessage(nil), payload...), result: make(chan lxSessionResponse, 1)}
	select {
	case <-s.done:
		return nil, errors.New("LX 音源运行实例已关闭")
	case <-ctx.Done():
		return nil, ctx.Err()
	case s.requests <- call:
	}
	select {
	case result := <-call.result:
		return result.value, result.err
	case <-s.done:
		return nil, errors.New("LX 音源运行实例已关闭")
	case <-ctx.Done():
		// 洛雪宿主超时后会移除 requestKey 对应的等待项；脚本本身继续运行，
		// 迟到的结果会被丢弃。这里用缓冲结果通道实现相同行为。
		return nil, ctx.Err()
	}
}

func (s *LXUserSourceSession) Close() error {
	if s == nil { return nil }
	s.closeOnce.Do(func() {
		close(s.closeCh)
		s.mu.RLock()
		vm := s.vm
		s.mu.RUnlock()
		if vm != nil { vm.Interrupt("LX source session closed") }
	})
	<-s.done
	return nil
}

func (s *LXUserSourceSession) run(script string, requestClient *http.Client) {
	defer close(s.done)
	vm := goja.New()
	vm.SetMaxCallStackSize(256)
	s.mu.Lock()
	s.vm = vm
	s.mu.Unlock()
	select {
	case <-s.closeCh:
		s.ready <- lxSessionReady{err: errors.New("LX 音源运行实例已关闭")}
		return
	default:
	}

	var initialized json.RawMessage
	var requestHandler goja.Value
	updateAlertSent := false
	lx := vm.NewObject()
	eventNames := vm.NewObject()
	_ = eventNames.Set("inited", "inited")
	_ = eventNames.Set("request", "request")
	_ = eventNames.Set("updateAlert", "updateAlert")
	_ = lx.Set("EVENT_NAMES", eventNames)
	_ = lx.Set("env", "desktop")
	_ = lx.Set("version", "2.0.0")
	if source, err := ParseLXUserSource(script); err == nil {
		currentInfo := vm.NewObject()
		_ = currentInfo.Set("name", source.Metadata.Name)
		_ = currentInfo.Set("description", source.Metadata.Description)
		_ = currentInfo.Set("author", source.Metadata.Author)
		_ = currentInfo.Set("homepage", source.Metadata.Homepage)
		_ = currentInfo.Set("version", source.Metadata.Version)
		_ = currentInfo.Set("rawScript", script)
		_ = lx.Set("currentScriptInfo", currentInfo)
	}
	console := vm.NewObject()
	for _, method := range []string{"log", "info", "warn", "error", "debug", "group", "groupCollapsed", "groupEnd", "time", "timeEnd", "trace"} {
		_ = console.Set(method, func(goja.FunctionCall) goja.Value { return goja.Undefined() })
	}
	_ = vm.Set("console", console)
	loop := newLXRuntimeEventLoop(vm)
	defer loop.stop()
	_ = vm.Set("setTimeout", func(call goja.FunctionCall) goja.Value {
		delay := time.Duration(call.Argument(1).ToInteger()) * time.Millisecond
		if delay < 0 { delay = 0 }
		if delay > lxWorkerTimeout { delay = lxWorkerTimeout }
		return vm.ToValue(loop.setTimer(call.Argument(0), delay, false))
	})
	_ = vm.Set("clearTimeout", func(call goja.FunctionCall) goja.Value {
		loop.clearTimer(int(call.Argument(0).ToInteger()))
		return goja.Undefined()
	})
	_ = vm.Set("setInterval", func(call goja.FunctionCall) goja.Value {
		delay := time.Duration(call.Argument(1).ToInteger()) * time.Millisecond
		if delay < time.Millisecond { delay = time.Millisecond }
		if delay > lxWorkerTimeout { delay = lxWorkerTimeout }
		return vm.ToValue(loop.setTimer(call.Argument(0), delay, true))
	})
	_ = vm.Set("clearInterval", func(call goja.FunctionCall) goja.Value {
		loop.clearTimer(int(call.Argument(0).ToInteger()))
		return goja.Undefined()
	})
	if err := installLXSourceUtilities(vm, lx); err != nil {
		s.ready <- lxSessionReady{err: fmt.Errorf("初始化 LX 工具函数失败：%w", err)}
		return
	}
	_ = lx.Set("send", func(call goja.FunctionCall) goja.Value {
		promise, resolve, reject := vm.NewPromise()
		event := call.Argument(0).String()
		switch event {
		case "inited":
			if len(initialized) > 0 {
				_ = reject(vm.NewGoError(errors.New("Script is inited")))
				return vm.ToValue(promise)
			}
			encoded, err := json.Marshal(call.Argument(1).Export())
			if err != nil {
				_ = reject(vm.NewGoError(err))
				return vm.ToValue(promise)
			}
			initialized = encoded
			_ = resolve(goja.Undefined())
		case "updateAlert":
			if updateAlertSent {
				_ = reject(vm.NewGoError(errors.New("The update alert can only be called once.")))
				return vm.ToValue(promise)
			}
			updateAlertSent = true
			data, ok := call.Argument(1).Export().(map[string]interface{})
			if !ok || strings.TrimSpace(fmt.Sprint(data["log"])) == "" || data["log"] == nil {
				_ = reject(vm.NewGoError(errors.New("log is required.")))
				return vm.ToValue(promise)
			}
			_ = resolve(goja.Undefined())
		default:
			_ = reject(vm.NewGoError(fmt.Errorf("The event is not supported: %s", event)))
		}
		return vm.ToValue(promise)
	})
	_ = lx.Set("on", func(call goja.FunctionCall) goja.Value {
		promise, resolve, reject := vm.NewPromise()
		event := call.Argument(0).String()
		if event != "request" {
			_ = reject(vm.NewGoError(fmt.Errorf("The event is not supported: %s", event)))
			return vm.ToValue(promise)
		}
		if _, ok := goja.AssertFunction(call.Argument(1)); !ok {
			_ = reject(vm.NewGoError(errors.New("LX request handler must be a function")))
			return vm.ToValue(promise)
		}
		requestHandler = call.Argument(1)
		_ = resolve(goja.Undefined())
		return vm.ToValue(promise)
	})
	if err := installLXRequestAsync(vm, lx, requestClient, loop, nil); err != nil {
		s.ready <- lxSessionReady{err: fmt.Errorf("初始化 LX request 请求桥接失败：%w", err)}
		return
	}
	if err := vm.Set("lx", lx); err != nil {
		s.ready <- lxSessionReady{err: fmt.Errorf("初始化 LX 音源运行环境失败：%w", err)}
		return
	}
	executionTimeout := time.AfterFunc(30*time.Second, func() { vm.Interrupt("LX source initialization timeout") })
	_, scriptErr := vm.RunString(script)
	executionTimeout.Stop()
	if scriptErr != nil {
		s.ready <- lxSessionReady{err: fmt.Errorf("LX 音源脚本初始化失败：%s", scriptErr.Error())}
		return
	}
	if len(initialized) == 0 {
		if err := s.pumpUntil(loop, vm, func() bool { return len(initialized) > 0 }, lxRequestTimeout+5*time.Second); err != nil {
			s.ready <- lxSessionReady{err: fmt.Errorf("LX 音源未发送 inited 初始化事件：%w", err)}
			return
		}
	}
	normalized, platforms, declared, err := filterLXSourceDeclaration(initialized)
	if err != nil {
		s.ready <- lxSessionReady{err: err}
		return
	}
	handlers := []string{}
	if requestHandler != nil && !goja.IsUndefined(requestHandler) && !goja.IsNull(requestHandler) { handlers = append(handlers, "request") }
	s.ready <- lxSessionReady{result: LXSourceRuntimeResult{
		Initialized: true, Platforms: platforms, DeclaredSources: declared,
		Sources: normalized, Handlers: handlers,
	}}

	for {
		select {
		case <-s.closeCh:
			return
		case call := <-s.requests:
			s.handleRequest(vm, lx, requestHandler, declared, call)
			_, _ = vm.RunString("void 0")
		case task := <-loop.tasks:
			if task != nil { task() }
			_, _ = vm.RunString("void 0")
		}
	}
}

func (s *LXUserSourceSession) pumpUntil(loop *lxRuntimeEventLoop, vm *goja.Runtime, done func() bool, timeout time.Duration) error {
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	for !done() {
		select {
		case <-s.closeCh:
			return errors.New("LX 音源运行实例已关闭")
		case task := <-loop.tasks:
			if task != nil { task() }
			if _, err := vm.RunString("void 0"); err != nil { return err }
		case <-deadline.C:
			return errors.New("LX 音源异步操作超时")
		}
	}
	return nil
}

func (s *LXUserSourceSession) handleRequest(vm *goja.Runtime, lx *goja.Object, handler goja.Value, declared []LXDeclaredSource, call lxSessionRequest) {
	var payload map[string]any
	if err := json.Unmarshal(call.payload, &payload); err != nil {
		call.result <- lxSessionResponse{err: errors.New("LX 音源请求数据格式无效")}
		return
	}
	source, _ := payload["source"].(string)
	action, _ := payload["action"].(string)
	var selected *LXDeclaredSource
	for i := range declared {
		if declared[i].Key == source { selected = &declared[i]; break }
	}
	if selected == nil {
		call.result <- lxSessionResponse{err: fmt.Errorf("LX 音源未声明来源 %q", source)}
		return
	}
	if !containsLXValue(selected.Actions, action) {
		call.result <- lxSessionResponse{err: fmt.Errorf("LX 音源 %q 不支持动作 %q", source, action)}
		return
	}
	if goja.IsUndefined(handler) || goja.IsNull(handler) {
		call.result <- lxSessionResponse{err: errors.New("Request event is not defined")}
		return
	}
	callable, ok := goja.AssertFunction(handler)
	if !ok {
		call.result <- lxSessionResponse{err: errors.New("LX request handler must be a function")}
		return
	}
	executionTimeout := time.AfterFunc(30*time.Second, func() { vm.Interrupt("LX source request execution timeout") })
	value, err := callable(lx, vm.ToValue(payload))
	executionTimeout.Stop()
	if err != nil {
		call.result <- lxSessionResponse{err: err}
		return
	}
	if goja.IsUndefined(value) || goja.IsNull(value) {
		call.result <- lxSessionResponse{err: errors.New("LX request handler must return a Promise")}
		return
	}
	promiseObject := value.ToObject(vm)
	then, ok := goja.AssertFunction(promiseObject.Get("then"))
	if !ok {
		call.result <- lxSessionResponse{err: errors.New("LX request handler must return a Promise")}
		return
	}
	var once sync.Once
	finish := func(result lxSessionResponse) {
		once.Do(func() { call.result <- result })
	}
	onFulfilled := vm.ToValue(func(callArgs goja.FunctionCall) goja.Value {
		encoded, err := marshalLXUserAPIResult(source, action, payload, callArgs.Argument(0))
		finish(lxSessionResponse{value: encoded, err: err})
		return goja.Undefined()
	})
	onRejected := vm.ToValue(func(callArgs goja.FunctionCall) goja.Value {
		message := callArgs.Argument(0).String()
		finish(lxSessionResponse{err: errors.New(message)})
		return goja.Undefined()
	})
	if _, err := then(promiseObject, onFulfilled, onRejected); err != nil {
		finish(lxSessionResponse{err: err})
	}
}

func marshalLXUserAPIResult(source, action string, payload map[string]any, value goja.Value) (json.RawMessage, error) {
	exported := value.Export()
	switch action {
	case "musicUrl":
		raw, ok := exported.(string)
		if !ok || len(raw) > 2048 || !(strings.HasPrefix(raw, "http:") || strings.HasPrefix(raw, "https:")) {
			return nil, errors.New("LX musicUrl response is invalid")
		}
		info, _ := payload["info"].(map[string]any)
		data := map[string]any{"url": raw}
		if quality, exists := info["type"]; exists { data["type"] = quality }
		return json.Marshal(map[string]any{"source": source, "action": action, "data": data})
	case "pic":
		raw, ok := exported.(string)
		if !ok || len(raw) > 2048 || !(strings.HasPrefix(raw, "http:") || strings.HasPrefix(raw, "https:")) {
			return nil, errors.New("LX pic response is invalid")
		}
		return json.Marshal(map[string]any{"source": source, "action": action, "data": raw})
	case "lyric":
		info, ok := exported.(map[string]interface{})
		if !ok { return nil, errors.New("LX lyric response is invalid") }
		lyric, ok := info["lyric"].(string)
		if !ok || len(lyric) > 51200 { return nil, errors.New("LX lyric response is invalid") }
		data := map[string]any{"lyric": lyric, "tlyric": nil, "rlyric": nil, "lxlyric": nil}
		if value, ok := info["tlyric"].(string); ok && len(value) < 5120 { data["tlyric"] = value }
		if value, ok := info["rlyric"].(string); ok && len(value) < 5120 { data["rlyric"] = value }
		if value, ok := info["lxlyric"].(string); ok && len(value) < 8192 { data["lxlyric"] = value }
		return json.Marshal(map[string]any{"source": source, "action": action, "data": data})
	default:
		return json.Marshal(map[string]any{"source": source, "action": action})
	}
}
