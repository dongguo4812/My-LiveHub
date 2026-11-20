//go:build lxquickjs && cgo

package lxruntime

import (
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The pinned lx.js source registers its request handler before requesting
// server-managed configuration. These cases ensure that a failed /rconfig
// request never marks the source as initialized.
func TestLXSourceDoesNotInitializeWhenRConfigFails(t *testing.T) {
	source, err := os.ReadFile(filepath.Join("testdata", "sources", "lx.js"))
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name      string
		roundTrip func(*http.Request) (*http.Response, error)
	}{
		{
			name: "server rejects script fingerprint",
			roundTrip: func(request *http.Request) (*http.Response, error) {
				if request.Method != http.MethodGet || request.URL.Path != "/rconfig" {
					return nil, errors.New("unexpected configuration request: " + request.Method + " " + request.URL.Redacted())
				}
				return &http.Response{
					StatusCode: http.StatusForbidden,
					Status:     "403 Forbidden",
					Header:     make(http.Header),
					Body:       io.NopCloser(strings.NewReader(`{"code":1,"msg":"脚本指纹验证失败"}`)),
					Request:    request,
				}, nil
			},
		},
		{
			name: "transport failure",
			roundTrip: func(request *http.Request) (*http.Response, error) {
				if request.Method != http.MethodGet || request.URL.Path != "/rconfig" {
					return nil, errors.New("unexpected configuration request: " + request.Method + " " + request.URL.Redacted())
				}
				return nil, errors.New("simulated configuration transport failure")
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			rt := newTestRuntime(t, 10*time.Second)
			client := &http.Client{Transport: roundTripFunc(test.roundTrip)}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()

			err := rt.InitializeLXSource(ctx, ScriptInfo{
				Name:        "独家音源",
				Version:     "7",
				Author:      "w",
				Description: "后续更新，请关注微信公众号: 洛雪科技",
				RawScript:   string(source),
			}, string(source), client)
			if err == nil {
				t.Fatal("expected initialization to fail when /rconfig is unavailable")
			}

			state, evalErr := rt.Eval(ctx, `JSON.stringify({
				initialized: globalThis.__lxHostState.initialized,
				requestHandler: typeof globalThis.__lxHostHandlers.request
			})`, "lx-rconfig-failure-state.js")
			if evalErr != nil {
				t.Fatal(evalErr)
			}
			if state != `{"initialized":false,"requestHandler":"function"}` {
				t.Fatalf("host state after failed /rconfig = %s, want initialized=false with the pre-registered request handler (initialization error: %v)", state, err)
			}
		})
	}
}
