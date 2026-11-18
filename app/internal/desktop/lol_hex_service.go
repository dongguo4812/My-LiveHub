package desktop

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
)

const lolHexStopCleanupTimeout = 36 * time.Second

type LOLHexStatus struct {
	State   string `json:"state"`
	Running bool   `json:"running"`
	Owned   bool   `json:"owned"`
	External bool  `json:"external"`
	Port    int    `json:"port"`
	BaseURL string `json:"base_url"`
	PID     int    `json:"pid,omitempty"`
	Error   string `json:"error,omitempty"`
}

type LOLHexService struct {
	runtimeDir string
	configPath   string
	pythonPath   string
	host         string
	port         int
	client       *http.Client

	mu       sync.Mutex
	owned    bool
	launch   *exec.Cmd
	lastErr  string
	token    string
}

func NewLOLHexService(runtimeDir, configPath string) (*LOLHexService, error) {
	if runtimeDir == "" || configPath == "" {
		return nil, errors.New("LOL Hex 内置运行目录和配置文件路径不能为空")
	}
	if runtime.GOOS != "windows" {
		return nil, errors.New("LOL Hex 当前仅支持 Windows")
	}
	configBytes, err := os.ReadFile(configPath)
	if err != nil {
		return nil, fmt.Errorf("读取 LOL Hex 配置失败：%w", err)
	}
	var cfg struct {
		Host string `json:"host"`
		Port int    `json:"port"`
	}
	if err := json.Unmarshal(configBytes, &cfg); err != nil {
		return nil, fmt.Errorf("解析 LOL Hex 配置失败：%w", err)
	}
	if cfg.Host == "" || cfg.Host == "0.0.0.0" || cfg.Host == "::" {
		cfg.Host = "127.0.0.1"
	}
	if cfg.Port < 1 || cfg.Port > 65535 {
		cfg.Port = 8767
	}
	pythonPath := strings.TrimSpace(os.Getenv("MY_LIVEHUB_PYTHON"))
	if pythonPath == "" {
		pythonPath = "pythonw"
	}
	return &LOLHexService{
		runtimeDir: runtimeDir,
		configPath: configPath,
		pythonPath: pythonPath,
		host: cfg.Host,
		port: cfg.Port,
		client: &http.Client{Timeout: 2 * time.Second},
	}, nil
}

func (s *LOLHexService) baseURL() string {
	host := s.host
	if strings.Contains(host, ":") && !strings.HasPrefix(host, "[") {
		host = "[" + host + "]"
	}
	return fmt.Sprintf("http://%s:%d", host, s.port)
}

func (s *LOLHexService) health() (bool, int) {
	request, err := http.NewRequest(http.MethodGet, s.baseURL()+"/api/client-status", nil)
	if err != nil { return false, 0 }
	response, err := s.client.Do(request)
	if err != nil { return false, 0 }
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK { return false, 0 }
	var payload map[string]json.RawMessage
	if json.NewDecoder(response.Body).Decode(&payload) != nil { return false, 0 }
	if _, ok := payload["recognized"]; !ok { return false, 0 }
	return true, 0
}

type lolHexPIDRecord struct {
	PID int `json:"pid"`
	ManagerToken string `json:"manager_token"`
}

func (s *LOLHexService) readPIDRecord() lolHexPIDRecord {
	// Source mode writes beside launcher.py; the packaged EXE writes beside
	// itself under dist/lol_hex_display. Check both locations.
	paths := []string{
		filepath.Join(s.runtimeDir, "lol_hex_display.pid"),
		filepath.Join(s.runtimeDir, "dist", "lol_hex_display", "lol_hex_display.pid"),
	}
	var fallback lolHexPIDRecord
	for _, path := range paths {
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		var record lolHexPIDRecord
		if json.Unmarshal(data, &record) != nil || record.PID <= 0 {
			continue
		}
		if s.token != "" && record.ManagerToken == s.token {
			return record
		}
		if fallback.PID == 0 {
			fallback = record
		}
	}
	return fallback
}

func (s *LOLHexService) readPID() int { return s.readPIDRecord().PID }

func (s *LOLHexService) GetStatus() LOLHexStatus {
	if s == nil { return LOLHexStatus{State: "unavailable", Error: "LOL Hex 服务不可用"} }
	s.mu.Lock()
	owned, lastErr := s.owned, s.lastErr
	s.mu.Unlock()
	healthy, _ := s.health()
	pid := s.readPID()
	status := LOLHexStatus{State: "stopped", Owned: false, External: false, Port: s.port, BaseURL: s.baseURL(), PID: pid, Error: lastErr}
	if healthy {
		status.State, status.Running = "running", true
		status.Owned = owned && s.token != "" && s.readPIDRecord().ManagerToken == s.token
		status.External = !status.Owned
		return status
	}
	if owned {
		status.State = "starting"
		status.Owned = true
	}
	return status
}

func (s *LOLHexService) Start() (LOLHexStatus, error) {
	if s == nil { return LOLHexStatus{}, errors.New("LOL Hex 服务不可用") }
	s.mu.Lock()
	defer s.mu.Unlock()
	if healthy, _ := s.health(); healthy {
		// Preserve this service instance's ownership on repeated Start clicks.
		// Never adopt an instance carrying a different or missing manager token.
		return s.GetStatusUnlocked(), nil
	}
	if s.owned {
		return s.GetStatusUnlocked(), errors.New("LOL Hex 已由 My-LiveHub 启动，正在等待服务恢复")
	}
	if _, err := os.Stat(filepath.Join(s.runtimeDir, "launcher.py")); err != nil {
		return LOLHexStatus{}, fmt.Errorf("找不到 LOL Hex 启动入口：%w", err)
	}
	var command *exec.Cmd
	exePath := filepath.Join(s.runtimeDir, "dist", "lol_hex_display", "lol_hex_display.exe")
	if _, err := os.Stat(exePath); err != nil {
		exePath = filepath.Join(s.runtimeDir, "lol_hex_display.exe")
	}
	if _, err := os.Stat(exePath); err == nil {
		command = exec.Command(exePath, "--config", s.configPath)
	} else {
		python := filepath.Join(s.runtimeDir, ".venv", "Scripts", "pythonw.exe")
		if _, err := os.Stat(python); err != nil {
			python = filepath.Join(s.runtimeDir, ".venv", "Scripts", "python.exe")
			if _, statErr := os.Stat(python); statErr != nil {
				python, err = exec.LookPath(s.pythonPath)
				if err != nil && s.pythonPath == "pythonw" {
					python, err = exec.LookPath("python")
				}
				if err != nil {
					return LOLHexStatus{}, fmt.Errorf("未找到 Python 运行环境。请在内置运行目录创建 .venv，或设置 MY_LIVEHUB_PYTHON：%w", err)
				}
			}
		}
		command = exec.Command(python, "launcher.py", "--config", s.configPath)
	}
	command.Dir = s.runtimeDir
	tokenBytes := make([]byte, 24)
	if _, err := rand.Read(tokenBytes); err != nil { return LOLHexStatus{}, fmt.Errorf("生成内置模块进程身份令牌失败：%w", err) }
	s.token = hex.EncodeToString(tokenBytes)
	startupStatusPath := filepath.Join(os.TempDir(), "MyLiveHub-LOLHex-"+s.token+".json")
	_ = os.Remove(startupStatusPath)
	defer os.Remove(startupStatusPath)
	// Explicit argv survives the ShellExecuteW UAC relaunch; retain the
	// environment variable for compatibility with existing launch paths.
	command.Args = append(command.Args, "--manager-token", s.token, "--startup-status", startupStatusPath)
	command.Env = append(os.Environ(), "PYTHONUNBUFFERED=1", "LOL_HEX_MANAGED_BY_MY_LIVEHUB=1", "LOL_HEX_MANAGER_TOKEN="+s.token)
	// Keep early Python startup tracebacks in the main application log; the
	// extension also retains its own structured daily log after initialization.
	command.Stdout, command.Stderr = log.Writer(), log.Writer()
	if err := command.Start(); err != nil {
		s.lastErr = err.Error()
		return LOLHexStatus{}, fmt.Errorf("启动 LOL Hex 失败：%w", err)
	}
	s.launch = command
	s.owned = true
	s.lastErr = ""
	launchDone := make(chan error, 1)
	go func(cmd *exec.Cmd) { launchDone <- cmd.Wait() }(command)

	deadline := time.Now().Add(35 * time.Second)
	for time.Now().Before(deadline) {
		if data, readErr := os.ReadFile(startupStatusPath); readErr == nil {
			var startup struct {
				State   string `json:"state"`
				Message string `json:"message"`
			}
			if json.Unmarshal(data, &startup) == nil && startup.State == "failed" {
				s.owned, s.launch, s.token = false, nil, ""
				s.lastErr = startup.Message
				if s.lastErr == "" {
					s.lastErr = "LOL Hex 启动失败，初始化资源未能完成。"
				}
				return s.GetStatusUnlocked(), errors.New(s.lastErr)
			}
		}
		select {
		case waitErr := <-launchDone:
			launchDone = nil
			if waitErr != nil {
				s.owned, s.launch, s.token = false, nil, ""
				var exitErr *exec.ExitError
				if errors.As(waitErr, &exitErr) && exitErr.ExitCode() == 1223 {
					s.lastErr = "已取消 LOL Hex 管理员权限请求，模块未启动。"
				} else {
					s.lastErr = fmt.Sprintf("LOL Hex 启动入口提前退出：%v", waitErr)
				}
				return s.GetStatusUnlocked(), errors.New(s.lastErr)
			}
			// Exit code 0 is expected when the unelevated launcher successfully
			// hands off to its elevated child. Continue waiting for the child PID.
		default:
		}
		if healthy, _ := s.health(); healthy {
			record := s.readPIDRecord()
			if record.ManagerToken == s.token {
				return s.GetStatusUnlocked(), nil
			}
			if record.PID > 0 {
				s.owned = false
				s.lastErr = "检测到已有独立启动的 LOL Hex 实例；为避免接管或关闭外部进程，本次启动不会认领该实例。"
				return s.GetStatusUnlocked(), errors.New(s.lastErr)
			}
		}
		time.Sleep(250 * time.Millisecond)
	}
	s.lastErr = "等待 LOL Hex 服务启动超时。请检查 UAC 提权提示和模块日志。"
	return s.GetStatusUnlocked(), errors.New(s.lastErr)
}

func (s *LOLHexService) GetStatusUnlocked() LOLHexStatus {
	healthy, _ := s.health()
	record := s.readPIDRecord()
	pid := record.PID
	owned := s.owned && s.token != "" && record.ManagerToken == s.token
	state := "stopped"
	if s.owned && !healthy { state = "starting" }
	if healthy { state = "running" }
	return LOLHexStatus{State: state, Running: healthy, Owned: owned, External: healthy && !owned, Port: s.port, BaseURL: s.baseURL(), PID: pid, Error: s.lastErr}
}

func (s *LOLHexService) Stop() (LOLHexStatus, error) {
	if s == nil { return LOLHexStatus{}, nil }
	s.mu.Lock()
	defer s.mu.Unlock()
	healthy, _ := s.health()
	if !s.owned {
		if healthy { return s.GetStatusUnlocked(), errors.New("当前 LOL Hex 由外部启动，My-LiveHub 不会关闭它") }
		return s.GetStatusUnlocked(), nil
	}
	if !healthy {
		// HTTP 服务停止不代表 Python 进程已经完成后台线程、日志和单实例锁清理。
		// 等待由本次 manager token 标识的 PID 记录被进程最终清理；Python 端保证
		// 先释放单实例互斥锁，再删除 PID 文件，因而观察到记录消失后才允许重新启动。
		record := s.readPIDRecord()
		if record.PID <= 0 || record.ManagerToken != s.token || s.token == "" {
			return s.GetStatusUnlocked(), errors.New("LOL Hex 当前没有健康响应，且无法核验本次启动的进程身份；已保留进程管理状态。")
		}
		deadline := time.Now().Add(lolHexStopCleanupTimeout)
		for time.Now().Before(deadline) {
			current := s.readPIDRecord()
			if current.PID <= 0 || current.ManagerToken != s.token {
				s.owned, s.launch = false, nil
				s.lastErr = ""
				return s.GetStatusUnlocked(), nil
			}
			time.Sleep(100 * time.Millisecond)
		}
		return s.GetStatusUnlocked(), errors.New("LOL Hex HTTP 服务已停止，但进程尚未完成退出清理；请稍后重试，未清除进程管理状态。")
	}
	record := s.readPIDRecord()
	if s.token == "" || record.ManagerToken != s.token {
		return s.GetStatusUnlocked(), errors.New("无法核验 LOL Hex 进程属于本次 My-LiveHub 启动；拒绝发送关闭请求")
	}
	payload, _ := json.Marshal(map[string]string{"action": "shutdown"})
	request, _ := http.NewRequest(http.MethodPost, s.baseURL()+"/api/action", bytes.NewReader(payload))
	request.Header.Set("Content-Type", "application/json")
	response, err := s.client.Do(request)
	if err != nil {
		// Flask 在响应后开始退出时可能提前关闭连接；继续核验服务状态。
		if !errors.Is(err, net.ErrClosed) { s.lastErr = err.Error() }
	} else { response.Body.Close() }
	// HTTP 服务停止后，Python 进程仍需完成后台线程清理并释放单实例互斥锁。
	// Python 端保证先释放互斥锁、再删除带 manager token 的 PID 文件；
	// 因此只有观察到本次 PID 记录消失，才能允许下一次 Start。
	deadline := time.Now().Add(lolHexStopCleanupTimeout)
	for time.Now().Before(deadline) {
		healthy, _ := s.health()
		if !healthy {
			current := s.readPIDRecord()
			if current.PID <= 0 || current.ManagerToken != s.token {
				s.owned, s.launch = false, nil
				s.lastErr = ""
				return s.GetStatusUnlocked(), nil
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	if healthy, _ := s.health(); !healthy {
		return s.GetStatusUnlocked(), errors.New("LOL Hex HTTP 服务已停止，但进程尚未释放单实例锁并完成退出清理；已保留进程管理状态，请稍后重试")
	}
	return s.GetStatusUnlocked(), errors.New("LOL Hex 未在规定时间内退出；为避免误关其他进程，没有强制结束进程")
}

func (s *LOLHexService) StopIfOwned() error {
	if s == nil { return nil }
	s.mu.Lock()
	owned := s.owned
	s.mu.Unlock()
	if owned {
		_, err := s.Stop()
		if err != nil {
			s.mu.Lock()
			s.lastErr = err.Error()
			s.mu.Unlock()
		}
		return err
	}
	return nil
}
