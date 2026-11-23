//go:build lxquickjs && cgo

package lxruntime

import (
	"context"
	"encoding/json"
	"io"
	"mime"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestExecuteLXHTTPRequestJSONAndHeaders(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost { t.Errorf("method = %s", r.Method) }
		if got := r.Header.Get("X-LX-Test"); got != "present" { t.Errorf("header = %q", got) }
		body, _ := io.ReadAll(r.Body)
		if string(body) != `{"song":"demo"}` { t.Errorf("request body = %s", body) }
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"code":200,"url":"https://cdn.example/test.mp3"}`)
	}))
	defer server.Close()

	response, err := executeLXHTTPRequestWithPolicy(context.Background(), lxRequestDescriptor{
		URL: server.URL, Method: "post", Headers: map[string]any{"X-LX-Test":"present"},
		Body: json.RawMessage(`{"song":"demo"}`),
	}, server.Client(), true)
	if err != nil { t.Fatal(err) }
	if response.StatusCode != http.StatusOK || response.Bytes == 0 { t.Fatalf("unexpected response metadata: %+v", response) }
	body, ok := response.Body.(map[string]any)
	if !ok || body["code"] != float64(200) { t.Fatalf("JSON body not decoded: %#v", response.Body) }
	if string(response.Raw) != `{"code":200,"url":"https://cdn.example/test.mp3"}` { t.Fatalf("raw body = %q", response.Raw) }
}

func TestExecuteLXHTTPRequestFormAndMultipart(t *testing.T) {
	t.Run("urlencoded", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if got := r.Header.Get("Content-Type"); !strings.HasPrefix(got, "application/x-www-form-urlencoded") { t.Errorf("content type = %q", got) }
			_ = r.ParseForm()
			if r.Form.Get("keyword") != "hello world" { t.Errorf("form = %#v", r.Form) }
			_, _ = io.WriteString(w, "ok")
		}))
		defer server.Close()
		_, err := executeLXHTTPRequestWithPolicy(context.Background(), lxRequestDescriptor{URL:server.URL, Method:http.MethodPost, Form:map[string]any{"keyword":"hello world"}}, server.Client())
		if err != nil { t.Fatal(err) }
	})
	t.Run("multipart", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			mediaType, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
			if mediaType != "multipart/form-data" { t.Errorf("content type = %q", mediaType) }
			if err := r.ParseMultipartForm(1 << 20); err != nil { t.Error(err); return }
			if r.FormValue("token") != "abc" { t.Errorf("token = %q", r.FormValue("token")) }
			_, _ = io.WriteString(w, "ok")
		}))
		defer server.Close()
		_, err := executeLXHTTPRequestWithPolicy(context.Background(), lxRequestDescriptor{URL:server.URL, Method:http.MethodPost, FormData:map[string]any{"token":"abc"}}, server.Client())
		if err != nil { t.Fatal(err) }
	})
}

func TestExecuteLXHTTPRequestTimeoutAndURLValidation(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(150 * time.Millisecond)
		_, _ = io.WriteString(w, "late")
	}))
	defer server.Close()
	_, err := executeLXHTTPRequestWithPolicy(context.Background(), lxRequestDescriptor{URL:server.URL, Timeout:25}, server.Client())
	if err == nil || !strings.Contains(strings.ToLower(err.Error()), "deadline") { t.Fatalf("timeout error = %v", err) }
	_, err = executeLXHTTPRequestWithPolicy(context.Background(), lxRequestDescriptor{URL:"file:///tmp/a"}, server.Client())
	if err == nil { t.Fatal("expected non-HTTP URL to be rejected") }
}

func TestExecuteLXHTTPRequestResponseSizeLimit(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, strings.Repeat("x", lxMaxResponseBytes+1))
	}))
	defer server.Close()
	_, err := executeLXHTTPRequestWithPolicy(context.Background(), lxRequestDescriptor{URL:server.URL}, server.Client())
	if err == nil || !strings.Contains(err.Error(), "exceeds") { t.Fatalf("oversized response error = %v", err) }
}


func TestLXRequestTimeoutClampsWithoutOverflow(t *testing.T) {
	if got := lxRequestTimeout(25); got != 25*time.Millisecond {
		t.Fatalf("small timeout = %s", got)
	}
	if got := lxRequestTimeout(1<<62); got != lxMaxRequestTimeout {
		t.Fatalf("large timeout = %s, want %s", got, lxMaxRequestTimeout)
	}
	if got := lxRequestTimeout(0); got != lxDefaultRequestTimeout {
		t.Fatalf("default timeout = %s", got)
	}
}

func TestBuildLXRequestBodyUsesJavaScriptTruthySemantics(t *testing.T) {
	for _, raw := range []string{`null`, `false`, `0`, `""`} {
		body, contentType, err := buildLXRequestBody(lxRequestDescriptor{
			Body: json.RawMessage(raw),
			Form: map[string]any{"keyword": "fallback"},
		})
		if err != nil {
			t.Fatalf("body %s: %v", raw, err)
		}
		if body == nil || contentType != "application/x-www-form-urlencoded" {
			t.Fatalf("body %s did not fall back to form: content type %q", raw, contentType)
		}
		data, _ := io.ReadAll(body)
		if string(data) != "keyword=fallback" {
			t.Fatalf("body %s produced %q", raw, data)
		}
	}
	for _, raw := range []string{`{}`, `[]`, `"payload"`} {
		body, _, err := buildLXRequestBody(lxRequestDescriptor{Body: json.RawMessage(raw)})
		if err != nil || body == nil {
			t.Fatalf("truthy body %s: body=%v err=%v", raw, body, err)
		}
	}
}
