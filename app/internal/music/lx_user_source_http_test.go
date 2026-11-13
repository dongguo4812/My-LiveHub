package music

import (
	"bytes"
	"io"
	"net/http"
	"net/url"

	"github.com/dongguo4812/My-LiveHub/app/internal/config"
	"strings"
	"testing"
	"time"

	"github.com/dop251/goja"
)

type lxRoundTripFunc func(*http.Request) (*http.Response, error)

func (f lxRoundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

func TestLXRequestDefaultTimeoutMatchesLXDesktop(t *testing.T) {
	client := &http.Client{Transport: lxRoundTripFunc(func(request *http.Request) (*http.Response, error) {
		if _, ok := request.Context().Deadline(); ok {
			t.Error("Needle response_timeout is not a total request deadline")
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Status: "200 OK",
			Header: make(http.Header),
			Body: io.NopCloser(strings.NewReader("ok")),
			Request: request,
		}, nil
	})}
	vm := goja.New()
	if _, _, err := performLXRequestWithClient("https://music.example.test/resolve", vm.ToValue(map[string]any{}), client); err != nil {
		t.Fatal(err)
	}
	transport := newLXNeedleTransport(lxRequestTimeout)
	defer transport.CloseIdleConnections()
	if transport.ResponseHeaderTimeout != 60*time.Second {
		t.Errorf("Needle response timeout = %s, want 60 seconds", transport.ResponseHeaderTimeout)
	}
	if transport.TLSHandshakeTimeout != 10*time.Second {
		t.Errorf("Needle TLS/open timeout = %s, want 10 seconds", transport.TLSHandshakeTimeout)
	}
	if !transport.DisableCompression {
		t.Error("Needle default compressed=false must not advertise compressed responses")
	}
	if lxRequestRedirectLimit != 5 {
		t.Errorf("Needle redirect limit = %d, want 5", lxRequestRedirectLimit)
	}
}

func TestLXRequestCallbackReceivesResponseBody(t *testing.T) {
	client := &http.Client{Transport: lxRoundTripFunc(func(request *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Status: "200 OK",
			Header: http.Header{"Content-Type": []string{"text/plain"}},
			Body: io.NopCloser(strings.NewReader("  {\"url\":\"https://cdn.example.test/song.mp3\"}\n")),
			Request: request,
		}, nil
	})}
	vm := goja.New()
	lx := vm.NewObject()
	if err := installLXRequestWithClient(vm, lx, client); err != nil { t.Fatal(err) }
	if err := vm.Set("lx", lx); err != nil { t.Fatal(err) }
	value, err := vm.RunString("var callbackBody=''; lx.request('https://music.example.test/resolve', {}, function(err,response){ if(err) throw new Error(String(err)); callbackBody=JSON.stringify(response.body); }); callbackBody;")
	if err != nil { t.Fatal(err) }
	if got,want:=value.String(),"{\"url\":\"https://cdn.example.test/song.mp3\"}"; got!=want {
		t.Fatalf("LX request callback body = %q, want %q",got,want)
	}
}

func TestLXRequestPromiseCanResolveFromResponseBody(t *testing.T) {
	client := &http.Client{Transport: lxRoundTripFunc(func(request *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Status: "200 OK",
			Header: make(http.Header),
			Body: io.NopCloser(strings.NewReader("https://cdn.example.test/song.mp3")),
			Request: request,
		}, nil
	})}

	vm := goja.New()
	lx := vm.NewObject()
	if err := installLXRequestWithClient(vm, lx, client); err != nil {
		t.Fatal(err)
	}
	if err := vm.Set("lx", lx); err != nil {
		t.Fatal(err)
	}
	value, err := vm.RunString(`
		var resolved = "";
		var finished = false;
		new Promise(function (resolve, reject) {
			lx.request("https://music.example.test/resolve", {}, function (err, response) {
				if (err) return reject(err);
				resolve(response.body);
			});
		}).then(function (body) {
			resolved = body;
			finished = true;
		});
	`)
	if err != nil {
		t.Fatal(err)
	}
	_ = value
	if _, err := vm.RunString("void 0"); err != nil {
		t.Fatal(err)
	}
	result, err := vm.RunString("finished && resolved")
	if err != nil {
		t.Fatal(err)
	}
	if got, want := result.String(), "https://cdn.example.test/song.mp3"; got != want {
		t.Fatalf("LX request Promise result = %q, want %q", got, want)
	}
}

func TestLXRequestAsyncPromiseResolvesFullResponse(t *testing.T) {
	client := &http.Client{Transport: lxRoundTripFunc(func(request *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Status: "200 OK",
			Header: http.Header{"Content-Type": []string{"application/json"}, "X-Test": []string{"present"}},
			Body: io.NopCloser(strings.NewReader(`{"url":"https://cdn.example.test/song.mp3"}`)),
			Request: request,
		}, nil
	})}
	vm := goja.New()
	lx := vm.NewObject()
	loop := newLXRuntimeEventLoop(vm)
	defer loop.stop()
	if err := installLXRequestAsync(vm, lx, client, loop, nil); err != nil { t.Fatal(err) }
	if err := vm.Set("lx", lx); err != nil { t.Fatal(err) }
	_, err := vm.RunString(`
		var promiseResult;
		var promiseError = "";
		var promiseFinished = false;
		(async function () {
			try { promiseResult = await lx.request("https://music.example.test/resolve", {}); }
			catch (error) { promiseError = String(error); }
			promiseFinished = true;
		})();
	`)
	if err != nil { t.Fatal(err) }
	if err := loop.pumpUntil(func() bool {
		value, runErr := vm.RunString("promiseFinished")
		return runErr == nil && value.ToBoolean()
	}, time.Second); err != nil { t.Fatal(err) }
	value, err := vm.RunString(`promiseError === "" &&
		promiseResult.statusCode === 200 &&
		promiseResult.statusMessage === "OK" &&
		promiseResult.headers["content-type"] === "application/json" &&
		promiseResult.body.url === "https://cdn.example.test/song.mp3" &&
		promiseResult.bytes > 0 &&
		promiseResult.raw !== undefined`)
	if err != nil { t.Fatal(err) }
	if !value.ToBoolean() { t.Fatal("await lx.request() did not resolve the complete LX response object") }
}

func TestLXRequestAsyncPromiseRejectsOnTransportError(t *testing.T) {
	client := &http.Client{Transport: lxRoundTripFunc(func(*http.Request) (*http.Response, error) {
		return nil, errors.New("test transport failure")
	})}
	vm := goja.New()
	lx := vm.NewObject()
	loop := newLXRuntimeEventLoop(vm)
	defer loop.stop()
	if err := installLXRequestAsync(vm, lx, client, loop, nil); err != nil { t.Fatal(err) }
	if err := vm.Set("lx", lx); err != nil { t.Fatal(err) }
	_, err := vm.RunString(`
		var rejected = false;
		var rejectionMessage = "";
		var promiseFinished = false;
		(async function () {
			try { await lx.request("https://music.example.test/fail", {}); }
			catch (error) { rejected = true; rejectionMessage = String(error); }
			promiseFinished = true;
		})();
	`)
	if err != nil { t.Fatal(err) }
	if err := loop.pumpUntil(func() bool {
		value, runErr := vm.RunString("promiseFinished")
		return runErr == nil && value.ToBoolean()
	}, time.Second); err != nil { t.Fatal(err) }
	value, err := vm.RunString("rejected && rejectionMessage !== ''")
	if err != nil { t.Fatal(err) }
	if !value.ToBoolean() { t.Fatal("await lx.request() must reject when the HTTP request fails") }
}

func TestLXRequestSupportsFormDataAndReturnsCancelFunction(t *testing.T) {
	client := &http.Client{Transport: lxRoundTripFunc(func(request *http.Request) (*http.Response, error) {
		if request.Method != http.MethodPost {
			t.Errorf("request method = %q, want POST", request.Method)
		}
		if request.Header.Get("Content-Type") != "application/x-www-form-urlencoded" {
			t.Errorf("unexpected multipart content type: %q", request.Header.Get("Content-Type"))
		}
		body, err := io.ReadAll(request.Body)
		if err != nil {
			t.Fatal(err)
		}
		if string(body) != "keyword=%E7%A8%BB%E9%A6%99" {
			t.Errorf("formData body differs from Needle querystring encoding: %s", body)
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Status: "200 OK",
			Header: make(http.Header),
			Body: io.NopCloser(strings.NewReader("ok")),
			Request: request,
		}, nil
	})}

	vm := goja.New()
	lx := vm.NewObject()
	if err := installLXRequestWithClient(vm, lx, client); err != nil {
		t.Fatal(err)
	}
	if err := vm.Set("lx", lx); err != nil {
		t.Fatal(err)
	}
	result, err := vm.RunString(`
		var callbackResult = "";
		var cancel = lx.request("https://music.example.test/search", {
			method: "post",
			formData: { keyword: "稻香" }
		}, function (err, response) {
			if (err) throw new Error(String(err));
			callbackResult = response.body;
		});
		(typeof cancel === "function" && callbackResult === "ok");
	`)
	if err != nil {
		t.Fatal(err)
	}
	if !result.ToBoolean() {
		t.Fatal("LX request should return a cancel function and provide the response body")
	}
}


func TestLXRequestParsesJSONRegardlessOfContentTypeAndLeadingWhitespace(t *testing.T) {
	client := &http.Client{Transport: lxRoundTripFunc(func(request *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Status: "200 OK",
			Header: http.Header{"Content-Type": []string{"text/plain"}},
			Body: io.NopCloser(strings.NewReader("  {\"url\":\"https://cdn.example.test/song.mp3\"}\n")),
			Request: request,
		}, nil
	})}
	vm := goja.New()
	response, _, err := performLXRequestWithClient("https://music.example.test/resolve", vm.ToValue(map[string]any{}), client)
	if err != nil { t.Fatal(err) }
	body, ok := response["body"].(map[string]any)
	if !ok { t.Fatalf("LX response body was not parsed as JSON: %#v", response["body"]) }
	if body["url"] != "https://cdn.example.test/song.mp3" { t.Fatalf("unexpected parsed body: %#v", body) }
}

func TestLXRequestMatchesNeedleDefaultObjectEncoding(t *testing.T) {
	for _, tc := range []struct{name, method, wantURL, wantBody, wantType string}{
		{"get query", "GET", "https://music.example.test/search?keyword=%E7%A8%BB%E9%A6%99", "", ""},
		{"post form", "POST", "https://music.example.test/search", "keyword=%E7%A8%BB%E9%A6%99", "application/x-www-form-urlencoded"},
	} {
		t.Run(tc.name,func(t *testing.T){
			client:=&http.Client{Transport:lxRoundTripFunc(func(req *http.Request)(*http.Response,error){
				if req.URL.String()!=tc.wantURL { t.Errorf("URL=%q want %q",req.URL.String(),tc.wantURL) }
				body,_:=io.ReadAll(req.Body)
				if string(body)!=tc.wantBody { t.Errorf("body=%q want %q",body,tc.wantBody) }
				if got:=req.Header.Get("Content-Type");got!=tc.wantType { t.Errorf("content-type=%q want %q",got,tc.wantType) }
				return &http.Response{StatusCode:200,Status:"200 OK",Header:make(http.Header),Body:io.NopCloser(strings.NewReader("ok")),Request:req},nil
			})}
			options:=map[string]any{"method":tc.method,"body":map[string]any{"keyword":"稻香"}}
			if _,_,err:=performLXRequestWithClient("https://music.example.test/search",goja.New().ToValue(options),client);err!=nil { t.Fatal(err) }
		})
	}
}

func TestLXRequestUsesJSONOnlyWhenContentTypeRequestsIt(t *testing.T) {
	client:=&http.Client{Transport:lxRoundTripFunc(func(req *http.Request)(*http.Response,error){
		body,_:=io.ReadAll(req.Body)
		if string(body)!=`{"keyword":"稻香"}` { t.Errorf("JSON body=%q",body) }
		if got:=req.Header.Get("Content-Type");!strings.HasPrefix(got,"application/json") { t.Errorf("content-type=%q",got) }
		return &http.Response{StatusCode:200,Status:"200 OK",Header:http.Header{"X-Test":[]string{"ok"}},Body:io.NopCloser(strings.NewReader(`{"ok":true}`)),Request:req},nil
	})}
	options:=map[string]any{"method":"POST","headers":map[string]any{"Content-Type":"application/json"},"body":map[string]any{"keyword":"稻香"}}
	response,_,err:=performLXRequestWithClient("https://music.example.test/search",goja.New().ToValue(options),client)
	if err!=nil { t.Fatal(err) }
	if response["bytes"]!=int(11) { t.Errorf("bytes=%#v",response["bytes"]) }
	if _,ok:=response["raw"].([]byte);!ok { t.Errorf("raw must preserve response bytes, got %T",response["raw"]) }
	headers,ok:=response["headers"].(map[string]string)
	if !ok||headers["x-test"]!="ok" { t.Errorf("headers must use lower-case keys: %#v",response["headers"]) }
	if _,ok:=response["url"];ok { t.Error("LX preload response does not expose url") }
	if _,ok:=response["status"];ok { t.Error("LX preload response does not expose status") }
	if response["statusMessage"]!="OK" { t.Errorf("statusMessage=%#v",response["statusMessage"]) }
}


func TestLXRequestPreservesJavaScriptObjectKeyOrder(t *testing.T) {
	client := &http.Client{Transport: lxRoundTripFunc(func(request *http.Request) (*http.Response, error) {
		if got, want := request.URL.RawQuery, "z=1&a=2&m=3"; got != want {
			t.Errorf("query parameter order = %q, want %q", got, want)
		}
		return &http.Response{StatusCode: 200, Status: "200 OK", Header: make(http.Header), Body: io.NopCloser(strings.NewReader("ok")), Request: request}, nil
	})}
	vm := goja.New()
	lx := vm.NewObject()
	if err := installLXRequestWithClient(vm, lx, client); err != nil { t.Fatal(err) }
	if err := vm.Set("lx", lx); err != nil { t.Fatal(err) }
	_, err := vm.RunString(`lx.request("https://music.example.test/search", {
  method: "get",
  body: { z: "1", a: "2", m: "3" }
}, function (error) { if (error) throw error; });`)
	if err != nil { t.Fatal(err) }
}


func TestLXRequestSendsBufferBodyAsRawBytes(t *testing.T) {
	client := &http.Client{Transport: lxRoundTripFunc(func(request *http.Request) (*http.Response, error) {
		body, err := io.ReadAll(request.Body)
		if err != nil { t.Fatal(err) }
		if !bytes.Equal(body, []byte{0, 1, 2, 255}) {
			t.Errorf("Buffer request body = %v, want [0 1 2 255]", body)
		}
		return &http.Response{StatusCode: 200, Status: "200 OK", Header: make(http.Header), Body: io.NopCloser(strings.NewReader("ok")), Request: request}, nil
	})}
	vm := goja.New()
	lx := vm.NewObject()
	if err := installLXRequestWithClient(vm, lx, client); err != nil { t.Fatal(err) }
	if err := vm.Set("lx", lx); err != nil { t.Fatal(err) }
	if err := vm.Set("buffer", newLXBuffer(vm, []byte{0, 1, 2, 255})); err != nil { t.Fatal(err) }
	_, err := vm.RunString(`lx.request("https://music.example.test/upload", {
  method: "post",
  body: buffer
}, function (error) { if (error) throw error; });`)
	if err != nil { t.Fatal(err) }
}


func TestLXRequestAsyncCancelAbortsTransportAndCallsCallback(t *testing.T) {
	started := make(chan struct{})
	cancelled := make(chan struct{})
	client := &http.Client{Transport: lxRoundTripFunc(func(request *http.Request) (*http.Response, error) {
		close(started)
		select {
		case <-request.Context().Done():
			close(cancelled)
			return nil, request.Context().Err()
		case <-time.After(time.Second):
			return &http.Response{
				StatusCode: http.StatusOK,
				Status: "200 OK",
				Header: make(http.Header),
				Body: io.NopCloser(strings.NewReader("unexpected")),
				Request: request,
			}, nil
		}
	})}

	vm := goja.New()
	lx := vm.NewObject()
	loop := newLXRuntimeEventLoop(vm)
	defer loop.stop()
	if err := installLXRequestAsync(vm, lx, client, loop, nil); err != nil { t.Fatal(err) }
	if err := vm.Set("lx", lx); err != nil { t.Fatal(err) }
	if err := vm.Set("window", vm.GlobalObject()); err != nil { t.Fatal(err) }
	_, err := vm.RunString(`
		var callbackCalled = false;
		var callbackThis = false;
		var callbackError = "";
		var cancelRequest = lx.request("https://music.example.test/slow", {}, function (error) {
			callbackCalled = true;
			callbackThis = this === lx;
			callbackError = error ? error.message : "";
		});
	`)
	if err != nil { t.Fatal(err) }

	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("HTTP request did not start")
	}
	if _, err := vm.RunString("cancelRequest()"); err != nil { t.Fatal(err) }
	if err := loop.pumpUntil(func() bool {
		value, runErr := vm.RunString("callbackCalled")
		return runErr == nil && value.ToBoolean()
	}, time.Second); err != nil {
		t.Fatal(err)
	}
	value, err := vm.RunString("callbackCalled && callbackThis && callbackError !== ''")
	if err != nil { t.Fatal(err) }
	if !value.ToBoolean() {
		t.Fatal("cancelled request did not call back with the original lx.request this context and an error")
	}
	select {
	case <-cancelled:
	case <-time.After(time.Second):
		t.Fatal("cancel function did not abort the underlying HTTP request")
	}
}

func TestLXRequestRejectsOversizedRequestAndResponseBodies(t *testing.T) {
	t.Run("request", func(t *testing.T) {
		client := &http.Client{Transport: lxRoundTripFunc(func(request *http.Request) (*http.Response, error) {
			t.Fatal("oversized request must be rejected before transport")
			return nil, nil
		})}
		options := map[string]any{"method": "POST", "body": strings.Repeat("x", lxRequestBodyLimit+1)}
		if _, _, err := performLXRequestWithClient("https://music.example.test/upload", goja.New().ToValue(options), client); err == nil || !strings.Contains(err.Error(), "2 MB") {
			t.Fatalf("oversized request error = %v, want 2 MB limit", err)
		}
	})
	t.Run("response", func(t *testing.T) {
		client := &http.Client{Transport: lxRoundTripFunc(func(request *http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusOK,
				Status: "200 OK",
				Header: make(http.Header),
				Body: io.NopCloser(strings.NewReader(strings.Repeat("x", lxRequestBodyLimit+1))),
				Request: request,
			}, nil
		})}
		if _, _, err := performLXRequestWithClient("https://music.example.test/large", goja.New().ToValue(map[string]any{}), client); err == nil || !strings.Contains(err.Error(), "2 MB") {
			t.Fatalf("oversized response error = %v, want 2 MB limit", err)
		}
	})
}

func TestLXNeedleTransportUsesConfiguredHTTPProxy(t *testing.T) {
	proxy := config.LXUserSourceProxyConfig{Enabled:true, Host:"127.0.0.1", Port:"7890"}
	transport := newLXNeedleTransportWithProxy(time.Second, proxy)
	defer transport.CloseIdleConnections()
	for _, scheme := range []string{"http", "https"} {
		request := &http.Request{URL: mustLXTestURL(""+scheme+"://music.example.test/song")}
		got, err := transport.Proxy(request)
		if err != nil { t.Fatalf("proxy lookup for %s: %v", scheme, err) }
		if got == nil || got.String() != "http://127.0.0.1:7890" { t.Errorf("proxy for %s = %v", scheme, got) }
	}
}

func mustLXTestURL(raw string) *url.URL { parsed, err := url.Parse(raw); if err != nil { panic(err) }; return parsed }
