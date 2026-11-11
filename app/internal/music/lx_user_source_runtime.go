package music

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sort"
	"strings"
	"time"

	"github.com/dop251/goja"
)

const (
	lxWorkerArgument = "--lx-source-worker"
	lxWorkerInputLimit = MaxLXUserSourceBytes + 4096
	lxWorkerOutputLimit = 256 << 10
	lxWorkerTimeout = 6 * time.Second
)

type LXSourceRuntimeResult struct {
	Initialized bool            `json:"initialized"`
	Platforms   []string        `json:"platforms"`
	Sources     json.RawMessage `json:"sources"`
	Handlers    []string        `json:"handlers"`
}

type lxWorkerRequest struct {
	Script string `json:"script"`
}

type lxWorkerResponse struct {
	Result LXSourceRuntimeResult `json:"result,omitempty"`
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
	output, err := command.Output()
	if ctx.Err() != nil {
		return LXSourceRuntimeResult{}, fmt.Errorf("LX 音源执行超时（%s）", lxWorkerTimeout)
	}
	if err != nil {
		return LXSourceRuntimeResult{}, fmt.Errorf("LX 音源隔离进程执行失败：%w", err)
	}
	if len(output) > lxWorkerOutputLimit {
		return LXSourceRuntimeResult{}, errors.New("LX 音源运行结果超过大小限制")
	}
	var response lxWorkerResponse
	if err := json.Unmarshal(output, &response); err != nil {
		return LXSourceRuntimeResult{}, fmt.Errorf("读取 LX 音源运行结果失败：%w", err)
	}
	if response.Error != "" {
		return LXSourceRuntimeResult{}, errors.New(response.Error)
	}
	if !response.Result.Initialized || len(response.Result.Platforms) == 0 {
		return LXSourceRuntimeResult{}, errors.New("LX 音源未声明可用平台")
	}
	return response.Result, nil
}

// RunLXSourceWorker is called only by the private child-process entry point.
func RunLXSourceWorker(input io.Reader, output io.Writer) int {
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
	timeout := time.AfterFunc(lxWorkerTimeout, func() {
		vm.Interrupt("LX source execution timeout")
	})
	defer timeout.Stop()

	var initialized json.RawMessage
	handlers := make(map[string]bool)
	eventNames := vm.NewObject()
	_ = eventNames.Set("inited", "inited")
	_ = eventNames.Set("request", "request")
	lx := vm.NewObject()
	_ = lx.Set("EVENT_NAMES", eventNames)
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
			handlers[event] = true
		}
		return goja.Undefined()
	})
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
		Sources map[string]json.RawMessage `json:"sources"`
	}
	if err := json.Unmarshal(initialized, &declaration); err != nil || len(declaration.Sources) == 0 {
		response.Error = "LX 音源初始化数据无效：没有声明 sources"
		return 1
	}
	platforms := make([]string, 0, len(declaration.Sources))
	for platform := range declaration.Sources {
		platform = strings.TrimSpace(platform)
		if platform != "" {
			platforms = append(platforms, platform)
		}
	}
	sort.Strings(platforms)
	handlerNames := make([]string, 0, len(handlers))
	for name := range handlers {
		handlerNames = append(handlerNames, name)
	}
	sort.Strings(handlerNames)
	response.Result = LXSourceRuntimeResult{
		Initialized: true,
		Platforms: platforms,
		Sources: append(json.RawMessage(nil), initialized...),
		Handlers: handlerNames,
	}
	return 0
}
