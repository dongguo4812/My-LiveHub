package music

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sync"
	"sync/atomic"
	"time"
)

const LXSourceSessionWorkerArgument = "--lx-source-session"

type lxSessionWorkerInput struct {
	Operation string `json:"operation"`
	Script string `json:"script,omitempty"`
	RequestKey string `json:"requestKey,omitempty"`
	Payload json.RawMessage `json:"payload,omitempty"`
}

type lxSessionWorkerOutput struct {
	Type string `json:"type"`
	RequestKey string `json:"requestKey,omitempty"`
	Result LXSourceRuntimeResult `json:"result,omitempty"`
	Value json.RawMessage `json:"result_value,omitempty"`
	Error string `json:"error,omitempty"`
	Update *LXUserSourceUpdate `json:"update,omitempty"`
}

// LXUserSourceProcess owns one isolated, long-lived JS worker process. Its
// protocol follows LX's single active userApi window and requestKey response map.
type LXUserSourceProcess struct {
	command *exec.Cmd
	stdin io.WriteCloser
	writeMu sync.Mutex
	pendingMu sync.Mutex
	pending map[string]chan lxSessionResponse
	requestCounter atomic.Uint64
	ready chan lxSessionReady
	updates chan LXUserSourceUpdate
	done chan struct{}
	closeOnce sync.Once
	result LXSourceRuntimeResult
}

func StartLXUserSourceProcess(ctx context.Context, script string) (*LXUserSourceProcess, error) {
	if ctx == nil { ctx = context.Background() }
	if len(script) == 0 || len(script) > MaxLXUserSourceBytes {
		return nil, errors.New("LX 音源脚本为空或超过 9 MB 限制")
	}
	executable, err := os.Executable()
	if err != nil { return nil, fmt.Errorf("定位 LX 音源运行进程失败：%w", err) }
	command := exec.Command(executable, LXSourceWorkerArgument, LXSourceSessionWorkerArgument)
	stdin, err := command.StdinPipe()
	if err != nil { return nil, fmt.Errorf("创建 LX 音源输入管道失败：%w", err) }
	stdout, err := command.StdoutPipe()
	if err != nil { _ = stdin.Close(); return nil, fmt.Errorf("创建 LX 音源输出管道失败：%w", err) }
	command.Stderr = io.Discard
	process := &LXUserSourceProcess{
		command: command, stdin: stdin,
		pending: make(map[string]chan lxSessionResponse),
		ready: make(chan lxSessionReady, 1),
		updates: make(chan LXUserSourceUpdate, 4),
		done: make(chan struct{}),
	}
	if err := command.Start(); err != nil {
		_ = stdin.Close()
		return nil, fmt.Errorf("启动 LX 音源隔离进程失败：%w", err)
	}
	go process.readLoop(stdout)
	if err := process.write(lxSessionWorkerInput{Operation: "init", Script: script}); err != nil {
		_ = process.Close()
		return nil, err
	}
	select {
	case ready := <-process.ready:
		if ready.err != nil {
			_ = process.Close()
			return nil, ready.err
		}
		process.result = ready.result
		return process, nil
	case <-ctx.Done():
		_ = process.Close()
		return nil, ctx.Err()
	case <-process.done:
		select {
		case ready := <-process.ready:
			if ready.err != nil { return nil, ready.err }
		default:
		}
		return nil, errors.New("LX 音源隔离进程在初始化完成前退出")
	}
}

func (p *LXUserSourceProcess) Snapshot() LXSourceRuntimeResult {
	if p == nil { return LXSourceRuntimeResult{} }
	return p.result
}

func (p *LXUserSourceProcess) Updates() <-chan LXUserSourceUpdate {
	if p == nil { return nil }
	return p.updates
}

func (p *LXUserSourceProcess) Request(ctx context.Context, payload json.RawMessage) (json.RawMessage, error) {
	if p == nil { return nil, errors.New("LX 音源隔离进程不可用") }
	if ctx == nil { ctx = context.Background() }
	if len(payload) == 0 {
		return nil, errors.New("LX 音源请求数据为空")
	}
	requestKey := fmt.Sprintf("request__%d", p.requestCounter.Add(1))
	result := make(chan lxSessionResponse, 1)
	p.pendingMu.Lock()
	p.pending[requestKey] = result
	p.pendingMu.Unlock()
	if err := p.write(lxSessionWorkerInput{Operation: "request", RequestKey: requestKey, Payload: payload}); err != nil {
		p.removePending(requestKey)
		return nil, err
	}
	select {
	case response := <-result:
		return response.value, response.err
	case <-ctx.Done():
		// LX removes the timed-out requestKey from its response queue; the
		// script keeps running and any late response is ignored.
		_ = p.write(lxSessionWorkerInput{Operation: "cancel", RequestKey: requestKey})
		p.removePending(requestKey)
		return nil, errors.New("Cancel request")
	case <-p.done:
		p.removePending(requestKey)
		return nil, errors.New("LX 音源隔离进程已退出")
	}
}

func (p *LXUserSourceProcess) write(message lxSessionWorkerInput) error {
	encoded, err := json.Marshal(message)
	if err != nil { return err }
	p.writeMu.Lock()
	defer p.writeMu.Unlock()
	select {
	case <-p.done:
		return errors.New("LX 音源隔离进程已关闭")
	default:
	}
	if _, err := p.stdin.Write(append(encoded, '\n')); err != nil {
		return fmt.Errorf("向 LX 音源运行进程发送请求失败：%w", err)
	}
	return nil
}

func (p *LXUserSourceProcess) removePending(requestKey string) {
	p.pendingMu.Lock()
	delete(p.pending, requestKey)
	p.pendingMu.Unlock()
}

func (p *LXUserSourceProcess) readLoop(reader io.Reader) {
	defer func() {
		_ = p.command.Wait()
		p.pendingMu.Lock()
		for key, channel := range p.pending {
			delete(p.pending, key)
			channel <- lxSessionResponse{err: errors.New("LX 音源隔离进程已退出")}
		}
		p.pendingMu.Unlock()
		close(p.updates)
		close(p.done)
	}()
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 64*1024), 1<<20)
	for scanner.Scan() {
		var message lxSessionWorkerOutput
		if err := json.Unmarshal(scanner.Bytes(), &message); err != nil { continue }
		switch message.Type {
		case "ready":
			var readyErr error
			if message.Error != "" { readyErr = errors.New(message.Error) }
			p.ready <- lxSessionReady{result: message.Result, err: readyErr}
		case "response":
			p.pendingMu.Lock()
			channel := p.pending[message.RequestKey]
			delete(p.pending, message.RequestKey)
			p.pendingMu.Unlock()
			if channel != nil {
				var responseErr error
				if message.Error != "" { responseErr = errors.New(message.Error) }
				channel <- lxSessionResponse{value: message.Value, err: responseErr}
			}
		case "update":
			if message.Update != nil {
				select { case p.updates <- *message.Update: default: }
			}
		}
	}
}

func (p *LXUserSourceProcess) Close() error {
	if p == nil { return nil }
	p.closeOnce.Do(func() {
		_ = p.write(lxSessionWorkerInput{Operation: "close"})
		_ = p.stdin.Close()
		select {
		case <-p.done:
		case <-time.After(2 * time.Second):
			if p.command.Process != nil { _ = p.command.Process.Kill() }
			<-p.done
		}
	})
	return nil
}

// RunLXUserSourceSessionWorker is the child-process entry point for the
// persistent custom-source runtime. One JS VM serves multiple requestKey calls.
func RunLXUserSourceSessionWorker(input io.Reader, output io.Writer) int {
	scanner := bufio.NewScanner(input)
	scanner.Buffer(make([]byte, 64*1024), MaxLXUserSourceBytes*6 + 64*1024)
	if !scanner.Scan() { return 1 }
	var initMessage lxSessionWorkerInput
	if err := json.Unmarshal(scanner.Bytes(), &initMessage); err != nil || initMessage.Operation != "init" {
		return 1
	}
	session, err := StartLXUserSourceSession(context.Background(), initMessage.Script, nil)
	writeMu := sync.Mutex{}
	write := func(message lxSessionWorkerOutput) {
		writeMu.Lock()
		defer writeMu.Unlock()
		_ = json.NewEncoder(output).Encode(message)
	}
	if err != nil {
		write(lxSessionWorkerOutput{Type: "ready", Error: err.Error()})
		return 1
	}
	write(lxSessionWorkerOutput{Type: "ready", Result: session.Snapshot()})
	go func() {
		for update := range session.Updates() {
			copy := update
			write(lxSessionWorkerOutput{Type: "update", Update: &copy})
		}
	}()
	var requestWG sync.WaitGroup
	var requestMu sync.Mutex
	requestCancels := make(map[string]context.CancelFunc)
	defer func() {
		_ = session.Close()
		requestMu.Lock()
		for key, cancel := range requestCancels { cancel(); delete(requestCancels, key) }
		requestMu.Unlock()
		requestWG.Wait()
	}()
	for scanner.Scan() {
		var message lxSessionWorkerInput
		if err := json.Unmarshal(scanner.Bytes(), &message); err != nil { continue }
		switch message.Operation {
		case "request":
			requestCtx, cancel := context.WithCancel(context.Background())
			requestMu.Lock()
			requestCancels[message.RequestKey] = cancel
			requestMu.Unlock()
			requestWG.Add(1)
			go func(requestKey string, payload json.RawMessage, ctx context.Context, cancel context.CancelFunc) {
				defer requestWG.Done()
				defer cancel()
				defer func() {
					requestMu.Lock()
					delete(requestCancels, requestKey)
					requestMu.Unlock()
				}()
				value, requestErr := session.Request(ctx, payload)
				response := lxSessionWorkerOutput{Type: "response", RequestKey: requestKey, Value: value}
				if requestErr != nil { response.Error = requestErr.Error() }
				write(response)
			}(message.RequestKey, append(json.RawMessage(nil), message.Payload...), requestCtx, cancel)
		case "cancel":
			requestMu.Lock()
			cancel := requestCancels[message.RequestKey]
			delete(requestCancels, message.RequestKey)
			requestMu.Unlock()
			if cancel != nil { cancel() }
		case "close":
			return 0
		}
	}
	return 0
}
