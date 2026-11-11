package music

import (
	"io"
	"net/http"
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
		deadline, ok := request.Context().Deadline()
		if !ok {
			t.Fatal("LX request must set a bounded default timeout")
		}
		remaining := time.Until(deadline)
		if remaining < 59*time.Second || remaining > 60*time.Second {
			t.Errorf("LX request default timeout = %s, want 60 seconds like LX custom-source preload", remaining)
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
}

func TestLXRequestCallbackReceivesResponseBody(t *testing.T) {
	client := &http.Client{Transport: lxRoundTripFunc(func(request *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Status: "200 OK",
			Header: make(http.Header),
			Body: io.NopCloser(strings.NewReader("{\"url\":\"https://cdn.example.test/song.mp3\"}")),
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
		var callbackBody = "";
		lx.request("https://music.example.test/resolve", {}, function (err, response) {
			if (err) throw new Error(String(err));
			callbackBody = JSON.stringify(response.body);
		});
		callbackBody;
	`)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := value.String(), "{\"url\":\"https://cdn.example.test/song.mp3\"}"; got != want {
		t.Fatalf("LX request callback body = %q, want %q", got, want)
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

func TestLXRequestSupportsFormDataAndReturnsCancelFunction(t *testing.T) {
	client := &http.Client{Transport: lxRoundTripFunc(func(request *http.Request) (*http.Response, error) {
		if request.Method != http.MethodPost {
			t.Errorf("request method = %q, want POST", request.Method)
		}
		if !strings.HasPrefix(request.Header.Get("Content-Type"), "multipart/form-data; boundary=") {
			t.Errorf("unexpected multipart content type: %q", request.Header.Get("Content-Type"))
		}
		body, err := io.ReadAll(request.Body)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(body), "name=\"keyword\"") || !strings.Contains(string(body), "稻香") {
			t.Errorf("multipart body does not contain form field: %s", body)
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
		return &http.Response{StatusCode:http.StatusOK, Status:"200 OK", Header:http.Header{"Content-Type":[]string{"text/plain"}}, Body:io.NopCloser(strings.NewReader("  {\\"url\\":\\"https://cdn.example.test/song.mp3\\"}\\n")), Request:request}, nil
	})}
	vm:=goja.New()
	response,_,err:=performLXRequestWithClient("https://music.example.test/resolve",vm.ToValue(map[string]any{}),client)
	if err!=nil { t.Fatal(err) }
	body,ok:=response["body"].(map[string]any)
	if !ok { t.Fatalf("LX response body was not parsed as JSON: %#v",response["body"]) }
	if body["url"]!="https://cdn.example.test/song.mp3" { t.Fatalf("unexpected parsed body: %#v",body) }
}
