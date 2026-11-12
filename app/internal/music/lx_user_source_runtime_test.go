package music

import (
	"bytes"
	"errors"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestSummarizeLXRequestResponseRedactsSensitiveURLParts(t *testing.T) {
	response := map[string]any{
		"statusCode": 404,
		"url": "https://user:password@music.example.test/flower/v1/url/tx/abc123/128k?tag=SECRET&token=PRIVATE#fragment",
		"headers": map[string]any{"Content-Type": "text/html; charset=utf-8", "Set-Cookie": "session=PRIVATE"},
		"body": "<html>404 Not Found</html>",
	}
	got := summarizeLXRequestResponse(response)
	for _, want := range []string{"HTTP 404", "music.example.test/flower/v1/url/tx/abc123/128k", "content-type=text/html; charset=utf-8", "404 Not Found"} {
		if !strings.Contains(got, want) {
			t.Fatalf("diagnostic %q does not contain %q", got, want)
		}
	}
	for _, forbidden := range []string{"user", "password", "SECRET", "PRIVATE", "fragment", "Set-Cookie"} {
		if strings.Contains(got, forbidden) {
			t.Fatalf("diagnostic leaked %q: %s", forbidden, got)
		}
	}
}

func TestSummarizeLXRequestResponseIncludesFlowerAPIError(t *testing.T) {
	response := map[string]any{
		"statusCode": 200,
		"url": "http://music.example.test/flower/v1/url/tx/abc123/128k?tag=SECRET",
		"headers": map[string]string{"Content-Type": "application/json"},
		"body": map[string]any{"code": float64(1), "msg": "no matching track"},
	}
	got := summarizeLXRequestResponse(response)
	for _, want := range []string{"HTTP 200", "API code=1", "no matching track"} {
		if !strings.Contains(got, want) {
			t.Fatalf("diagnostic %q does not contain %q", got, want)
		}
	}
	if strings.Contains(got, "SECRET") || strings.Contains(got, "?tag") {
		t.Fatalf("diagnostic leaked URL query: %s", got)
	}
}

func TestRunLXSourceWorkerAggregatesConcurrentRequestDiagnostics(t *testing.T) {
	client := &http.Client{Transport: lxRoundTripFunc(func(request *http.Request) (*http.Response, error) {
		status := http.StatusBadGateway
		body := "upstream A unavailable"
		switch request.URL.Path {
		case "/route-b":
			status, body = http.StatusServiceUnavailable, "upstream B unavailable"
		case "/route-c":
			status, body = http.StatusNotFound, "upstream C unavailable"
		}
		return &http.Response{
			StatusCode: status,
			Status: http.StatusText(status),
			Header: http.Header{"Content-Type": []string{"text/plain; charset=utf-8"}},
			Body: io.NopCloser(strings.NewReader(body)),
			Request: request,
		}, nil
	})}
	script := `lx.on(lx.EVENT_NAMES.request, function () {
  const urls = [
    "https://music.example.test/route-a?token=SECRET_A",
    "https://music.example.test/route-b?token=SECRET_B",
    "https://music.example.test/route-c?token=SECRET_C"
  ];
  return Promise.any(urls.map(function (url) {
    return new Promise(function (resolve, reject) {
      lx.request(url, {}, function (err, response) {
        if (err) return reject(err);
        reject(new Error("HTTP " + response.statusCode));
      });
    });
  }));
});
lx.send(lx.EVENT_NAMES.inited, {
  sources: { tx: { name: "QQ Music", type: "music", actions: ["musicUrl"], qualitys: ["128k"] } }
});`
	payload := json.RawMessage(`{"source":"tx","action":"musicUrl","info":{"type":"128k","musicInfo":{"id":"1"}}}`)
	input, err := json.Marshal(lxWorkerRequest{Script: script, Operation: "resolve", Payload: payload})
	if err != nil { t.Fatal(err) }
	var output bytes.Buffer
	if code := runLXSourceWorker(bytes.NewReader(input), &output, client); code == 0 {
		t.Fatalf("expected all concurrent resolvers to fail: %s", output.String())
	}
	var response lxWorkerResponse
	if err := json.Unmarshal(output.Bytes(), &response); err != nil { t.Fatal(err) }
	for _, want := range []string{
		"request#1", "request#2", "request#3",
		"music.example.test/route-a", "music.example.test/route-b", "music.example.test/route-c",
		"HTTP 502", "HTTP 503", "HTTP 404",
		"upstream A unavailable", "upstream B unavailable", "upstream C unavailable",
	} {
		if !strings.Contains(response.Error, want) {
			t.Errorf("aggregated diagnostic missing %q: %s", want, response.Error)
		}
	}
	for _, secret := range []string{"SECRET_A", "SECRET_B", "SECRET_C"} {
		if strings.Contains(response.Error, secret) { t.Errorf("diagnostic leaked %s: %s", secret, response.Error) }
	}
}

func TestRunLXSourceWorkerIncludesTransportFailurePerRequest(t *testing.T) {
	client := &http.Client{Transport: lxRoundTripFunc(func(request *http.Request) (*http.Response, error) {
		return nil, errors.New("dial tcp: connection refused")
	})}
	script := `lx.on(lx.EVENT_NAMES.request, function () {
  return new Promise(function (resolve, reject) {
    lx.request("https://music.example.test/transport?token=DO_NOT_LOG", {}, function (err) {
      if (err) return reject(err);
      resolve("unexpected");
    });
  });
});
lx.send(lx.EVENT_NAMES.inited, {
  sources: { tx: { name: "QQ Music", type: "music", actions: ["musicUrl"], qualitys: ["128k"] } }
});`
	payload := json.RawMessage(`{"source":"tx","action":"musicUrl","info":{"type":"128k","musicInfo":{"id":"1"}}}`)
	input, err := json.Marshal(lxWorkerRequest{Script: script, Operation: "resolve", Payload: payload})
	if err != nil { t.Fatal(err) }
	var output bytes.Buffer
	_ = runLXSourceWorker(bytes.NewReader(input), &output, client)
	var response lxWorkerResponse
	if err := json.Unmarshal(output.Bytes(), &response); err != nil { t.Fatal(err) }
	for _, want := range []string{"request#1", "music.example.test/transport", "connection refused"} {
		if !strings.Contains(response.Error, want) { t.Errorf("transport diagnostic missing %q: %s", want, response.Error) }
	}
	if strings.Contains(response.Error, "DO_NOT_LOG") || strings.Contains(response.Error, "?token") {
		t.Fatalf("transport diagnostic leaked query data: %s", response.Error)
	}
}

func TestRunLXSourceWorkerInitializesDeclaredPlatforms(t *testing.T) {
	script := `/*
 * @name Runtime Test
 */
if (lx.currentScriptInfo.name !== "Runtime Test" || lx.env !== "desktop" || typeof console.log !== "function" || window !== globalThis || self !== window) {
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
	if len(response.Result.DeclaredSources) != 2 || response.Result.DeclaredSources[0].Key != "tx" || response.Result.DeclaredSources[1].Key != "wy" || response.Result.DeclaredSources[0].Qualitys[1] != "320k" {
		t.Fatalf("unexpected declared source details: %#v", response.Result.DeclaredSources)
	}
	if len(response.Result.Handlers) != 1 || response.Result.Handlers[0] != "request" {
		t.Fatalf("unexpected handlers: %#v", response.Result.Handlers)
	}
}


func TestRunLXSourceWorkerFiltersDeclarationsLikeLXPreload(t *testing.T) {
	script := `lx.send(lx.EVENT_NAMES.inited, {
  sources: {
    tx: { type: "music", actions: ["musicUrl", "lyric"], qualitys: ["128k", "lossless"] },
    local: { type: "music", actions: ["musicUrl", "lyric", "pic"], qualitys: ["320k"] },
    xm: { type: "music", actions: ["musicUrl"], qualitys: ["128k"] },
    custom: { type: "music", actions: ["musicUrl"], qualitys: ["128k"] },
    wy_disabled: { type: "video", actions: ["musicUrl"], qualitys: ["320k"] }
  }
});`
	input, _ := json.Marshal(lxWorkerRequest{Script: script})
	var output bytes.Buffer
	if code := RunLXSourceWorker(bytes.NewReader(input), &output); code != 0 {
		t.Fatalf("worker returned %d: %s", code, output.String())
	}
	var response lxWorkerResponse
	if err := json.Unmarshal(output.Bytes(), &response); err != nil { t.Fatal(err) }
	if got := strings.Join(response.Result.Platforms, ","); got != "tx,local" {
		t.Fatalf("LX-supported platforms = %q, want tx,local (fixed preload order)", got)
	}
	if len(response.Result.DeclaredSources) != 2 { t.Fatalf("unexpected filtered declarations: %#v", response.Result.DeclaredSources) }
	tx := response.Result.DeclaredSources[0]
	if tx.Type != "music" || strings.Join(tx.Actions, ",") != "musicUrl" || strings.Join(tx.Qualitys, ",") != "128k" {
		t.Fatalf("tx declaration was not filtered to LX capabilities: %#v", tx)
	}
	local := response.Result.DeclaredSources[1]
	if strings.Join(local.Actions, ",") != "musicUrl,lyric,pic" || len(local.Qualitys) != 0 {
		t.Fatalf("local declaration was not filtered to LX capabilities: %#v", local)
	}
	var normalized struct {
		Sources map[string]struct {
			Name string `json:"name"`
			Type string `json:"type"`
			Actions []string `json:"actions"`
			Qualitys []string `json:"qualitys"`
		} `json:"sources"`
	}
	if err := json.Unmarshal(response.Result.Sources, &normalized); err != nil { t.Fatal(err) }
	if normalized.Sources["tx"].Name != "" {
		t.Fatalf("LX init response must not preserve user-provided source name: %#v", normalized.Sources["tx"])
	}
}

func TestRunLXSourceWorkerWaitsForAsyncInitialization(t *testing.T) {
	script := `setTimeout(function () {
  lx.send(lx.EVENT_NAMES.inited, {
    sources: {
      tx: { name: "QQ音乐", type: "music", actions: ["musicUrl"], qualitys: ["320k"] }
    }
  });
}, 20);
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
	if !response.Result.Initialized || len(response.Result.Platforms) != 1 || response.Result.Platforms[0] != "tx" {
		t.Fatalf("unexpected async initialization result: %#v", response.Result)
	}
}

func TestRunLXSourceWorkerRejectsMissingHandshake(t *testing.T) {
	input := strings.NewReader(`{"script":"var source = 1;" }`)
	var output bytes.Buffer
	if code := runLXSourceWorkerWithTimeout(input, &output, nil, 40*time.Millisecond); code == 0 {
		t.Fatal("expected worker to reject script without inited event")
	}
	var response lxWorkerResponse
	if err := json.Unmarshal(output.Bytes(), &response); err != nil { t.Fatal(err) }
	if !strings.Contains(response.Error, "inited") {
		t.Fatalf("unexpected error: %q", response.Error)
	}
}

func TestRunLXSourceWorkerAllowsEmptySourceDeclarationLikeLX(t *testing.T) {
	script := `lx.send(lx.EVENT_NAMES.inited, { sources: {} });`
	input, _ := json.Marshal(lxWorkerRequest{Script: script})
	var output bytes.Buffer
	if code := RunLXSourceWorker(bytes.NewReader(input), &output); code != 0 {
		t.Fatalf("LX accepts an empty source declaration, worker returned %d: %s", code, output.String())
	}
	var response lxWorkerResponse
	if err := json.Unmarshal(output.Bytes(), &response); err != nil { t.Fatal(err) }
	if response.Error != "" || !response.Result.Initialized || len(response.Result.Platforms) != 0 {
		t.Fatalf("unexpected empty-source initialization: %#v", response)
	}
}


func TestRunLXSourceWorkerInvokesRequestHandler(t *testing.T) {
	script := `lx.send(lx.EVENT_NAMES.inited, { sources: { wy: { name: "网易云音乐", type: "music", actions: ["musicUrl"], qualitys: ["320k"] } } });
lx.on(lx.EVENT_NAMES.request, function (request) {
  if (request.action !== "musicUrl" || request.source !== "wy" || request.info.type !== "128k") throw new Error("unexpected request");
  return Promise.resolve("https://media.example.test/song.mp3");
});`
	payload, err := json.Marshal(map[string]any{
		"source": "wy",
		"action": "musicUrl",
		"info": map[string]any{"type": "128k", "musicInfo": map[string]any{"id": "150617", "name": "找自己", "artist": "陶喆"}},
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
	_ = runLXSourceWorkerWithTimeout(bytes.NewReader(input), &output, nil, 40*time.Millisecond)
	var response lxWorkerResponse
	if err := json.Unmarshal(output.Bytes(), &response); err != nil { t.Fatal(err) }
	if !strings.Contains(response.Error, "Promise 尚未完成") { t.Fatalf("unexpected error: %q", response.Error) }
}


func TestRunLXSourceWorkerAllowsPrivateNetworkRequestsLikeLX(t *testing.T) {
	client := &http.Client{Transport: lxRoundTripFunc(func(request *http.Request) (*http.Response, error) {
		if request.URL.Hostname() != "127.0.0.1" { t.Fatalf("request host = %q", request.URL.Hostname()) }
		return &http.Response{StatusCode: http.StatusOK, Status: "200 OK", Header: make(http.Header), Body: io.NopCloser(strings.NewReader("local response")), Request: request}, nil
	})}
	script := `lx.send(lx.EVENT_NAMES.inited, { sources: { wy: { name: "网易云音乐", type: "music", actions: ["musicUrl"], qualitys: ["320k"] } } });
lx.on(lx.EVENT_NAMES.request, function () {
  return new Promise(function (resolve, reject) {
    lx.request("http://127.0.0.1/private", {}, function (err, response) {
      if (err) return reject(err);
      resolve(response.body);
    });
  });
});`
	payload := json.RawMessage(`{"source":"wy","action":"musicUrl","info":{"type":"320k","musicInfo":{"id":"1"}}}`)
	input, _ := json.Marshal(lxWorkerRequest{Script: script, Operation: "resolve", Payload: payload})
	var output bytes.Buffer
	if code := runLXSourceWorker(bytes.NewReader(input), &output, client); code != 0 { t.Fatalf("worker returned %d: %s", code, output.String()) }
	var response lxWorkerResponse
	if err := json.Unmarshal(output.Bytes(), &response); err != nil { t.Fatal(err) }
	if response.Error != "" { t.Fatal(response.Error) }
	var message string
	if err := json.Unmarshal(response.Value, &message); err != nil { t.Fatal(err) }
	if message != "local response" { t.Fatalf("loopback response = %q", message) }
}

func TestValidateLXPlaybackURLMatchesLXPreloadPrefixCheck(t *testing.T) {
	for _, raw := range []string{"http://127.0.0.1/song.mp3", "http://localhost/song.mp3", "https://media.example.test/song.mp3", "http:relative"} {
		if err := ValidateLXPlaybackURL(raw); err != nil { t.Fatalf("LX-compatible URL %q was rejected: %v", raw, err) }
	}
	for _, raw := range []string{"", "file:///C:/music/song.mp3", "javascript:alert(1)", "ftp://media.example.test/song.mp3", strings.Repeat("a", 2049)} {
		if err := ValidateLXPlaybackURL(raw); err == nil { t.Fatalf("expected invalid LX URL to be rejected: %s", raw) }
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

func TestValidateLXSourceRuntimeResultMatchesLXInitializationHandshake(t *testing.T) {
	result := LXSourceRuntimeResult{Initialized: true}
	if err := validateLXSourceRuntimeResult(result); err != nil {
		t.Fatalf("LX accepts initialized sources without request handlers: %v", err)
	}
	result.Initialized = false
	if err := validateLXSourceRuntimeResult(result); err == nil {
		t.Fatal("an incomplete inited handshake must be rejected")
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


// Flower's musicUrl action uses a GET URL keyed by the LX musicInfo identifier,
// a script-generated hex JSON tag, and response.body.code/msg/data.
func TestRunLXSourceWorkerMatchesFlowerMusicURLRequestContract(t *testing.T) {
	const endpoint = "http://97.64.37.235/flower/v1/url/tx/0039MnYb0qxYhV/128k"
	const tag = "5b0a2022303033394d6e5962307178596856222c0a20223132386b220a5d"
	script := `lx.on(lx.EVENT_NAMES.request, async function () {
  const response = await new Promise(function (resolve, reject) {
    lx.request("`+endpoint+`", {
      method: "GET",
      headers: {
        "User-Agent": "lx-music/desktop",
        "ver": "2.0.0",
        "source-ver": "1",
        "tag": "`+tag+`"
      }
    }, function (err, response) {
      if (err) return reject(err);
      if (response.body.code !== 0) return reject(new Error(response.body.msg));
      resolve(response);
    });
  });
  return response.body.data;
});
lx.send(lx.EVENT_NAMES.inited, {
  sources: {
    tx: { name: "QQ Music", type: "music", actions: ["musicUrl"], qualitys: ["128k"] }
  }
});`
	payload := json.RawMessage(`{"source":"tx","action":"musicUrl","info":{"type":"128k","musicInfo":{"source":"tx","songmid":"0039MnYb0qxYhV","name":"稻香","singer":"周杰伦"}}}`)

	run := func(body string) (string, *http.Request, error) {
		var captured *http.Request
		client := &http.Client{Transport: lxRoundTripFunc(func(request *http.Request) (*http.Response, error) {
			captured = request.Clone(request.Context())
			return &http.Response{
				StatusCode: http.StatusOK,
				Status: "200 OK",
				Header: http.Header{"Content-Type": []string{"application/json"}},
				Body: io.NopCloser(strings.NewReader(body)),
				Request: request,
			}, nil
		})}
		input, err := json.Marshal(lxWorkerRequest{Script: script, Operation: "resolve", Payload: payload})
		if err != nil { return "", nil, err }
		var output bytes.Buffer
		if code := runLXSourceWorker(bytes.NewReader(input), &output, client); code != 0 {
			var response lxWorkerResponse
			if decodeErr := json.Unmarshal(output.Bytes(), &response); decodeErr != nil { return "", captured, decodeErr }
			return "", captured, errors.New(response.Error)
		}
		var response lxWorkerResponse
		if err := json.Unmarshal(output.Bytes(), &response); err != nil { return "", captured, err }
		var resolved string
		if err := json.Unmarshal(response.Value, &resolved); err != nil { return "", captured, err }
		return resolved, captured, nil
	}

	resolved, request, err := run(`{"code":0,"msg":"","data":"https://cdn.example.test/稻香.mp3"}`)
	if err != nil { t.Fatal(err) }
	if resolved != "https://cdn.example.test/稻香.mp3" { t.Fatalf("resolved URL = %q", resolved) }
	if request == nil || request.Method != http.MethodGet || request.URL.String() != endpoint {
		t.Fatalf("Flower request mismatch: %#v", request)
	}
	if request.Header.Get("User-Agent") != "lx-music/desktop" ||
		request.Header.Get("ver") != "2.0.0" ||
		request.Header.Get("source-ver") != "1" ||
		request.Header.Get("tag") != tag {
		t.Fatalf("Flower request headers were not preserved: %#v", request.Header)
	}

	_, _, err = run(`{"code":1,"msg":"no matching track","data":null}`)
	if err == nil || !strings.Contains(err.Error(), "no matching track") {
		t.Fatalf("Flower API rejection was not propagated: %v", err)
	}
	if !strings.Contains(err.Error(), "HTTP 200") || !strings.Contains(err.Error(), "API code=1") {
		t.Fatalf("Flower rejection did not include safe response diagnostics: %v", err)
	}
	if strings.Contains(err.Error(), tag) || !strings.Contains(err.Error(), "97.64.37.235/flower/v1/url/tx/0039MnYb0qxYhV/128k") {
		t.Fatalf("Flower diagnostics did not safely identify the endpoint: %v", err)
	}
}


func TestFilterLXSourceDeclarationUsesJavaScriptIncludesSemantics(t *testing.T) {
	raw := json.RawMessage(`{"sources":{
		"wy":{"type":"music","actions":"prefix-musicUrl-suffix","qualitys":"prefix-320k-suffix"},
		"local":{"type":"music","actions":["musicUrl","lyric","pic"]}
	}}`)
	_, platforms, declared, err := filterLXSourceDeclaration(raw)
	if err != nil { t.Fatal(err) }
	if len(platforms) != 2 { t.Fatalf("platforms = %#v", platforms) }
	var wy, local *LXDeclaredSource
	for i := range declared {
		switch declared[i].Key {
		case "wy": wy = &declared[i]
		case "local": local = &declared[i]
		}
	}
	if wy == nil || len(wy.Actions) != 1 || wy.Actions[0] != "musicUrl" || len(wy.Qualitys) != 1 || wy.Qualitys[0] != "320k" {
		t.Fatalf("string includes semantics mismatch: %#v", wy)
	}
	if local == nil || len(local.Actions) != 3 || len(local.Qualitys) != 0 {
		t.Fatalf("local source without qualitys should remain valid: %#v", local)
	}
}
