//go:build lxquickjs && cgo

package lxruntime

import (
	"context"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
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
