//go:build lxquickjs && cgo

package lxruntime

import (
	"bytes"
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
globalThis.__lxCharCodeTrace = [];
const __lxCharCodeAt = String.prototype.charCodeAt;
const __lxCharCodeSeen = new Set();
String.prototype.charCodeAt = function(index) {
  const value = String(this);
  if ((value.length >= 16 && value.length <= 80 && /^[0-9a-f]+$/i.test(value)) ||
      value.length > 256) {
    const key = value.length + ':' + value.slice(0, 96) + ':' + value.slice(-32);
    if (!__lxCharCodeSeen.has(key) && globalThis.__lxCharCodeTrace.length < 160) {
      __lxCharCodeSeen.add(key);
      globalThis.__lxCharCodeTrace.push({ length: value.length, digest: globalThis.lx.utils.crypto.md5(value), head: value.slice(0, 160), tail: value.slice(-64) });
    }
  }
  return __lxCharCodeAt.call(this, index);
};
globalThis.__lxMD5Trace = [];
const __lxCrypto = globalThis.lx.utils.crypto;
const __lxOriginalMD5 = __lxCrypto.md5.bind(__lxCrypto);
__lxCrypto.md5 = function(value) {
  const output = __lxOriginalMD5(value);
  globalThis.__lxMD5Trace.push({
    input: (typeof value === 'string' ? value : String(value)).slice(0, 256),
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
Object.defineProperty(globalThis, '__lxCompleteRequest', {
  configurable: true,
  value: function(id, error, response) {
    if (response && typeof response === 'object') response = __lxTraceResponse(response, 'response');
    return __lxCompleteRequest(id, error, response);
  }
});
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
	charTraceJSON, err := rt.Eval(ctx, "JSON.stringify(globalThis.__lxCharCodeTrace)", "lx-rconfig-charcode-trace.js")
	if err != nil { t.Fatal(err) }
	t.Logf("source charCodeAt input trace: %s", charTraceJSON)
	metadataTraceJSON, err := rt.Eval(ctx, `JSON.stringify({scriptMD5Type: typeof globalThis.SCRIPT_MD5, scriptMD5: globalThis.SCRIPT_MD5, rawScriptLength: globalThis.lx.currentScriptInfo.rawScript.length, rawScriptMD5: globalThis.lx.utils.crypto.md5(globalThis.lx.currentScriptInfo.rawScript), rawScriptHead: globalThis.lx.currentScriptInfo.rawScript.slice(0, 320), rawScriptTail: globalThis.lx.currentScriptInfo.rawScript.slice(-320), globalKeys: Object.keys(globalThis).filter(key => /md5|script/i.test(key)).slice(0, 80)})`, "lx-rconfig-metadata-trace.js")
	if err != nil { t.Fatal(err) }
	t.Logf("source integrity globals and metadata: %s", metadataTraceJSON)
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


// TestLXSourceRConfigLiveHandshake performs one real, signed V7 /rconfig
// request when explicitly enabled by the dedicated CI workflow. Unlike the
// deterministic protocol tests, this verifies the live server contract.
func TestLXSourceRConfigLiveHandshake(t *testing.T) {
	if os.Getenv("LX_RCONFIG_LIVE") != "1" {
		t.Skip("set LX_RCONFIG_LIVE=1 to exercise the live LX configuration endpoint")
	}
	source, err := os.ReadFile(filepath.Join("testdata", "sources", "lx.js"))
	if err != nil { t.Fatal(err) }

	var status int
	var responseBody []byte
	transport := roundTripFunc(func(request *http.Request) (*http.Response, error) {
		response, err := http.DefaultTransport.RoundTrip(request)
		if err != nil { return nil, err }
		if request.URL.Path == "/rconfig" {
			t.Logf("live /rconfig request headers: content-type=%q user-agent=%q", request.Header.Get("Content-Type"), request.Header.Get("User-Agent"))
			status = response.StatusCode
			responseBody, err = io.ReadAll(io.LimitReader(response.Body, 1<<20))
			_ = response.Body.Close()
			if err != nil { return nil, err }
			response.Body = io.NopCloser(bytes.NewReader(responseBody))
		}
		return response, nil
	})
	client := &http.Client{Transport: transport, Timeout: 15 * time.Second}
	rt := newTestRuntime(t, 10*time.Second)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	err = rt.InitializeLXSource(ctx, ScriptInfo{
		Name: "独家音源", Version: "7", Author: "w",
		Description: "后续更新，请关注微信公众号: 洛雪科技",
		RawScript: string(source),
	}, string(source), client)
	t.Logf("live /rconfig HTTP status: %d", status)
	t.Logf("live /rconfig response body: %s", string(responseBody))
	if err != nil {
		t.Fatalf("live LX /rconfig handshake failed (HTTP %d): %v", status, err)
	}
	if status < 200 || status >= 300 {
		t.Fatalf("live LX /rconfig returned HTTP %d", status)
	}
}
