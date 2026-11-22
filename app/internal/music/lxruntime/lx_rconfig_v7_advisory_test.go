//go:build lxquickjs && cgo

package lxruntime

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"strconv"
	"testing"
	"sort"
	"time"
)

// TestLXSourceAcceptsLiveV7RConfigAdvisory pins the currently observed server
// response: V7 is still enabled even though the server advertises V8 as its
// minimum version. The update notice must not prevent the V7 source from
// registering its built-in providers.
func TestLXSourceAcceptsLiveV7RConfigAdvisory(t *testing.T) {
	source, err := os.ReadFile(filepath.Join("testdata", "sources", "lx.js"))
	if err != nil {
		t.Fatal(err)
	}

	const liveResponse = `{"code":0,"msg":"success","data":{"enabled":true,"paused":false,"pausedText":"源已更新，请关注更新公告\\n更新地址:https://www.kdocs.cn/l/cpb2oArQ6mUA","version":8,"minVersion":8,"updateUrl":"https://www.kdocs.cn/l/cpb2oArQ6mUA","hqQuality":{"tx":{"qualities":[]},"kg":{"qualities":[]},"wy":{"qualities":[]}}}}`
	requests := 0
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		requests++
		if request.Method != http.MethodGet || request.URL.Path != "/rconfig" {
			return nil, io.ErrUnexpectedEOF
		}
		if request.URL.Query().Get("v") != "7" {
			t.Errorf("V7 fixture request version = %q, want 7", request.URL.Query().Get("v"))
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Status:     "200 OK",
			Header:    http.Header{"Content-Type": []string{"application/json"}},
			Body:      io.NopCloser(strings.NewReader(liveResponse)),
			Request:   request,
		}, nil
	})}

	rt := newTestRuntime(t, 10*time.Second)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := rt.InitializeLXSource(ctx, ScriptInfo{
		Name: "独家音源", Version: "7", Author: "w",
		Description: "后续更新，请关注微信公众号: 洛雪科技",
		RawScript: string(source),
	}, string(source), client); err != nil {
		t.Fatalf("V7 source should initialize when live /rconfig advertises V8: %v", err)
	}
	if requests != 1 {
		t.Fatalf("/rconfig request count = %d, want 1", requests)
	}

	state, err := rt.Eval(ctx, `JSON.stringify({
		initialized: globalThis.__lxHostState.initialized,
		requestHandler: typeof globalThis.__lxHostHandlers.request
	})`, "lx-rconfig-v7-advisory-state.js")
	if err != nil {
		t.Fatal(err)
	}
	if state != `{"initialized":true,"requestHandler":"function"}` {
		t.Fatalf("V7 state after successful V8 advisory response = %s", state)
	}
}


func TestLXSourceRConfigDeterministicProtocolFixture(t *testing.T) {
	source, err := os.ReadFile(filepath.Join("testdata", "sources", "lx.js"))
	if err != nil { t.Fatal(err) }

	const timestamp int64 = 1795334998585
	const responseFixture = `{"code":0,"msg":"success","data":{"enabled":true,"paused":false,"pausedText":"V8 is available; V7 remains accepted","version":8,"minVersion":8,"updateUrl":"https://updates.example/lx-v8","hqQuality":{"tx":{"qualities":["flac"]},"kg":{"qualities":["320k"]},"wy":{"qualities":["flac"]}}}}`
	protocolShim := "Date.now = function() { return " + strconv.FormatInt(timestamp, 10) + "; };\n"

	var captured *http.Request
	requestCount := 0
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		requestCount++
		captured = request.Clone(request.Context())
		if request.Method != http.MethodGet || request.URL.Path != "/rconfig" {
			return nil, errors.New("unexpected /rconfig request: " + request.Method + " " + request.URL.Redacted())
		}
		return &http.Response{
			StatusCode: http.StatusOK, Status: "200 OK",
			Header: http.Header{"Content-Type": []string{"application/json"}},
			Body: io.NopCloser(strings.NewReader(responseFixture)), Request: request,
		}, nil
	})}

	rt := newTestRuntime(t, 10*time.Second)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := rt.InitializeLXSource(ctx, ScriptInfo{
		Name: "独家音源", Version: "7", Author: "w",
		Description: "后续更新，请关注微信公众号: 洛雪科技",
		RawScript: string(source),
	}, protocolShim+string(source), client); err != nil {
		t.Fatalf("V7 source failed to initialize from deterministic /rconfig success fixture: %v", err)
	}
	if requestCount != 1 || captured == nil {
		t.Fatalf("/rconfig request count=%d, want exactly one", requestCount)
	}
	if captured.URL.Scheme != "https" {
		t.Errorf("/rconfig scheme = %q, want https", captured.URL.Scheme)
	}
	if got := captured.Header.Get("Content-Type"); got != "application/json" {
		t.Errorf("request Content-Type = %q, want application/json", got)
	}
	if got := captured.Header.Get("User-Agent"); got != "lx-music-desktop/2.0.0" {
		t.Errorf("request User-Agent = %q, want lx-music-desktop/2.0.0", got)
	}
	query := captured.URL.Query()
	keys := make([]string, 0, len(query))
	for key := range query { keys = append(keys, key) }
	sort.Strings(keys)
	if got, want := strings.Join(keys, ","), "e,fp,s2,si,to,v"; got != want {
		t.Fatalf("/rconfig query keys = %q, want %q", got, want)
	}
	if query.Get("v") != "7" || query.Get("fp") != "42bc92aaf1a30247e40eab9523ca0713" {
		t.Fatalf("V7 version/fingerprint mismatch: %s", captured.URL.Redacted())
	}
	sum := md5.Sum([]byte(strconv.FormatInt(timestamp, 10)))
	if got, want := query.Get("e"), hex.EncodeToString(sum[:]); got != want {
		t.Errorf("timestamp parameter e = %q, want MD5(timestamp) %q", got, want)
	}
	for key, size := range map[string]int{"e": 32, "to": 22, "s2": 32, "si": 32} {
		value := query.Get(key)
		decoded, decodeErr := hex.DecodeString(value)
		if decodeErr != nil || len(value) != size || (key == "to" && len(decoded) != 11) {
			t.Fatalf("signature parameter %s=%q is malformed", key, value)
		}
	}
	if got, want := query.Get("si"), lxRConfigSignature(timestamp, string(source)); got != want {
		t.Fatalf("deterministic V7 si = %q, want %q", got, want)
	}

	stateJSON, err := rt.Eval(ctx, "JSON.stringify({initialized:globalThis.__lxHostState.initialized,updateAlertShown:globalThis.__lxHostState.updateAlertShown,sources:Object.keys(globalThis.__lxHostState.initInfo.sources)})", "lx-rconfig-deterministic-protocol-state.js")
	if err != nil { t.Fatal(err) }
	var state struct {
		Initialized bool `json:"initialized"`
		UpdateAlertShown bool `json:"updateAlertShown"`
		Sources []string `json:"sources"`
	}
	if err := json.Unmarshal([]byte(stateJSON), &state); err != nil {
		t.Fatalf("decode deterministic protocol state %s: %v", stateJSON, err)
	}
	if !state.Initialized { t.Fatalf("successful /rconfig response did not initialize V7 source: %s", stateJSON) }
	if !state.UpdateAlertShown { t.Fatalf("V8 update advisory was not surfaced separately from V7 initialization: %s", stateJSON) }
	if len(state.Sources) == 0 { t.Fatalf("successful /rconfig response did not register providers: %s", stateJSON) }
}
