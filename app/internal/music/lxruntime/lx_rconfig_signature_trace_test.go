//go:build lxquickjs && cgo

package lxruntime

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestLXSourceRConfigSignatureTrace records the source's own MD5 inputs while
// constructing /rconfig. The mock response is intentionally invalid: this is
// a signature-observation test, not a successful-handshake claim.
func TestLXSourceRConfigSignatureTrace(t *testing.T) {
	source, err := os.ReadFile(filepath.Join("testdata", "sources", "lx.js"))
	if err != nil {
		t.Fatal(err)
	}

	const traceShim = `
globalThis.__lxMD5Trace = [];
const __lxCrypto = globalThis.lx.utils.crypto;
const __lxOriginalMD5 = __lxCrypto.md5.bind(__lxCrypto);
__lxCrypto.md5 = function(value) {
  const output = __lxOriginalMD5(value);
  globalThis.__lxMD5Trace.push({
    input: typeof value === 'string' ? value : String(value),
    output
  });
  return output;
};
globalThis.__lxCryptoTrace = [];
globalThis.__lxResponseTrace = [];
const __lxCompleteRequest = globalThis.__lxCompleteRequest;
const __lxTraceResponse = (value, path) => {
  if (!value || typeof value !== 'object') return value;
  return new Proxy(value, {
    get(target, key, receiver) {
      const result = Reflect.get(target, key, receiver);
      globalThis.__lxResponseTrace.push({ path: path + '.' + String(key), type: typeof result,
        value: result == null || ['string', 'number', 'boolean'].includes(typeof result) ? result : undefined });
      return result && typeof result === 'object' ? __lxTraceResponse(result, path + '.' + String(key)) : result;
    }
  });
};
globalThis.__lxCompleteRequest = function(id, error, response) {
  if (response && typeof response === 'object') response = __lxTraceResponse(response, 'response');
  return __lxCompleteRequest(id, error, response);
};
const __lxHex = value => Array.from(value || []).map(byte => Number(byte).toString(16).padStart(2, '0')).join('');
for (const name of ['aesEncrypt', 'rsaEncrypt']) {
  const original = __lxCrypto[name].bind(__lxCrypto);
  __lxCrypto[name] = function(...args) {
    const result = original(...args);
    globalThis.__lxCryptoTrace.push({
      name,
      args: args.map(value => typeof value === 'string' ? value : __lxHex(value)),
      output: __lxHex(result)
    });
    return result;
  };
}
`

	var capturedURL string
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		capturedURL = request.URL.String()
		return &http.Response{
			StatusCode: http.StatusOK,
			Status:     "200 OK",
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(`{}`)),
			Request:    request,
		}, nil
	})}

	rt := newTestRuntime(t, 10*time.Second)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	instrumentedSource := traceShim + string(source)
	err = rt.InitializeLXSource(ctx, ScriptInfo{
		Name: "独家音源", Version: "7", Author: "w",
		Description: "后续更新，请关注微信公众号: 洛雪科技",
		RawScript: string(source),
	}, instrumentedSource, client)
	if err == nil {
		t.Fatal("expected invalid empty /rconfig response to leave initialization incomplete")
	}
	if capturedURL == "" {
		t.Fatalf("source did not issue /rconfig before initialization failed: %v", err)
	}
	parsedURL, err := url.Parse(capturedURL)
	if err != nil {
		t.Fatal(err)
	}
	traceJSON, err := rt.Eval(ctx, "JSON.stringify(globalThis.__lxMD5Trace)", "lx-rconfig-md5-trace.js")
	if err != nil {
		t.Fatal(err)
	}
	var trace []struct {
		Input  string `json:"input"`
		Output string `json:"output"`
	}
	if err := json.Unmarshal([]byte(traceJSON), &trace); err != nil {
		t.Fatalf("decode MD5 trace: %v", err)
	}
	si := parsedURL.Query().Get("si")
	matched := false
	for _, call := range trace {
		if strings.EqualFold(call.Output, si) {
			matched = true
			t.Logf("rconfig si preimage observed through lx.utils.crypto.md5: input=%q output=%s", call.Input, call.Output)
		}
	}
	t.Logf("captured rconfig URL: %s", parsedURL.Redacted())
	t.Logf("source MD5 trace (%d calls): %#v", len(trace), trace)
	cryptoTraceJSON, err := rt.Eval(ctx, "JSON.stringify(globalThis.__lxCryptoTrace)", "lx-rconfig-crypto-trace.js")
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("source AES/RSA trace: %s", cryptoTraceJSON)
	responseTraceJSON, err := rt.Eval(ctx, "JSON.stringify(globalThis.__lxResponseTrace)", "lx-rconfig-response-trace.js")
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("source /rconfig response property trace: %s", responseTraceJSON)
	if !matched {
		t.Logf("rconfig si=%s was not a direct output of a traced MD5 call; it may be derived through byte-level crypto or another transform", si)
	}
}
