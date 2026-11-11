package music

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/dop251/goja"
)

const (
	LXSourceWorkerArgument = "--lx-source-worker"
	lxWorkerArgument = LXSourceWorkerArgument
	lxWorkerInputLimit = MaxLXUserSourceBytes + 4096
	lxWorkerOutputLimit = 256 << 10
	lxWorkerTimeout = 180 * time.Second
	lxWorkerExecutionTimeout = 170 * time.Second
)

type LXDeclaredSource struct {
	Key string `json:"key"`
	Name string `json:"name,omitempty"`
	Type string `json:"type,omitempty"`
	Actions []string `json:"actions,omitempty"`
	Qualitys []string `json:"qualitys,omitempty"`
}

type LXSourceRuntimeResult struct {
	Initialized bool `json:"initialized"`
	Platforms []string `json:"platforms"`
	DeclaredSources []LXDeclaredSource `json:"declared_sources"`
	Sources json.RawMessage `json:"sources"`
	Handlers []string `json:"handlers"`
}

type lxWorkerRequest struct {
	Script string `json:"script"`
	Operation string `json:"operation,omitempty"`
	Payload json.RawMessage `json:"payload,omitempty"`
}

type lxWorkerResponse struct {
	Result LXSourceRuntimeResult `json:"result,omitempty"`
	Value json.RawMessage `json:"value,omitempty"`
	Error  string                `json:"error,omitempty"`
}

type lxRuntimeEventLoop struct {
	vm *goja.Runtime
	tasks chan func()
	mu sync.Mutex
	timers map[int]*time.Timer
	nextID int
}

func newLXRuntimeEventLoop(vm *goja.Runtime) *lxRuntimeEventLoop {
	return &lxRuntimeEventLoop{vm:vm, tasks:make(chan func(),256), timers:make(map[int]*time.Timer)}
}
func (loop *lxRuntimeEventLoop) enqueue(task func()) { loop.tasks <- task }
func (loop *lxRuntimeEventLoop) setTimer(callback goja.Value, delay time.Duration, repeat bool) int {
	fn, ok := goja.AssertFunction(callback); if !ok { return 0 }
	loop.mu.Lock(); loop.nextID++; id:=loop.nextID
	var timer *time.Timer
	var fire func(); fire=func() { loop.enqueue(func(){ if _,err:=fn(goja.Undefined()); err!=nil { loop.vm.Interrupt(err) } }); if repeat { loop.mu.Lock(); if _,exists:=loop.timers[id]; exists { loop.timers[id]=time.AfterFunc(delay,fire) }; loop.mu.Unlock() } else { loop.clearTimer(id) } }
	timer=time.AfterFunc(delay,fire); loop.timers[id]=timer; loop.mu.Unlock()
	return id
}
func (loop *lxRuntimeEventLoop) clearTimer(id int) {
	loop.mu.Lock(); if timer:=loop.timers[id]; timer!=nil { timer.Stop(); delete(loop.timers,id) }; loop.mu.Unlock()
}
func (loop *lxRuntimeEventLoop) stop() {
	loop.mu.Lock(); for id,timer:=range loop.timers { timer.Stop(); delete(loop.timers,id) }; loop.mu.Unlock()
}
func (loop *lxRuntimeEventLoop) pumpUntil(done func() bool, timeout time.Duration) error {
	deadline:=time.NewTimer(timeout); defer deadline.Stop()
	for !done() {
		select {
		case task:=<-loop.tasks:
			if task!=nil { task() }
			if _,err:=loop.vm.RunString("void 0"); err!=nil { return err }
		case <-deadline.C: return errors.New("LX 音源异步操作超时")
		}
	}
	return nil
}

// InspectLXUserSource runs an LX source in a short-lived copy of the desktop
// executable. The child has no Node.js globals or host filesystem/process APIs.
func InspectLXUserSource(parent context.Context, script string) (LXSourceRuntimeResult, error) {
	if parent == nil {
		parent = context.Background()
	}
	if strings.TrimSpace(script) == "" || len(script) > MaxLXUserSourceBytes {
		return LXSourceRuntimeResult{}, errors.New("LX 音源脚本为空或超过 1 MB 限制")
	}
	ctx, cancel := context.WithTimeout(parent, lxWorkerTimeout)
	defer cancel()

	executable, err := os.Executable()
	if err != nil {
		return LXSourceRuntimeResult{}, fmt.Errorf("定位音源运行进程失败：%w", err)
	}
	command := exec.CommandContext(ctx, executable, lxWorkerArgument)
	command.Stderr = io.Discard
	request, err := json.Marshal(lxWorkerRequest{Script: script})
	if err != nil {
		return LXSourceRuntimeResult{}, err
	}
	command.Stdin = bytes.NewReader(request)
	output, runErr := command.Output()
	if ctx.Err() != nil {
		return LXSourceRuntimeResult{}, fmt.Errorf("LX 音源执行超时（%s）", lxWorkerTimeout)
	}
	if len(output) > lxWorkerOutputLimit {
		return LXSourceRuntimeResult{}, errors.New("LX 音源运行结果超过大小限制")
	}
	var response lxWorkerResponse
	if err := json.Unmarshal(output, &response); err != nil {
		if runErr != nil {
			return LXSourceRuntimeResult{}, fmt.Errorf("LX 音源隔离进程执行失败：%w", runErr)
		}
		return LXSourceRuntimeResult{}, fmt.Errorf("读取 LX 音源运行结果失败：%w", err)
	}
	if response.Error != "" {
		return LXSourceRuntimeResult{}, errors.New(response.Error)
	}
	if runErr != nil {
		return LXSourceRuntimeResult{}, fmt.Errorf("LX 音源隔离进程执行失败：%w", runErr)
	}
	if err := validateLXSourceRuntimeResult(response.Result); err != nil {
		return LXSourceRuntimeResult{}, err
	}
	return response.Result, nil
}

// validateLXSourceRuntimeResult rejects scripts that initialize platform
// declarations but cannot handle the music URL requests required for playback.
func validateLXSourceRuntimeResult(result LXSourceRuntimeResult) error {
	// 洛雪的 preload 只把 inited 握手是否成功作为音源初始化状态。
	// 即使 sources 为空，或暂时没有注册 request 处理器，也不会阻止音源初始化；
	// 对应能力只会在宿主真正发起请求时体现为请求失败。
	if !result.Initialized {
		return errors.New("LX 音源未完成 inited 初始化")
	}
	return nil
}

// LX desktop user-api preload.js filters declarations against these exact
// platform capabilities. Unknown platforms/actions/qualities are ignored.
var lxSupportedSourceActions = map[string][]string{
	"kw": {"musicUrl"},
	"kg": {"musicUrl"},
	"tx": {"musicUrl"},
	"wy": {"musicUrl"},
	"mg": {"musicUrl"},
	"local": {"musicUrl", "lyric", "pic"},
}

var lxSupportedSourceQualitys = map[string][]string{
	"kw": {"128k", "320k", "flac", "flac24bit"},
	"kg": {"128k", "320k", "flac", "flac24bit"},
	"tx": {"128k", "320k", "flac", "flac24bit"},
	"wy": {"128k", "320k", "flac", "flac24bit"},
	"mg": {"128k", "320k", "flac", "flac24bit"},
	"local": {},
}

func filterLXSourceDeclaration(raw json.RawMessage) (json.RawMessage, []string, []LXDeclaredSource, error) {
	var input struct {
		Sources map[string]struct {
			Type string `json:"type"`
			Actions []string `json:"actions"`
			Qualitys []string `json:"qualitys"`
		} `json:"sources"`
	}
	if err := json.Unmarshal(raw, &input); err != nil {
		return nil, nil, nil, fmt.Errorf("LX 音源初始化数据无效：%w", err)
	}
	if input.Sources == nil {
		return nil, nil, nil, errors.New("LX 音源初始化数据无效：缺少 sources")
	}

	filtered := struct {
		Sources map[string]struct {
			Type string `json:"type"`
			Actions []string `json:"actions"`
			Qualitys []string `json:"qualitys"`
		} `json:"sources"`
	}{Sources: make(map[string]struct {
		Type string `json:"type"`
		Actions []string `json:"actions"`
		Qualitys []string `json:"qualitys"`
	})}
	keys := make([]string, 0, len(input.Sources))
	for key := range input.Sources { keys = append(keys, key) }
	sort.Strings(keys)
	platforms := make([]string, 0, len(keys))
	declared := make([]LXDeclaredSource, 0, len(keys))
	for _, key := range keys {
		source := input.Sources[key]
		allowedActions, supported := lxSupportedSourceActions[key]
		if !supported || source.Type != "music" { continue }
		actions := make([]string, 0, len(source.Actions))
		for _, action := range allowedActions {
			if containsLXValue(source.Actions, action) { actions = append(actions, action) }
		}
		allowedQualitys := lxSupportedSourceQualitys[key]
		qualitys := make([]string, 0, len(source.Qualitys))
		for _, quality := range allowedQualitys {
			if containsLXValue(source.Qualitys, quality) { qualitys = append(qualitys, quality) }
		}
		filtered.Sources[key] = struct {
			Type string `json:"type"`
			Actions []string `json:"actions"`
			Qualitys []string `json:"qualitys"`
		}{Type: "music", Actions: actions, Qualitys: qualitys}
		platforms = append(platforms, key)
		declared = append(declared, LXDeclaredSource{Key: key, Type: "music", Actions: actions, Qualitys: qualitys})
	}
	encoded, err := json.Marshal(filtered)
	return encoded, platforms, declared, err
}

// RunLXSourceWorker is called only by the private child-process entry point.
func RunLXSourceWorker(input io.Reader, output io.Writer) int {
	return runLXSourceWorker(input, output, nil)
}

func runLXSourceWorker(input io.Reader, output io.Writer, requestClient *http.Client) int {
	response := lxWorkerResponse{}
	defer func() {
		if recovered := recover(); recovered != nil {
			response = lxWorkerResponse{Error: fmt.Sprintf("LX 音源运行异常：%v", recovered)}
		}
		_ = json.NewEncoder(output).Encode(response)
	}()

	data, err := io.ReadAll(io.LimitReader(input, lxWorkerInputLimit+1))
	if err != nil {
		response.Error = "读取 LX 音源请求失败"
		return 1
	}
	if len(data) > lxWorkerInputLimit {
		response.Error = "LX 音源请求超过大小限制"
		return 1
	}
	var request lxWorkerRequest
	if err := json.Unmarshal(data, &request); err != nil {
		response.Error = "LX 音源请求格式无效"
		return 1
	}
	if strings.TrimSpace(request.Script) == "" || len(request.Script) > MaxLXUserSourceBytes {
		response.Error = "LX 音源脚本为空或超过 1 MB 限制"
		return 1
	}

	vm := goja.New()
	vm.SetMaxCallStackSize(256)
	timeout := time.AfterFunc(lxWorkerExecutionTimeout, func() {
		vm.Interrupt("LX source execution timeout")
	})
	defer timeout.Stop()

	var initialized json.RawMessage
	handlers := make(map[string]goja.Value)
	requestSummaries := make([]string, 0, 8)
	eventNames := vm.NewObject()
	_ = eventNames.Set("inited", "inited")
	_ = eventNames.Set("request", "request")
	_ = eventNames.Set("updateAlert", "updateAlert")
	updateAlertSent := false
	lx := vm.NewObject()
	_ = lx.Set("EVENT_NAMES", eventNames)
	_ = lx.Set("env", "desktop")
	_ = lx.Set("version", "2.0.0")
	if source, parseErr := ParseLXUserSource(request.Script); parseErr == nil {
		currentInfo := vm.NewObject()
		_ = currentInfo.Set("name", source.Metadata.Name)
		_ = currentInfo.Set("description", source.Metadata.Description)
		_ = currentInfo.Set("author", source.Metadata.Author)
		_ = currentInfo.Set("homepage", source.Metadata.Homepage)
		_ = currentInfo.Set("version", source.Metadata.Version)
		_ = currentInfo.Set("rawScript", request.Script)
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
		delay:=time.Duration(call.Argument(1).ToInteger())*time.Millisecond
		if delay<0 { delay=0 }; if delay>lxWorkerTimeout { delay=lxWorkerTimeout }
		return vm.ToValue(loop.setTimer(call.Argument(0),delay,false))
	})
	_ = vm.Set("clearTimeout", func(call goja.FunctionCall) goja.Value { loop.clearTimer(int(call.Argument(0).ToInteger())); return goja.Undefined() })
	_ = vm.Set("setInterval", func(call goja.FunctionCall) goja.Value {
		delay:=time.Duration(call.Argument(1).ToInteger())*time.Millisecond
		if delay<1 { delay=time.Millisecond }; if delay>lxWorkerTimeout { delay=lxWorkerTimeout }
		return vm.ToValue(loop.setTimer(call.Argument(0),delay,true))
	})
	_ = vm.Set("clearInterval", func(call goja.FunctionCall) goja.Value { loop.clearTimer(int(call.Argument(0).ToInteger())); return goja.Undefined() })
	if err := installLXSourceUtilities(vm, lx); err != nil {
		response.Error = "初始化 LX 工具函数失败"
		return 1
	}
	_ = lx.Set("send", func(call goja.FunctionCall) goja.Value {
		promise, resolve, reject := vm.NewPromise()
		event := call.Argument(0).String()
		if event == "updateAlert" {
			if updateAlertSent {
				_ = reject(vm.NewGoError(errors.New("The update alert can only be called once.")))
				return vm.ToValue(promise)
			}
			updateAlertSent = true
			_ = resolve(goja.Undefined())
			return vm.ToValue(promise)
		}
		if event != "inited" {
			_ = reject(vm.NewGoError(fmt.Errorf("The event is not supported: %s", event)))
			return vm.ToValue(promise)
		}
		if len(initialized) > 0 {
			_ = reject(vm.NewGoError(errors.New("Script is inited")))
			return vm.ToValue(promise)
		}
		encoded, marshalErr := json.Marshal(call.Argument(1).Export())
		if marshalErr != nil {
			_ = reject(vm.NewGoError(marshalErr))
			return vm.ToValue(promise)
		}
		initialized = encoded
		_ = resolve(goja.Undefined())
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
		handlers[event] = call.Argument(1)
		_ = resolve(goja.Undefined())
		return vm.ToValue(promise)
	})
	if err := installLXRequestAsync(vm, lx, requestClient, loop, func(requestID int64, rawURL string, response map[string]any, requestErr error) {
		if requestErr != nil {
			requestSummaries = append(requestSummaries, summarizeLXRequestError(requestID, rawURL, requestErr))
			return
		}
		requestSummaries = append(requestSummaries, fmt.Sprintf("request#%d %s", requestID, summarizeLXRequestResponse(response)))
	}); err != nil {
		response.Error = "初始化 LX request 请求桥接失败"
		return 1
	}
	if err := vm.Set("lx", lx); err != nil {
		response.Error = "初始化 LX 音源运行环境失败"
		return 1
	}
	if _, err := vm.RunString(request.Script); err != nil {
		response.Error = fmt.Sprintf("LX 音源脚本初始化失败：%s", err.Error())
		return 1
	}
	if len(initialized) == 0 {
		// LX source scripts may announce initialization asynchronously (for
		// example from setTimeout or an HTTP callback). Keep the VM event loop
		// alive until the real inited event arrives or initialization times out.
		if pumpErr := loop.pumpUntil(func() bool { return len(initialized) > 0 }, lxRequestTimeout+5*time.Second); pumpErr != nil {
			response.Error = fmt.Sprintf("LX 音源未发送 inited 初始化事件：%s", pumpErr.Error())
			return 1
		}
	}
	if len(initialized) == 0 {
		response.Error = "LX 音源未发送 inited 初始化事件"
		return 1
	}
	normalizedSources, platforms, declaredSources, declarationErr := filterLXSourceDeclaration(initialized)
	if declarationErr != nil {
		response.Error = declarationErr.Error()
		return 1
	}
	response.Result = LXSourceRuntimeResult{
		Initialized: true,
		Platforms: platforms,
		DeclaredSources: declaredSources,
		Sources: normalizedSources,
	}
	handlerNames := make([]string, 0, len(handlers))
	for name := range handlers {
		handlerNames = append(handlerNames, name)
	}
	sort.Strings(handlerNames)
	response.Result = LXSourceRuntimeResult{
		Initialized: true,
		Platforms: platforms,
		DeclaredSources: declaredSources,
		Sources: append(json.RawMessage(nil), initialized...),
		Handlers: handlerNames,
	}
	if request.Operation == "resolve" {
		var resolveRequest struct {
			Source string `json:"source"`
			Action string `json:"action"`
			Info struct { Type any `json:"type"` } `json:"info"`
		}
		if err := json.Unmarshal(request.Payload, &resolveRequest); err != nil { response.Error = "LX 音源请求数据格式无效"; return 1 }
		var selected *LXDeclaredSource
		for i := range declaredSources { if declaredSources[i].Key == resolveRequest.Source { selected = &declaredSources[i]; break } }
		if selected == nil { response.Error = fmt.Sprintf("LX 音源未声明来源 %q", resolveRequest.Source); return 1 }
		if !containsLXValue(selected.Actions, resolveRequest.Action) { response.Error = fmt.Sprintf("LX 音源 %q 不支持动作 %q", resolveRequest.Source, resolveRequest.Action); return 1 }
		handler, ok := handlers["request"]
		if !ok { response.Error = "LX 音源没有注册 request 处理器"; return 1 }
		if len(request.Payload) == 0 { response.Error = "LX 音源请求数据为空"; return 1 }
		var payload any
		if err := json.Unmarshal(request.Payload, &payload); err != nil { response.Error = "LX 音源请求数据格式无效"; return 1 }
		callable, ok := goja.AssertFunction(handler)
		if !ok { response.Error = "LX 音源 request 处理器不可调用"; return 1 }
		// Clear initialization-request diagnostics so a rejected musicUrl reports its own request.
		requestSummaries = requestSummaries[:0]
		//洛雪 calls the request handler with the lx API object as its this context.
		value, callErr := callable(lx, vm.ToValue(payload))
		if callErr != nil { response.Error = fmt.Sprintf("LX 音源 request 处理失败：%s", callErr.Error()); return 1 }
		if promise, ok := value.Export().(*goja.Promise); ok {
			if pumpErr:=loop.pumpUntil(func() bool { return promise.State()!=goja.PromiseStatePending }, lxWorkerExecutionTimeout); pumpErr!=nil {
				response.Error=fmt.Sprintf("LX 音源异步执行失败：Promise 尚未完成（%s）",pumpErr.Error()); return 1
			}
			switch promise.State() {
			case goja.PromiseStateFulfilled: value=promise.Result()
			case goja.PromiseStateRejected:
				response.Error = fmt.Sprintf("LX 音源 request Promise 被拒绝：%s", promise.Result().String())
				if len(requestSummaries) > 0 {
					response.Error += fmt.Sprintf("；请求诊断：%s", strings.Join(requestSummaries, " | "))
				}
				return 1
			}
		}
		encoded, marshalErr := json.Marshal(value.Export())
		if marshalErr != nil { response.Error = fmt.Sprintf("序列化 LX 音源解析结果失败：%s", marshalErr.Error()); return 1 }
		response.Value = encoded
	} else if request.Operation != "" && request.Operation != "inspect" {
		response.Error = "不支持的 LX 音源运行操作"
		return 1
	}
	return 0
}


// safeLXRequestEndpoint omits credentials, query parameters, and fragments.
func safeLXRequestEndpoint(rawURL string) string {
	parsed, err := url.Parse(rawURL)
	if err != nil || parsed.Host == "" {
		return ""
	}
	path := parsed.EscapedPath()
	if path == "" {
		path = "/"
	}
	return parsed.Host + path
}

// summarizeLXRequestError preserves useful transport/security errors without
// leaking the request URL's query string or credentials.
func summarizeLXRequestError(requestID int64, rawURL string, requestErr error) string {
	parts := []string{fmt.Sprintf("request#%d", requestID)}
	if endpoint := safeLXRequestEndpoint(rawURL); endpoint != "" {
		parts = append(parts, "endpoint="+endpoint)
	}
	cause := requestErr
	var urlErr *url.Error
	if errors.As(cause, &urlErr) && urlErr.Err != nil {
		cause = urlErr.Err
	}
	message := "unknown request error"
	if cause != nil {
		message = strings.Join(strings.Fields(cause.Error()), " ")
	}
	if len(message) > 200 {
		message = message[:200] + "…"
	}
	parts = append(parts, "request-error="+message)
	return strings.Join(parts, "; ")
}

// summarizeLXRequestResponse records only non-secret response diagnostics for a
// rejected custom-source request. URL credentials, query strings, fragments,
// and request headers (which may contain source signatures) are never exposed.
func summarizeLXRequestResponse(response map[string]any) string {
	status := response["statusCode"]
	if status == nil {
		status = response["status"]
	}
	parts := []string{fmt.Sprintf("HTTP %v", status)}
	if rawURL, ok := response["url"].(string); ok {
		if endpoint := safeLXRequestEndpoint(rawURL); endpoint != "" {
			parts = append(parts, "endpoint="+endpoint)
		}
	}
	if headers, ok := response["headers"].(map[string]string); ok {
		for key, value := range headers {
			if strings.EqualFold(key, "Content-Type") && strings.TrimSpace(value) != "" {
				parts = append(parts, "content-type="+strings.TrimSpace(value))
				break
			}
		}
	} else if headers, ok := response["headers"].(map[string]any); ok {
		for key, value := range headers {
			if strings.EqualFold(key, "Content-Type") && strings.TrimSpace(fmt.Sprint(value)) != "" {
				parts = append(parts, "content-type="+strings.TrimSpace(fmt.Sprint(value)))
				break
			}
		}
	}
	if body, exists := response["body"]; exists && body != nil {
		var preview string
		switch value := body.(type) {
		case map[string]any:
			if code, ok := value["code"]; ok {
				parts = append(parts, "API code="+fmt.Sprint(code))
			}
			for _, key := range []string{"msg", "message", "error"} {
				if message, ok := value[key]; ok && strings.TrimSpace(fmt.Sprint(message)) != "" {
					preview = fmt.Sprint(message)
					break
				}
			}
			if preview == "" {
				encoded, err := json.Marshal(value)
				if err == nil {
					preview = string(encoded)
				}
			}
		case string:
			preview = value
		default:
			preview = fmt.Sprint(value)
		}
		preview = strings.Join(strings.Fields(strings.Map(func(r rune) rune {
			if r < 0x20 || r == 0x7f {
				return ' '
			}
			return r
		}, preview)), " ")
		if len(preview) > 200 {
			preview = preview[:200] + "…"
		}
		if preview != "" {
			parts = append(parts, "response="+preview)
		}
	}
	return strings.Join(parts, "; ")
}

func containsLXValue(values []string, target string) bool {
	for _, value := range values { if value == target { return true } }
	return false
}

// ResolveLXUserSource invokes the request handler in a separate worker process.
// Synchronous results and settled Promises are supported. Callback-based
// HTTP requests complete synchronously inside the isolated worker; queued
// Promise microtasks are drained before the result is read.
func ResolveLXUserSource(parent context.Context, script string, payload json.RawMessage) (json.RawMessage, error) {
	if parent == nil { parent = context.Background() }
	if strings.TrimSpace(script) == "" || len(script) > MaxLXUserSourceBytes { return nil, errors.New("LX 音源脚本为空或超过 1 MB 限制") }
	if len(payload) == 0 || len(payload) > 64<<10 { return nil, errors.New("LX 音源请求数据为空或超过 64 KB 限制") }
	ctx, cancel := context.WithTimeout(parent, lxWorkerTimeout)
	defer cancel()
	executable, err := os.Executable()
	if err != nil { return nil, fmt.Errorf("定位音源运行进程失败：%w", err) }
	request, err := json.Marshal(lxWorkerRequest{Script: script, Operation: "resolve", Payload: payload})
	if err != nil { return nil, err }
	command := exec.CommandContext(ctx, executable, lxWorkerArgument)
	command.Stderr = io.Discard
	command.Stdin = bytes.NewReader(request)
	output, runErr := command.Output()
	if ctx.Err() != nil { return nil, fmt.Errorf("LX 音源执行超时（%s）", lxWorkerTimeout) }
	if len(output) > lxWorkerOutputLimit { return nil, errors.New("LX 音源运行结果超过大小限制") }
	var response lxWorkerResponse
	if err := json.Unmarshal(output, &response); err != nil {
		if runErr != nil { return nil, fmt.Errorf("LX 音源隔离进程执行失败：%w", runErr) }
		return nil, fmt.Errorf("读取 LX 音源运行结果失败：%w", err)
	}
	if response.Error != "" { return nil, errors.New(response.Error) }
	if runErr != nil { return nil, fmt.Errorf("LX 音源隔离进程执行失败：%w", runErr) }
	if len(response.Value) == 0 { return nil, errors.New("LX 音源没有返回解析结果") }
	return response.Value, nil
}
