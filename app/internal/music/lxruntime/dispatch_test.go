//go:build lxquickjs && cgo

package lxruntime

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestDispatchLXRequestServicesQueuedHTTPAndResolvesAction(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("id") != "song-1" { t.Errorf("query = %q", r.URL.RawQuery) }
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"url":"https://cdn.example/song.mp3"}`))
	}))
	defer server.Close()

	rt := newTestRuntime(t, 5*time.Second)
	if err := rt.InstallLXHost(context.Background(), ScriptInfo{Name:"dispatch-test", RawScript:""}); err != nil {
		t.Fatal(err)
	}
	install := `
lx.on('request', async ({ source, action, info }) => {
  if (source !== 'kw' || action !== 'musicUrl') throw new Error('unexpected action');
  return await new Promise((resolve, reject) => {
    lx.request(` + string(mustJSONQuote(server.URL+"?id=song-1")) + `, {}, (err, resp, body) => {
      if (err) return reject(err);
      if (resp.statusCode !== 200 || resp.body.url !== 'https://cdn.example/song.mp3') return reject(new Error('bad response'));
      resolve(resp.body.url);
    });
  });
});
'registered';
`
	if _, err := rt.Eval(context.Background(), install, "register-dispatch-test.js"); err != nil {
		t.Fatal(err)
	}
	value, err := rt.DispatchLXRequest(context.Background(), "kw", "musicUrl", map[string]any{
		"musicInfo": map[string]any{"id":"song-1"}, "type":"320k",
	})
	if err != nil { t.Fatal(err) }
	if value != "https://cdn.example/song.mp3" { t.Fatalf("dispatch result = %#v", value) }
}

func TestDispatchLXRequestPropagatesHTTPFailure(t *testing.T) {
	rt := newTestRuntime(t, 5*time.Second)
	if err := rt.InstallLXHost(context.Background(), ScriptInfo{}); err != nil { t.Fatal(err) }
	_, err := rt.Eval(context.Background(), `
lx.on('request', async () => await new Promise((resolve, reject) => {
  lx.request('http://127.0.0.1:1/unreachable', {}, error => error ? reject(error) : resolve('unexpected'));
}));
'registered';
`, "register-failing-request.js")
	if err != nil { t.Fatal(err) }
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_, err = rt.DispatchLXRequest(ctx, "kw", "musicUrl", json.RawMessage(`{}`))
	if err == nil { t.Fatal("expected network error to reject source action") }
}
