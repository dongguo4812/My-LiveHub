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
	logDirs []string
	mu sync.Mutex
	lastCPUAt time.Time
	lastSystemTotal uint64
	lastSystemIdle uint64
	lastProcessTotal uint64
}

func NewMonitorService(logDir string, additionalLogDirs ...string) *MonitorService {
	logDirs := make([]string, 0, 1+len(additionalLogDirs))
	if strings.TrimSpace(logDir) != "" { logDirs = append(logDirs, logDir) }
	for _, dir := range additionalLogDirs {
		if strings.TrimSpace(dir) == "" { continue }
		duplicate := false
		for _, existing := range logDirs { if filepath.Clean(existing) == filepath.Clean(dir) { duplicate = true; break } }
		if !duplicate { logDirs = append(logDirs, dir) }
	}
	return &MonitorService{startedAt: time.Now(), logDirs: logDirs}
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
	case strings.Contains(lower, "level=error"), strings.Contains(lower, "[error]"), strings.Contains(lower, " error "), strings.Contains(lower, " error:"): level = "ERROR"
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
