package music

import (
	"bufio"
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
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
	conn net.Conn
	writeMu sync.Mutex
	pendingMu sync.Mutex
	pending map[string]chan lxElectronPending
	counter atomic.Uint64
	windowReady chan error
	initDone chan struct{}
	initOnce sync.Once
	initMu sync.RWMutex
	initErr error
	updates chan LXUserSourceUpdate
	done chan struct{}
	closeOnce sync.Once
	resultMu sync.RWMutex
	result LXSourceRuntimeResult
}

func findLXElectronRuntime() (string, []string, error) {
	if override := strings.TrimSpace(os.Getenv("MY_LIVEHUB_LX_RUNTIME_EXE")); override != "" {
		if info, err := os.Stat(override); err == nil && !info.IsDir() { return override, nil, nil }
		return "", nil, fmt.Errorf("指定的洛雪 Electron 运行程序不存在：%s", override)
	}
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

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil { return nil, fmt.Errorf("创建洛雪 Electron 本机 IPC 监听失败：%w", err) }
	defer listener.Close()
	tokenBytes := make([]byte, 32)
	if _, err := rand.Read(tokenBytes); err != nil { return nil, fmt.Errorf("生成洛雪 Electron IPC 验证令牌失败：%w", err) }
	token := hex.EncodeToString(tokenBytes)

	commandArgs := append([]string{}, args...)
	commandArgs = append(commandArgs, "--lx-runtime-ipc", listener.Addr().String(), "--lx-runtime-token", token)
	command := exec.Command(executable, commandArgs...)
	command.Stdout = io.Discard
	command.Stderr = io.Discard
	if err := command.Start(); err != nil { return nil, fmt.Errorf("启动洛雪 Electron 音源运行环境失败：%w", err) }

	if tcpListener, ok := listener.(*net.TCPListener); ok {
		_ = tcpListener.SetDeadline(time.Now().Add(lxElectronReadyTimeout))
	}
	conn, err := listener.Accept()
	if err != nil {
		if command.Process != nil { _ = command.Process.Kill() }
		_ = command.Wait()
		return nil, fmt.Errorf("等待洛雪 Electron 本机 IPC 连接失败：%w", err)
	}
	reader := bufio.NewReader(conn)
	helloLine, err := reader.ReadString('\n')
	if err != nil {
		_ = conn.Close()
		if command.Process != nil { _ = command.Process.Kill() }
		_ = command.Wait()
		return nil, fmt.Errorf("读取洛雪 Electron IPC 握手失败：%w", err)
	}
	var hello struct { Type string `json:"type"`; Token string `json:"token"` }
	if err := json.Unmarshal([]byte(helloLine), &hello); err != nil || hello.Type != "hello" || subtle.ConstantTimeCompare([]byte(hello.Token), []byte(token)) != 1 {
		_ = conn.Close()
		if command.Process != nil { _ = command.Process.Kill() }
		_ = command.Wait()
		return nil, errors.New("洛雪 Electron IPC 握手验证失败")
	}

	process := &LXElectronUserSourceProcess{
		command: command, conn: conn, pending: make(map[string]chan lxElectronPending),
		windowReady: make(chan error, 1), initDone: make(chan struct{}), updates: make(chan LXUserSourceUpdate, 8), done: make(chan struct{}),
	}
	go process.readLoop(reader)
	proxyValue := lxElectronProxy{}
	if proxy.Enabled { proxyValue = lxElectronProxy{Host: proxy.Host, Port: proxy.Port} }
	init := lxElectronInput{
		Operation:"init", Script:script, Proxy:proxyValue, ID:metadata.ID,
		Name:metadata.Name, Description:metadata.Description, Version:metadata.Version,
		Author:metadata.Author, Homepage:metadata.Homepage,
	}
	if err := process.write(init); err != nil { _ = process.Close(); return nil, err }
	select {
	case err := <-process.windowReady:
		if err != nil { _ = process.Close(); return nil, err }
		return process, nil
	case <-ctx.Done():
		_ = process.Close(); return nil, ctx.Err()
	case <-time.After(lxElectronReadyTimeout):
		_ = process.Close(); return nil, errors.New("洛雪 Electron 音源窗口启动超时")
	case <-process.done:
		return nil, errors.New("洛雪 Electron 音源运行环境在窗口启动前退出")
	}
}

func (p *LXElectronUserSourceProcess) write(message lxElectronInput) error {
	if p == nil { return errors.New("洛雪 Electron 音源运行环境不可用") }
	data, err := json.Marshal(message)
	if err != nil { return err }
	p.writeMu.Lock()
	defer p.writeMu.Unlock()
	select { case <-p.done: return errors.New("洛雪 Electron 音源运行环境已关闭"); default: }
	if p.conn == nil { return errors.New("洛雪 Electron IPC 连接不可用") }; if _, err := p.conn.Write(append(data, '\n')); err != nil { return fmt.Errorf("发送洛雪音源消息失败：%w", err) }
	return nil
}

func (p *LXElectronUserSourceProcess) readLoop(reader io.Reader) {
	defer func() {
		p.finishInitialization(errors.New("洛雪 Electron 音源运行环境已退出"))
		select { case p.windowReady <- errors.New("洛雪 Electron 音源运行环境已退出"): default: }
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
			select { case p.windowReady <- nil: default: }
		case "error":
			err := errors.New(message.Message)
			select { case p.windowReady <- err: default: }
			p.finishInitialization(err)
		case "event":
			var envelope lxElectronEnvelope
			if err := json.Unmarshal(message.Params, &envelope); err != nil { continue }
			switch message.Name {
			case "userApi_init":
				if !envelope.Status {
					p.finishInitialization(errors.New(envelope.Message))
					continue
				}
				var data lxElectronInitData
				if err := json.Unmarshal(envelope.Data, &data); err != nil {
					p.finishInitialization(fmt.Errorf("解析洛雪音源初始化结果失败：%w",err))
					continue
				}
				result := LXSourceRuntimeResult{Runtime:LXUserSourceRuntimeVersion, Initialized:true, Sources:append(json.RawMessage(nil), envelope.Data...)}
				for key, source := range data.Sources {
					if source.Type != "music" { continue }
					result.Platforms = append(result.Platforms,key)
					result.DeclaredSources = append(result.DeclaredSources,LXDeclaredSource{Key:key,Type:source.Type,Actions:append([]string(nil),source.Actions...),Qualitys:append([]string(nil),source.Qualitys...)})
				}
				p.resultMu.Lock(); p.result=result; p.resultMu.Unlock()
				p.finishInitialization(nil)
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
		readErr := fmt.Errorf("读取洛雪 Electron 音源进程输出失败：%w",err)
		select { case p.windowReady <- readErr: default: }
		p.finishInitialization(readErr)
	}
}

func (p *LXElectronUserSourceProcess) finishInitialization(err error) {
	if p == nil { return }
	p.initOnce.Do(func() {
		p.initMu.Lock()
		p.initErr = err
		p.initMu.Unlock()
		if err != nil {
			p.resultMu.Lock()
			p.result.Message = err.Error()
			p.resultMu.Unlock()
		}
		close(p.initDone)
	})
}

func (p *LXElectronUserSourceProcess) WaitInitialized(ctx context.Context) error {
	if p == nil { return errors.New("洛雪 Electron 音源运行环境不可用") }
	if ctx == nil { ctx = context.Background() }
	select {
	case <-p.initDone:
		p.initMu.RLock()
		err := p.initErr
		p.initMu.RUnlock()
		return err
	case <-ctx.Done():
		return ctx.Err()
	case <-p.done:
		return errors.New("洛雪 Electron 音源运行环境已退出")
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
	if err:=p.WaitInitialized(ctx);err!=nil{return nil,err}
	key:=fmt.Sprintf("request__%d",p.counter.Add(1))
	ch:=make(chan lxElectronPending,1)
	p.pendingMu.Lock();p.pending[key]=ch;p.pendingMu.Unlock()
	if err:=p.write(lxElectronInput{Operation:"request",RequestKey:key,Data:payload});err!=nil{
		p.pendingMu.Lock();delete(p.pending,key);p.pendingMu.Unlock();return nil,err
	}
	select{
	case result:=<-ch:
		if result.err != nil {
			return nil, result.err
		}
		return result.value,nil
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
		if p.conn != nil { _ = p.conn.Close() }
		select{case <-p.done:case <-time.After(5*time.Second):
			if p.command.Process!=nil{_ = p.command.Process.Kill()}
			<-p.done
		}
	})
	return nil
}
