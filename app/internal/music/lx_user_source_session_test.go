package music

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestLXUserSourceSessionPreservesScriptStateAcrossRequests(t *testing.T) {
	script := `/*
 * @name Persistent Session Test
 */
let counter = 0;
lx.on(lx.EVENT_NAMES.request, function () {
  return Promise.resolve("https://cdn.example.test/" + (++counter) + ".mp3");
});
lx.send(lx.EVENT_NAMES.inited, {
  sources: { wy: { type: "music", actions: ["musicUrl"], qualitys: ["320k"] } }
});`
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	session, err := StartLXUserSourceSession(ctx, script, nil)
	if err != nil { t.Fatal(err) }
	defer session.Close()

	payload := json.RawMessage(`{"source":"wy","action":"musicUrl","info":{"type":"320k","musicInfo":{"id":"1"}}}`)
	first, err := session.Request(ctx, payload)
	if err != nil { t.Fatal(err) }
	second, err := session.Request(ctx, payload)
	if err != nil { t.Fatal(err) }

	for i, raw := range []json.RawMessage{first, second} {
		var result struct {
			Source string `json:"source"`
			Action string `json:"action"`
			Data struct { URL string `json:"url"`; Type string `json:"type"` } `json:"data"`
		}
		if err := json.Unmarshal(raw, &result); err != nil { t.Fatal(err) }
		wantURL := "https://cdn.example.test/" + string(rune('1'+i)) + ".mp3"
		if result.Source != "wy" || result.Action != "musicUrl" || result.Data.URL != wantURL || result.Data.Type != "320k" {
			t.Fatalf("request %d result mismatch: %#v", i+1, result)
		}
	}
}

func TestLXUserSourceSessionCorrelatesConcurrentRequests(t *testing.T) {
	script := `lx.on(lx.EVENT_NAMES.request, function (request) {
  return new Promise(function (resolve) {
    setTimeout(function () {
      resolve("https://cdn.example.test/" + request.info.musicInfo.id + ".mp3");
    }, request.info.delay);
  });
});
lx.send(lx.EVENT_NAMES.inited, {
  sources: { tx: { type: "music", actions: ["musicUrl"], qualitys: ["128k"] } }
});`
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	session, err := StartLXUserSourceSession(ctx, script, nil)
	if err != nil { t.Fatal(err) }
	defer session.Close()

	results := make(chan string, 2)
	for _, item := range []struct{ id string; delay int }{{"slow", 35}, {"fast", 5}} {
		item := item
		go func() {
			payload, _ := json.Marshal(map[string]any{
				"source": "tx", "action": "musicUrl",
				"info": map[string]any{"type": "128k", "delay": item.delay, "musicInfo": map[string]any{"id": item.id}},
			})
			raw, err := session.Request(ctx, payload)
			if err != nil { results <- "ERROR:" + err.Error(); return }
			var result struct { Data struct { URL string `json:"url"` } `json:"data"` }
			if err := json.Unmarshal(raw, &result); err != nil { results <- "ERROR:" + err.Error(); return }
			results <- result.Data.URL
		}()
	}
	got := map[string]bool{<-results: true, <-results: true}
	if !got["https://cdn.example.test/slow.mp3"] || !got["https://cdn.example.test/fast.mp3"] {
		t.Fatalf("concurrent requests were not correlated to their callers: %#v", got)
	}
}

func TestLXUserSourceSessionRejectsInvalidMusicURLResponse(t *testing.T) {
	script := `lx.on(lx.EVENT_NAMES.request, function () { return Promise.resolve("file:///private/song.mp3"); });
lx.send(lx.EVENT_NAMES.inited, {
  sources: { wy: { type: "music", actions: ["musicUrl"], qualitys: ["320k"] } }
});`
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	session, err := StartLXUserSourceSession(ctx, script, nil)
	if err != nil { t.Fatal(err) }
	defer session.Close()
	_, err = session.Request(ctx, json.RawMessage(`{"source":"wy","action":"musicUrl","info":{"type":"320k"}}`))
	if err == nil || !strings.Contains(err.Error(), "failed") {
		t.Fatalf("invalid musicUrl result was accepted: %v", err)
	}
}


func TestLXUserSourceSessionForwardsUnknownActionLikeLX(t *testing.T) {
	script := `lx.on(lx.EVENT_NAMES.request, function (request) {
  if (request.source !== "custom" || request.action !== "customAction" || request.info.value !== 7) {
    return Promise.reject(new Error("request payload mismatch"));
  }
  return Promise.resolve("ignored");
});
lx.send(lx.EVENT_NAMES.inited, { sources: {} });`
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	session, err := StartLXUserSourceSession(ctx, script, nil)
	if err != nil { t.Fatal(err) }
	defer session.Close()
	value, err := session.Request(ctx, json.RawMessage(`{"source":"custom","action":"customAction","info":{"value":7},"extra":"must be dropped"}`))
	if err != nil { t.Fatal(err) }
	if len(value) != 0 { t.Fatalf("unknown LX actions should resolve without a result payload, got %s", value) }
}


func TestLXUserSourceSessionCancellationIgnoresLatePromiseAndKeepsSessionAlive(t *testing.T) {
	script := `let completed = 0;
lx.on(lx.EVENT_NAMES.request, function (request) {
  return new Promise(function (resolve) {
    setTimeout(function () {
      completed++;
      resolve("https://cdn.example.test/" + completed + "-" + request.info.musicInfo.id + ".mp3");
    }, request.info.delay);
  });
});
lx.send(lx.EVENT_NAMES.inited, {
  sources: { wy: { type: "music", actions: ["musicUrl"], qualitys: ["320k"] } }
});`
	startupCtx, startupCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer startupCancel()
	session, err := StartLXUserSourceSession(startupCtx, script, nil)
	if err != nil { t.Fatal(err) }
	defer session.Close()

	firstCtx, firstCancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	firstPayload := json.RawMessage(`{"source":"wy","action":"musicUrl","info":{"type":"320k","delay":60,"musicInfo":{"id":"cancelled"}}}`)
	_, err = session.Request(firstCtx, firstPayload)
	firstCancel()
	if err != context.DeadlineExceeded {
		t.Fatalf("cancelled request error = %v, want context deadline exceeded", err)
	}

	// LX cancels the host requestKey wait, not the script's own Promise. Its
	// late completion must be ignored without closing or poisoning the session.
	time.Sleep(90 * time.Millisecond)
	secondCtx, secondCancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer secondCancel()
	secondPayload := json.RawMessage(`{"source":"wy","action":"musicUrl","info":{"type":"320k","delay":1,"musicInfo":{"id":"next"}}}`)
	value, err := session.Request(secondCtx, secondPayload)
	if err != nil { t.Fatal(err) }
	var result struct {
		Data struct { URL string `json:"url"` } `json:"data"`
	}
	if err := json.Unmarshal(value, &result); err != nil { t.Fatal(err) }
	if result.Data.URL != "https://cdn.example.test/2-next.mp3" {
		t.Fatalf("late cancelled Promise was not isolated from the next request: %q", result.Data.URL)
	}
}
