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


// TestHelloWorldKuwoStandardQualityGETContract covers the Kuwo convert_url
// endpoint selected after the first API rejects standard audio quality.
func TestHelloWorldKuwoStandardQualityGETContract(t *testing.T) {
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
		if request.Method != http.MethodGet {
			return nil, fmt.Errorf("Kuwo convert_url method = %s, want GET", request.Method)
		}
		if request.URL.Scheme != "http" || request.URL.Host != "nmobi.kuwo.cn" || request.URL.Path != "/mobi.s" {
			return nil, fmt.Errorf("unexpected Kuwo convert_url endpoint: %s", request.URL.Redacted())
		}
		query := request.URL.Query()
		want := map[string]string{
			"f": "web", "user": "0", "source": "kwplayerhd_ar_1.1.4.5_tianbao_T1A_qirui.apk",
			"type": "convert_url_with_sign", "rid": "helloworld-kuwo-fixture-002", "br": "128kmp3",
		}
		if len(query) != len(want) {
			return nil, fmt.Errorf("Kuwo query = %#v, want exactly %#v", query, want)
		}
		for key, value := range want {
			if got := query.Get(key); got != value {
				return nil, fmt.Errorf("Kuwo query[%q] = %q, want %q (all: %#v)", key, got, value, query)
			}
		}
		if got := request.Header.Get("User-Agent"); got != "Mozilla/5.0 (Linux; Android) AppleWebKit/537.36" {
			return nil, fmt.Errorf("Kuwo User-Agent = %q", got)
		}
		return &http.Response{
			StatusCode: http.StatusOK, Status: "200 OK", Header: http.Header{"Content-Type": []string{"text/plain"}},
			Body: io.NopCloser(strings.NewReader("https://cdn.example/helloworld-kuwo-128k.mp3")),
			Request: request,
		}, nil
	})}

	if err := rt.InitializeLXSource(ctx, ScriptInfo{
		Name: "HelloWorld", Version: "260925", Author: "hello world", RawScript: string(source),
	}, string(source), client); err != nil {
		t.Fatalf("initialize pinned HelloWorld source: %v", err)
	}
	result, err := rt.dispatchLXRequest(ctx, "kw", "musicUrl", map[string]any{
		"type": "128k",
		"musicInfo": map[string]any{"rid": "helloworld-kuwo-fixture-002"},
	}, client)
	if err != nil {
		t.Fatalf("dispatch HelloWorld Kuwo 128k musicUrl: %v", err)
	}
	if got, ok := result.(string); !ok || got != "https://cdn.example/helloworld-kuwo-128k.mp3" {
		t.Fatalf("Kuwo musicUrl = %#v, want fixture CDN URL", result)
	}
	if requests != 1 {
		t.Fatalf("Kuwo request count = %d, want exactly one convert_url request", requests)
	}
}


// TestHelloWorldQQTangFallbackGETContract covers the pinned QQ source's
// ordered legacy-provider fallback and the tang API's quality/response mapping.
func TestHelloWorldQQTangFallbackGETContract(t *testing.T) {
	source, err := os.ReadFile(filepath.Join("testdata", "sources", "helloworld.js"))
	if err != nil {
		t.Fatal(err)
	}
	rt := newTestRuntime(t, 10*time.Second)
	defer rt.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()

	var requests []string
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		path := request.URL.Host + request.URL.Path
		requests = append(requests, path)
		if request.Method != http.MethodGet {
			return nil, fmt.Errorf("QQ fallback method = %s, want GET", request.Method)
		}
		switch path {
		case "103.79.184.97/api/music/url":
			query := request.URL.Query()
			if query.Get("source") != "tx" || query.Get("songId") != "helloworld-qq-fixture-003" ||
				query.Get("quality") != "320k" || query.Get("key") != "6C1F-53W0-GRKI-EVFG" || len(query) != 4 {
				return nil, fmt.Errorf("unexpected QQ card API query: %#v", query)
			}
			if request.Header.Get("X-Card-Key") != "6C1F-53W0-GRKI-EVFG" {
				return nil, fmt.Errorf("QQ card API X-Card-Key = %q", request.Header.Get("X-Card-Key"))
			}
			return &http.Response{StatusCode: 200, Status: "200 OK", Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(`{}`)), Request: request}, nil
		case "yy.zddyr.top/lx/api/":
			query := request.URL.Query()
			if query.Get("source") != "tx" || query.Get("songmid") != "helloworld-qq-fixture-003" ||
				query.Get("quality") != "320k" || len(query) != 3 {
				return nil, fmt.Errorf("unexpected QQ YY API query: %#v", query)
			}
			if request.Header.Get("X-Client") != "YYMusicSource/v1.0.0 (Android)" || request.Header.Get("X-Token") == "" {
				return nil, fmt.Errorf("QQ YY API token/client headers missing: %#v", request.Header)
			}
			return &http.Response{StatusCode: 200, Status: "200 OK", Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(`{}`)), Request: request}, nil
		case "tang.api.s01s.cn/music_open_api.php":
			query := request.URL.Query()
			if query.Get("mid") != "helloworld-qq-fixture-003" || len(query) != 1 {
				return nil, fmt.Errorf("unexpected QQ tang API query: %#v", query)
			}
			if request.Header.Get("User-Agent") != "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/126.0.0.0 Safari/537.36" {
				return nil, fmt.Errorf("QQ tang API User-Agent = %q", request.Header.Get("User-Agent"))
			}
			body := `{"song_play_url_hq":"https://cdn.example/helloworld-qq-tang-hq.mp3"}`
			return &http.Response{StatusCode: 200, Status: "200 OK", Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(body)), Request: request}, nil
		default:
			return nil, fmt.Errorf("unexpected QQ API endpoint: %s", request.URL.Redacted())
		}
	})}

	if err := rt.InitializeLXSource(ctx, ScriptInfo{
		Name: "HelloWorld", Version: "260925", Author: "hello world", RawScript: string(source),
	}, string(source), client); err != nil {
		t.Fatalf("initialize pinned HelloWorld source: %v", err)
	}
	result, err := rt.dispatchLXRequest(ctx, "tx", "musicUrl", map[string]any{
		"type": "320k",
		"musicInfo": map[string]any{"songmid": "helloworld-qq-fixture-003", "name": "测试歌曲"},
	}, client)
	if err != nil {
		t.Fatalf("dispatch HelloWorld QQ 320k musicUrl: %v", err)
	}
	if got, ok := result.(string); !ok || got != "https://cdn.example/helloworld-qq-tang-hq.mp3" {
		t.Fatalf("QQ musicUrl = %#v, want tang HQ fixture URL", result)
	}
	wantRequests := []string{
		"103.79.184.97/api/music/url",
		"yy.zddyr.top/lx/api/",
		"tang.api.s01s.cn/music_open_api.php",
	}
	if len(requests) != len(wantRequests) {
		t.Fatalf("QQ API requests = %#v, want ordered fallback %#v", requests, wantRequests)
	}
	for i := range wantRequests {
		if requests[i] != wantRequests[i] {
			t.Fatalf("QQ API request[%d] = %q, want %q (all: %#v)", i, requests[i], wantRequests[i], requests)
		}
	}
}
