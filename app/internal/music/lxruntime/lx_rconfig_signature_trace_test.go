//go:build lxquickjs

package lxruntime

import (
	"bytes"
	"context"
	"encoding/json"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestLXPinnedSourceFixtureUsesCanonicalLF verifies the checked-out script bytes
// are identical across Windows and Unix. The V7 integrity signature is computed
// from rawScript, so Git's Windows CRLF conversion changes the server-side digest.
func TestLXPinnedSourceFixtureUsesCanonicalLF(t *testing.T) {
	source, err := os.ReadFile(filepath.Join("testdata", "sources", "lx.js"))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(source, []byte("\r\n")) {
		t.Fatal("pinned lx.js contains CRLF; V7 /rconfig integrity requires canonical LF source bytes")
	}
}

// TestLXSourceRConfigSignatureTrace records the source's own MD5 inputs while
// constructing /rconfig. The mock response is intentionally empty: the source falls back to its
// built-in providers, allowing this test to observe signature construction
// without requiring the live configuration service.
func TestLXSourceRConfigSignatureTrace(t *testing.T) {
	source, err := os.ReadFile(filepath.Join("testdata", "sources", "lx.js"))
	if err != nil {
		t.Fatal(err)
	}

	const traceShim = `
Error.stackTraceLimit = 40;
globalThis.__lxDecodeTrace = [];
for (const name of ['atob', 'btoa']) {
  const original = globalThis[name];
  if (typeof original === 'function') globalThis[name] = function(value) {
    const result = original.call(globalThis, value);
    if (globalThis.__lxDecodeTrace.length < 100) globalThis.__lxDecodeTrace.push({name, inputLength: String(value).length, outputLength: String(result).length, outputHead: String(result).slice(0, 96), stack: new Error().stack});
    return result;
  };
}
globalThis.__lxCharCodeTrace = [];
const __lxCharCodeAt = String.prototype.charCodeAt;
const __lxCharCodeSeen = new Set();
globalThis.__lxCharCodeCallTrace = [];
String.prototype.charCodeAt = function(index) {
  const value = String(this);
  if (value.length >= 8) {
    const key = value.length + ':' + value.slice(0, 96) + ':' + value.slice(-32);
    if (!__lxCharCodeSeen.has(key) && globalThis.__lxCharCodeTrace.length < 500) {
      __lxCharCodeSeen.add(key);
      globalThis.__lxCharCodeTrace.push({ length: value.length, digest: globalThis.lx.utils.crypto.md5(value), head: value.slice(0, 240), tail: value.slice(-96) });
    }
    if (globalThis.__lxCharCodeCallTrace.length < 2500) {
      const stack = new Error().stack;
      if (stack.includes('354:7947') || stack.includes('354:17665') || stack.includes('354:14818') || stack.includes('354:15411')) {
        globalThis.__lxCharCodeCallTrace.push({
          length: value.length, index, char: value.charAt(index),
          code: __lxCharCodeAt.call(this, index),
          head: value.slice(0, 96), tail: value.slice(-64), stack
        });
      }
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
const __lxRandomBytes = __lxCrypto.randomBytes.bind(__lxCrypto);
__lxCrypto.randomBytes = function(...args) {
  const result = __lxRandomBytes(...args);
  globalThis.__lxCryptoTrace.push({ name: 'randomBytes', args, output: __lxHex(result) });
  return result;
};
const __lxDateNow = Date.now;
Date.now = function() {
  const value = __lxDateNow.call(Date);
  globalThis.__lxCryptoTrace.push({ name: 'Date.now', output: String(value), stack: new Error().stack });
  return value;
};
const __lxMathRandom = Math.random;
Math.random = function() {
  const value = __lxMathRandom.call(Math);
  globalThis.__lxCryptoTrace.push({ name: 'Math.random', output: String(value) });
  return value;
};
const __lxMathImul = Math.imul;
if (typeof __lxMathImul === 'function') Math.imul = function(a, b) {
  const value = __lxMathImul.call(Math, a, b);
  if (globalThis.__lxCryptoTrace.length < 1200) globalThis.__lxCryptoTrace.push({ name: 'Math.imul', args: [a, b], output: value, stack: new Error().stack });
  return value;
};
for (const name of ['floor', 'ceil', 'round', 'trunc']) {
  const original = Math[name];
  if (typeof original !== 'function') continue;
  Math[name] = function(value) {
    const result = original.call(Math, value);
    if (globalThis.__lxCryptoTrace.length < 1200 && (Math.abs(Number(value)) > 100000 || Math.abs(Number(result)) > 100000)) {
      globalThis.__lxCryptoTrace.push({ name: 'Math.' + name, input: value, output: result, stack: new Error().stack });
    }
    return result;
  };
}
const __lxPerformanceNow = globalThis.performance && globalThis.performance.now;
if (__lxPerformanceNow) {
  globalThis.performance.now = function() {
    const value = __lxPerformanceNow.call(globalThis.performance);
    globalThis.__lxCryptoTrace.push({ name: 'performance.now', output: String(value) });
    return value;
  };
}
const __lxNumberToString = Number.prototype.toString;
globalThis.__lxHexDigitTrace = [];
Number.prototype.toString = function(radix) {
  const value = __lxNumberToString.call(this, radix);
  if (radix === 16) {
    const stack = new Error().stack;
    if (globalThis.__lxCryptoTrace.length < 1200) globalThis.__lxCryptoTrace.push({ name: 'Number.toString(16)', input: String(this), output: value, stack });
    if (globalThis.__lxHexDigitTrace.length < 12000) {
      globalThis.__lxHexDigitTrace.push({ input: Number(this), output: value, stack: stack.split(String.fromCharCode(10)).slice(1, 7) });
    }
  }
  return value;
};

globalThis.__lxArrayTrace = [];
// Instrument only the typed-array prototype and record metadata, never call
// Array helpers from inside these wrappers. Patching Array.prototype.map/join
// recursively instruments the trace collector itself in QuickJS.
const __lxTypedArrayPrototype = globalThis.Uint8Array && Object.getPrototypeOf(Uint8Array.prototype);
if (__lxTypedArrayPrototype) {
  for (const method of ['set', 'fill', 'reverse', 'sort']) {
    const original = __lxTypedArrayPrototype[method];
    if (typeof original !== 'function') continue;
    try {
      __lxTypedArrayPrototype[method] = function(...args) {
        if (globalThis.__lxArrayTrace.length < 500 && this && this.length >= 8 && this.length <= 64) {
          globalThis.__lxArrayTrace.push({
            method,
            length: this.length,
            argumentLengths: args.map(value => value && typeof value.length === 'number' ? value.length : null),
            stack: new Error().stack
          });
        }
        return original.apply(this, args);
      };
    } catch (_) {}
  }
}
globalThis.__lxTypedArrayMapTrace = [];
if (__lxTypedArrayPrototype && typeof __lxTypedArrayPrototype.map === 'function') {
  const __lxTypedArrayMap = __lxTypedArrayPrototype.map;
  __lxTypedArrayPrototype.map = function(callback, thisArg) {
    const before = this && this.length >= 8 && this.length <= 64 ? Array.from(this) : null;
    const result = __lxTypedArrayMap.call(this, callback, thisArg);
    if (before && globalThis.__lxTypedArrayMapTrace.length < 300) {
      globalThis.__lxTypedArrayMapTrace.push({
        input: before,
        output: result && typeof result.length === 'number' ? Array.from(result) : String(result),
        stack: new Error().stack
      });
    }
    return result;
  };
}

// Trace typed-array creation and view/copy operations: the 16-byte prefix
// is converted to hex in the obfuscated interpreter, so map/set hooks alone
// miss the source buffer that feeds that conversion.
globalThis.__lxTypedArrayCreateTrace = [];
const __lxOriginalUint8Array = globalThis.Uint8Array;
try {
  globalThis.Uint8Array = new Proxy(__lxOriginalUint8Array, {
    construct(target, args, newTarget) {
      const value = Reflect.construct(target, args, newTarget);
      if (value && value.length >= 8 && value.length <= 64 &&
          globalThis.__lxTypedArrayCreateTrace.length < 500) {
        globalThis.__lxTypedArrayCreateTrace.push({
          args: args.map(arg => arg && typeof arg.length === 'number'
            ? {length: arg.length, head: Array.from(arg).slice(0, 40)} : arg),
          output: Array.from(value),
          stack: new Error().stack
        });
      }
      return value;
    }
  });
} catch (_) {}
if (__lxTypedArrayPrototype) {
  for (const method of ['slice', 'subarray']) {
    const original = __lxTypedArrayPrototype[method];
    if (typeof original !== 'function') continue;
    __lxTypedArrayPrototype[method] = function(...args) {
      const result = original.apply(this, args);
      if (this && this.length >= 8 && this.length <= 64 &&
          globalThis.__lxTypedArrayCreateTrace.length < 500) {
        globalThis.__lxTypedArrayCreateTrace.push({
          method, args, input: Array.from(this),
          output: result && typeof result.length === 'number' ? Array.from(result) : null,
          stack: new Error().stack
        });
      }
      return result;
    };
  }
}
globalThis.__lxStringSliceTrace = [];
for (const method of ['slice', 'substring', 'substr']) {
  const original = String.prototype[method];
  String.prototype[method] = function(...args) {
    const result = original.apply(this, args);
    if (String(this).length >= 8 && String(this).length <= 256 &&
        globalThis.__lxStringSliceTrace.length < 800) {
      globalThis.__lxStringSliceTrace.push({
        method, input: String(this), args, output: result,
        stack: new Error().stack
      });
    }
    return result;
  };
}
globalThis.__lxByteMapTrace = [];
const __lxArrayMap = Array.prototype.map;
Array.prototype.map = function(callback, thisArg) {
  const result = __lxArrayMap.call(this, callback, thisArg);
  if (this && this.length === 16 && globalThis.__lxByteMapTrace.length < 100) {
    globalThis.__lxByteMapTrace.push({
      input: Array.from(this),
      output: Array.from(result),
      stack: new Error().stack
    });
  }
  return result;
};
globalThis.__lxBytePushTrace = [];
const __lxArrayPush = Array.prototype.push;
const __lxPushArrayIDs = new WeakMap();
let __lxNextPushArrayID = 1;
Array.prototype.push = function(...values) {
  const before = this && typeof this.length === 'number' ? this.length : -1;
  let arrayID = 0;
  if (Array.isArray(this) && before >= 0 && before < 16) {
    arrayID = __lxPushArrayIDs.get(this);
    if (!arrayID) { arrayID = __lxNextPushArrayID++; __lxPushArrayIDs.set(this, arrayID); }
  }
  const result = __lxArrayPush.apply(this, values);
  if (arrayID && this.length <= 16 && values.length > 0 &&
      values.every(value => typeof value === 'number' || typeof value === 'string') &&
      globalThis.__lxBytePushTrace.length < 2500) {
    globalThis.__lxBytePushTrace.push({
      arrayID,
      before,
      values: values.slice(0, 16),
      result: this.length === 16 ? Array.from(this) : undefined,
      // Capture every append, not just the first and final byte. The 16-byte
      // rconfig prefix is assembled through an obfuscated interpreter, so the
      // caller frame can change between individual byte conversions.
      stack: new Error().stack
    });
  }
  return result;
};
globalThis.__lxTransformTrace = [];
let __lxTransformBusy = false;
const __lxRecordTransform = (name, input, output) => {
  if (__lxTransformBusy || globalThis.__lxTransformTrace.length >= 500) return;
  const text = typeof output === 'string' ? output : '';
  const interesting = /^[0-9a-f]{16,64}$/i.test(text) ||
    (Array.isArray(input) && input.length >= 8 && input.length <= 64) ||
    (typeof input === 'string' && input.length >= 8 && input.length <= 64);
  if (!interesting) return;
  __lxTransformBusy = true;
  try {
    globalThis.__lxTransformTrace.push({
      name,
      input: typeof input === 'string' ? input : (input && typeof input.length === 'number' ? {length: input.length, head: Array.from(input).slice(0, 40)} : String(input)),
      output: text.length > 160 ? text.slice(0, 160) : text,
      stack: new Error().stack
    });
  } finally { __lxTransformBusy = false; }
};
for (const method of ['join', 'reverse', 'map', 'reduce']) {
  const original = Array.prototype[method];
  Array.prototype[method] = function(...args) {
    const result = original.apply(this, args);
    __lxRecordTransform('Array.' + method, this, typeof result === 'string' ? result : (result && typeof result.length === 'number' ? Array.from(result).slice(0, 40) : result));
    return result;
  };
}
const __lxStringSplit = String.prototype.split;
String.prototype.split = function(...args) {
  const result = __lxStringSplit.apply(this, args);
  __lxRecordTransform('String.split', String(this), result);
  return result;
};
const __lxStringReplace = String.prototype.replace;
String.prototype.replace = function(...args) {
  const result = __lxStringReplace.apply(this, args);
  __lxRecordTransform('String.replace', String(this), result);
  return result;
};
const __lxParseInt = globalThis.parseInt;
globalThis.parseInt = function(...args) {
  const result = __lxParseInt.apply(globalThis, args);
  if (args.length && (String(args[0]).length >= 8 || args[1] === 16)) __lxRecordTransform('parseInt', String(args[0]), String(result));
  return result;
};

const __lxFromCharCode = String.fromCharCode;
String.fromCharCode = function(...codes) {
  const value = __lxFromCharCode.apply(String, codes);
  if (value.length >= 8) globalThis.__lxCryptoTrace.push({ name: 'String.fromCharCode', input: codes.slice(0, 64), output: value.slice(0, 128), stack: new Error().stack });
  return value;
};
if (globalThis.crypto && typeof globalThis.crypto === 'object') {
  for (const name of ['getRandomValues', 'randomUUID']) {
    const original = globalThis.crypto[name];
    if (typeof original === 'function') {
      try {
        globalThis.crypto[name] = function(...args) {
          const value = original.apply(this, args);
          globalThis.__lxCryptoTrace.push({ name: 'crypto.' + name, args: args.map(arg => arg && arg.length != null ? arg.length : String(arg)), output: name === 'randomUUID' ? String(value) : __lxHex(value) });
          return value;
        };
      } catch (_) {}
    }
  }
}
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
	if err != nil {
		t.Fatalf("source should initialize with built-in providers when /rconfig is empty: %v", err)
	}
	stateJSON, stateErr := rt.Eval(ctx, `JSON.stringify({initialized: globalThis.__lxHostState.initialized, requestHandler: typeof globalThis.__lxHostHandlers.request})`, "lx-rconfig-empty-response-state.js")
	if stateErr != nil { t.Fatal(stateErr) }
	if stateJSON != `{"initialized":true,"requestHandler":"function"}` {
		t.Fatalf("host state after empty /rconfig response = %s, want initialized=true with request handler", stateJSON)
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
	var signaturePreimage string
	var signatureOutput string
	for _, call := range trace {
		if strings.EqualFold(call.Output, si) {
			signaturePreimage = call.Input
			signatureOutput = call.Output
			preimage, decodeErr := hex.DecodeString(call.Input)
			if decodeErr != nil || len(preimage) != 32 || len(call.Input) != 64 {
				t.Fatalf("rconfig si preimage must be exactly 32 bytes of hex, got %q", call.Input)
			}
			matched = true
			// Keep the 16-byte dynamic prefix separate from the second
			// 16-byte digest. This is the exact boundary needed by the
			// ongoing V7 signature reverse-engineering work.
			t.Logf("rconfig si preimage components: dynamic_prefix=%s trailing_digest=%s output=%s",
				call.Input[:32], call.Input[32:], call.Output)
		}
	}
	t.Logf("captured rconfig URL: %s", parsedURL.Redacted())
	charTraceJSON, err := rt.Eval(ctx, "JSON.stringify(globalThis.__lxCharCodeTrace)", "lx-rconfig-charcode-trace.js")
	if err != nil { t.Fatal(err) }
	t.Logf("source charCodeAt input trace: %s", charTraceJSON)
	charCodeCallTraceJSON, err := rt.Eval(ctx, "JSON.stringify(globalThis.__lxCharCodeCallTrace)", "lx-rconfig-charcode-call-trace.js")
	if err != nil { t.Fatal(err) }
	t.Logf("source charCodeAt call-site trace: %s", charCodeCallTraceJSON)
	metadataTraceJSON, err := rt.Eval(ctx, `JSON.stringify({scriptMD5Type: typeof globalThis.SCRIPT_MD5, scriptMD5: globalThis.SCRIPT_MD5, rawScriptLength: globalThis.lx.currentScriptInfo.rawScript.length, rawScriptMD5: globalThis.lx.utils.crypto.md5(globalThis.lx.currentScriptInfo.rawScript), rawScriptHead: globalThis.lx.currentScriptInfo.rawScript.slice(0, 320), rawScriptTail: globalThis.lx.currentScriptInfo.rawScript.slice(-320), globalKeys: Object.keys(globalThis).filter(key => /md5|script/i.test(key)).slice(0, 80)})`, "lx-rconfig-metadata-trace.js")
	if err != nil { t.Fatal(err) }
	t.Logf("source integrity globals and metadata: %s", metadataTraceJSON)
	t.Logf("source MD5 trace (%d calls): %#v", len(trace), trace)
	cryptoTraceJSON, err := rt.Eval(ctx, "JSON.stringify(globalThis.__lxCryptoTrace)", "lx-rconfig-crypto-trace.js")
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("source AES/RSA trace: %s", cryptoTraceJSON)
	// V7 reconstructs its legacy signature salt at runtime through the
	// obfuscated String.fromCharCode decoder. The prefix is independently
	// reconstructed below from the timestamp and checked byte-for-byte
	// against the source-generated si preimage.
	if !strings.Contains(cryptoTraceJSON, `"output":"Nd7Sk/2Mj9s/Vdk"`) {
		t.Fatalf("V7 runtime did not decode the expected signature salt; crypto trace=%s", cryptoTraceJSON)
	}
	t.Log("V7 runtime signature salt decoded: Nd7Sk/2Mj9s/Vdk")
	hexDigitTraceJSON, err := rt.Eval(ctx, "JSON.stringify(globalThis.__lxHexDigitTrace)", "lx-rconfig-hex-digit-trace.js")
	if err != nil { t.Fatal(err) }
	t.Logf("source hex digit conversion trace: %s", hexDigitTraceJSON)
	// Keep the two observed timestamp digests distinct. MD5(decimal timestamp)
	// is the request's e parameter; MD5(hex timestamp) is a separate obfuscated
	// trace stage. The 16-byte si prefix is checked independently below against
	// the timestamp-reduction and per-byte multiplier reconstruction.
	var hexTrace []struct {
		Input float64 `json:"input"`
		Output string `json:"output"`
	}
	if err := json.Unmarshal([]byte(hexDigitTraceJSON), &hexTrace); err != nil {
		t.Fatalf("decode timestamp hex trace: %v", err)
	}
	timestampDecimalMD5Observed := false
	timestampHexMD5Observed := false
	var observedTimestamp int64
	for _, hexCall := range hexTrace {
		if hexCall.Input < 1e12 || hexCall.Output == "" {
			continue
		}
		for _, md5Call := range trace {
			if md5Call.Input == fmt.Sprintf("%.0f", hexCall.Input) && len(md5Call.Output) == 32 {
				timestampDecimalMD5Observed = true
				observedTimestamp = int64(hexCall.Input)
				t.Logf("V7 signature timestamp digest: decimal=%0.0f md5(decimal)=%s",
					hexCall.Input, md5Call.Output)
			}
			if md5Call.Input == hexCall.Output && len(md5Call.Output) == 32 {
				timestampHexMD5Observed = true
				t.Logf("V7 obfuscated prefix timestamp stage: decimal=%0.0f hex=%s md5(hex)=%s",
					hexCall.Input, hexCall.Output, md5Call.Output)
			}
		}
	}
	if !timestampDecimalMD5Observed || !timestampHexMD5Observed {
		t.Fatalf("V7 timestamp digest stages incomplete: decimalMD5=%t hexMD5=%t hex trace=%s",
			timestampDecimalMD5Observed, timestampHexMD5Observed, hexDigitTraceJSON)
	}
	if len(signaturePreimage) != 64 || signatureOutput != si {
		t.Fatalf("captured V7 si preimage/output missing: preimage=%q output=%q si=%q", signaturePreimage, signatureOutput, si)
	}
	wantPrefix := lxRConfigDynamicPrefix(observedTimestamp)
	if gotPrefix := signaturePreimage[:32]; gotPrefix != wantPrefix {
		t.Fatalf("V7 runtime dynamic prefix mismatch at timestamp %d: source=%s Go=%s", observedTimestamp, gotPrefix, wantPrefix)
	}
	wantDigest := lxRConfigScriptDigest(string(source))
	if gotDigest := signaturePreimage[32:]; gotDigest != wantDigest {
		t.Fatalf("V7 runtime script digest mismatch: source=%s Go=%s", gotDigest, wantDigest)
	}
	if wantSignature := lxRConfigSignature(observedTimestamp, string(source)); signatureOutput != wantSignature {
		t.Fatalf("V7 runtime si mismatch at timestamp %d: source=%s Go=%s", observedTimestamp, signatureOutput, wantSignature)
	}
	t.Logf("V7 si algorithm verified against live source execution: timestamp=%d prefix=%s script_digest=%s si=%s",
		observedTimestamp, wantPrefix, wantDigest, signatureOutput)
	decodeTraceJSON, err := rt.Eval(ctx, "JSON.stringify(globalThis.__lxDecodeTrace)", "lx-rconfig-decode-trace.js")
	if err != nil { t.Fatal(err) }
	t.Logf("source base64 decode trace: %s", decodeTraceJSON)
	arrayTraceJSON, err := rt.Eval(ctx, "JSON.stringify(globalThis.__lxArrayTrace)", "lx-rconfig-array-trace.js")
	if err != nil { t.Fatal(err) }
	t.Logf("source typed-array operation trace: %s", arrayTraceJSON)
	typedArrayCreateTraceJSON, err := rt.Eval(ctx, "JSON.stringify(globalThis.__lxTypedArrayCreateTrace)", "lx-rconfig-typed-array-create-trace.js")
	if err != nil { t.Fatal(err) }
	t.Logf("source typed-array allocation/view trace: %s", typedArrayCreateTraceJSON)
	stringSliceTraceJSON, err := rt.Eval(ctx, "JSON.stringify(globalThis.__lxStringSliceTrace)", "lx-rconfig-string-slice-trace.js")
	if err != nil { t.Fatal(err) }
	t.Logf("source string slice trace: %s", stringSliceTraceJSON)
	typedArrayMapTraceJSON, err := rt.Eval(ctx, "JSON.stringify(globalThis.__lxTypedArrayMapTrace)", "lx-rconfig-typed-array-map-trace.js")
	if err != nil { t.Fatal(err) }
	t.Logf("source typed-array map trace: %s", typedArrayMapTraceJSON)
	transformTraceJSON, err := rt.Eval(ctx, "JSON.stringify(globalThis.__lxTransformTrace)", "lx-rconfig-transform-trace.js")
	if err != nil { t.Fatal(err) }
	byteMapTraceJSON, err := rt.Eval(ctx, "JSON.stringify(globalThis.__lxByteMapTrace)", "lx-rconfig-byte-map-trace.js")
	if err != nil { t.Fatal(err) }
	t.Logf("source 16-byte map trace: %s", byteMapTraceJSON)
	bytePushTraceJSON, err := rt.Eval(ctx, "JSON.stringify(globalThis.__lxBytePushTrace)", "lx-rconfig-byte-push-trace.js")
	if err != nil { t.Fatal(err) }
	t.Logf("source 16-byte push trace: %s", bytePushTraceJSON)
	t.Logf("source signature string transform trace: %s", transformTraceJSON)
	responseTraceJSON, err := rt.Eval(ctx, "JSON.stringify(globalThis.__lxResponseTrace)", "lx-rconfig-response-trace.js")
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("source /rconfig response property trace: %s", responseTraceJSON)
	if !matched {
		t.Fatalf("rconfig si=%s was not produced by a traced MD5 call; signature derivation is not verified", si)
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
	var liveRequestURL string
	var responseBody []byte
	transport := roundTripFunc(func(request *http.Request) (*http.Response, error) {
		response, err := http.DefaultTransport.RoundTrip(request)
		if err != nil { return nil, err }
		if request.URL.Path == "/rconfig" {
			liveRequestURL = request.URL.String()
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
	liveTraceShim := `
globalThis.__lxLiveTrace = [];
globalThis.__lxLiveMD5Trace = [];
globalThis.__lxLiveEntropyTrace = [];
globalThis.__lxLiveArithmeticTrace = [];
globalThis.__lxLiveBytePushTrace = [];
globalThis.__lxLiveTypedArrayTrace = [];
globalThis.__lxLiveDecodeTrace = [];
const __lxLiveU8 = Uint8Array.prototype;
for (const method of ['set', 'fill', 'reverse', 'sort']) {
  const original = __lxLiveU8[method];
  if (typeof original !== 'function') continue;
  __lxLiveU8[method] = function(...args) {
    const before = this.length >= 8 && this.length <= 64 ? Array.from(this) : null;
    const result = original.apply(this, args);
    if (before && globalThis.__lxLiveTypedArrayTrace.length < 500) {
      globalThis.__lxLiveTypedArrayTrace.push({
        method, before, after: Array.from(this),
        args: args.map(value => value && typeof value.length === 'number'
          ? Array.from(value).slice(0, 32) : value),
        stack: new Error().stack
      });
    }
    return result;
  };
}
globalThis.__lxLiveStringTrace = [];
const __lxLiveArrayPush = Array.prototype.push;
const __lxLivePushIDs = new WeakMap();
let __lxLiveNextPushID = 1;
Array.prototype.push = function(...values) {
  const before = this && typeof this.length === 'number' ? this.length : -1;
  let id = 0;
  if (Array.isArray(this) && before >= 0 && before < 16 && values.length &&
      values.every(value => typeof value === 'number' && Number.isFinite(value))) {
    id = __lxLivePushIDs.get(this);
    if (!id) { id = __lxLiveNextPushID++; __lxLivePushIDs.set(this, id); }
  }
  const result = __lxLiveArrayPush.apply(this, values);
  if (id && globalThis.__lxLiveBytePushTrace.length < 1000 && this.length <= 16) {
    globalThis.__lxLiveBytePushTrace.push({id, before, values: values.slice(0, 16),
      result: this.length === 16 ? Array.from(this) : undefined,
      stack: before === 0 || this.length === 16 ? new Error().stack : undefined});
  }
  return result;
};
const __lxLiveFromCharCode = String.fromCharCode;
String.fromCharCode = function(...codes) {
  const value = __lxLiveFromCharCode.apply(String, codes);
  if (value.length >= 8 && globalThis.__lxLiveStringTrace.length < 300) {
    globalThis.__lxLiveStringTrace.push({codes: codes.slice(0, 64), output: value.slice(0, 128), stack: new Error().stack});
  }
  return value;
};
if (typeof globalThis.atob === 'function') {
  const __lxLiveAtob = globalThis.atob;
  globalThis.atob = function(value) {
    const output = __lxLiveAtob.call(globalThis, value);
    if (globalThis.__lxLiveDecodeTrace.length < 40) globalThis.__lxLiveDecodeTrace.push({inputLength: String(value).length, outputLength: output.length, outputHead: output.slice(0, 48), stack: new Error().stack});
    return output;
  };
}
const __lxLiveCrypto = globalThis.lx.utils.crypto;
for (const name of ['aesEncrypt', 'rsaEncrypt']) {
  const original = __lxLiveCrypto[name].bind(__lxLiveCrypto);
  __lxLiveCrypto[name] = function(...args) {
    const output = original(...args);
    if (globalThis.__lxLiveEntropyTrace.length < 200) globalThis.__lxLiveEntropyTrace.push({
      kind: name,
      args: args.map(value => typeof value === 'string' ? value.slice(0, 256) :
        (value && typeof value.length === 'number' ? {length: value.length, head: Array.from(value).slice(0, 32)} : String(value))),
      output: output && typeof output.length === 'number' ? Array.from(output).slice(0, 64) : String(output).slice(0, 256),
      stack: new Error().stack
    });
    return output;
  };
}
const __lxLiveImul = Math.imul;
if (typeof __lxLiveImul === 'function') Math.imul = function(a, b) {
  const value = __lxLiveImul.call(Math, a, b);
  if (globalThis.__lxLiveArithmeticTrace.length < 2000) globalThis.__lxLiveArithmeticTrace.push({kind: 'Math.imul', args: [a, b], output: value, stack: new Error().stack});
  return value;
};
const __lxLiveNumberToString = Number.prototype.toString;
Number.prototype.toString = function(radix) {
  const value = __lxLiveNumberToString.call(this, radix);
  if (radix === 16 && globalThis.__lxLiveArithmeticTrace.length < 2000) globalThis.__lxLiveArithmeticTrace.push({kind: 'hex', input: Number(this), output: value, stack: new Error().stack});
  return value;
};
const __lxLiveCryptoForSignature = globalThis.lx.utils.crypto;
const __lxLiveRandomBytes = __lxLiveCryptoForSignature.randomBytes.bind(__lxLiveCryptoForSignature);
__lxLiveCryptoForSignature.randomBytes = function(...args) {
  const value = __lxLiveRandomBytes(...args);
  globalThis.__lxLiveEntropyTrace.push({kind: 'randomBytes', args, output: Array.from(value || []).map(byte => Number(byte).toString(16).padStart(2, '0')).join('')});
  return value;
};
const __lxLiveMathRandom = Math.random;
Math.random = function() {
  const value = __lxLiveMathRandom.call(Math);
  globalThis.__lxLiveEntropyTrace.push({kind: 'Math.random', output: String(value)});
  return value;
};
if (globalThis.crypto && typeof globalThis.crypto.getRandomValues === 'function') {
  const __lxLiveGetRandomValues = globalThis.crypto.getRandomValues.bind(globalThis.crypto);
  globalThis.crypto.getRandomValues = function(array) {
    const value = __lxLiveGetRandomValues(array);
    globalThis.__lxLiveEntropyTrace.push({kind: 'crypto.getRandomValues', output: Array.from(value || []).map(byte => Number(byte).toString(16).padStart(2, '0')).join('')});
    return value;
  };
}
const __lxLiveMD5 = __lxLiveCryptoForSignature.md5.bind(__lxLiveCryptoForSignature);
__lxLiveCryptoForSignature.md5 = function(value) {
  const output = __lxLiveMD5(value);
  globalThis.__lxLiveMD5Trace.push({input: String(value), output});
  return output;
};
const __lxLiveSend = globalThis.lx.send.bind(globalThis.lx);
globalThis.lx.send = function(...args) {
  globalThis.__lxLiveTrace.push({kind: 'send', event: args[0], data: args[1]});
  return __lxLiveSend(...args).then(value => {
    globalThis.__lxLiveTrace.push({kind: 'send-resolved', event: args[0]});
    return value;
  }, error => {
    globalThis.__lxLiveTrace.push({kind: 'send-rejected', event: args[0], error: String(error && error.stack || error)});
    throw error;
  });
};
const __lxLiveComplete = globalThis.__lxCompleteRequest;
globalThis.__lxCompleteRequest = function(id, error, response) {
  globalThis.__lxLiveTrace.push({kind: 'http-complete', id, error: error && String(error), statusCode: response && response.statusCode, body: response && response.body});
  return __lxLiveComplete(id, error, response);
};
const __lxConsole = globalThis.console;
for (const method of ['log', 'info', 'warn', 'error']) {
  const original = __lxConsole[method];
  __lxConsole[method] = function(...args) {
    globalThis.__lxLiveTrace.push({kind: 'console.' + method, args: args.map(value => String(value))});
    return original.apply(this, args);
  };
}
`
	rt := newTestRuntime(t, 10*time.Second)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	err = rt.InitializeLXSource(ctx, ScriptInfo{
		Name: "独家音源", Version: "7", Author: "w",
		Description: "后续更新，请关注微信公众号: 洛雪科技",
		RawScript: string(source),
	}, liveTraceShim+string(source), client)
	traceCtx, traceCancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer traceCancel()
	trace, traceErr := rt.Eval(traceCtx, `JSON.stringify({state: globalThis.__lxHostState, trace: globalThis.__lxLiveTrace, md5Trace: globalThis.__lxLiveMD5Trace, entropyTrace: globalThis.__lxLiveEntropyTrace, arithmeticTrace: globalThis.__lxLiveArithmeticTrace, bytePushTrace: globalThis.__lxLiveBytePushTrace, typedArrayTrace: globalThis.__lxLiveTypedArrayTrace, decodeTrace: globalThis.__lxLiveDecodeTrace, stringTrace: globalThis.__lxLiveStringTrace, timerErrors: globalThis.__lxTimerErrors, missingCapabilities: globalThis.__lxMissingCapabilities})`, "lx-live-handshake-trace.js")
	if traceErr != nil { t.Logf("live handshake diagnostic trace unavailable: %v", traceErr) } else { t.Logf("live handshake diagnostic trace: %s", trace) }
	t.Logf("live /rconfig HTTP status: %d", status)
	t.Logf("live /rconfig response body: %s", string(responseBody))
	if err != nil {
		t.Fatalf("live LX /rconfig handshake failed (HTTP %d): %v", status, err)
	}
	if status < 200 || status >= 300 {
		t.Fatalf("live LX /rconfig returned HTTP %d", status)
	}
	parsedLiveURL, err := url.Parse(liveRequestURL)
	if err != nil { t.Fatalf("parse live /rconfig request URL %q: %v", liveRequestURL, err) }
	query := parsedLiveURL.Query()
	for key, size := range map[string]int{"e": 32, "to": 22, "fp": 32, "s2": 32, "si": 32} {
		value := query.Get(key)
		decoded, decodeErr := hex.DecodeString(value)
		if decodeErr != nil || len(value) != size || (key == "to" && len(decoded) != 11) {
			t.Fatalf("live /rconfig signature parameter %s=%q is malformed", key, value)
		}
	}
	if query.Get("fp") != "42bc92aaf1a30247e40eab9523ca0713" || query.Get("v") != "7" {
		t.Fatalf("live /rconfig fingerprint/version mismatch: %s", parsedLiveURL.Redacted())
	}
	var response struct {
		Code int `json:"code"`
		Msg string `json:"msg"`
		Data struct {
			Enabled bool `json:"enabled"`
			Paused bool `json:"paused"`
			Version int `json:"version"`
			MinVersion int `json:"minVersion"`
			UpdateURL string `json:"updateUrl"`
			PausedText string `json:"pausedText"`
		} `json:"data"`
	}
	if err := json.Unmarshal(responseBody, &response); err != nil {
		t.Fatalf("decode live /rconfig success response %q: %v", responseBody, err)
	}
	if response.Code != 0 || response.Msg != "success" || !response.Data.Enabled || response.Data.Paused || response.Data.Version < 7 || response.Data.MinVersion < 7 || response.Data.Version < response.Data.MinVersion {
		t.Fatalf("live /rconfig did not return an enabled supported-version success payload: %+v", response)
	}
	// A newer server-side script version is an update advisory, not a protocol
	// failure: V7 remains usable while the service advertises V8.
	if response.Data.Version > 7 {
		if response.Data.UpdateURL == "" {
			t.Fatalf("server advertised newer source version %d without an update URL", response.Data.Version)
		}
		t.Logf("non-blocking source update advisory: running V7, server version=%d minVersion=%d updateUrl=%s pausedText=%q",
			response.Data.Version, response.Data.MinVersion, response.Data.UpdateURL, response.Data.PausedText)
	}
	var handshake struct {
		State struct { Initialized bool `json:"initialized"` } `json:"state"`
		Trace []struct { Kind string `json:"kind"`; Event string `json:"event"` } `json:"trace"`
		MD5Trace []struct { Input string `json:"input"`; Output string `json:"output"` } `json:"md5Trace"`
		TimerErrors []string `json:"timerErrors"`
		MissingCapabilities []string `json:"missingCapabilities"`
	}
	if err := json.Unmarshal([]byte(trace), &handshake); err != nil {
		t.Fatalf("decode live LX initialization trace: %v", err)
	}
	if !handshake.State.Initialized {
		t.Fatalf("live /rconfig returned success but LX did not initialize: %s", trace)
	}
	si := query.Get("si")
	matchedSI := false
	for _, call := range handshake.MD5Trace {
		if call.Output == si {
			preimage, decodeErr := hex.DecodeString(call.Input)
			if decodeErr != nil || len(preimage) != 32 {
				t.Fatalf("live /rconfig si preimage is not a 64-character hex digest: %q", call.Input)
			}
			matchedSI = true
			break
		}
	}
	if !matchedSI {
		t.Fatalf("live /rconfig si=%s was not produced by a traced MD5 call", si)
	}
	initedResolved := false
	for _, event := range handshake.Trace {
		if event.Kind == "send-resolved" && event.Event == "inited" { initedResolved = true; break }
	}
	if !initedResolved {
		t.Fatalf("live LX did not resolve its inited event: %s", trace)
	}
	if len(handshake.TimerErrors) != 0 || len(handshake.MissingCapabilities) != 0 {
		t.Fatalf("live LX initialization reported timer errors or missing capabilities: %s", trace)
	}
}
