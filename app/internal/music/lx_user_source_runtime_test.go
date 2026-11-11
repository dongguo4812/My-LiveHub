package music

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestRunLXSourceWorkerInitializesDeclaredPlatforms(t *testing.T) {
	script := `/*
 * @name Runtime Test
 */
lx.send(lx.EVENT_NAMES.inited, {
  sources: {
    wy: { name: "网易云音乐", actions: ["musicUrl"], qualitys: ["standard"] },
    tx: { name: "QQ音乐", actions: ["musicUrl"], qualitys: ["standard"] }
  }
});
lx.on(lx.EVENT_NAMES.request, function (request) { return request; });
`
	input, err := json.Marshal(lxWorkerRequest{Script: script})
	if err != nil { t.Fatal(err) }
	var output bytes.Buffer
	if code := RunLXSourceWorker(bytes.NewReader(input), &output); code != 0 {
		t.Fatalf("worker returned %d: %s", code, output.String())
	}
	var response lxWorkerResponse
	if err := json.Unmarshal(output.Bytes(), &response); err != nil { t.Fatal(err) }
	if response.Error != "" { t.Fatal(response.Error) }
	if !response.Result.Initialized { t.Fatal("source was not initialized") }
	if strings.Join(response.Result.Platforms, ",") != "tx,wy" {
		t.Fatalf("unexpected platforms: %#v", response.Result.Platforms)
	}
	if len(response.Result.Handlers) != 1 || response.Result.Handlers[0] != "request" {
		t.Fatalf("unexpected handlers: %#v", response.Result.Handlers)
	}
}

func TestRunLXSourceWorkerRejectsMissingHandshake(t *testing.T) {
	input := strings.NewReader(`{"script":"var source = 1;" }`)
	var output bytes.Buffer
	if code := RunLXSourceWorker(input, &output); code == 0 {
		t.Fatal("expected worker to reject script without inited event")
	}
	var response lxWorkerResponse
	if err := json.Unmarshal(output.Bytes(), &response); err != nil { t.Fatal(err) }
	if !strings.Contains(response.Error, "inited") {
		t.Fatalf("unexpected error: %q", response.Error)
	}
}

func TestRunLXSourceWorkerRejectsUndeclaredPlatforms(t *testing.T) {
	script := `lx.send(lx.EVENT_NAMES.inited, { sources: {} });`
	input, _ := json.Marshal(lxWorkerRequest{Script: script})
	var output bytes.Buffer
	_ = RunLXSourceWorker(bytes.NewReader(input), &output)
	var response lxWorkerResponse
	if err := json.Unmarshal(output.Bytes(), &response); err != nil { t.Fatal(err) }
	if !strings.Contains(response.Error, "没有声明 sources") {
		t.Fatalf("unexpected error: %q", response.Error)
	}
}


func TestRunLXSourceWorkerInvokesRequestHandler(t *testing.T) {
	script := `lx.send(lx.EVENT_NAMES.inited, { sources: { wy: { name: "网易云音乐", actions: ["musicUrl"], qualitys: ["standard"] } } });
lx.on(lx.EVENT_NAMES.request, function (request) {
  if (request.action !== "musicUrl" || request.source !== "wy") throw new Error("unexpected request");
  return Promise.resolve("https://media.example.test/song.mp3");
});`
	payload, err := json.Marshal(map[string]any{
		"source": "wy",
		"action": "musicUrl",
		"info": map[string]any{"type": "wy", "musicInfo": map[string]any{"id": "150617", "name": "找自己", "artist": "陶喆"}},
	})
	if err != nil { t.Fatal(err) }
	input, err := json.Marshal(lxWorkerRequest{Script: script, Operation: "resolve", Payload: payload})
	if err != nil { t.Fatal(err) }
	var output bytes.Buffer
	if code := RunLXSourceWorker(bytes.NewReader(input), &output); code != 0 { t.Fatalf("worker returned %d: %s", code, output.String()) }
	var response lxWorkerResponse
	if err := json.Unmarshal(output.Bytes(), &response); err != nil { t.Fatal(err) }
	if response.Error != "" { t.Fatal(response.Error) }
	var resolved string
	if err := json.Unmarshal(response.Value, &resolved); err != nil { t.Fatal(err) }
	if resolved != "https://media.example.test/song.mp3" { t.Fatalf("unexpected resolve value: %q", resolved) }
}

func TestRunLXSourceWorkerRejectsPendingRequestPromise(t *testing.T) {
	script := `lx.send(lx.EVENT_NAMES.inited, { sources: { wy: { name: "网易云音乐", actions: ["musicUrl"], qualitys: ["standard"] } } });
lx.on(lx.EVENT_NAMES.request, function () { return new Promise(function () {}); });`
	payload := json.RawMessage(`{"source":"wy","action":"musicUrl","info":{"type":"wy","musicInfo":{"id":"1"}}}`)
	input, _ := json.Marshal(lxWorkerRequest{Script: script, Operation: "resolve", Payload: payload})
	var output bytes.Buffer
	_ = RunLXSourceWorker(bytes.NewReader(input), &output)
	var response lxWorkerResponse
	if err := json.Unmarshal(output.Bytes(), &response); err != nil { t.Fatal(err) }
	if !strings.Contains(response.Error, "Promise 尚未完成") { t.Fatalf("unexpected error: %q", response.Error) }
}
