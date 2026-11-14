package desktop

import (
	"bufio"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	runtimemetrics "runtime/metrics"
)

type SystemMetricsSnapshot struct {
	UptimeSeconds int64 `json:"uptime_seconds"`
	GoHeapAllocBytes uint64 `json:"go_heap_alloc_bytes"`
	GoHeapSysBytes uint64 `json:"go_heap_sys_bytes"`
	GoTotalAllocBytes uint64 `json:"go_total_alloc_bytes"`
	Goroutines int `json:"goroutines"`
	CPUCount int `json:"cpu_count"`
	GoCPUPercent float64 `json:"go_cpu_percent"`
	CollectedAt time.Time `json:"collected_at"`
}

type MonitorLogEntry struct {
	Time string `json:"time"`
	Level string `json:"level"`
	Message string `json:"message"`
}

type MonitorService struct {
	startedAt time.Time
	logDir string
	mu sync.Mutex
	lastCPUAt time.Time
	lastCPUSeconds float64
}

func NewMonitorService(logDir string) *MonitorService {
	return &MonitorService{startedAt: time.Now(), logDir: logDir}
}

// GetSystemMetrics returns read-only Go runtime and process-lifetime metrics.
// It deliberately does not label Go heap allocation as total OS process memory.
func (s *MonitorService) GetSystemMetrics() SystemMetricsSnapshot {
	var memory runtime.MemStats
	runtime.ReadMemStats(&memory)
	collectedAt := time.Now()
	cpuSample := []runtimemetrics.Sample{{Name: "/cpu/classes/total:cpu-seconds"}}
	runtimemetrics.Read(cpuSample)
	cpuSeconds := cpuSample[0].Value.Float64()
	cpuPercent := 0.0
	if s != nil {
		s.mu.Lock()
		if !s.lastCPUAt.IsZero() {
			elapsed := collectedAt.Sub(s.lastCPUAt).Seconds()
			if elapsed > 0 {
				cpuPercent = (cpuSeconds - s.lastCPUSeconds) / elapsed / float64(runtime.NumCPU()) * 100
				if cpuPercent < 0 { cpuPercent = 0 }
				if cpuPercent > 100 { cpuPercent = 100 }
			}
		}
		s.lastCPUAt, s.lastCPUSeconds = collectedAt, cpuSeconds
		s.mu.Unlock()
	}
	startedAt := collectedAt
	if s != nil && !s.startedAt.IsZero() { startedAt = s.startedAt }
	return SystemMetricsSnapshot{
		UptimeSeconds: int64(time.Since(startedAt).Seconds()),
		GoHeapAllocBytes: memory.HeapAlloc,
		GoHeapSysBytes: memory.HeapSys,
		GoTotalAllocBytes: memory.TotalAlloc,
		Goroutines: runtime.NumGoroutine(),
		CPUCount: runtime.NumCPU(),
		GoCPUPercent: cpuPercent,
		CollectedAt: collectedAt,
	}
}

// GetRecentLogs reads the newest application log lines without changing log files.
func (s *MonitorService) GetRecentLogs(limit int) []MonitorLogEntry {
	if s == nil || strings.TrimSpace(s.logDir) == "" { return []MonitorLogEntry{} }
	if limit < 1 { limit = 100 }
	if limit > 500 { limit = 500 }
	files, err := filepath.Glob(filepath.Join(s.logDir, "my-livehub-*.log*"))
	if err != nil || len(files) == 0 { return []MonitorLogEntry{} }
	sort.Strings(files)
	lines := make([]string, 0, limit)
	for i := len(files)-1; i >= 0 && len(lines) < limit; i-- {
		file, openErr := os.Open(files[i])
		if openErr != nil { continue }
		scanner := bufio.NewScanner(file)
		scanner.Buffer(make([]byte, 4096), 1024*1024)
		var current []string
		for scanner.Scan() { current = append(current, scanner.Text()) }
		_ = file.Close()
		for j := len(current)-1; j >= 0 && len(lines) < limit; j-- { lines = append(lines, current[j]) }
	}
	result := make([]MonitorLogEntry, 0, len(lines))
	for i := len(lines)-1; i >= 0; i-- {
		line := strings.TrimSpace(lines[i])
		if line == "" { continue }
		level := "INFO"
		lower := strings.ToLower(line)
		if strings.Contains(lower, " error") || strings.Contains(lower, "failed") || strings.Contains(lower, "fatal") { level = "ERROR" } else if strings.Contains(lower, " warn") { level = "WARN" } else if strings.Contains(lower, " debug") { level = "DEBUG" }
		stamp := ""
		message := line
		if len(line) >= 19 { stamp, message = line[:19], strings.TrimSpace(line[19:]) }
		result = append(result, MonitorLogEntry{Time: stamp, Level: level, Message: message})
	}
	return result
}
