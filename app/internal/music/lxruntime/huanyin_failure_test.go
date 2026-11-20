//go:build lxquickjs && cgo

package lxruntime

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestHuanyinMiguPlaybackRetriesAfterTransportFailures verifies that a
// transport error on each Migu lookup is treated as a failed candidate and
// the pinned source advances album -> singer -> title before returning its
// source-level not-found error. No external network is used.
func TestHuanyinMiguPlaybackRetriesAfterTransportFailures(t *testing.T) {
	source, err := os.ReadFile(filepath.Join("testdata", "sources", "huanyin.js"))
	if err != nil {
		t.Fatal(err)
	}
	rt := newTestRuntime(t, 12*time.Second)
	defer rt.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	var keywords []string
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if request.Method != http.MethodGet || request.URL.Host != "api.xcvts.cn" || request.URL.Path != "/api/music/migu" {
			return nil, fmt.Errorf("unexpected Huanyin transport-failure request: %s %s", request.Method, request.URL.Redacted())
		}
		keywords = append(keywords, request.URL.Query().Get("gm"))
		return nil, fmt.Errorf("deterministic transport failure")
	})}
	if err := rt.InitializeLXSource(ctx, ScriptInfo{
		Name: "幻音音源", Version: "v3", Author: "竹佀", RawScript: string(source),
	}, string(source), client); err != nil {
		t.Fatalf("initialize pinned Huanyin source: %v", err)
	}

	_, err = rt.dispatchLXRequest(ctx, "mg", "musicUrl", map[string]any{
		"type": "320k",
		"musicInfo": map[string]any{
			"name": "网络故障歌曲", "singer": "网络故障歌手", "albumName": "网络故障专辑",
		},
	}, client)
	if err == nil || !strings.Contains(err.Error(), "咪咕找不到") {
		t.Fatalf("transport failure result = %v, want source-level not-found error", err)
	}
	want := []string{"网络故障歌曲网络故障专辑", "网络故障歌曲网络故障歌手", "网络故障歌曲"}
	if len(keywords) != len(want) {
		t.Fatalf("transport attempts = %v, want %v", keywords, want)
	}
	for i := range want {
		if keywords[i] != want[i] {
			t.Fatalf("transport attempt %d keyword = %q, want %q (all: %v)", i+1, keywords[i], want[i], keywords)
		}
	}
}

// TestHuanyinMiguPlaybackTimesOutAndFallsBack verifies that each pinned
// source request's 3-second timeout is observed by the real LX HTTP host,
// that the source proceeds through its three matching candidates, and that
// a stalled transport is cancelled rather than left running.
func TestHuanyinMiguPlaybackTimesOutAndFallsBack(t *testing.T) {
	source, err := os.ReadFile(filepath.Join("testdata", "sources", "huanyin.js"))
	if err != nil {
		t.Fatal(err)
	}
	rt := newTestRuntime(t, 18*time.Second)
	defer rt.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 16*time.Second)
	defer cancel()

	var keywords []string
	var cancelled int
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if request.Method != http.MethodGet || request.URL.Host != "api.xcvts.cn" || request.URL.Path != "/api/music/migu" {
			return nil, fmt.Errorf("unexpected Huanyin timeout request: %s %s", request.Method, request.URL.Redacted())
		}
		keywords = append(keywords, request.URL.Query().Get("gm"))
		<-request.Context().Done()
		cancelled++
		return nil, request.Context().Err()
	})}
	if err := rt.InitializeLXSource(ctx, ScriptInfo{
		Name: "幻音音源", Version: "v3", Author: "竹佀", RawScript: string(source),
	}, string(source), client); err != nil {
		t.Fatalf("initialize pinned Huanyin source: %v", err)
	}

	started := time.Now()
	_, err = rt.dispatchLXRequest(ctx, "mg", "musicUrl", map[string]any{
		"type": "320k",
		"musicInfo": map[string]any{
			"name": "超时歌曲", "singer": "超时歌手", "albumName": "超时专辑",
		},
	}, client)
	elapsed := time.Since(started)
	if err == nil || !strings.Contains(err.Error(), "咪咕找不到") {
		t.Fatalf("timeout result = %v, want source-level not-found error", err)
	}
	want := []string{"超时歌曲超时专辑", "超时歌曲超时歌手", "超时歌曲"}
	if len(keywords) != len(want) {
		t.Fatalf("timeout attempts = %v, want %v", keywords, want)
	}
	for i := range want {
		if keywords[i] != want[i] {
			t.Fatalf("timeout attempt %d keyword = %q, want %q (all: %v)", i+1, keywords[i], want[i], keywords)
		}
	}
	if cancelled != 3 {
		t.Fatalf("cancelled stalled HTTP requests = %d, want 3", cancelled)
	}
	if elapsed < 8*time.Second || elapsed > 14*time.Second {
		t.Fatalf("three source timeout fallbacks took %s, want approximately 9 seconds", elapsed)
	}
}
