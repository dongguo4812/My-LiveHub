//go:build (lxquickjs || lxproduction) && cgo

package lxruntime

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestInitializeLXSourceServicesPreInitializationRequest(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/rconfig" {
			t.Errorf("request path = %q", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"config":"fixture-config"}`)
	}))
	defer server.Close()

	rt := newTestRuntime(t, 5*time.Second)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	source := `
globalThis.__preInitConfig = '';
lx.request(`+string(mustJSONQuote(server.URL+"/rconfig"))+`, {}, (error, response, body) => {
  if (error) throw error;
  globalThis.__preInitConfig = body.config;
  lx.on('request', async () => 'ready');
  lx.send('inited', { sources: {
    kw: { type: 'music', actions: ['musicUrl'], qualitys: ['128k'] }
  } });
});
`
	if err := rt.InitializeLXSource(ctx, ScriptInfo{Name: "pre-init-request-fixture", RawScript: source}, source, server.Client()); err != nil {
		t.Fatal(err)
	}
	value, err := rt.Eval(ctx, "globalThis.__preInitConfig", "read-pre-init-config.js")
	if err != nil {
		t.Fatal(err)
	}
	if value != "fixture-config" {
		t.Fatalf("pre-initialization request result = %q", value)
	}
}


func TestInitializeLXSourceRejectsOversizedScriptAndMetadata(t *testing.T) {
	rt := newTestRuntime(t, 5*time.Second)
	defer rt.Close()
	large := strings.Repeat("x", maxLXScriptSourceBytes+1)
	if err := rt.InitializeLXSource(context.Background(), ScriptInfo{Name: "large-source"}, large, nil); err == nil || !strings.Contains(err.Error(), "byte limit") {
		t.Fatalf("oversized source should be rejected before evaluation, got %v", err)
	}
	small := "lx.send('inited', {});"
	if err := rt.InitializeLXSource(context.Background(), ScriptInfo{Name: "large-metadata", RawScript: large}, small, nil); err == nil || !strings.Contains(err.Error(), "metadata exceeds") {
		t.Fatalf("oversized raw script metadata should be rejected, got %v", err)
	}
}
