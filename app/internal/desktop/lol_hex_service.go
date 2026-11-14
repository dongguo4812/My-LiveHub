package desktop

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"context"
	"encoding/json"
	"errors"
	"fmt"
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
	extensionDir string
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

func NewLOLHexService(extensionDir, configPath string) (*LOLHexService, error) {
	if extensionDir == "" || configPath == "" {
		return nil, errors.New("LOL Hex 扩展目录和配置文件路径不能为空")
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
		pythonPath = "python"
	}
	return &LOLHexService{
		extensionDir: extensionDir,
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
	data, err := os.ReadFile(filepath.Join(s.extensionDir, "lol_hex_display.pid"))
	if err != nil { return lolHexPIDRecord{} }
	var record lolHexPIDRecord
	if json.Unmarshal(data, &record) != nil || record.PID <= 0 { return lolHexPIDRecord{} }
	return record
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
		s.owned = false
		return s.GetStatusUnlocked(), nil
	}
	if s.owned {
		return s.GetStatusUnlocked(), errors.New("LOL Hex 已由 My-LiveHub 启动，正在等待服务恢复")
	}
	if _, err := os.Stat(filepath.Join(s.extensionDir, "launcher.py")); err != nil {
		return LOLHexStatus{}, fmt.Errorf("找不到 LOL Hex 启动入口：%w", err)
	}
	var command *exec.Cmd
	exePath := filepath.Join(s.extensionDir, "dist", "lol_hex_display", "lol_hex_display.exe")
	if _, err := os.Stat(exePath); err != nil {
		exePath = filepath.Join(s.extensionDir, "lol_hex_display.exe")
	}
	if _, err := os.Stat(exePath); err == nil {
		command = exec.Command(exePath, "--config", s.configPath)
	} else {
		python := filepath.Join(s.extensionDir, ".venv", "Scripts", "python.exe")
		if _, err := os.Stat(python); err != nil {
			python, err = exec.LookPath(s.pythonPath)
			if err != nil {
				return LOLHexStatus{}, fmt.Errorf("未找到 Python 运行环境。请在扩展目录创建 .venv，或设置 MY_LIVEHUB_PYTHON：%w", err)
			}
		}
		command = exec.Command(python, "launcher.py", "--config", s.configPath)
	}
	command.Dir = s.extensionDir
	tokenBytes := make([]byte, 24)
	if _, err := rand.Read(tokenBytes); err != nil { return LOLHexStatus{}, fmt.Errorf("生成扩展进程身份令牌失败：%w", err) }
	s.token = hex.EncodeToString(tokenBytes)
	command.Env = append(os.Environ(), "PYTHONUNBUFFERED=1", "LOL_HEX_MANAGED_BY_MY_LIVEHUB=1", "LOL_HEX_MANAGER_TOKEN="+s.token)
	command.Stdout, command.Stderr = nil, nil
	if err := command.Start(); err != nil {
		s.lastErr = err.Error()
		return LOLHexStatus{}, fmt.Errorf("启动 LOL Hex 失败：%w", err)
	}
	s.launch = command
	s.owned = true
	s.lastErr = ""
	go func(cmd *exec.Cmd) { _ = cmd.Wait() }(command)

	deadline := time.Now().Add(35 * time.Second)
	for time.Now().Before(deadline) {
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
	s.lastErr = "等待 LOL Hex 服务启动超时。请检查 UAC 提权提示和扩展日志。"
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
	if healthy {
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
	}
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if healthy, _ := s.health(); !healthy {
			s.owned, s.launch = false, nil
			s.lastErr = ""
			return s.GetStatusUnlocked(), nil
		}
		time.Sleep(200 * time.Millisecond)
	}
	return s.GetStatusUnlocked(), errors.New("LOL Hex 未在规定时间内退出；为避免误关其他进程，没有强制结束进程")
}

func (s *LOLHexService) StopIfOwned() {
	if s == nil { return }
	s.mu.Lock()
	owned := s.owned
	s.mu.Unlock()
	if owned {
		if _, err := s.Stop(); err != nil {
			s.mu.Lock()
			s.lastErr = err.Error()
			s.mu.Unlock()
		}
	}
}
