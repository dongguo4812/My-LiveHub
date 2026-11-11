package music

import (
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/dop251/goja"
)

type lxRoundTripFunc func(*http.Request) (*http.Response, error)

func (f lxRoundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

func TestLXRequestCallbackReceivesResponseBody(t *testing.T) {
	client := &http.Client{Transport: lxRoundTripFunc(func(request *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Status: "200 OK",
			Header: make(http.Header),
			Body: io.NopCloser(strings.NewReader("{\\"url\\":\\"https://cdn.example.test/song.mp3\\"}")),
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
			callbackBody = response.body;
		});
		callbackBody;
	`)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := value.String(), "{\\"url\\":\\"https://cdn.example.test/song.mp3\\"}"; got != want {
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
