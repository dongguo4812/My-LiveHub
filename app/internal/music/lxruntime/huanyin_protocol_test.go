//go:build lxquickjs && cgo

package lxruntime

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func huanyinProtocolFixture(t *testing.T, client *http.Client) (*Runtime, context.Context, context.CancelFunc) {
	t.Helper()
	source, err := os.ReadFile(filepath.Join("testdata", "sources", "huanyin.js"))
	if err != nil {
		t.Fatal(err)
	}
	rt := newTestRuntime(t, 12*time.Second)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	if err := rt.InitializeLXSource(ctx, ScriptInfo{
		Name: "幻音音源", Version: "v3", Author: "竹佀", RawScript: string(source),
	}, string(source), client); err != nil {
		cancel()
		_ = rt.Close()
		t.Fatalf("initialize pinned Huanyin source: %v", err)
	}
	return rt, ctx, cancel
}

func huanyinJSONResponse(request *http.Request, status int, body string) (*http.Response, error) {
	return &http.Response{
		StatusCode: status, Status: http.StatusText(status), Header: make(http.Header),
		Body: io.NopCloser(strings.NewReader(body)), Request: request,
	}, nil
}

func TestHuanyinMiguPlaybackAllMatchPathsCanResolve(t *testing.T) {
	cases := []struct {
		name string
		successAt int
		wantKeywords []string
	}{
		{name: "album_match", successAt: 1, wantKeywords: []string{"测试歌曲测试专辑"}},
		{name: "singer_match", successAt: 2, wantKeywords: []string{"测试歌曲测试专辑", "测试歌曲测试歌手"}},
		{name: "title_match", successAt: 3, wantKeywords: []string{"测试歌曲测试专辑", "测试歌曲测试歌手", "测试歌曲"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var requests []string
			client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
				if request.Method != http.MethodGet || request.URL.Host != "api.xcvts.cn" || request.URL.Path != "/api/music/migu" {
					return nil, fmt.Errorf("unexpected Huanyin request: %s %s", request.Method, request.URL.Redacted())
				}
				q := request.URL.Query()
				if q.Get("n") != "1" || q.Get("num") != "1" || q.Get("type") != "json" {
					return nil, fmt.Errorf("unexpected Migu lookup query: %s", request.URL.RawQuery)
				}
				requests = append(requests, q.Get("gm"))
				if len(requests) < tc.successAt {
					return huanyinJSONResponse(request, http.StatusOK,
						`{"code":200,"title":"其他歌曲","artist":"其他歌手","album":"其他专辑","music_url":"https://cdn.example.invalid/decoy.mp3"}`)
				}
				return huanyinJSONResponse(request, http.StatusOK,
					`{"code":200,"title":"测试歌曲","artist":"测试歌手","album":"测试专辑","music_url":"https://cdn.example.invalid/`+tc.name+`.mp3"}`)
			})}
			rt, ctx, cancel := huanyinProtocolFixture(t, client)
			defer cancel()
			defer rt.Close()
			result, err := rt.dispatchLXRequest(ctx, "mg", "musicUrl", map[string]any{
				"type": "320k",
				"musicInfo": map[string]any{"name": "测试歌曲", "singer": "测试歌手", "albumName": "测试专辑"},
			}, client)
			if err != nil {
				t.Fatalf("resolve through %s: %v", tc.name, err)
			}
			wantURL := "https://cdn.example.invalid/" + tc.name + ".mp3"
			if result != wantURL {
				t.Fatalf("playback URL = %#v, want %q", result, wantURL)
			}
			if len(requests) != len(tc.wantKeywords) {
				t.Fatalf("lookup sequence = %v, want %v", requests, tc.wantKeywords)
			}
			for i, want := range tc.wantKeywords {
				if requests[i] != want {
					t.Fatalf("lookup %d keyword = %q, want %q", i+1, requests[i], want)
				}
			}
		})
	}
}

func TestHuanyinMiguPlaybackRetriesAfterHTTPTransportAndJSONFailures(t *testing.T) {
	cases := []struct {
		name string
		first func(*http.Request) (*http.Response, error)
	}{
		{name: "http_503", first: func(r *http.Request) (*http.Response, error) {
			return huanyinJSONResponse(r, http.StatusServiceUnavailable, `{"code":503}`)
		}},
		{name: "transport_error", first: func(*http.Request) (*http.Response, error) {
			return nil, fmt.Errorf("deterministic connection reset")
		}},
		{name: "malformed_json", first: func(r *http.Request) (*http.Response, error) {
			return huanyinJSONResponse(r, http.StatusOK, `{"code":200,`)
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var requests []string
			client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
				if request.URL.Host != "api.xcvts.cn" || request.URL.Path != "/api/music/migu" {
					return nil, fmt.Errorf("unexpected endpoint: %s", request.URL.Redacted())
				}
				requests = append(requests, request.URL.Query().Get("gm"))
				if len(requests) == 1 {
					return tc.first(request)
				}
				return huanyinJSONResponse(request, http.StatusOK,
					`{"code":200,"title":"测试歌曲","artist":"测试歌手","album":"测试专辑","music_url":"https://cdn.example.invalid/recovered.mp3"}`)
			})}
			rt, ctx, cancel := huanyinProtocolFixture(t, client)
			defer cancel()
			defer rt.Close()
			result, err := rt.dispatchLXRequest(ctx, "mg", "musicUrl", map[string]any{
				"musicInfo": map[string]any{"name": "测试歌曲", "singer": "测试歌手", "albumName": "测试专辑"},
			}, client)
			if err != nil {
				t.Fatalf("fallback after %s: %v", tc.name, err)
			}
			if result != "https://cdn.example.invalid/recovered.mp3" {
				t.Fatalf("fallback result = %#v", result)
			}
			if len(requests) != 2 || requests[0] != "测试歌曲测试专辑" || requests[1] != "测试歌曲测试歌手" {
				t.Fatalf("fallback request sequence = %v", requests)
			}
		})
	}
}

func TestHuanyinMiguSearchUsesThreePagesAndSkipsNegativeResponses(t *testing.T) {
	var pages []string
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if request.Method != http.MethodGet || request.URL.Host != "api.xcvts.cn" || request.URL.Path != "/api/music/migu" {
			return nil, fmt.Errorf("unexpected Migu search endpoint: %s", request.URL.Redacted())
		}
		q := request.URL.Query()
		if q.Get("gm") != "搜索关键词" || q.Get("num") != "1" || q.Get("type") != "json" {
			return nil, fmt.Errorf("unexpected search query: %s", request.URL.RawQuery)
		}
		pages = append(pages, q.Get("n"))
		switch q.Get("n") {
		case "1":
			return huanyinJSONResponse(request, http.StatusOK, `{"code":404,"message":"no result"}`)
		case "2":
			return huanyinJSONResponse(request, http.StatusOK,
				`{"code":200,"title":"第二页歌曲","artist":"测试歌手","album":"测试专辑","duration":"03:20"}`)
		case "3":
			return huanyinJSONResponse(request, http.StatusOK, `not-json`)
		default:
			return nil, fmt.Errorf("unexpected page %q", q.Get("n"))
		}
	})}
	rt, ctx, cancel := huanyinProtocolFixture(t, client)
	defer cancel()
	defer rt.Close()
	result, err := rt.dispatchLXRequest(ctx, "mg", "search", map[string]any{
		"keyword": "搜索关键词", "page": 8, "limit": 10,
	}, client)
	if err != nil {
		t.Fatalf("search with mixed page responses: %v", err)
	}
	items, ok := result.([]any)
	if !ok || len(items) != 1 {
		t.Fatalf("search result = %#v, want one successful page result", result)
	}
	item, ok := items[0].(map[string]any)
	if !ok || item["name"] != "第二页歌曲" || item["singer"] != "测试歌手" ||
		item["albumName"] != "测试专辑" || item["source"] != "mg" {
		t.Fatalf("mapped search item = %#v", items[0])
	}
	if strings.Join(pages, ",") != "1,2,3" {
		t.Fatalf("search page sequence = %v, want [1 2 3]", pages)
	}
}

func TestHuanyinMiguSearchRejectsInvalidAndEmptyResults(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		return huanyinJSONResponse(request, http.StatusOK, `{"code":404,"message":"not found"}`)
	})}
	rt, ctx, cancel := huanyinProtocolFixture(t, client)
	defer cancel()
	defer rt.Close()

	if _, err := rt.dispatchLXRequest(ctx, "mg", "search", map[string]any{"keyword": "  "}, client); err == nil || !strings.Contains(err.Error(), "没关键词") {
		t.Fatalf("empty keyword error = %v, want missing-keyword error", err)
	}
	if _, err := rt.dispatchLXRequest(ctx, "mg", "search", map[string]any{"keyword": "不存在"}, client); err == nil || !strings.Contains(err.Error(), "没找到") {
		t.Fatalf("empty search error = %v, want not-found error", err)
	}
}

func TestHuanyinSuccessfulMiguPlaybackURLIsCached(t *testing.T) {
	var requests int
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		requests++
		if request.URL.Query().Get("gm") != "缓存歌曲缓存专辑" {
			return nil, fmt.Errorf("unexpected cache lookup: %s", request.URL.RawQuery)
		}
		return huanyinJSONResponse(request, http.StatusOK,
			`{"code":200,"title":"缓存歌曲","artist":"缓存歌手","album":"缓存专辑","music_url":"https://cdn.example.invalid/cached.mp3"}`)
	})}
	rt, ctx, cancel := huanyinProtocolFixture(t, client)
	defer cancel()
	defer rt.Close()
	info := map[string]any{"musicInfo": map[string]any{
		"name": "缓存歌曲", "singer": "缓存歌手", "albumName": "缓存专辑",
	}}
	for i := 0; i < 2; i++ {
		result, err := rt.dispatchLXRequest(ctx, "mg", "musicUrl", info, client)
		if err != nil || result != "https://cdn.example.invalid/cached.mp3" {
			t.Fatalf("cached playback %d = %#v, error %v", i+1, result, err)
		}
	}
	if requests != 1 {
		t.Fatalf("identical successful playback made %d requests, want one cached lookup", requests)
	}
}
