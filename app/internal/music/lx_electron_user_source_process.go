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
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/dongguo4812/My-LiveHub/app/internal/config"
)

func lxRequestContextError(err error) error {
	if errors.Is(err, context.DeadlineExceeded) { return errors.New("Request timeout") }
	return errors.New("Cancel request")
}

const (
	lxElectronReadyTimeout = 20 * time.Second
	lxElectronInitTimeout = 70 * time.Second
)

type lxElectronInput struct {
	Operation string          `json:"operation"`
	Script    string          `json:"script,omitempty"`
	RequestKey string         `json:"requestKey,omitempty"`
	Data      json.RawMessage `json:"data,omitempty"`
	Proxy     lxElectronProxy `json:"proxy,omitempty"`
	Name      string          `json:"name,omitempty"`
	Description string        `json:"description,omitempty"`
	Version   string          `json:"version,omitempty"`
	Author    string          `json:"author,omitempty"`
	Homepage  string          `json:"homepage,omitempty"`
	ID        string          `json:"id,omitempty"`
}

type lxElectronProxy struct {
	Host string `json:"host"`
	Port string `json:"port"`
}

type lxElectronOutput struct {
	Type string          `json:"type"`
	Name string          `json:"name,omitempty"`
	Params json.RawMessage `json:"params,omitempty"`
	Message string        `json:"message,omitempty"`
}

type lxElectronEnvelope struct {
	Status bool            `json:"status"`
	Message string         `json:"message,omitempty"`
	Data json.RawMessage   `json:"data,omitempty"`
}

type lxElectronInitData struct {
	Sources map[string]struct {
		Type string   `json:"type"`
		Actions []string `json:"actions"`
		Qualitys []string `json:"qualitys"`
	} `json:"sources"`
}

type lxElectronResponseData struct {
	RequestKey string          `json:"requestKey"`
	Result json.RawMessage     `json:"result"`
}

type lxElectronPending struct {
	value json.RawMessage
	err error
}

type LXElectronUserSourceProcess struct {
	command *exec.Cmd
	stdin io.WriteCloser
	writeMu sync.Mutex
	pendingMu sync.Mutex
	pending map[string]chan lxElectronPending
	counter atomic.Uint64
	ready chan error
	updates chan LXUserSourceUpdate
	done chan struct{}
	closeOnce sync.Once
	resultMu sync.RWMutex
	result LXSourceRuntimeResult
}

func findLXElectronRuntime() (string, []string, error) {
	exe, err := os.Executable()
	if err != nil { return "", nil, fmt.Errorf("定位 My-LiveHub 可执行文件失败：%w", err) }
	exeDir := filepath.Dir(exe)
	candidates := []string{
		filepath.Join(exeDir, "lx-runtime", "My-LiveHub LX Runtime.exe"),
		filepath.Join(exeDir, "lx-runtime", "win-unpacked", "My-LiveHub LX Runtime.exe"),
		filepath.Join(exeDir, "lx-runtime.exe"),
	}
	for _, candidate := range candidates {
		if info, statErr := os.Stat(candidate); statErr == nil && !info.IsDir() {
			return candidate, nil, nil
		}
	}
	if runtime.GOOS == "windows" {
		working, _ := os.Getwd()
		devRoots := []string{working, filepath.Join(working, "app"), filepath.Join(exeDir, "app")}
		for _, root := range devRoots {
			electron := filepath.Join(root, "lx-runtime", "node_modules", "electron", "dist", "electron.exe")
			main := filepath.Join(root, "lx-runtime", "main.cjs")
			if _, e1 := os.Stat(electron); e1 == nil {
				if _, e2 := os.Stat(main); e2 == nil { return electron, []string{main}, nil }
			}
		}
	}
	return "", nil, errors.New("未找到洛雪 Electron 音源运行环境；请先构建 app/lx-runtime 并随客户端部署 lx-runtime 目录")
}

func StartLXElectronUserSourceProcess(ctx context.Context, script string, proxy config.LXUserSourceProxyConfig, metadata LXUserSourceMetadata) (*LXElectronUserSourceProcess, error) {
	if ctx == nil { ctx = context.Background() }
	if strings.TrimSpace(script) == "" || lxUserSourceStringLength(script) > MaxLXUserSourceBytes {
		return nil, errors.New("LX 音源脚本为空或超过 9 MB 限制")
	}
	if err := proxy.Validate(); err != nil { return nil, err }
	executable, args, err := findLXElectronRuntime()
	if err != nil { return nil, err }
	command := exec.Command(executable, args...)
	stdin, err := command.StdinPipe()
	if err != nil { return nil, fmt.Errorf("创建洛雪 Electron 输入管道失败：%w", err) }
	stdout, err := command.StdoutPipe()
	if err != nil { _ = stdin.Close(); return nil, fmt.Errorf("创建洛雪 Electron 输出管道失败：%w", err) }
	command.Stderr = io.Discard
	process := &LXElectronUserSourceProcess{
		command: command, stdin: stdin, pending: make(map[string]chan lxElectronPending),
		ready: make(chan error, 1), updates: make(chan LXUserSourceUpdate, 8), done: make(chan struct{}),
	}
	if err := command.Start(); err != nil { _ = stdin.Close(); return nil, fmt.Errorf("启动洛雪 Electron 音源运行环境失败：%w", err) }
	go process.readLoop(stdout)
	proxyValue := lxElectronProxy{}
	if proxy.Enabled { proxyValue = lxElectronProxy{Host: proxy.Host, Port: proxy.Port} }
	init := lxElectronInput{
		Operation:"init", Script:script, Proxy:proxyValue, ID:metadata.ID,
		Name:metadata.Name, Description:metadata.Description, Version:metadata.Version,
		Author:metadata.Author, Homepage:metadata.Homepage,
	}
	if err := process.write(init); err != nil { _ = process.Close(); return nil, err }
	select {
	case err := <-process.ready:
		if err != nil { _ = process.Close(); return nil, err }
		return process, nil
	case <-ctx.Done():
		_ = process.Close(); return nil, ctx.Err()
	case <-time.After(lxElectronInitTimeout):
		_ = process.Close(); return nil, errors.New("洛雪音源初始化超时")
	case <-process.done:
		return nil, errors.New("洛雪 Electron 音源运行环境在初始化前退出")
	}
}

func (p *LXElectronUserSourceProcess) write(message lxElectronInput) error {
	if p == nil { return errors.New("洛雪 Electron 音源运行环境不可用") }
	data, err := json.Marshal(message)
	if err != nil { return err }
	p.writeMu.Lock()
	defer p.writeMu.Unlock()
	select { case <-p.done: return errors.New("洛雪 Electron 音源运行环境已关闭"); default: }
	if _, err := p.stdin.Write(append(data, '\n')); err != nil { return fmt.Errorf("发送洛雪音源消息失败：%w", err) }
	return nil
}

func (p *LXElectronUserSourceProcess) readLoop(reader io.Reader) {
	defer func() {
		_ = p.command.Wait()
		p.pendingMu.Lock()
		for key, ch := range p.pending { delete(p.pending,key); ch <- lxElectronPending{err:errors.New("洛雪 Electron 音源运行环境已退出")} }
		p.pendingMu.Unlock()
		close(p.updates)
		close(p.done)
	}()
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 64*1024), MaxLXUserSourceBytes*6+64*1024)
	for scanner.Scan() {
		var message lxElectronOutput
		if err := json.Unmarshal(scanner.Bytes(), &message); err != nil { continue }
		switch message.Type {
		case "ready":
			// The renderer sends userApi_init only after the original LX preload
			// has executed the complete source script and accepted inited().
		case "error":
			select { case p.ready <- errors.New(message.Message): default: }
		case "event":
			var envelope lxElectronEnvelope
			if err := json.Unmarshal(message.Params, &envelope); err != nil { continue }
			switch message.Name {
			case "userApi_init":
				if !envelope.Status {
					select { case p.ready <- errors.New(envelope.Message): default: }
					continue
				}
				var data lxElectronInitData
				if err := json.Unmarshal(envelope.Data, &data); err != nil {
					select { case p.ready <- fmt.Errorf("解析洛雪音源初始化结果失败：%w",err): default: }
					continue
				}
				result := LXSourceRuntimeResult{Initialized:true, Sources:append(json.RawMessage(nil), envelope.Data...)}
				for key, source := range data.Sources {
					if source.Type != "music" { continue }
					result.Platforms = append(result.Platforms,key)
					result.DeclaredSources = append(result.DeclaredSources,LXDeclaredSource{Key:key,Type:source.Type,Actions:append([]string(nil),source.Actions...),Qualitys:append([]string(nil),source.Qualitys...)})
				}
				p.resultMu.Lock(); p.result=result; p.resultMu.Unlock()
				select { case p.ready <- nil: default: }
			case "userApi_response":
				var data lxElectronResponseData
				if err := json.Unmarshal(envelope.Data,&data); err != nil { continue }
				p.pendingMu.Lock(); ch:=p.pending[data.RequestKey]; delete(p.pending,data.RequestKey); p.pendingMu.Unlock()
				if ch != nil {
					if envelope.Status { ch <- lxElectronPending{value:append(json.RawMessage(nil),data.Result...)} } else { ch <- lxElectronPending{err:errors.New(envelope.Message)} }
				}
			case "userApi_showUpdateAlert":
				var data struct { Log string `json:"log"`; UpdateURL string `json:"updateUrl"` }
				if json.Unmarshal(envelope.Data,&data)==nil {
					select { case p.updates <- LXUserSourceUpdate{Log:data.Log,UpdateURL:data.UpdateURL}: default: }
				}
			}
		}
	}
	if err := scanner.Err(); err != nil {
		select { case p.ready <- fmt.Errorf("读取洛雪 Electron 音源进程输出失败：%w",err): default: }
	}
}

func (p *LXElectronUserSourceProcess) IsRunning() bool {
	if p==nil { return false }
	select { case <-p.done: return false; default: return true }
}
func (p *LXElectronUserSourceProcess) Snapshot() LXSourceRuntimeResult {
	if p==nil { return LXSourceRuntimeResult{} }
	p.resultMu.RLock(); defer p.resultMu.RUnlock(); return p.result
}
func (p *LXElectronUserSourceProcess) Updates() <-chan LXUserSourceUpdate {
	if p==nil { return nil }; return p.updates
}
func (p *LXElectronUserSourceProcess) SetProxy(proxy config.LXUserSourceProxyConfig) error {
	if err:=proxy.Validate(); err!=nil{return err}
	value:=lxElectronProxy{}
	if proxy.Enabled { value=lxElectronProxy{Host:proxy.Host,Port:proxy.Port} }
	return p.write(lxElectronInput{Operation:"proxy",Proxy:value})
}
func (p *LXElectronUserSourceProcess) Request(ctx context.Context, payload json.RawMessage) (json.RawMessage,error) {
	if p==nil{return nil,errors.New("洛雪 Electron 音源运行环境不可用")}
	if ctx==nil{ctx=context.Background()}
	if len(payload)==0 || len(payload)>64<<10{return nil,errors.New("LX 音源请求数据为空或超过 64 KB 限制")}
	if err:=ctx.Err();err!=nil{return nil,lxRequestContextError(err)}
	key:=fmt.Sprintf("request__%d",p.counter.Add(1))
	ch:=make(chan lxElectronPending,1)
	p.pendingMu.Lock();p.pending[key]=ch;p.pendingMu.Unlock()
	if err:=p.write(lxElectronInput{Operation:"request",RequestKey:key,Data:payload});err!=nil{
		p.pendingMu.Lock();delete(p.pending,key);p.pendingMu.Unlock();return nil,err
	}
	select{
	case result:=<-ch:return result.value,result.err
	case <-ctx.Done():
		p.pendingMu.Lock();delete(p.pending,key);p.pendingMu.Unlock()
		return nil,lxRequestContextError(ctx.Err())
	case <-p.done:
		p.pendingMu.Lock();delete(p.pending,key);p.pendingMu.Unlock()
		return nil,errors.New("洛雪 Electron 音源运行环境已退出")
	}
}
func (p *LXElectronUserSourceProcess) Close() error {
	if p==nil{return nil}
	p.closeOnce.Do(func(){
		_ = p.write(lxElectronInput{Operation:"close"})
		_ = p.stdin.Close()
		select{case <-p.done:case <-time.After(5*time.Second):
			if p.command.Process!=nil{_ = p.command.Process.Kill()}
			<-p.done
		}
	})
	return nil
}
