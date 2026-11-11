package music

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestRunLXSourceWorkerInitializesDeclaredPlatforms(t *testing.T) {
	script := `/*
 * @name Runtime Test
 */
if (lx.currentScriptInfo.name !== "Runtime Test" || lx.env !== "desktop" || typeof console.log !== "function") {
  throw new Error("LX runtime metadata globals are unavailable");
}
if (lx.utils.buffer.bufToString(lx.utils.buffer.from("source", "utf-8"), "base64") !== "c291cmNl" || typeof setTimeout !== "function") {
  throw new Error("LX utility globals are unavailable");
}
lx.send(lx.EVENT_NAMES.inited, {
  sources: {
    wy: { name: "网易云音乐", type: "music", actions: ["musicUrl"], qualitys: ["320k"] },
    tx: { name: "QQ音乐", type: "music", actions: ["musicUrl"], qualitys: ["128k", "320k"] }
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
	if len(response.Result.DeclaredSources) != 2 || response.Result.DeclaredSources[0].Key != "tx" || response.Result.DeclaredSources[0].Qualitys[1] != "320k" {
		t.Fatalf("unexpected declared source details: %#v", response.Result.DeclaredSources)
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
	script := `lx.send(lx.EVENT_NAMES.inited, { sources: { wy: { name: "网易云音乐", type: "music", actions: ["musicUrl"], qualitys: ["320k"] } } });
lx.on(lx.EVENT_NAMES.request, function (request) {
  if (request.action !== "musicUrl" || request.source !== "wy") throw new Error("unexpected request");
  return Promise.resolve("https://media.example.test/song.mp3");
});`
	payload, err := json.Marshal(map[string]any{
		"source": "wy",
		"action": "musicUrl",
		"info": map[string]any{"type": "320k", "musicInfo": map[string]any{"id": "150617", "name": "找自己", "artist": "陶喆"}},
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

func TestRunLXSourceWorkerMatchesLXEventPromiseAndThisContext(t *testing.T) {
	script := `lx.on(lx.EVENT_NAMES.request, function (request) {
  if (this !== lx) throw new Error("request handler this context is not lx");
  if (request.action !== "musicUrl") throw new Error("unexpected action");
  return Promise.resolve("https://cdn.example.test/song.mp3");
}).then(function () {
  return lx.send(lx.EVENT_NAMES.inited, {
    sources: { wy: { name: "NetEase", type: "music", actions: ["musicUrl"], qualitys: ["320k"] } }
  });
});`
	payload := json.RawMessage(`{"source":"wy","action":"musicUrl","info":{"type":"320k","musicInfo":{"id":"1"}}}`)
	input, err := json.Marshal(lxWorkerRequest{Script: script, Operation: "resolve", Payload: payload})
	if err != nil { t.Fatal(err) }
	var output bytes.Buffer
	if code := RunLXSourceWorker(bytes.NewReader(input), &output); code != 0 {
		t.Fatalf("worker returned %d: %s", code, output.String())
	}
	var response lxWorkerResponse
	if err := json.Unmarshal(output.Bytes(), &response); err != nil { t.Fatal(err) }
	if response.Error != "" { t.Fatal(response.Error) }
	var resolved string
	if err := json.Unmarshal(response.Value, &resolved); err != nil { t.Fatal(err) }
	if resolved != "https://cdn.example.test/song.mp3" { t.Fatalf("resolved URL = %q", resolved) }
}

func TestRunLXSourceWorkerAwaitsLXTimersAndAsyncPromise(t *testing.T) {
	script := `lx.on(lx.EVENT_NAMES.request, async function () {
  return await new Promise(function (resolve) {
    setTimeout(function () { resolve("https://cdn.example.test/timer.mp3"); }, 15);
  });
});
lx.send(lx.EVENT_NAMES.inited, {
  sources: { wy: { name: "NetEase", type: "music", actions: ["musicUrl"], qualitys: ["320k"] } }
});`
	payload := json.RawMessage(`{"source":"wy","action":"musicUrl","info":{"type":"320k"}}`)
	input, err := json.Marshal(lxWorkerRequest{Script:script,Operation:"resolve",Payload:payload})
	if err != nil { t.Fatal(err) }
	var output bytes.Buffer
	if code:=RunLXSourceWorker(bytes.NewReader(input),&output); code!=0 { t.Fatalf("worker returned %d: %s",code,output.String()) }
	var response lxWorkerResponse
	if err:=json.Unmarshal(output.Bytes(),&response); err!=nil { t.Fatal(err) }
	if response.Error!="" { t.Fatal(response.Error) }
	var resolved string
	if err:=json.Unmarshal(response.Value,&resolved); err!=nil { t.Fatal(err) }
	if resolved!="https://cdn.example.test/timer.mp3" { t.Fatalf("resolved URL = %q",resolved) }
}

func TestRunLXSourceWorkerRejectsPendingRequestPromise(t *testing.T) {
	script := `lx.send(lx.EVENT_NAMES.inited, { sources: { wy: { name: "网易云音乐", type: "music", actions: ["musicUrl"], qualitys: ["320k"] } } });
lx.on(lx.EVENT_NAMES.request, function () { return new Promise(function () {}); });`
	payload := json.RawMessage(`{"source":"wy","action":"musicUrl","info":{"type":"320k","musicInfo":{"id":"1"}}}`)
	input, _ := json.Marshal(lxWorkerRequest{Script: script, Operation: "resolve", Payload: payload})
	var output bytes.Buffer
	_ = RunLXSourceWorker(bytes.NewReader(input), &output)
	var response lxWorkerResponse
	if err := json.Unmarshal(output.Bytes(), &response); err != nil { t.Fatal(err) }
	if !strings.Contains(response.Error, "Promise 尚未完成") { t.Fatalf("unexpected error: %q", response.Error) }
}


func TestRunLXSourceWorkerProvidesSafeLXRequestBridge(t *testing.T) {
	script := `lx.send(lx.EVENT_NAMES.inited, { sources: { wy: { name: "网易云音乐", type: "music", actions: ["musicUrl"], qualitys: ["320k"] } } });
lx.on(lx.EVENT_NAMES.request, function () {
  return new Promise(function (resolve) {
    lx.request("http://127.0.0.1/private", {}, function (err) {
      resolve(String(err));
    });
  });
});`
	payload := json.RawMessage(`{"source":"wy","action":"musicUrl","info":{"type":"320k","musicInfo":{"id":"1"}}}`)
	input, _ := json.Marshal(lxWorkerRequest{Script: script, Operation: "resolve", Payload: payload})
	var output bytes.Buffer
	if code := RunLXSourceWorker(bytes.NewReader(input), &output); code != 0 { t.Fatalf("worker returned %d: %s", code, output.String()) }
	var response lxWorkerResponse
	if err := json.Unmarshal(output.Bytes(), &response); err != nil { t.Fatal(err) }
	if response.Error != "" { t.Fatal(response.Error) }
	var message string
	if err := json.Unmarshal(response.Value, &message); err != nil { t.Fatal(err) }
	if !strings.Contains(message, "不允许访问本机或内网地址") { t.Fatalf("private request was not blocked: %q", message) }
}


func TestValidateLXPlaybackURLRejectsPrivateTargets(t *testing.T) {
	for _, raw := range []string{
		"http://127.0.0.1/song.mp3",
		"http://localhost/song.mp3",
		"file:///C:/music/song.mp3",
		"javascript:alert(1)",
	} {
		if err := ValidateLXPlaybackURL(raw); err == nil { t.Fatalf("expected URL to be rejected: %s", raw) }
	}
}


func TestRunLXSourceWorkerResolvesMusicURLThroughHTTPCallback(t *testing.T) {
	client := &http.Client{Transport: lxRoundTripFunc(func(request *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Status: "200 OK",
			Header: make(http.Header),
			Body: io.NopCloser(strings.NewReader(`{"url":"https://cdn.example.test/track.mp3"}`)),
			Request: request,
		}, nil
	})}
	script := `/*
 * @name HTTP Resolver Test
 */
lx.on(lx.EVENT_NAMES.request, async function (request) {
  const response = await new Promise(function (resolve, reject) {
    lx.request("https://music.example.test/resolve", {}, function (err, response) {
      if (err) return reject(err);
      if (response.bytes !== 44 || lx.utils.buffer.bufToString(response.raw, "utf8").indexOf("cdn.example.test") < 0) return reject(new Error("LX response raw/bytes fields are missing"));
      resolve(response);
    });
  });
  return response.body.url;
});
lx.send(lx.EVENT_NAMES.inited, {
  sources: {
    wy: { name: "NetEase", type: "music", actions: ["musicUrl"], qualitys: ["320k"] }
  }
});
`
	payload := json.RawMessage(`{"source":"wy","action":"musicUrl","info":{"type":"320k","musicInfo":{"id":"150617","name":"找自己","artist":"陶喆"}}}`)
	input, err := json.Marshal(lxWorkerRequest{Script: script, Operation: "resolve", Payload: payload})
	if err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	if code := runLXSourceWorker(bytes.NewReader(input), &output, client); code != 0 {
		t.Fatalf("worker returned %d: %s", code, output.String())
	}
	var response lxWorkerResponse
	if err := json.Unmarshal(output.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.Error != "" {
		t.Fatal(response.Error)
	}
	var resolved string
	if err := json.Unmarshal(response.Value, &resolved); err != nil {
		t.Fatal(err)
	}
	if resolved != "https://cdn.example.test/track.mp3" {
		t.Fatalf("resolved URL = %q", resolved)
	}
}

func TestValidateLXSourceRuntimeResultRequiresPlaybackHandler(t *testing.T) {
	result := LXSourceRuntimeResult{
		Initialized: true,
		Platforms: []string{"wy"},
		DeclaredSources: []LXDeclaredSource{{Key:"wy", Actions:[]string{"musicUrl"}, Qualitys:[]string{"320k"}}},
	}
	if err := validateLXSourceRuntimeResult(result); err == nil || !strings.Contains(err.Error(), "request 处理器") {
		t.Fatalf("expected missing request handler to be rejected, got %v", err)
	}
	result.Handlers = []string{"request"}
	if err := validateLXSourceRuntimeResult(result); err != nil {
		t.Fatalf("valid playback source was rejected: %v", err)
	}
}

func TestValidateLXSourceRuntimeResultAllowsNonPlaybackSourcesWithoutRequest(t *testing.T) {
	result := LXSourceRuntimeResult{
		Initialized: true,
		Platforms: []string{"wy"},
		DeclaredSources: []LXDeclaredSource{{Key:"wy", Actions:[]string{"lyric"}}},
	}
	if err := validateLXSourceRuntimeResult(result); err != nil {
		t.Fatalf("non-playback source should not require a request handler: %v", err)
	}
}
