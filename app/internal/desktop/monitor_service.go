package desktop

import (
	"bufio"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"regexp"
)

type SystemMetricsSnapshot struct {
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

type MonitorService struct {
	startedAt time.Time
	logDir string
	mu sync.Mutex
	lastCPUAt time.Time
	lastSystemTotal uint64
	lastSystemIdle uint64
	lastProcessTotal uint64
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
	systemTotal, systemIdle, processTotal, cpuErr := readCPUSnapshot()
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
		SystemCPUPercent: systemCPUPercent,
		ProcessCPUPercent: processCPUPercent,
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
	sort.Slice(files, func(i, j int) bool {
		left, right := filepath.Base(files[i]), filepath.Base(files[j])
		leftDate := strings.TrimPrefix(strings.Split(left, ".log")[0], "my-livehub-")
		rightDate := strings.TrimPrefix(strings.Split(right, ".log")[0], "my-livehub-")
		if leftDate != rightDate { return leftDate > rightDate }
		leftRotation, rightRotation := 0, 0
		if suffix := strings.TrimPrefix(left, "my-livehub-"+leftDate+".log."); suffix != left { leftRotation, _ = strconv.Atoi(suffix) }
		if suffix := strings.TrimPrefix(right, "my-livehub-"+rightDate+".log."); suffix != right { rightRotation, _ = strconv.Atoi(suffix) }
		return leftRotation < rightRotation
	})
	lines := make([]string, 0, limit)
	for i := 0; i < len(files) && len(lines) < limit; i++ {
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


// datedMonitorLogName accepts the active daily log and its numbered rotations.
var datedMonitorLogName = regexp.MustCompile(`^my-livehub-(\d{4}-\d{2}-\d{2})\.log(?:\.(\d+))?$`)

// GetLogDates returns all available log dates in descending order.
func (s *MonitorService) GetLogDates() []string {
	if s == nil || strings.TrimSpace(s.logDir) == "" {
		return []string{}
	}
	entries, err := os.ReadDir(s.logDir)
	if err != nil {
		return []string{}
	}
	seen := make(map[string]struct{})
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		match := datedMonitorLogName.FindStringSubmatch(entry.Name())
		if len(match) != 3 {
			continue
		}
		if _, err := time.Parse("2006-01-02", match[1]); err != nil {
			continue
		}
		seen[match[1]] = struct{}{}
	}
	dates := make([]string, 0, len(seen))
	for date := range seen {
		dates = append(dates, date)
	}
	sort.Sort(sort.Reverse(sort.StringSlice(dates)))
	return dates
}

// GetLogsByDate returns every readable entry for one exact calendar date,
// including that date's rotated log files. Invalid dates never reach the filesystem.
func (s *MonitorService) GetLogsByDate(date string) []MonitorLogEntry {
	if s == nil || strings.TrimSpace(s.logDir) == "" {
		return []MonitorLogEntry{}
	}
	parsed, err := time.Parse("2006-01-02", date)
	if err != nil || parsed.Format("2006-01-02") != date {
		return []MonitorLogEntry{}
	}
	files, err := filepath.Glob(filepath.Join(s.logDir, "my-livehub-"+date+".log*"))
	if err != nil || len(files) == 0 {
		return []MonitorLogEntry{}
	}
	files = filterAndSortDatedLogFiles(files, date)
	result := make([]MonitorLogEntry, 0)
	for _, path := range files {
		file, openErr := os.Open(path)
		if openErr != nil {
			continue
		}
		scanner := bufio.NewScanner(file)
		scanner.Buffer(make([]byte, 4096), 1024*1024)
		for scanner.Scan() {
			if entry, ok := parseMonitorLogLine(scanner.Text()); ok {
				result = append(result, entry)
			}
		}
		_ = file.Close()
	}
	return result
}

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
	if line == "" {
		return MonitorLogEntry{}, false
	}
	level := "INFO"
	lower := strings.ToLower(line)
	if strings.Contains(lower, " error") || strings.Contains(lower, "failed") || strings.Contains(lower, "fatal") {
		level = "ERROR"
	} else if strings.Contains(lower, " warn") {
		level = "WARN"
	} else if strings.Contains(lower, " debug") {
		level = "DEBUG"
	}
	stamp, message := "", line
	if len(line) >= 19 {
		stamp, message = line[:19], strings.TrimSpace(line[19:])
	}
	return MonitorLogEntry{Time: stamp, Level: level, Message: message}, true
}
