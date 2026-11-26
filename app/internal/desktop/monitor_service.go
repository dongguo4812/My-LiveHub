package desktop

import (
	"bufio"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"regexp"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/dongguo4812/My-LiveHub/app/internal/platform"
)

type ProcessMemorySnapshot struct {
	ProcessID uint32
	ProcessWorkingSetBytes uint64
	ProcessPrivateBytes uint64
	WebView2ProcessCount int
	WebView2WorkingSetBytes uint64
	WebView2PrivateBytes uint64
}

type SystemMetricsSnapshot struct {
	ProcessID uint32 `json:"process_id"`
	ProcessWorkingSetBytes uint64 `json:"process_working_set_bytes"`
	ProcessPrivateBytes uint64 `json:"process_private_bytes"`
	WebView2ProcessCount int `json:"webview2_process_count"`
	WebView2WorkingSetBytes uint64 `json:"webview2_working_set_bytes"`
	WebView2PrivateBytes uint64 `json:"webview2_private_bytes"`
	MemoryBaselineReady bool `json:"memory_baseline_ready"`
	ProcessWorkingSetDeltaBytes int64 `json:"process_working_set_delta_bytes"`
	WebView2WorkingSetDeltaBytes int64 `json:"webview2_working_set_delta_bytes"`
	ProcessMemoryError string `json:"process_memory_error,omitempty"`
	UptimeSeconds int64 `json:"uptime_seconds"`
	GoHeapAllocBytes uint64 `json:"go_heap_alloc_bytes"`
	GoHeapSysBytes uint64 `json:"go_heap_sys_bytes"`
	GoTotalAllocBytes uint64 `json:"go_total_alloc_bytes"`
	Goroutines int `json:"goroutines"`
	CPUCount int `json:"cpu_count"`
	SystemCPUPercent float64 `json:"system_cpu_percent"`
	ProcessCPUPercent float64 `json:"process_cpu_percent"`
	CollectedAt time.Time `json:"collected_at"`
}

type MonitorLogEntry struct {
	Time string `json:"time"`
	Level string `json:"level"`
	Message string `json:"message"`
}

// WebView2InstanceSnapshot describes a Wails window that hosts a WebView2 instance.
type WebView2InstanceSnapshot struct {
	Name string `json:"name"`
	Purpose string `json:"purpose"`
	Kind string `json:"kind"`
	Status string `json:"status"`
	Visible bool `json:"visible"`
}

type FrontendHeartbeatSnapshot struct {
	LastSeenAt time.Time `json:"last_seen_at"`
	Stale bool `json:"stale"`
}

type MonitorService struct {
	startedAt time.Time
	logDirs []string
	mu sync.Mutex
	webViewApp *application.App
	platformManager *platform.Manager
	lastCPUAt time.Time
	lastSystemTotal uint64
	lastSystemIdle uint64
	lastProcessTotal uint64
	lastFrontendHeartbeat time.Time
	frontendHeartbeatStale bool
	frontendHeartbeatStarted bool
	frontendMonitoringSuspended bool
	memoryBaselineReady bool
	processWorkingSetBaseline uint64
	webView2WorkingSetBaseline uint64
}

func NewMonitorService(logDir string, additionalLogDirs ...string) *MonitorService {
	return NewMonitorServiceWithPlatformManager(logDir, nil, additionalLogDirs...)
}

func NewMonitorServiceWithPlatformManager(logDir string, manager *platform.Manager, additionalLogDirs ...string) *MonitorService {
	logDirs := make([]string, 0, 1+len(additionalLogDirs))
	if strings.TrimSpace(logDir) != "" { logDirs = append(logDirs, logDir) }
	for _, dir := range additionalLogDirs {
		if strings.TrimSpace(dir) == "" { continue }
		duplicate := false
		for _, existing := range logDirs { if filepath.Clean(existing) == filepath.Clean(dir) { duplicate = true; break } }
		if !duplicate { logDirs = append(logDirs, dir) }
	}
	return &MonitorService{startedAt: time.Now(), logDirs: logDirs, platformManager: manager}
}


// AttachWebView2Monitoring supplies the live Wails window registry context.
func (s *MonitorService) AttachWebView2Monitoring(app *application.App) {
	if s == nil { return }
	s.mu.Lock()
	s.webViewApp = app
	s.mu.Unlock()
}

// GetWebView2Instances returns the complete inventory of known application
// WebView2 windows, including known windows that have not been created yet.
func (s *MonitorService) GetWebView2Instances() []WebView2InstanceSnapshot {
	if s == nil { return []WebView2InstanceSnapshot{} }
	s.mu.Lock()
	app, manager := s.webViewApp, s.platformManager
	s.mu.Unlock()
	definitions := []WebView2InstanceSnapshot{
		{Name: "main-window", Purpose: "主程序界面；音乐播放、HTMLAudio 与 Web Audio 音频链路运行在此 WebView2 中", Kind: "主窗口"},
		{Name: "danmaku-overlay", Purpose: "弹幕透明展示与置顶窗口", Kind: "辅助窗口"},
		{Name: "music-lyrics-overlay", Purpose: "桌面歌词展示；不负责播放歌曲", Kind: "辅助窗口"},
		{Name: "bilibili-webview-player", Purpose: "Bilibili 网页视频播放器", Kind: "辅助窗口"},
	}
	if manager != nil {
		for _, platform := range manager.Snapshot() {
			name := "simulator-" + platform.PlatformID
			definitions = append(definitions, WebView2InstanceSnapshot{
				Name: name, Purpose: platform.PlatformID + " 互动事件模拟器", Kind: "辅助窗口",
			})
		}
	}
	sort.Slice(definitions, func(i, j int) bool {
		if definitions[i].Kind != definitions[j].Kind { return definitions[i].Kind == "主窗口" }
		return definitions[i].Name < definitions[j].Name
	})
	for i := range definitions {
		definitions[i].Status = "not_created"
		if app == nil { continue }
		window, ok := app.Window.GetByName(definitions[i].Name)
		if !ok || window == nil { continue }
		definitions[i].Visible = window.IsVisible()
		if definitions[i].Visible { definitions[i].Status = "visible" } else { definitions[i].Status = "hidden" }
	}
	return definitions
}

// RecordFrontendHeartbeat records that the main frontend JavaScript event loop
// completed a scheduled callback. A missing heartbeat is diagnostic evidence,
// not proof that the native UI thread or WebView2 renderer is dead.
func (s *MonitorService) RecordFrontendHeartbeat() {
	if s == nil { return }
	now := time.Now()
	s.mu.Lock()
	wasStale := s.frontendHeartbeatStale
	first := !s.frontendHeartbeatStarted
	s.lastFrontendHeartbeat = now
	s.frontendHeartbeatStale = false
	s.frontendHeartbeatStarted = true
	s.mu.Unlock()
	if first {
		log.Printf("frontend heartbeat: monitoring started")
	} else if wasStale {
		log.Printf("frontend heartbeat: recovered after interruption")
	}
}

// GetFrontendHeartbeatSnapshot returns the last frontend heartbeat without
// treating a missing timestamp as a measured failure.
func (s *MonitorService) GetFrontendHeartbeatSnapshot() FrontendHeartbeatSnapshot {
	if s == nil { return FrontendHeartbeatSnapshot{} }
	s.mu.Lock()
	defer s.mu.Unlock()
	return FrontendHeartbeatSnapshot{LastSeenAt: s.lastFrontendHeartbeat, Stale: s.frontendHeartbeatStale}
}

// SetFrontendMonitoringSuspended pauses stale-heartbeat detection while the
// native main window is hidden or minimised, when WebView timers may be throttled.
// Resuming grants the frontend a fresh response window before it is marked stale.
func (s *MonitorService) SetFrontendMonitoringSuspended(suspended bool) {
	if s == nil { return }
	s.mu.Lock()
	changed := s.frontendMonitoringSuspended != suspended
	s.frontendMonitoringSuspended = suspended
	if changed && !suspended {
		if s.frontendHeartbeatStarted {
			s.lastFrontendHeartbeat = time.Now()
		} else {
			s.startedAt = time.Now()
		}
	}
	s.mu.Unlock()
	if changed {
		if suspended {
			log.Printf("frontend heartbeat: stale detection suspended because main window is hidden or minimised")
		} else {
			log.Printf("frontend heartbeat: stale detection resumed after main window became visible")
		}
	}
}

// StartFrontendHeartbeatWatchdog logs one warning when the main frontend stops
// reporting heartbeats and one recovery message when it resumes.
func (s *MonitorService) StartFrontendHeartbeatWatchdog(checkEvery, staleAfter time.Duration) {
	if s == nil { return }
	if checkEvery <= 0 { checkEvery = 15 * time.Second }
	if staleAfter <= checkEvery { staleAfter = 45 * time.Second }
	go func() {
		ticker := time.NewTicker(checkEvery)
		defer ticker.Stop()
		for range ticker.C {
			now := time.Now()
			s.mu.Lock()
			lastSeen := s.lastFrontendHeartbeat
			lastActivity := s.startedAt
			if s.frontendHeartbeatStarted {
				lastActivity = lastSeen
			}
			alreadyStale := s.frontendHeartbeatStale
			suspended := s.frontendMonitoringSuspended
			if !suspended && !alreadyStale && !lastActivity.IsZero() && now.Sub(lastActivity) >= staleAfter {
				s.frontendHeartbeatStale = true
			}
			becameStale := !suspended && !alreadyStale && s.frontendHeartbeatStale
			s.mu.Unlock()
			if becameStale {
				if lastSeen.IsZero() {
					log.Printf("frontend heartbeat: STALE; no heartbeat received since application startup; threshold=%s; frontend may have failed to mount or renderer/event-loop may be unresponsive",
						staleAfter)
				} else {
					log.Printf("frontend heartbeat: STALE; last_seen=%s; threshold=%s; renderer/event-loop unresponsive or page suspended",
						lastSeen.Format(time.RFC3339Nano), staleAfter)
				}
			}
		}
	}()
}

// WriteFrontendLog persists frontend console output and uncaught runtime errors
// through the same application logger used by the rest of the Go process.
func (s *MonitorService) WriteFrontendLog(level, message string) {
	if s == nil {
		return
	}
	level = strings.ToUpper(strings.TrimSpace(level))
	switch level {
	case "DEBUG", "INFO", "WARN", "ERROR":
	default:
		level = "INFO"
	}
	message = strings.TrimSpace(message)
	if message == "" {
		return
	}
	message = strings.ReplaceAll(message, "\r", "")
	message = strings.ReplaceAll(message, "\n", "\\n")
	if len(message) > 32768 {
		message = message[:32768] + "…[truncated]"
	}
	log.Printf("frontend [%s] %s", level, message)
}

// GetSystemMetrics returns read-only Go runtime and process-lifetime metrics.
// It deliberately does not label Go heap allocation as total OS process memory.
func (s *MonitorService) GetSystemMetrics() SystemMetricsSnapshot {
	var memory runtime.MemStats
	runtime.ReadMemStats(&memory)
	collectedAt := time.Now()
	systemTotal, systemIdle, processTotal, cpuErr := readCPUSnapshot()
	processMemory, memoryErr := readProcessMemorySnapshot()
	systemCPUPercent, processCPUPercent := 0.0, 0.0
	if s != nil {
		s.mu.Lock()
		if cpuErr == nil && !s.lastCPUAt.IsZero() {
			elapsed := collectedAt.Sub(s.lastCPUAt).Seconds()
			if elapsed > 0 && systemTotal >= s.lastSystemTotal && systemIdle >= s.lastSystemIdle && processTotal >= s.lastProcessTotal {
				totalDelta := systemTotal - s.lastSystemTotal
				idleDelta := systemIdle - s.lastSystemIdle
				if totalDelta > 0 && idleDelta <= totalDelta {
					systemCPUPercent = float64(totalDelta-idleDelta) / float64(totalDelta) * 100
				}
				processDelta := processTotal - s.lastProcessTotal
				processCPUPercent = float64(processDelta) / (elapsed * 10_000_000 * float64(runtime.NumCPU())) * 100
				if processCPUPercent > 100 { processCPUPercent = 100 }
				if processCPUPercent < 0 { processCPUPercent = 0 }
			}
		}
		if cpuErr == nil {
			s.lastCPUAt = collectedAt
			s.lastSystemTotal, s.lastSystemIdle, s.lastProcessTotal = systemTotal, systemIdle, processTotal
		}
		if memoryErr == nil {
			if !s.memoryBaselineReady {
				s.memoryBaselineReady = true
				s.processWorkingSetBaseline = processMemory.ProcessWorkingSetBytes
				s.webView2WorkingSetBaseline = processMemory.WebView2WorkingSetBytes
			}
		}
		s.mu.Unlock()
	}
	startedAt := collectedAt
	if s != nil && !s.startedAt.IsZero() { startedAt = s.startedAt }
	metrics := SystemMetricsSnapshot{
		UptimeSeconds: int64(time.Since(startedAt).Seconds()),
		GoHeapAllocBytes: memory.HeapAlloc,
		GoHeapSysBytes: memory.HeapSys,
		GoTotalAllocBytes: memory.TotalAlloc,
		Goroutines: runtime.NumGoroutine(),
		CPUCount: runtime.NumCPU(),
		SystemCPUPercent: systemCPUPercent,
		ProcessCPUPercent: processCPUPercent,
		CollectedAt: collectedAt,
	}
	if memoryErr == nil {
		metrics.ProcessID = processMemory.ProcessID
		metrics.ProcessWorkingSetBytes = processMemory.ProcessWorkingSetBytes
		metrics.ProcessPrivateBytes = processMemory.ProcessPrivateBytes
		metrics.WebView2ProcessCount = processMemory.WebView2ProcessCount
		metrics.WebView2WorkingSetBytes = processMemory.WebView2WorkingSetBytes
		metrics.WebView2PrivateBytes = processMemory.WebView2PrivateBytes
		if s != nil {
			s.mu.Lock()
			metrics.MemoryBaselineReady = s.memoryBaselineReady
			metrics.ProcessWorkingSetDeltaBytes = int64(processMemory.ProcessWorkingSetBytes) - int64(s.processWorkingSetBaseline)
			metrics.WebView2WorkingSetDeltaBytes = int64(processMemory.WebView2WorkingSetBytes) - int64(s.webView2WorkingSetBaseline)
			s.mu.Unlock()
		}
	} else {
		metrics.ProcessMemoryError = memoryErr.Error()
	}
	return metrics
}

// GetRecentLogs returns the newest entries across every configured log source.
func (s *MonitorService) GetRecentLogs(limit int) []MonitorLogEntry {
	if s == nil || len(s.logDirs) == 0 { return []MonitorLogEntry{} }
	if limit < 1 { limit = 100 }
	if limit > 500 { limit = 500 }
	var result []MonitorLogEntry
	for _, date := range s.GetLogDates() {
		for _, entry := range s.GetLogsByDate(date) {
			result = append(result, entry)
			if len(result) >= limit { return result }
		}
	}
	return result
}

// datedMonitorLogName accepts the active daily log and its numbered rotations.
var datedMonitorLogName = regexp.MustCompile(`^my-livehub-(\d{4}-\d{2}-\d{2})\.log(?:\.(\d+))?$`)

// GetLogDates returns all available log dates in descending order across sources.
func (s *MonitorService) GetLogDates() []string {
	if s == nil || len(s.logDirs) == 0 { return []string{} }
	seen := make(map[string]struct{})
	for _, dir := range s.logDirs {
		entries, err := os.ReadDir(dir)
		if err != nil { continue }
		for _, entry := range entries {
			if entry.IsDir() { continue }
			name := entry.Name()
			match := datedMonitorLogName.FindStringSubmatch(name)
			date := ""
			if len(match) == 3 { date = match[1] } else if plain := plainDatedMonitorLogName.FindStringSubmatch(name); len(plain) == 2 { date = plain[1] } else { continue }
			if _, err := time.Parse("2006-01-02", date); err == nil { seen[date] = struct{}{} }
		}
	}
	dates := make([]string, 0, len(seen))
	for date := range seen { dates = append(dates, date) }
	sort.Sort(sort.Reverse(sort.StringSlice(dates)))
	return dates
}

// GetLogsByDate merges main application and extension logs, newest first.
func (s *MonitorService) GetLogsByDate(date string) []MonitorLogEntry {
	if s == nil || len(s.logDirs) == 0 { return []MonitorLogEntry{} }
	parsed, err := time.Parse("2006-01-02", date)
	if err != nil || parsed.Format("2006-01-02") != date { return []MonitorLogEntry{} }
	result := make([]MonitorLogEntry, 0)
	for _, dir := range s.logDirs {
		files, globErr := filepath.Glob(filepath.Join(dir, "my-livehub-"+date+".log*"))
		if globErr == nil {
			for _, file := range filterAndSortDatedLogFiles(files, date) { result = appendMonitorLogFile(result, file) }
		}
		extensionLog := filepath.Join(dir, date+".log")
		if info, statErr := os.Stat(extensionLog); statErr == nil && !info.IsDir() { result = appendMonitorLogFile(result, extensionLog) }
	}
	sort.SliceStable(result, func(i, j int) bool { return monitorLogEntryTime(result[i]).After(monitorLogEntryTime(result[j])) })
	return result
}

func appendMonitorLogFile(entries []MonitorLogEntry, path string) []MonitorLogEntry {
	file, err := os.Open(path)
	if err != nil { return entries }
	defer file.Close()
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 4096), 1024*1024)
	for scanner.Scan() { if entry, ok := parseMonitorLogLine(scanner.Text()); ok { entries = append(entries, entry) } }
	return entries
}

var plainDatedMonitorLogName = regexp.MustCompile(`^(\d{4}-\d{2}-\d{2})\.log$`)

func filterAndSortDatedLogFiles(files []string, date string) []string {
	valid := make([]string, 0, len(files))
	for _, path := range files {
		match := datedMonitorLogName.FindStringSubmatch(filepath.Base(path))
		if len(match) == 3 && match[1] == date {
			valid = append(valid, path)
		}
	}
	sort.Slice(valid, func(i, j int) bool {
		left := datedMonitorLogName.FindStringSubmatch(filepath.Base(valid[i]))
		right := datedMonitorLogName.FindStringSubmatch(filepath.Base(valid[j]))
		leftRotation, rightRotation := 0, 0
		if left[2] != "" { leftRotation, _ = strconv.Atoi(left[2]) }
		if right[2] != "" { rightRotation, _ = strconv.Atoi(right[2]) }
		// Older rotations are numbered higher and must be read first.
		return leftRotation > rightRotation
	})
	return valid
}

func parseMonitorLogLine(raw string) (MonitorLogEntry, bool) {
	line := strings.TrimSpace(raw)
	if line == "" { return MonitorLogEntry{}, false }
	lower := strings.ToLower(line)
	level := "INFO"
	switch {
	case strings.Contains(lower, "level=error"), strings.Contains(lower, "[error]"), strings.Contains(lower, " error "), strings.Contains(lower, " error:"), strings.Contains(lower, "failed"), strings.Contains(lower, "fatal"): level = "ERROR"
	case strings.Contains(lower, "level=warn"), strings.Contains(lower, "[warn]"), strings.Contains(lower, "[warning]"), strings.Contains(lower, " warn "), strings.Contains(lower, " warning"): level = "WARN"
	case strings.Contains(lower, "level=debug"), strings.Contains(lower, "[debug]"), strings.Contains(lower, " debug "): level = "DEBUG"
	}
	stamp, message := "", line
	if strings.HasPrefix(line, "time=") {
		value := strings.TrimPrefix(line, "time=")
		if end := strings.IndexByte(value, ' '); end > 0 {
			stamp = value[:end]
			message = strings.TrimSpace(value[end:])
		}
	} else if len(line) >= 19 {
		stamp = line[:19]
		if len(line) > 20 && line[19] == '.' {
			if end := strings.IndexByte(line[19:], ' '); end > 0 {
				stamp = line[:19+end]
			}
		}
		message = strings.TrimSpace(line[len(stamp):])
	}
	return MonitorLogEntry{Time: stamp, Level: level, Message: message}, true
}

func monitorLogEntryTime(entry MonitorLogEntry) time.Time {
	for _, layout := range []string{time.RFC3339Nano, "2006/01/02 15:04:05.999999", "2006/01/02 15:04:05", "2006-01-02 15:04:05.999999", "2006-01-02 15:04:05"} {
		if parsed, err := time.ParseInLocation(layout, entry.Time, time.Local); err == nil { return parsed }
	}
	return time.Time{}
}
