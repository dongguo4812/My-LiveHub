package logging

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestDailyWriterCreatesSeparateFiles(t *testing.T) {
	now := time.Date(2026, 11, 14, 10, 0, 0, 0, time.Local)
	directory := t.TempDir()
	writer, err := newDailyWriter(directory, 14, 1024, 2, func() time.Time { return now })
	if err != nil { t.Fatal(err) }
	if _, err := writer.Write([]byte("day one\n")); err != nil { t.Fatal(err) }
	now = now.AddDate(0, 0, 1)
	if _, err := writer.Write([]byte("day two\n")); err != nil { t.Fatal(err) }
	if err := writer.Close(); err != nil { t.Fatal(err) }

	first, err := os.ReadFile(filepath.Join(directory, "my-livehub-2026-11-14.log"))
	if err != nil { t.Fatal(err) }
	second, err := os.ReadFile(filepath.Join(directory, "my-livehub-2026-11-15.log"))
	if err != nil { t.Fatal(err) }
	if !strings.Contains(string(first), "day one") || !strings.Contains(string(second), "day two") {
		t.Fatalf("daily log files contain unexpected data: first=%q second=%q", first, second)
	}
}

func TestDailyWriterRemovesExpiredLogs(t *testing.T) {
	now := time.Date(2026, 11, 14, 10, 0, 0, 0, time.Local)
	directory := t.TempDir()
	for _, name := range []string{"my-livehub-2026-11-12.log", "my-livehub-2026-11-13.log", "my-livehub-2026-11-14.log", "keep-me.txt"} {
		if err := os.WriteFile(filepath.Join(directory, name), []byte("x"), 0o644); err != nil { t.Fatal(err) }
	}
	writer, err := newDailyWriter(directory, 2, 1024, 2, func() time.Time { return now })
	if err != nil { t.Fatal(err) }
	defer writer.Close()
	if _, err := os.Stat(filepath.Join(directory, "my-livehub-2026-11-12.log")); !os.IsNotExist(err) {
		t.Fatalf("expired log was not removed: %v", err)
	}
	for _, name := range []string{"my-livehub-2026-11-13.log", "my-livehub-2026-11-14.log", "keep-me.txt"} {
		if _, err := os.Stat(filepath.Join(directory, name)); err != nil { t.Fatalf("expected %s to remain: %v", name, err) }
	}
}

func TestNewCreatesLogFileAndDirectory(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "logs")
	logger, err := New(directory, 7)
	if err != nil { t.Fatal(err) }
	if _, err := os.Stat(logger.Path()); err != nil { t.Fatalf("log file was not created: %v", err) }
	if filepath.Base(logger.Path()) != "my-livehub-"+time.Now().Format("2006-01-02")+".log" {
		t.Fatalf("unexpected daily log path: %s", logger.Path())
	}
	if err := logger.Close(); err != nil { t.Fatal(err) }
}
