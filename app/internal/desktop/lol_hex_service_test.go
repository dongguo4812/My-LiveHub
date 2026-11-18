package desktop

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLOLHexStopWaitsForPIDRecordCleanupWhenHTTPIsAlreadyDown(t *testing.T) {
	runtimeDir := t.TempDir()
	pidPath := filepath.Join(runtimeDir, "lol_hex_display.pid")
	record := lolHexPIDRecord{PID: 12345, ManagerToken: "test-manager-token"}
	data, err := json.Marshal(record)
	if err != nil {
		t.Fatalf("marshal PID record: %v", err)
	}
	if err := os.WriteFile(pidPath, data, 0o600); err != nil {
		t.Fatalf("write PID record: %v", err)
	}

	service := &LOLHexService{
		runtimeDir: runtimeDir,
		host:         "127.0.0.1",
		port:         1,
		client:       &http.Client{Timeout: 100 * time.Millisecond},
		owned:        true,
		token:        record.ManagerToken,
	}

	go func() {
		time.Sleep(300 * time.Millisecond)
		_ = os.Remove(pidPath)
	}()

	started := time.Now()
	status, err := service.Stop()
	if err != nil {
		t.Fatalf("Stop returned error: %v", err)
	}
	if elapsed := time.Since(started); elapsed < 250*time.Millisecond {
		t.Fatalf("Stop returned before PID cleanup was observed: elapsed %s", elapsed)
	}
	if service.owned {
		t.Fatal("service ownership was not cleared after PID cleanup")
	}
	if status.State != "stopped" {
		t.Fatalf("expected stopped status, got %q", status.State)
	}
}

func TestLOLHexStopPreservesOwnershipWhenPIDIdentityCannotBeVerified(t *testing.T) {
	service := &LOLHexService{
		runtimeDir: t.TempDir(),
		host:         "127.0.0.1",
		port:         1,
		client:       &http.Client{Timeout: 100 * time.Millisecond},
		owned:        true,
		token:        "test-manager-token",
	}

	if _, err := service.Stop(); err == nil {
		t.Fatal("expected an error when the owned PID record is missing")
	}
	if !service.owned {
		t.Fatal("service ownership must be preserved when process identity is unknown")
	}
}


func TestLOLHexStopWaitsForPIDCleanupAfterHTTPShutdown(t *testing.T) {
	runtimeDir := t.TempDir()
	pidPath := filepath.Join(runtimeDir, "lol_hex_display.pid")
	record := lolHexPIDRecord{PID: 12345, ManagerToken: "test-manager-token"}
	data, err := json.Marshal(record)
	if err != nil {
		t.Fatalf("marshal PID record: %v", err)
	}
	if err := os.WriteFile(pidPath, data, 0o600); err != nil {
		t.Fatalf("write PID record: %v", err)
	}

	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/client-status":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"recognized":true}`))
		case "/api/action":
			go func() {
				time.Sleep(300 * time.Millisecond)
				_ = os.Remove(pidPath)
				server.Close()
			}()
			w.WriteHeader(http.StatusOK)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	service := &LOLHexService{
		runtimeDir: runtimeDir,
		host:       "127.0.0.1",
		port:       server.Listener.Addr().(*net.TCPAddr).Port,
		client:     &http.Client{Timeout: 100 * time.Millisecond},
		owned:      true,
		token:      record.ManagerToken,
	}

	started := time.Now()
	status, err := service.Stop()
	if err != nil {
		t.Fatalf("Stop returned error: %v", err)
	}
	if elapsed := time.Since(started); elapsed < 250*time.Millisecond {
		t.Fatalf("Stop returned before PID cleanup was observed: elapsed %s", elapsed)
	}
	if service.owned {
		t.Fatal("service ownership was not cleared after PID cleanup")
	}
	if status.State != "stopped" {
		t.Fatalf("expected stopped status, got %q", status.State)
	}
}
