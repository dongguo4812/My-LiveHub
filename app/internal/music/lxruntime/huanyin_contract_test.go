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

// These tests exercise the pinned Huanyin source's actual Migu handlers.
// HTTP is intercepted at the Go host boundary; no third-party service is called.
func TestHuanyinMiguMusicURLRetriesMetadataMismatchAndCachesResult(t *testing.T) {
	source, err := os.ReadFile(filepath.Join("testdata", "sources", "huanyin.js"))
	if err != nil {
		t.Fatal(err)
	}
	rt := newTestRuntime(t, 10*time.Second)
	defer rt.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()

	var requests []string
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if request.Method != http.MethodGet || request.URL.Host != "api.xcvts.cn" || request.URL.Path != "/api/music/migu" {
			return nil, fmt.Errorf("unexpected Huanyin Migu request: %s %s", request.Method, request.URL.Redacted())
		}
		keyword := request.URL.Query().Get("gm")
		requests = append(requests, keyword)
		if request.URL.Query().Get("type") != "json" || request.URL.Query().Get("num") != "1" {
			return nil, fmt.Errorf("unexpected Migu query parameters: %s", request.URL.RawQuery)
		}

		body := `{"code":200,"title":"稻香","artist":"周杰伦","album":"十一月的萧邦","music_url":"https://cdn.example/wrong.mp3"}`
		switch keyword {
		case "夜曲十一月的萧邦":
			// A title mismatch must reject this candidate and try the next keyword.
		case "夜曲周杰伦":
			body = `{"code":200,"title":"夜曲","artist":"周杰伦","album":"十一月的萧邦","music_url":"https://cdn.example/yequ.mp3"}`
		default:
			return nil, fmt.Errorf("unexpected fallback keyword %q", keyword)
		}
		return &http.Response{
			StatusCode: http.StatusOK, Status: "200 OK", Header: make(http.Header),
			Body: io.NopCloser(strings.NewReader(body)), Request: request,
		}, nil
	})}

	if err := rt.InitializeLXSource(ctx, ScriptInfo{
		Name: "幻音音源", Version: "v3", Author: "竹佀", RawScript: string(source),
	}, string(source), client); err != nil {
		t.Fatalf("initialize Huanyin source: %v", err)
	}

	info := map[string]any{
		"name": "夜曲", "singer": "周杰伦", "albumName": "十一月的萧邦",
	}
	result, err := rt.dispatchLXRequest(ctx, "mg", "musicUrl", map[string]any{
		"type": "320k", "musicInfo": info,
	}, client)
	if err != nil {
		t.Fatalf("resolve Migu music URL after metadata mismatch: %v", err)
	}
	if got, ok := result.(string); !ok || got != "https://cdn.example/yequ.mp3" {
		t.Fatalf("musicUrl = %#v, want matched Migu URL", result)
	}
	if len(requests) != 2 || requests[0] != "夜曲十一月的萧邦" || requests[1] != "夜曲周杰伦" {
		t.Fatalf("Migu fallback requests = %#v, want album query then singer query", requests)
	}

	// The source caches by song metadata. A second call must return the cached
	// URL without issuing another request (the source does not vary this path
	// by the requested quality).
	cached, err := rt.dispatchLXRequest(ctx, "mg", "musicUrl", map[string]any{
		"type": "128k", "musicInfo": info,
	}, client)
	if err != nil {
		t.Fatalf("read cached Migu music URL: %v", err)
	}
	if cached != result {
		t.Fatalf("cached musicUrl = %#v, first result = %#v", cached, result)
	}
	if len(requests) != 2 {
		t.Fatalf("Migu requests after cache hit = %d, want 2", len(requests))
	}
}

func TestHuanyinMiguSearchStopsWhenRequestedLimitIsReached(t *testing.T) {
	source, err := os.ReadFile(filepath.Join("testdata", "sources", "huanyin.js"))
	if err != nil {
		t.Fatal(err)
	}
	rt := newTestRuntime(t, 10*time.Second)
	defer rt.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()

	var pages []string
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if request.Method != http.MethodGet || request.URL.Host != "api.xcvts.cn" || request.URL.Path != "/api/music/migu" {
			return nil, fmt.Errorf("unexpected Huanyin search request: %s %s", request.Method, request.URL.Redacted())
		}
		if request.URL.Query().Get("gm") != "周杰伦" || request.URL.Query().Get("num") != "1" {
			return nil, fmt.Errorf("unexpected Migu search query: %s", request.URL.RawQuery)
		}
		page := request.URL.Query().Get("n")
		pages = append(pages, page)
		title := "歌曲" + page
		body, _ := json.Marshal(map[string]any{
			"code": 200, "title": title, "artist": "周杰伦",
			"album": "专辑", "duration": "04:00",
		})
		return &http.Response{
			StatusCode: http.StatusOK, Status: "200 OK", Header: make(http.Header),
			Body: io.NopCloser(strings.NewReader(string(body))), Request: request,
		}, nil
	})}

	if err := rt.InitializeLXSource(ctx, ScriptInfo{
		Name: "幻音音源", Version: "v3", Author: "竹佀", RawScript: string(source),
	}, string(source), client); err != nil {
		t.Fatalf("initialize Huanyin source: %v", err)
	}

	result, err := rt.dispatchLXRequest(ctx, "mg", "search", map[string]any{
		"keyword": "周杰伦", "page": 9, "limit": 2,
	}, client)
	if err != nil {
		t.Fatalf("search Migu with limit 2: %v", err)
	}
	items, ok := result.([]any)
	if !ok || len(items) != 2 {
		t.Fatalf("search result = %#v, want exactly two songs", result)
	}
	if len(pages) != 2 || pages[0] != "1" || pages[1] != "2" {
		t.Fatalf("Migu search pages = %#v, want [1 2] and stop at limit", pages)
	}
	for i, item := range items {
		song, ok := item.(map[string]any)
		if !ok {
			t.Fatalf("search item %d type = %T, want map", i, item)
		}
		wantName := "歌曲" + string(rune('1'+i))
		if song["name"] != wantName || song["singer"] != "周杰伦" || song["source"] != "mg" {
			t.Fatalf("search item %d = %#v, unexpected mapped metadata", i, song)
		}
	}
}
