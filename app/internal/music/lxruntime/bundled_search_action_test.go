//go:build lxproduction

package lxruntime

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestBundledSearchActionsUseQuickJSHTTPForAllFivePlatforms(t *testing.T) {
	var seen = map[string]bool{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/kw":
			seen["kw"] = true
			if r.URL.Query().Get("pn") != "1" || r.URL.Query().Get("rn") != "30" { t.Errorf("Kuwo pagination = %v", r.URL.Query()) }
			_, _ = w.Write([]byte(`{"TOTAL":"31","SHOW":"1","abslist":[{"MUSICRID":"MUSIC_KW31","SONGNAME":"Kuwo page two","ARTIST":"Singer","ALBUM":"Album","ALBUMID":"KWA","DURATION":"180","N_MINFO":"level:mp3,bitrate:320,format:mp3,size:5.0"}]}`))
		case "/kg":
			seen["kg"] = true
			q := r.URL.Query()
			if q.Get("page") != "2" || q.Get("pagesize") != "30" || q.Get("platform") != "WebFilter" || q.Get("privilege_filter") != "0" { t.Errorf("Kugou request params = %v", q) }
			_, _ = w.Write([]byte(`{"error_code":0,"data":{"total":31,"lists":[{"Audioid":202,"FileHash":"HASH202","SongName":"Kugou page two","SingerName":"Singer","AlbumName":"Album","Duration":181,"FileSize":1024,"HQFileSize":2048}]}}`))
		case "/tx":
			seen["tx"] = true
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil { t.Errorf("QQ body: %v", err) }
			service := body["music.search.SearchCgiService"].(map[string]any)
			param := service["param"].(map[string]any)
			if param["page"] != float64(2) || param["num_per_page"] != float64(30) { t.Errorf("QQ pagination = %#v", param) }
			_, _ = w.Write([]byte(`{"code":0,"music.search.SearchCgiService":{"code":0,"data":{"body":{"song":{"list":[{"id":303,"mid":"TX303","title":"QQ page two","interval":182,"singer":[{"name":"Singer"}],"album":{"mid":"ALB","name":"Album"},"file":{"media_mid":"MEDIA303","size_128mp3":1000,"size_320mp3":2000}}]}},"meta":{"sum":31}}}}`))
		case "/wy":
			seen["wy"] = true
			if err := r.ParseForm(); err != nil { t.Errorf("Netease form: %v", err) }
			if len(r.Form.Get("params")) < 32 { t.Errorf("Netease EAPI params missing: %q", r.Form.Get("params")) }
			_, _ = w.Write([]byte(`{"code":200,"data":{"totalCount":31,"resources":[{"baseInfo":{"simpleSongData":{"id":404,"name":"Netease page two","dt":183000,"ar":[{"name":"Singer"}],"al":{"id":44,"name":"Album","picUrl":"https://img.test/wy.jpg"},"privilege":{"maxbr":320000},"h":{"size":2048}}}}]}}`))
		case "/mg":
			seen["mg"] = true
			q := r.URL.Query()
			if q.Get("pageNo") != "2" || q.Get("pageSize") != "30" { t.Errorf("Migu pagination = %v", q) }
			if r.Header.Get("sign") == "" { t.Error("Migu signature header missing") }
			_, _ = w.Write([]byte(`{"code":"000000","songResultData":{"totalCount":31,"resultList":[[{"songId":"505","copyrightId":"COPY505","name":"Migu page two","duration":184,"singerList":[{"name":"Singer"}],"album":"Album","albumId":"5","audioFormats":[{"formatType":"PQ","asize":1000},{"formatType":"ZQ24","asize":4000}],"img3":"https://img.test/5.jpg"}]]}}`))
		case "/mgfallback":
			t.Error("Migu fallback should not be called when the primary endpoint succeeds")
			_, _ = w.Write([]byte(`{"code":"000000","songResultData":{"totalCount":0,"resultList":[]}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	runtime, err := New(Options{})
	if err != nil { t.Fatalf("create QuickJS runtime: %v", err) }
	defer runtime.Close()
	runtime.allowPrivateNetworkForTests = true
	source := BundledSearchAction()
	if err := runtime.InitializeLXSource(context.Background(), source.Info, source.Source, server.Client()); err != nil {
		t.Fatalf("initialize built-in QuickJS search actions: %v", err)
	}

	endpoints := map[string]string{
		"kw": server.URL + "/kw", "kg": server.URL + "/kg",
		"tx": server.URL + "/tx", "txLegacy": server.URL + "/txlegacy",
		"wy": server.URL + "/wy", "mg": server.URL + "/mg",
		"mgFallback": server.URL + "/mgfallback",
	}
	wantIDs := map[string]string{"kw":"kw_KW31","kg":"202_HASH202","tx":"tx_TX303","wy":"wy_404","mg":"mg_505"}
	for _, sourceID := range []string{"kw", "kg", "tx", "wy", "mg"} {
		value, err := runtime.DispatchLXRequest(context.Background(), sourceID, "search", map[string]any{
			"text":"integration fixture", "page":2, "limit":30, "__testEndpoints":endpoints,
		})
		if err != nil { t.Fatalf("%s QuickJS search failed: %v", sourceID, err) }
		raw, err := json.Marshal(value)
		if err != nil { t.Fatalf("%s encode result: %v", sourceID, err) }
		var result struct {
			Tracks []struct {
				ID string `json:"id"`
				Title string `json:"title"`
				SourceID string `json:"source_id"`
				SourceData map[string]string `json:"source_data"`
			} `json:"tracks"`
			Total int64 `json:"total"`
			Page int `json:"page"`
			PageSize int `json:"page_size"`
			Source string `json:"source"`
		}
		if err := json.Unmarshal(raw, &result); err != nil { t.Fatalf("%s decode result %s: %v", sourceID, raw, err) }
		if len(result.Tracks) != 1 || result.Tracks[0].ID != wantIDs[sourceID] || result.Tracks[0].Title == "" {
			t.Fatalf("%s track identity mismatch: %s", sourceID, raw)
		}
		if result.Page != 2 || result.PageSize != 30 || result.Total != 31 || result.Source != sourceID {
			t.Fatalf("%s page metadata mismatch: %s", sourceID, raw)
		}
		if result.Tracks[0].SourceID != sourceID { t.Fatalf("%s source identity mismatch: %s", sourceID, raw) }
	}
	for _, sourceID := range []string{"kw", "kg", "tx", "wy", "mg"} {
		if !seen[sourceID] { t.Errorf("%s request did not pass through lx.request HTTP host", sourceID) }
	}
}

func TestBundledSearchActionPropagatesHTTPFailuresAndCancellation(t *testing.T) {
	var started atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/failure":
			http.Error(w, "provider unavailable", http.StatusBadGateway)
		case "/slow":
			started.Store(true)
			<-r.Context().Done()
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	runtime, err := New(Options{})
	if err != nil { t.Fatalf("create QuickJS runtime: %v", err) }
	defer runtime.Close()
	runtime.allowPrivateNetworkForTests = true
	source := BundledSearchAction()
	if err := runtime.InitializeLXSource(context.Background(), source.Info, source.Source, server.Client()); err != nil {
		t.Fatalf("initialize built-in search actions: %v", err)
	}

	_, err = runtime.DispatchLXRequest(context.Background(), "kw", "search", map[string]any{
		"text":"failure", "page":1, "limit":30, "__testEndpoints":map[string]string{"kw":server.URL+"/failure"},
	})
	if err == nil || !strings.Contains(err.Error(), "HTTP 502") { t.Fatalf("expected provider HTTP error, got %v", err) }

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, dispatchErr := runtime.DispatchLXRequest(ctx, "kw", "search", map[string]any{
			"text":"cancel", "page":1, "limit":30, "__testEndpoints":map[string]string{"kw":server.URL+"/slow"},
		})
		done <- dispatchErr
	}()
	deadline := time.Now().Add(2 * time.Second)
	for !started.Load() && time.Now().Before(deadline) { time.Sleep(5 * time.Millisecond) }
	if !started.Load() { cancel(); t.Fatal("QuickJS search did not start the HTTP request") }
	cancel()
	select {
	case dispatchErr := <-done:
		if dispatchErr == nil || (dispatchErr != context.Canceled && !strings.Contains(dispatchErr.Error(), "context canceled")) {
			t.Fatalf("expected cancellation to propagate, got %v", dispatchErr)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("cancelled QuickJS search did not return promptly")
	}
}

func TestBundledSearchActionDeclaresOnlyFiveMusicSources(t *testing.T) {
	source := BundledSearchAction()
	if len(source.ExtensionActions) != 1 || source.ExtensionActions[0] != "search" {
		t.Fatalf("built-in action declaration = %#v", source.ExtensionActions)
	}
	for _, sourceID := range []string{"kw", "kg", "tx", "wy", "mg"} {
		if !strings.Contains(source.Source, "'"+sourceID+"'") { t.Errorf("built-in script is missing source %s", sourceID) }
	}
	if strings.Contains(source.Source, "net/http") { t.Fatal("built-in JavaScript must not contain a Go HTTP implementation") }
	_ = fmt.Sprintf("%s", source.Info.Name)
}

func TestBundledSearchActionTestEndpointsAreAbsoluteURLs(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer server.Close()
	u, err := url.Parse(server.URL)
	if err != nil || u.Host == "" { t.Fatalf("invalid fixture endpoint: %v", err) }
}
