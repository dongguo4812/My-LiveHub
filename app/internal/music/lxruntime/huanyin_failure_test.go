//go:build lxquickjs

package lxruntime

import (
	"context"
	"encoding/json"
	"io"
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

 
// TestHuanyinMiguPlaybackDoesNotAcceptServerErrorBodies verifies that a
// matching-looking JSON body returned with HTTP 503 is not treated as a
// playable candidate. The source must continue its album -> singer -> title
// fallback sequence instead of trusting the body of a failed HTTP response.
func TestHuanyinMiguPlaybackDoesNotAcceptServerErrorBodies(t *testing.T) {
	source, err := os.ReadFile(filepath.Join("testdata", "sources", "huanyin.js"))
	if err != nil { t.Fatal(err) }
	rt := newTestRuntime(t, 12*time.Second)
	defer rt.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	var keywords []string
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if request.Method != http.MethodGet || request.URL.Host != "api.xcvts.cn" || request.URL.Path != "/api/music/migu" {
			return nil, fmt.Errorf("unexpected Huanyin HTTP-status request: %s %s", request.Method, request.URL.Redacted())
		}
		keywords = append(keywords, request.URL.Query().Get("gm"))
		body := `{"code":200,"title":"状态码测试歌曲","artist":"状态码测试歌手","album":"状态码测试专辑","music_url":"https://cdn.example.invalid/should-not-play.mp3"}`
		return &http.Response{
			StatusCode: http.StatusServiceUnavailable, Status: "503 Service Unavailable",
			Header: http.Header{"Content-Type": []string{"application/json"}},
			Body: io.NopCloser(strings.NewReader(body)), Request: request,
		}, nil
	})}
	if err := rt.InitializeLXSource(ctx, ScriptInfo{
		Name: "幻音音源", Version: "v3", Author: "竹佀", RawScript: string(source),
	}, string(source), client); err != nil { t.Fatalf("initialize pinned Huanyin source: %v", err) }

	_, err = rt.dispatchLXRequest(ctx, "mg", "musicUrl", map[string]any{
		"type": "320k",
		"musicInfo": map[string]any{
			"name": "状态码测试歌曲", "singer": "状态码测试歌手", "albumName": "状态码测试专辑",
		},
	}, client)
	if err == nil || !strings.Contains(err.Error(), "咪咕找不到") {
		t.Fatalf("HTTP 503 playback result = %v, want source-level not-found error", err)
	}
	want := []string{"状态码测试歌曲状态码测试专辑", "状态码测试歌曲状态码测试歌手", "状态码测试歌曲"}
	if len(keywords) != len(want) { t.Fatalf("HTTP 503 fallback attempts = %v, want %v", keywords, want) }
	for i := range want {
		if keywords[i] != want[i] { t.Fatalf("HTTP 503 attempt %d keyword = %q, want %q (all: %v)", i+1, keywords[i], want[i], keywords) }
	}
}


// TestHuanyinMiguSearchDoesNotAcceptServerErrorBodies verifies that the
// search handler also rejects HTTP error bodies that contain a success-shaped
// JSON payload, and continues through its bounded page sequence.
func TestHuanyinMiguSearchDoesNotAcceptServerErrorBodies(t *testing.T) {
	source, err := os.ReadFile(filepath.Join("testdata", "sources", "huanyin.js"))
	if err != nil {
		t.Fatal(err)
	}
	rt := newTestRuntime(t, 12*time.Second)
	defer rt.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	var pages []string
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if request.Method != http.MethodGet || request.URL.Host != "api.xcvts.cn" || request.URL.Path != "/api/music/migu" {
			return nil, fmt.Errorf("unexpected Huanyin search-status request: %s %s", request.Method, request.URL.Redacted())
		}
		pages = append(pages, request.URL.Query().Get("n"))
		body := `{"code":200,"title":"错误状态歌曲","artist":"错误状态歌手","album":"错误状态专辑","music_url":"https://cdn.example.invalid/should-not-play.mp3"}`
		return &http.Response{
			StatusCode: http.StatusServiceUnavailable, Status: "503 Service Unavailable",
			Header: http.Header{"Content-Type": []string{"application/json"}},
			Body: io.NopCloser(strings.NewReader(body)), Request: request,
		}, nil
	})}
	if err := rt.InitializeLXSource(ctx, ScriptInfo{
		Name: "幻音音源", Version: "v3", Author: "竹佀", RawScript: string(source),
	}, string(source), client); err != nil {
		t.Fatalf("initialize pinned Huanyin source: %v", err)
	}

	_, err = rt.dispatchLXRequest(ctx, "mg", "search", map[string]any{
		"keyword": "错误状态歌曲", "limit": 10,
	}, client)
	if err == nil || !strings.Contains(err.Error(), "没找到") {
		t.Fatalf("HTTP 503 search result = %v, want source-level not-found error", err)
	}
	want := []string{"1", "2", "3"}
	if len(pages) != len(want) {
		t.Fatalf("HTTP 503 search pages = %v, want %v", pages, want)
	}
	for i := range want {
		if pages[i] != want[i] {
			t.Fatalf("HTTP 503 search page[%d] = %q, want %q (all: %v)", i, pages[i], want[i], pages)
		}
	}
}

// TestHuanyinMiguSearchSkipsHTTPFailureAndContinues verifies that a failed
// page does not become a result even when its body looks successful, and that
// subsequent successful pages are parsed until the requested limit is met.
func TestHuanyinMiguSearchSkipsHTTPFailureAndContinues(t *testing.T) {
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
			return nil, fmt.Errorf("unexpected Huanyin search continuation request: %s %s", request.Method, request.URL.Redacted())
		}
		query := request.URL.Query()
		if query.Get("gm") != "连续搜索测试" || query.Get("num") != "1" || query.Get("type") != "json" {
			return nil, fmt.Errorf("unexpected Migu search query: %s", request.URL.RawQuery)
		}
		page := query.Get("n")
		pages = append(pages, page)
		if page == "1" {
			body := `{"code":200,"title":"不能采纳的错误页","artist":"错误歌手","album":"错误专辑","duration":"00:01"}`
			return &http.Response{
				StatusCode: http.StatusServiceUnavailable, Status: "503 Service Unavailable",
				Header: http.Header{"Content-Type": []string{"application/json"}},
				Body: io.NopCloser(strings.NewReader(body)), Request: request,
			}, nil
		}
		if page != "2" && page != "3" {
			return nil, fmt.Errorf("unexpected Migu page %q", page)
		}
		body, _ := json.Marshal(map[string]any{
			"code": 200, "title": "有效歌曲" + page, "artist": "有效歌手",
			"album": "有效专辑", "duration": "03:21",
		})
		return &http.Response{
			StatusCode: http.StatusOK, Status: "200 OK", Header: make(http.Header),
			Body: io.NopCloser(strings.NewReader(string(body))), Request: request,
		}, nil
	})}

	if err := rt.InitializeLXSource(ctx, ScriptInfo{
		Name: "幻音音源", Version: "v3", Author: "竹佀", RawScript: string(source),
	}, string(source), client); err != nil {
		t.Fatalf("initialize pinned Huanyin source: %v", err)
	}

	result, err := rt.dispatchLXRequest(ctx, "mg", "search", map[string]any{
		"keyword": "连续搜索测试", "limit": 2,
	}, client)
	if err != nil {
		t.Fatalf("search Migu after first-page HTTP failure: %v", err)
	}
	items, ok := result.([]any)
	if !ok || len(items) != 2 {
		t.Fatalf("search result = %#v, want two successful-page songs", result)
	}
	if len(pages) != 3 || pages[0] != "1" || pages[1] != "2" || pages[2] != "3" {
		t.Fatalf("Migu search pages = %#v, want [1 2 3]", pages)
	}
	for i, item := range items {
		song, ok := item.(map[string]any)
		if !ok {
			t.Fatalf("search item %d type = %T, want map", i, item)
		}
		wantName := "有效歌曲" + string(rune('2'+i))
		if song["name"] != wantName || song["singer"] != "有效歌手" ||
			song["albumName"] != "有效专辑" || song["interval"] != "03:21" || song["source"] != "mg" {
			t.Fatalf("search item %d = %#v, unexpected mapped metadata", i, song)
		}
		if strings.Contains(song["name"].(string), "不能采纳") {
			t.Fatalf("HTTP 503 response leaked into search results: %#v", song)
		}
	}
}

// TestHuanyinMiguSearchSkipsMalformedAndRejectedJSONPages verifies that
// HTTP 200 responses with malformed JSON or a non-success business code are
// skipped, while a later valid page is still returned.
func TestHuanyinMiguSearchSkipsMalformedAndRejectedJSONPages(t *testing.T) {
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
			return nil, fmt.Errorf("unexpected Huanyin malformed-response request: %s %s", request.Method, request.URL.Redacted())
		}
		query := request.URL.Query()
		if query.Get("gm") != "响应体异常测试" || query.Get("num") != "1" || query.Get("type") != "json" {
			return nil, fmt.Errorf("unexpected Migu search query: %s", request.URL.RawQuery)
		}
		page := query.Get("n")
		pages = append(pages, page)
		var body string
		switch page {
		case "1":
			body = `{"code":200,"title":`
		case "2":
			body = `{"code":500,"title":"业务失败页","artist":"不应收录","music_url":"https://cdn.example.invalid/no.mp3"}`
		case "3":
			body = `{"code":200,"title":"最终有效歌曲","artist":"有效歌手","album":"有效专辑","duration":"03:45"}`
		default:
			return nil, fmt.Errorf("unexpected Migu page %q", page)
		}
		return &http.Response{
			StatusCode: http.StatusOK, Status: "200 OK",
			Header: http.Header{"Content-Type": []string{"application/json"}},
			Body: io.NopCloser(strings.NewReader(body)), Request: request,
		}, nil
	})}

	if err := rt.InitializeLXSource(ctx, ScriptInfo{
		Name: "幻音音源", Version: "v3", Author: "竹佀", RawScript: string(source),
	}, string(source), client); err != nil {
		t.Fatalf("initialize pinned Huanyin source: %v", err)
	}

	result, err := rt.dispatchLXRequest(ctx, "mg", "search", map[string]any{
		"keyword": "响应体异常测试", "limit": 1,
	}, client)
	if err != nil {
		t.Fatalf("search Migu after malformed and rejected JSON pages: %v", err)
	}
	items, ok := result.([]any)
	if !ok || len(items) != 1 {
		t.Fatalf("search result = %#v, want exactly one valid song", result)
	}
	if len(pages) != 3 || pages[0] != "1" || pages[1] != "2" || pages[2] != "3" {
		t.Fatalf("Migu search pages = %#v, want [1 2 3]", pages)
	}
	song, ok := items[0].(map[string]any)
	if !ok {
		t.Fatalf("search item type = %T, want map", items[0])
	}
	if song["name"] != "最终有效歌曲" || song["singer"] != "有效歌手" ||
		song["albumName"] != "有效专辑" || song["interval"] != "03:45" || song["source"] != "mg" {
		t.Fatalf("search item = %#v, unexpected mapped metadata", song)
	}
}
