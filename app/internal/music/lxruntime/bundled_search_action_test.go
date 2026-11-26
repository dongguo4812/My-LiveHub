//go:build lxproduction

package lxruntime

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
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
			_, _ = w.Write([]byte(`{"error_code":0,"data":{"total":31,"lists":[]}}`))
		case "/kgv3":
			seen["kgv3"] = true
			q := r.URL.Query()
			if q.Get("page") != "2" || q.Get("pagesize") != "30" || q.Get("format") != "json" { t.Errorf("Kugou V3 pagination = %v", q) }
			_, _ = w.Write([]byte(`{"status":1,"data":{"total":31,"info":[{"audio_id":202,"hash":"HASH202","songname":"Kugou page two","singername":"Singer","album_name":"Album","duration":181,"filesize":1024,"320filesize":2048,"320hash":"HQHASH202"}]}}`))
		case "/tx":
			seen["tx"] = true
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil { t.Errorf("QQ body: %v", err) }
			service := body["music.search.SearchCgiService"].(map[string]any)
			param := service["param"].(map[string]any)
			if param["page_num"] != float64(2) || param["num_per_page"] != float64(30) || param["search_type"] != float64(0) {
				t.Errorf("QQ pagination/search type = %#v", param)
			}
			_, _ = w.Write([]byte(`{"code":0,"music.search.SearchCgiService":{"code":0,"data":{"body":{"song":{"list":[{"id":303,"mid":"TX303","title":"QQ page two","interval":182,"singer":[{"name":"Singer"}],"album":{"mid":"ALB","name":"Album"},"file":{"media_mid":"MEDIA303","size_128mp3":1000,"size_320mp3":2000}}]}},"meta":{"sum":31}}}}`))
		case "/txfail":
			http.Error(w, "QQ primary unavailable", http.StatusInternalServerError)
		case "/txlegacy":
			seen["txlegacy"] = true
			q := r.URL.Query()
			if q.Get("p") != "2" || q.Get("n") != "30" { t.Errorf("QQ legacy pagination = %v", q) }
			_, _ = w.Write([]byte(`{"code":0,"data":{"song":{"totalnum":31,"list":[{"songmid":"LEGACY_TX","songid":909,"songname":"QQ legacy fallback","interval":180,"singer":[{"name":"Singer"}],"albummid":"ALB","albumname":"Album","file":{"media_mid":"LEGACY_MEDIA","size128":1000}}]}}}`))
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
		"kw": server.URL + "/kw", "kg": server.URL + "/kg", "kgV3": server.URL + "/kgv3",
		"tx": server.URL + "/tx", "txLegacy": server.URL + "/txlegacy",
		"wy": server.URL + "/wy", "mg": server.URL + "/mg",
		"mgFallback": server.URL + "/mgfallback",
	}
	wantIDs := map[string]string{"kw":"kw_KW31","kg":"202_HASH202","tx":"tx_TX303","wy":"wy_404","mg":"mg_505"}

	for sourceID, wantID := range wantIDs {
		result, dispatchErr := runtime.DispatchLXRequest(context.Background(), sourceID, "search", map[string]any{
			"text": "fixture", "page": 2, "limit": 30, "__testEndpoints": endpoints,
		})
		if dispatchErr != nil {
			t.Fatalf("%s QuickJS search failed: %v", sourceID, dispatchErr)
		}
		encoded, marshalErr := json.Marshal(result)
		if marshalErr != nil {
			t.Fatalf("%s search result JSON encoding failed: %v", sourceID, marshalErr)
		}
		var pageResult struct {
			Tracks []struct { ID string `json:"id"` } `json:"tracks"`
			Page int `json:"page"`
			PageSize int `json:"page_size"`
			Total int64 `json:"total"`
			Source string `json:"source"`
		}
		if decodeErr := json.Unmarshal(encoded, &pageResult); decodeErr != nil {
			t.Fatalf("%s search result JSON decoding failed: %v (%s)", sourceID, decodeErr, encoded)
		}
		if len(pageResult.Tracks) != 1 || pageResult.Tracks[0].ID != wantID {
			t.Fatalf("%s native track ID = %#v, want %q (result=%s)", sourceID, pageResult.Tracks, wantID, encoded)
		}
		if pageResult.Page != 2 || pageResult.PageSize != 30 || pageResult.Total != 31 || pageResult.Source != sourceID {
			t.Fatalf("%s pagination/source metadata mismatch: %#v", sourceID, pageResult)
		}
	}
	for _, sourceID := range []string{"kw", "kg", "tx", "wy", "mg"} {
		if !seen[sourceID] { t.Errorf("%s request did not pass through lx.request HTTP host", sourceID) }
	}
	if !seen["kgv3"] { t.Error("Kugou empty-page fallback did not pass through lx.request HTTP host") }
	fallback, fallbackErr := runtime.DispatchLXRequest(context.Background(), "tx", "search", map[string]any{
		"text": "legacy fallback fixture", "page": 2, "limit": 30,
		"__testEndpoints": map[string]string{"tx": server.URL + "/txfail", "txLegacy": server.URL + "/txlegacy"},
	})
	if fallbackErr != nil { t.Fatalf("QQ legacy fallback failed after primary HTTP 500: %v", fallbackErr) }
	fallbackJSON, _ := json.Marshal(fallback)
	var fallbackResult struct {
		Tracks []struct { ID string `json:"id"`; Title string `json:"title"`; SourceData map[string]string `json:"source_data"` } `json:"tracks"`
		Page int `json:"page"`; Total int64 `json:"total"`
	}
	if err := json.Unmarshal(fallbackJSON, &fallbackResult); err != nil { t.Fatalf("decode QQ fallback result: %v", err) }
	if len(fallbackResult.Tracks) != 1 || fallbackResult.Tracks[0].ID != "tx_LEGACY_TX" ||
		fallbackResult.Tracks[0].Title != "QQ legacy fallback" || fallbackResult.Tracks[0].SourceData["song_id"] != "909" ||
		fallbackResult.Page != 2 || fallbackResult.Total != 31 {
		t.Fatalf("QQ legacy fallback result mismatch: %s", fallbackJSON)
	}
	if !seen["txlegacy"] { t.Fatal("QQ legacy fallback did not use lx.request HTTP host") }
}

func TestBundledSearchActionPropagatesHTTPFailuresAndCancellation(t *testing.T) {
	var slowStarted atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/failure":
			http.Error(w, "provider unavailable", http.StatusBadGateway)
		case "/slow":
			slowStarted.Add(1)
			<-r.Context().Done()
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	runtime, err := New(Options{})
	if err != nil {
		t.Fatalf("create QuickJS runtime: %v", err)
	}
	defer runtime.Close()
	runtime.allowPrivateNetworkForTests = true
	source := BundledSearchAction()
	if err := runtime.InitializeLXSource(context.Background(), source.Info, source.Source, server.Client()); err != nil {
		t.Fatalf("initialize built-in search actions: %v", err)
	}

	providers := []string{"kw", "kg", "tx", "wy", "mg"}
	for _, sourceID := range providers {
		t.Run(sourceID+"/http_failure", func(t *testing.T) {
			endpoints := map[string]string{sourceID: server.URL + "/failure"}
			if sourceID == "mg" {
				endpoints["mgFallback"] = server.URL + "/failure"
			}
			if sourceID == "tx" {
				endpoints["txLegacy"] = server.URL + "/failure"
			}
			_, dispatchErr := runtime.DispatchLXRequest(context.Background(), sourceID, "search", map[string]any{
				"text": "failure", "page": 1, "limit": 30, "__testEndpoints": endpoints,
			})
			if dispatchErr == nil || !strings.Contains(dispatchErr.Error(), "HTTP 502") {
				t.Fatalf("%s HTTP failure was not propagated: %v", sourceID, dispatchErr)
			}
		})

		t.Run(sourceID+"/cancellation", func(t *testing.T) {
			before := slowStarted.Load()
			ctx, cancel := context.WithCancel(context.Background())
			done := make(chan error, 1)
			endpoints := map[string]string{sourceID: server.URL + "/slow"}
			if sourceID == "mg" {
				endpoints["mgFallback"] = server.URL + "/slow"
			}
			if sourceID == "tx" {
				endpoints["txLegacy"] = server.URL + "/slow"
			}
			go func() {
				_, dispatchErr := runtime.DispatchLXRequest(ctx, sourceID, "search", map[string]any{
					"text": "cancel", "page": 1, "limit": 30, "__testEndpoints": endpoints,
				})
				done <- dispatchErr
			}()

			deadline := time.Now().Add(2 * time.Second)
			for slowStarted.Load() == before && time.Now().Before(deadline) {
				time.Sleep(5 * time.Millisecond)
			}
			if slowStarted.Load() == before {
				cancel()
				t.Fatalf("%s QuickJS search did not start its HTTP request", sourceID)
			}
			cancel()
			select {
			case dispatchErr := <-done:
				if dispatchErr == nil || (dispatchErr != context.Canceled && !strings.Contains(dispatchErr.Error(), "context canceled")) {
					t.Fatalf("%s cancellation was not propagated: %v", sourceID, dispatchErr)
				}
				if sourceID == "tx" && slowStarted.Load() != before+1 {
					t.Fatalf("QQ cancellation started a legacy fallback request; request count=%d, want %d", slowStarted.Load()-before, 1)
				}
			case <-time.After(2 * time.Second):
				t.Fatalf("%s cancelled QuickJS search did not return promptly", sourceID)
			}
		})
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

	manager, err := NewSourceManagerWithScripts(nil, []LXSourceScript{source})
	if err != nil { t.Fatalf("construct source manager: %v", err) }
	defer manager.Close()
	if err := manager.Start(context.Background()); err != nil { t.Fatalf("initialize source manager: %v", err) }
	for _, sourceID := range []string{"kw", "kg", "tx", "wy", "mg"} {
		if !manager.HasExtensionAction(sourceID, "search") {
			t.Errorf("%s is not registered as a QuickJS search action", sourceID)
		}
	}
}

