package logging

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sync"
	"time"
)

const (
	defaultMaxBytes = 10 << 20
	defaultBackups  = 5
	defaultRetentionDays = 14
)

var datedLogName = regexp.MustCompile(`^my-livehub-(\d{4}-\d{2}-\d{2})\.log(?:\.\d+)?$`)

// Logger writes each calendar day's entries to its own file and prunes old files.
type Logger struct {
	file *dailyWriter
}

func New(directory string, retentionDays ...int) (*Logger, error) {
	if directory == "" {
		return nil, fmt.Errorf("log directory is required")
	}
	days := defaultRetentionDays
	if len(retentionDays) > 0 {
		days = retentionDays[0]
	}
	writer, err := newDailyWriter(directory, days, defaultMaxBytes, defaultBackups, time.Now)
	if err != nil {
		return nil, err
	}
	return &Logger{file: writer}, nil
}

func (l *Logger) Output() io.Writer {
	if l == nil || l.file == nil {
		return os.Stderr
	}
	return l.file
}

func (l *Logger) Path() string {
	if l == nil || l.file == nil {
		return ""
	}
	return l.file.Path()
}

func (l *Logger) SetRetentionDays(days int) error {
	if l == nil || l.file == nil {
		return fmt.Errorf("logger is unavailable")
	}
	return l.file.SetRetentionDays(days)
}

func (l *Logger) Close() error {
	if l == nil || l.file == nil {
		return nil
	}
	return l.file.Close()
}

type dailyWriter struct {
	mu sync.Mutex
	directory string
	file *os.File
	path string
	date string
	size int64
	maxBytes int64
	backups int
	retentionDays int
	now func() time.Time
}

func newDailyWriter(directory string, retentionDays int, maxBytes int64, backups int, now func() time.Time) (*dailyWriter, error) {
	if retentionDays < 1 || retentionDays > 365 {
		return nil, fmt.Errorf("log retention days must be between 1 and 365")
	}
	if maxBytes <= 0 || backups < 0 {
		return nil, fmt.Errorf("invalid log rotation limits")
	}
	if err := os.MkdirAll(directory, 0o755); err != nil {
		return nil, fmt.Errorf("create log directory: %w", err)
	}
	if now == nil {
		now = time.Now
	}
	w := &dailyWriter{directory: directory, retentionDays: retentionDays, maxBytes: maxBytes, backups: backups, now: now}
	if err := w.cleanup(now()); err != nil {
		return nil, err
	}
	if err := w.ensureFile(now()); err != nil {
		return nil, err
	}
	return w, nil
}

func (w *dailyWriter) Path() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.path
}

func (w *dailyWriter) SetRetentionDays(days int) error {
	if days < 1 || days > 365 {
		return fmt.Errorf("log retention days must be between 1 and 365")
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	w.retentionDays = days
	return w.cleanup(w.now())
}

func (w *dailyWriter) cleanup(now time.Time) error {
	entries, err := os.ReadDir(w.directory)
	if err != nil {
		return fmt.Errorf("read log directory: %w", err)
	}
	cutoff := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location()).AddDate(0, 0, -(w.retentionDays - 1))
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		match := datedLogName.FindStringSubmatch(entry.Name())
		if len(match) != 2 {
			continue
		}
		date, parseErr := time.ParseInLocation("2006-01-02", match[1], now.Location())
		if parseErr == nil && date.Before(cutoff) {
			if err := os.Remove(filepath.Join(w.directory, entry.Name())); err != nil && !os.IsNotExist(err) {
				return fmt.Errorf("remove expired log %q: %w", entry.Name(), err)
			}
		}
	}
	return nil
}

func (w *dailyWriter) ensureFile(now time.Time) error {
	date := now.Format("2006-01-02")
	if w.file != nil && w.date == date {
		return nil
	}
	if w.file != nil {
		if err := w.file.Close(); err != nil {
			return fmt.Errorf("close previous daily log: %w", err)
		}
		w.file = nil
	}
	w.date = date
	w.path = filepath.Join(w.directory, "my-livehub-"+date+".log")
	if err := w.rotateIfNeeded(); err != nil {
		return err
	}
	file, err := os.OpenFile(w.path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("open log file %q: %w", w.path, err)
	}
	info, err := file.Stat()
	if err != nil {
		_ = file.Close()
		return fmt.Errorf("inspect log file: %w", err)
	}
	w.file, w.size = file, info.Size()
	return nil
}

func (w *dailyWriter) rotateIfNeeded() error {
	info, err := os.Stat(w.path)
	if err != nil {
		if os.IsNotExist(err) { return nil }
		return fmt.Errorf("inspect log file: %w", err)
	}
	if info.Size() < w.maxBytes { return nil }
	if w.backups == 0 {
		return os.Remove(w.path)
	}
	oldest := fmt.Sprintf("%s.%d", w.path, w.backups)
	if err := os.Remove(oldest); err != nil && !os.IsNotExist(err) { return err }
	for i := w.backups - 1; i >= 1; i-- {
		source, target := fmt.Sprintf("%s.%d", w.path, i), fmt.Sprintf("%s.%d", w.path, i+1)
		if err := os.Rename(source, target); err != nil && !os.IsNotExist(err) { return err }
	}
	if err := os.Rename(w.path, w.path+".1"); err != nil && !os.IsNotExist(err) { return err }
	return nil
}

func (w *dailyWriter) Write(data []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	now := w.now()
	if now.Format("2006-01-02") != w.date {
		if w.file != nil {
			if err := w.file.Close(); err != nil { return 0, err }
			w.file = nil
		}
		if err := w.cleanup(now); err != nil { return 0, err }
	}
	if err := w.ensureFile(now); err != nil { return 0, err }
	if w.size > 0 && w.size+int64(len(data)) > w.maxBytes {
		if err := w.file.Close(); err != nil { return 0, err }
		w.file = nil
		if err := w.rotateIfNeededForce(); err != nil { return 0, err }
		file, err := os.OpenFile(w.path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
		if err != nil { return 0, err }
		w.file, w.size = file, 0
	}
	n, err := w.file.Write(data)
	w.size += int64(n)
	return n, err
}

func (w *dailyWriter) rotateIfNeededForce() error {
	if w.backups == 0 {
		if err := os.Remove(w.path); err != nil && !os.IsNotExist(err) { return err }
		return nil
	}
	oldest := fmt.Sprintf("%s.%d", w.path, w.backups)
	if err := os.Remove(oldest); err != nil && !os.IsNotExist(err) { return err }
	for i := w.backups - 1; i >= 1; i-- {
		source, target := fmt.Sprintf("%s.%d", w.path, i), fmt.Sprintf("%s.%d", w.path, i+1)
		if err := os.Rename(source, target); err != nil && !os.IsNotExist(err) { return err }
	}
	if err := os.Rename(w.path, w.path+".1"); err != nil && !os.IsNotExist(err) { return err }
	return nil
}

func (w *dailyWriter) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.file == nil { return nil }
	if err := w.file.Sync(); err != nil { _ = w.file.Close(); w.file = nil; return err }
	err := w.file.Close()
	w.file = nil
	return err
}
