package logging

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRotatingWriterKeepsBackups(t *testing.T) {
	path := filepath.Join(t.TempDir(), "app.log")
	writer, err := newRotatingWriter(path, 8, 2)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()

	for _, entry := range []string{"first\n", "second\n", "third\n"} {
		if _, err := writer.Write([]byte(entry)); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}

	current, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	backup, err := os.ReadFile(path + ".1")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(current), "third") {
		t.Fatalf("current log does not contain latest entry: %q", current)
	}
	if !strings.Contains(string(backup), "second") {
		t.Fatalf("first backup does not contain previous entry: %q", backup)
	}
}

func TestNewCreatesLogFileAndDirectory(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "logs")
	logger, err := New(directory)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(logger.Path()); err != nil {
		t.Fatalf("log file was not created: %v", err)
	}
	if err := logger.Close(); err != nil {
		t.Fatal(err)
	}
}
