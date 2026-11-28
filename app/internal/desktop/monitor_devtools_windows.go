//go:build windows

package desktop

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"time"

	"github.com/gorilla/websocket"
)

type RendererMemoryTraceResult struct {
	Path       string    `json:"path"`
	EventCount int       `json:"event_count"`
	DumpGUID   string    `json:"dump_guid"`
	CapturedAt time.Time `json:"captured_at"`
}

type devToolsVersion struct {
	WebSocketDebuggerURL string `json:"webSocketDebuggerUrl"`
}

type devToolsMessage struct {
	ID     int             `json:"id"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params"`
	Result json.RawMessage `json:"result"`
	Error  *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

type traceDataCollected struct {
	Value []json.RawMessage `json:"value"`
}

// SetDevToolsPort enables the loopback-only Chromium DevTools endpoint in
// development builds. Production builds leave it disabled.
func (s *MonitorService) SetDevToolsPort(port int) {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.devToolsPort = port
	s.mu.Unlock()
}

// CaptureWebView2MemoryTrace asks Chromium MemoryInfra for one detailed global
// memory dump. The resulting trace contains allocator breakdowns grouped by
// native process ID, allowing the growing Renderer PID to be identified.

type RendererDOMTargetResult struct {
	TargetID string `json:"target_id"`
	Title string `json:"title"`
	URL string `json:"url"`
	Type string `json:"type"`
	Documents int `json:"documents"`
	Nodes int `json:"nodes"`
	JSEventListeners int `json:"js_event_listeners"`
	Error string `json:"error,omitempty"`
}

type RendererDOMDiagnosticsResult struct {
	Path string `json:"path"`
	TargetCount int `json:"target_count"`
	CapturedAt time.Time `json:"captured_at"`
	Targets []RendererDOMTargetResult `json:"targets"`
}

type devToolsTarget struct {
	ID string `json:"id"`
	Type string `json:"type"`
	Title string `json:"title"`
	URL string `json:"url"`
	WebSocketDebuggerURL string `json:"webSocketDebuggerUrl"`
}

// CaptureWebView2DOMDiagnostics enumerates page targets and reads Chromium's
// DOM counters independently for each page. Target IDs are recorded explicitly;
// they are not assumed to map one-to-one to native Renderer PIDs.
func (s *MonitorService) CaptureWebView2DOMDiagnostics() (RendererDOMDiagnosticsResult, error) {
	if s == nil { return RendererDOMDiagnosticsResult{}, fmt.Errorf("监控服务不可用") }
	s.mu.Lock()
	port := s.devToolsPort
	if port == 0 { s.mu.Unlock(); return RendererDOMDiagnosticsResult{}, fmt.Errorf("Chromium DOM 诊断仅在开发环境启用；请使用 task dev 启动") }
	if s.devToolsCaptureActive { s.mu.Unlock(); return RendererDOMDiagnosticsResult{}, fmt.Errorf("已有一次 Chromium 诊断正在采集") }
	s.devToolsCaptureActive = true
	logDir := ""
	if len(s.logDirs) > 0 { logDir = s.logDirs[0] }
	s.mu.Unlock()
	defer func(){ s.mu.Lock(); s.devToolsCaptureActive = false; s.mu.Unlock() }()
	if logDir == "" { return RendererDOMDiagnosticsResult{}, fmt.Errorf("应用日志目录不可用") }

	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	client := &http.Client{Timeout: 5*time.Second}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf("http://127.0.0.1:%d/json/list", port), nil)
	if err != nil { return RendererDOMDiagnosticsResult{}, err }
	response, err := client.Do(request)
	if err != nil { return RendererDOMDiagnosticsResult{}, fmt.Errorf("读取 Chromium 页面列表失败：%w", err) }
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK { return RendererDOMDiagnosticsResult{}, fmt.Errorf("Chromium 页面列表返回 HTTP %d", response.StatusCode) }
	var targets []devToolsTarget
	if err := json.NewDecoder(response.Body).Decode(&targets); err != nil { return RendererDOMDiagnosticsResult{}, fmt.Errorf("解析 Chromium 页面列表失败：%w", err) }

	result := RendererDOMDiagnosticsResult{CapturedAt: time.Now(), Targets: make([]RendererDOMTargetResult, 0)}
	for _, target := range targets {
		if target.Type != "page" || target.WebSocketDebuggerURL == "" { continue }
		item := RendererDOMTargetResult{TargetID: target.ID, Title: target.Title, URL: target.URL, Type: target.Type}
		debugURL, parseErr := url.Parse(target.WebSocketDebuggerURL)
		if parseErr != nil { item.Error = parseErr.Error(); result.Targets = append(result.Targets, item); continue }
		debugURL.Host = fmt.Sprintf("127.0.0.1:%d", port)
		headers := http.Header{}
		headers.Set("Origin", "http://localhost")
		conn, _, dialErr := (&websocket.Dialer{HandshakeTimeout: 4*time.Second}).DialContext(ctx, debugURL.String(), headers)
		if dialErr != nil { item.Error = fmt.Sprintf("连接页面诊断通道失败：%v", dialErr); result.Targets = append(result.Targets, item); continue }
		conn.SetReadLimit(4 << 20)
		counterJSON, commandErr := sendDevToolsCommand(conn, 1, "Memory.getDOMCounters", map[string]any{})
		_ = conn.Close()
		if commandErr != nil { item.Error = commandErr.Error() } else {
			var counters struct { Documents int `json:"documents"`; Nodes int `json:"nodes"`; JSEventListeners int `json:"jsEventListeners"` }
			if err := json.Unmarshal(counterJSON, &counters); err != nil { item.Error = fmt.Sprintf("解析 DOM 计数失败：%v", err) } else {
				item.Documents, item.Nodes, item.JSEventListeners = counters.Documents, counters.Nodes, counters.JSEventListeners
			}
		}
		result.Targets = append(result.Targets, item)
	}
	result.TargetCount = len(result.Targets)
	if result.TargetCount == 0 { return result, fmt.Errorf("Chromium 没有发现可诊断的 page Target") }
	if err := os.MkdirAll(logDir, 0o700); err != nil { return RendererDOMDiagnosticsResult{}, fmt.Errorf("创建诊断文件目录失败：%w", err) }
	filename := "webview2-dom-" + result.CapturedAt.Format("20060102-150405") + ".json"
	result.Path = filepath.Join(logDir, filename)
	data, err := json.MarshalIndent(result, "", "  ")
	if err != nil { return RendererDOMDiagnosticsResult{}, fmt.Errorf("序列化 DOM 诊断结果失败：%w", err) }
	if err := os.WriteFile(result.Path, data, 0o600); err != nil { return RendererDOMDiagnosticsResult{}, fmt.Errorf("保存 DOM 诊断结果失败：%w", err) }
	return result, nil
}

func (s *MonitorService) CaptureWebView2MemoryTrace() (RendererMemoryTraceResult, error) {
	if s == nil {
		return RendererMemoryTraceResult{}, fmt.Errorf("监控服务不可用")
	}
	s.mu.Lock()
	port := s.devToolsPort
	if port == 0 {
		s.mu.Unlock()
		return RendererMemoryTraceResult{}, fmt.Errorf("Chromium 内存诊断仅在开发环境启用；请使用 task dev 启动")
	}
	if s.devToolsCaptureActive {
		s.mu.Unlock()
		return RendererMemoryTraceResult{}, fmt.Errorf("已有一次内存快照正在采集")
	}
	s.devToolsCaptureActive = true
	logDir := ""
	if len(s.logDirs) > 0 {
		logDir = s.logDirs[0]
	}
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		s.devToolsCaptureActive = false
		s.mu.Unlock()
	}()
	if logDir == "" {
		return RendererMemoryTraceResult{}, fmt.Errorf("应用日志目录不可用")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	client := &http.Client{Timeout: 4 * time.Second}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf("http://127.0.0.1:%d/json/version", port), nil)
	if err != nil {
		return RendererMemoryTraceResult{}, err
	}
	response, err := client.Do(request)
	if err != nil {
		return RendererMemoryTraceResult{}, fmt.Errorf("连接 Chromium DevTools 失败：%w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return RendererMemoryTraceResult{}, fmt.Errorf("Chromium DevTools 返回 HTTP %d", response.StatusCode)
	}
	var version devToolsVersion
	if err := json.NewDecoder(response.Body).Decode(&version); err != nil {
		return RendererMemoryTraceResult{}, fmt.Errorf("读取 Chromium DevTools 信息失败：%w", err)
	}
	if version.WebSocketDebuggerURL == "" {
		return RendererMemoryTraceResult{}, fmt.Errorf("Chromium 未提供 DevTools WebSocket 地址")
	}
	debugURL, err := url.Parse(version.WebSocketDebuggerURL)
	if err != nil {
		return RendererMemoryTraceResult{}, err
	}
	debugURL.Host = fmt.Sprintf("127.0.0.1:%d", port)
	headers := http.Header{}
	headers.Set("Origin", "http://localhost")
	conn, _, err := (&websocket.Dialer{HandshakeTimeout: 5 * time.Second}).DialContext(ctx, debugURL.String(), headers)
	if err != nil {
		return RendererMemoryTraceResult{}, fmt.Errorf("连接 Chromium 诊断通道失败：%w", err)
	}
	defer conn.Close()
	conn.SetReadLimit(128 << 20)

	processInfo, err := sendDevToolsCommand(conn, 1, "SystemInfo.getProcessInfo", map[string]any{})
	if err != nil {
		return RendererMemoryTraceResult{}, fmt.Errorf("读取 Chromium 进程清单失败：%w", err)
	}
	if _, err := sendDevToolsCommand(conn, 2, "Tracing.start", map[string]any{
		"categories":    "disabled-by-default-memory-infra",
		"transferMode":  "ReportEvents",
	}); err != nil {
		return RendererMemoryTraceResult{}, fmt.Errorf("启动 Chromium 内存跟踪失败：%w", err)
	}
	events := make([]json.RawMessage, 0, 256)
	dumpResponse, err := sendDevToolsCommand(conn, 3, "Tracing.requestMemoryDump", map[string]any{
		"deterministic": false,
		"levelOfDetail": "detailed",
	}, &events)
	if err != nil {
		_, _ = sendDevToolsCommand(conn, 4, "Tracing.end", map[string]any{})
		return RendererMemoryTraceResult{}, fmt.Errorf("请求 Chromium 内存快照失败：%w", err)
	}
	var dump struct {
		DumpGUID string `json:"dumpGuid"`
		Success  bool   `json:"success"`
	}
	if err := json.Unmarshal(dumpResponse, &dump); err != nil || !dump.Success {
		_, _ = sendDevToolsCommand(conn, 4, "Tracing.end", map[string]any{})
		return RendererMemoryTraceResult{}, fmt.Errorf("Chromium 未能生成完整内存快照")
	}

	if err := writeDevToolsCommand(conn, 4, "Tracing.end", map[string]any{}); err != nil {
		return RendererMemoryTraceResult{}, fmt.Errorf("结束 Chromium 内存跟踪失败：%w", err)
	}
	endAcknowledged, tracingComplete := false, false
	deadline := time.Now().Add(20 * time.Second)
	for !endAcknowledged || !tracingComplete {
		_ = conn.SetReadDeadline(deadline)
		var message devToolsMessage
		if err := conn.ReadJSON(&message); err != nil {
			return RendererMemoryTraceResult{}, fmt.Errorf("读取 Chromium 内存跟踪数据失败：%w", err)
		}
		if message.ID == 4 {
			endAcknowledged = true
			if message.Error != nil {
				return RendererMemoryTraceResult{}, fmt.Errorf("Chromium 结束跟踪失败：%s", message.Error.Message)
			}
		}
		switch message.Method {
		case "Tracing.dataCollected":
			var batch traceDataCollected
			if err := json.Unmarshal(message.Params, &batch); err == nil {
				events = append(events, batch.Value...)
			}
		case "Tracing.tracingComplete":
			tracingComplete = true
		}
		if len(events) > 500000 {
			return RendererMemoryTraceResult{}, fmt.Errorf("内存跟踪事件数量异常，已停止保存")
		}
	}
	if len(events) == 0 {
		return RendererMemoryTraceResult{}, fmt.Errorf("Chromium 没有返回 MemoryInfra 数据；当前 WebView2 Runtime 可能不支持该诊断类别")
	}

	capturedAt := time.Now()
	trace := struct {
		TraceEvents []json.RawMessage `json:"traceEvents"`
		Metadata    map[string]any    `json:"metadata"`
	}{
		TraceEvents: events,
		Metadata: map[string]any{
			"capturedAt": capturedAt.Format(time.RFC3339Nano),
			"dumpGuid":   dump.DumpGUID,
			"browser":    version,
			"processInfo": json.RawMessage(processInfo),
			"purpose":    "My-LiveHub WebView2 Renderer MemoryInfra snapshot",
		},
	}
	data, err := json.Marshal(trace)
	if err != nil {
		return RendererMemoryTraceResult{}, fmt.Errorf("序列化内存跟踪数据失败：%w", err)
	}
	if err := os.MkdirAll(logDir, 0o700); err != nil {
		return RendererMemoryTraceResult{}, fmt.Errorf("创建诊断文件目录失败：%w", err)
	}
	filename := "webview2-memory-" + capturedAt.Format("20060102-150405") + ".json"
	path := filepath.Join(logDir, filename)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return RendererMemoryTraceResult{}, fmt.Errorf("保存内存快照失败：%w", err)
	}
	return RendererMemoryTraceResult{
		Path: path, EventCount: len(events), DumpGUID: dump.DumpGUID, CapturedAt: capturedAt,
	}, nil
}

func writeDevToolsCommand(conn *websocket.Conn, id int, method string, params any) error {
	_ = conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
	return conn.WriteJSON(map[string]any{"id": id, "method": method, "params": params})
}

func sendDevToolsCommand(conn *websocket.Conn, id int, method string, params any, collected ...*[]json.RawMessage) (json.RawMessage, error) {
	if err := writeDevToolsCommand(conn, id, method, params); err != nil {
		return nil, err
	}
	deadline := time.Now().Add(8 * time.Second)
	for {
		_ = conn.SetReadDeadline(deadline)
		var message devToolsMessage
		if err := conn.ReadJSON(&message); err != nil {
			return nil, err
		}
		if message.Method == "Tracing.dataCollected" && len(collected) > 0 && collected[0] != nil {
			var batch traceDataCollected
			if json.Unmarshal(message.Params, &batch) == nil {
				*collected[0] = append(*collected[0], batch.Value...)
			}
		}
		if message.ID != id {
			continue
		}
		if message.Error != nil {
			return nil, fmt.Errorf("CDP %s: %s", method, message.Error.Message)
		}
		return message.Result, nil
	}
}
