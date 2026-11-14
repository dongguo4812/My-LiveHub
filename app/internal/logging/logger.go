package logging

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
)

const (
	defaultMaxBytes = 10 << 20
	defaultBackups  = 5
)

// Logger owns the application's rotating log file. The standard library logger
// writes to both stderr and this file after New succeeds.
type Logger struct {
	file *rotatingWriter
}

func New(directory string) (*Logger, error) {
	if directory == "" {
		return nil, fmt.Errorf("log directory is required")
	}
	if err := os.MkdirAll(directory, 0o755); err != nil {
		return nil, fmt.Errorf("create log directory: %w", err)
	}
	file, err := newRotatingWriter(filepath.Join(directory, "my-livehub.log"), defaultMaxBytes, defaultBackups)
	if err != nil {
		return nil, err
	}
	return &Logger{file: file}, nil
}

// Output returns a writer that duplicates log entries to the terminal and file.
func (l *Logger) Output() io.Writer {
	if l == nil || l.file == nil {
		return os.Stderr
	}
	return io.MultiWriter(os.Stderr, l.file)
}

func (l *Logger) Path() string {
	if l == nil || l.file == nil {
		return ""
	}
	return l.file.path
}

func (l *Logger) Close() error {
	if l == nil || l.file == nil {
		return nil
	}
	return l.file.Close()
}

type rotatingWriter struct {
	mu       sync.Mutex
	path     string
	file     *os.File
	size     int64
	maxBytes int64
	backups  int
}

func newRotatingWriter(path string, maxBytes int64, backups int) (*rotatingWriter, error) {
	if maxBytes <= 0 {
		return nil, fmt.Errorf("maximum log file size must be positive")
	}
	if backups < 0 {
		return nil, fmt.Errorf("log backup count cannot be negative")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("create log directory: %w", err)
	}
	writer := &rotatingWriter{path: path, maxBytes: maxBytes, backups: backups}
	info, err := os.Stat(path)
	if err == nil && info.Size() >= maxBytes {
		if err := writer.rotate(); err != nil {
			return nil, err
		}
	} else if err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("inspect log file: %w", err)
	}
	if err := writer.open(); err != nil {
		return nil, err
	}
	return writer, nil
}

func (w *rotatingWriter) open() error {
	file, err := os.OpenFile(w.path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("open log file %q: %w", w.path, err)
	}
	info, err := file.Stat()
	if err != nil {
		_ = file.Close()
		return fmt.Errorf("inspect log file %q: %w", w.path, err)
	}
	w.file = file
	w.size = info.Size()
	return nil
}

func (w *rotatingWriter) rotate() error {
	if w.file != nil {
		if err := w.file.Close(); err != nil {
			return fmt.Errorf("close log file before rotation: %w", err)
		}
		w.file = nil
	}
	if w.backups == 0 {
		if err := os.Remove(w.path); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("remove old log file: %w", err)
		}
		w.size = 0
		return nil
	}
	oldest := fmt.Sprintf("%s.%d", w.path, w.backups)
	if err := os.Remove(oldest); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove oldest log backup: %w", err)
	}
	for index := w.backups - 1; index >= 1; index-- {
		source := fmt.Sprintf("%s.%d", w.path, index)
		target := fmt.Sprintf("%s.%d", w.path, index+1)
		if err := os.Rename(source, target); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("rotate log backup %d: %w", index, err)
		}
	}
	if err := os.Rename(w.path, w.path+".1"); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("rotate current log file: %w", err)
	}
	w.size = 0
	return nil
}

func (w *rotatingWriter) Write(data []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.file == nil {
		return 0, os.ErrClosed
	}
	if w.size > 0 && w.size+int64(len(data)) > w.maxBytes {
		if err := w.rotate(); err != nil {
			return 0, err
		}
		if err := w.open(); err != nil {
			return 0, err
		}
	}
	n, err := w.file.Write(data)
	w.size += int64(n)
	return n, err
}

func (w *rotatingWriter) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.file == nil {
		return nil
	}
	err := w.file.Sync()
	closeErr := w.file.Close()
	w.file = nil
	if err != nil {
		return err
	}
	return closeErr
}
