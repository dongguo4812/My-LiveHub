package music

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"sort"
	"strings"
	"time"

	"github.com/dop251/goja"
)

const (
	LXSourceWorkerArgument = "--lx-source-worker"
	lxWorkerArgument = LXSourceWorkerArgument
	lxWorkerInputLimit = MaxLXUserSourceBytes + 4096
	lxWorkerOutputLimit = 256 << 10
	lxWorkerTimeout = 8 * time.Second
	lxWorkerExecutionTimeout = 5 * time.Second
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
	if !response.Result.Initialized || len(response.Result.Platforms) == 0 {
		return LXSourceRuntimeResult{}, errors.New("LX 音源未声明可用平台")
	}
	return response.Result, nil
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
	eventNames := vm.NewObject()
	_ = eventNames.Set("inited", "inited")
	_ = eventNames.Set("request", "request")
	_ = eventNames.Set("updateAlert", "updateAlert")
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
	// HTTP requests are executed synchronously and bounded by the host timeout;
	// these timer shims keep common LX request wrappers compatible without
	// allowing untrusted scripts to create background goroutines.
	_ = vm.Set("setTimeout", func(goja.FunctionCall) goja.Value { return vm.ToValue(1) })
	_ = vm.Set("clearTimeout", func(goja.FunctionCall) goja.Value { return goja.Undefined() })
	_ = vm.Set("setInterval", func(goja.FunctionCall) goja.Value { return vm.ToValue(1) })
	_ = vm.Set("clearInterval", func(goja.FunctionCall) goja.Value { return goja.Undefined() })
	if err := installLXSourceUtilities(vm, lx); err != nil {
		response.Error = "初始化 LX 工具函数失败"
		return 1
	}
	_ = lx.Set("send", func(call goja.FunctionCall) goja.Value {
		if call.Argument(0).String() == "inited" {
			encoded, marshalErr := json.Marshal(call.Argument(1).Export())
			if marshalErr == nil {
				initialized = encoded
			}
		}
		return goja.Undefined()
	})
	_ = lx.Set("on", func(call goja.FunctionCall) goja.Value {
		event := call.Argument(0).String()
		if _, ok := goja.AssertFunction(call.Argument(1)); ok && event != "" {
			handlers[event] = call.Argument(1)
		}
		return goja.Undefined()
	})
	if err := installLXRequestWithClient(vm, lx, requestClient); err != nil {
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
		response.Error = "LX 音源未发送 inited 初始化事件"
		return 1
	}
	var declaration struct {
		Sources map[string]struct {
			Name string `json:"name"`
			Type string `json:"type"`
			Actions []string `json:"actions"`
			Qualitys []string `json:"qualitys"`
		} `json:"sources"`
	}
	if err := json.Unmarshal(initialized, &declaration); err != nil || len(declaration.Sources) == 0 {
		response.Error = "LX 音源初始化数据无效：没有声明 sources"
		return 1
	}
	platforms := make([]string, 0, len(declaration.Sources))
	declaredSources := make([]LXDeclaredSource, 0, len(declaration.Sources))
	for key, source := range declaration.Sources {
		key = strings.TrimSpace(key)
		if key == "" { continue }
		platforms = append(platforms, key)
		declaredSources = append(declaredSources, LXDeclaredSource{Key:key, Name:source.Name, Type:source.Type, Actions:append([]string(nil), source.Actions...), Qualitys:append([]string(nil), source.Qualitys...)})
	}
	sort.Strings(platforms)
	sort.Slice(declaredSources, func(i,j int) bool { return declaredSources[i].Key < declaredSources[j].Key })
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
		if resolveRequest.Action == "musicUrl" && resolveRequest.Source != "local" {
			quality, ok := resolveRequest.Info.Type.(string)
			if !ok || !containsLXValue(selected.Qualitys, quality) { response.Error = fmt.Sprintf("LX 音源 %q 不支持音质 %v", resolveRequest.Source, resolveRequest.Info.Type); return 1 }
		}
		handler, ok := handlers["request"]
		if !ok { response.Error = "LX 音源没有注册 request 处理器"; return 1 }
		if len(request.Payload) == 0 { response.Error = "LX 音源请求数据为空"; return 1 }
		var payload any
		if err := json.Unmarshal(request.Payload, &payload); err != nil { response.Error = "LX 音源请求数据格式无效"; return 1 }
		callable, ok := goja.AssertFunction(handler)
		if !ok { response.Error = "LX 音源 request 处理器不可调用"; return 1 }
		value, callErr := callable(goja.Undefined(), vm.ToValue(payload))
		if callErr != nil { response.Error = fmt.Sprintf("LX 音源 request 处理失败：%s", callErr.Error()); return 1 }
		// LX sources commonly use async/await and Promise.then around the
		// callback-based HTTP API. Drive Goja's microtask queue after the
		// synchronous HTTP callback has settled the underlying Promise.
		for i := 0; i < 16; i++ {
			promise, ok := value.Export().(*goja.Promise)
			if !ok || promise.State() != goja.PromiseStatePending { break }
			if _, flushErr := vm.RunString("void 0"); flushErr != nil {
				response.Error = fmt.Sprintf("LX 音源 Promise 执行失败：%s", flushErr.Error())
				return 1
			}
		}
		if promise, ok := value.Export().(*goja.Promise); ok {
			switch promise.State() {
			case goja.PromiseStateFulfilled: value = promise.Result()
			case goja.PromiseStateRejected: response.Error = fmt.Sprintf("LX 音源 request Promise 被拒绝：%s", promise.Result().String()); return 1
			default: response.Error = "LX 音源 request Promise 尚未完成；当前只支持请求回调同步完成的脚本"; return 1
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
