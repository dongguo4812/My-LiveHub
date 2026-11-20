package lxruntime

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestHelloWorldKuwoMasterPOSTContract exercises the pinned source's Kuwo
// high-tier POST template through the real LX Host and deterministic transport.
func TestHelloWorldKuwoMasterPOSTContract(t *testing.T) {
	source, err := os.ReadFile(filepath.Join("testdata", "sources", "helloworld.js"))
	if err != nil {
		t.Fatal(err)
	}
	rt := newTestRuntime(t, 10*time.Second)
	defer rt.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()

	requests := 0
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		requests++
		if request.Method != http.MethodPost {
			return nil, fmt.Errorf("Kuwo method = %s, want POST", request.Method)
		}
		if request.URL.Scheme != "https" || request.URL.Host != "musicserver.haitangw.cc" || request.URL.Path != "/v1/music/resolve-url" {
			return nil, fmt.Errorf("unexpected Kuwo endpoint: %s", request.URL.Redacted())
		}
		if got := request.Header.Get("Content-Type"); got != "application/json" {
			return nil, fmt.Errorf("Content-Type = %q, want application/json", got)
		}
		if got := request.Header.Get("User-Agent"); got != "Mozilla/5.0" {
			return nil, fmt.Errorf("User-Agent = %q, want Mozilla/5.0", got)
		}
		body, err := io.ReadAll(request.Body)
		if err != nil {
			return nil, fmt.Errorf("read Kuwo request body: %w", err)
		}
		var payload map[string]string
		if err := json.Unmarshal(body, &payload); err != nil {
			return nil, fmt.Errorf("decode Kuwo request body %q: %w", body, err)
		}
		want := map[string]string{"source": "kw", "rid": "helloworld-kuwo-fixture-001", "level": "master"}
		if len(payload) != len(want) {
			return nil, fmt.Errorf("Kuwo payload = %#v, want exactly %#v", payload, want)
		}
		for key, value := range want {
			if payload[key] != value {
				return nil, fmt.Errorf("Kuwo payload[%q] = %q, want %q (all: %#v)", key, payload[key], value, payload)
			}
		}
		return &http.Response{
			StatusCode: http.StatusOK, Status: "200 OK", Header: http.Header{"Content-Type": []string{"application/json"}},
			Body: io.NopCloser(strings.NewReader(`{"data":{"url":"https://cdn.example/helloworld-kuwo-master.flac"}}`)),
			Request: request,
		}, nil
	})}

	if err := rt.InitializeLXSource(ctx, ScriptInfo{
		Name: "HelloWorld", Version: "260925", Author: "hello world", RawScript: string(source),
	}, string(source), client); err != nil {
		t.Fatalf("initialize pinned HelloWorld source: %v", err)
	}

	result, err := rt.dispatchLXRequest(ctx, "kw", "musicUrl", map[string]any{
		"type": "master",
		"musicInfo": map[string]any{"rid": "helloworld-kuwo-fixture-001"},
	}, client)
	if err != nil {
		t.Fatalf("dispatch HelloWorld Kuwo master musicUrl: %v", err)
	}
	if got, ok := result.(string); !ok || got != "https://cdn.example/helloworld-kuwo-master.flac" {
		t.Fatalf("Kuwo musicUrl = %#v, want fixture CDN URL", result)
	}
	if requests != 1 {
		t.Fatalf("Kuwo request count = %d, want exactly one successful primary API request", requests)
	}
}
