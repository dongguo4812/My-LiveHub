# QuickJS-NG LX Runtime PoC

## Goal

Validate whether My-LiveHub can embed QuickJS-NG in its existing Go/Wails Windows x64 application and host real 洛雪 music source scripts. This PoC is isolated from the production music service. It does not replace the current playback path and does not change master/dev.

## Current branch

- Branch: `feat/lx-js-runtime-quickjs-poc`
- Base: `dev`
- Upstream engine candidate: QuickJS-NG `v0.18.0`
- Engine source: https://github.com/quickjs-ng/quickjs
- Initial validation target: Windows x64, UCRT64 MinGW-w64 toolchain.

## Explicit non-goals

- No Electron, Node.js, V8, MPV, or full Chromium runtime.
- No platform-native music API rewrite as a substitute for running imported LX scripts.
- No integration into the production music service during the PoC.
- No claim of general LX compatibility until real scripts pass.

## Phases and gates

### P0 — Windows engine/toolchain proof

- Build upstream QuickJS-NG v0.18.0 from source on windows-latest.
- Run a JavaScript smoke test with the built engine.
- Record the compiler, build result, and generated binary sizes in CI.
- Do not proceed if the upstream source cannot be built reproducibly on the project's Windows CI runner.

### P1 — Go/CGo embedding proof (implementation added; CI evidence pending)

- Added a narrow C wrapper around the QuickJS C API; no third-party Go binding is used.
- Added a Go runtime host behind the `lxquickjs` build tag.
- Runtime creation, evaluation, pending Promise jobs, interruption, and destruction are confined to one locked OS thread.
- Memory and stack ceilings are configured at runtime creation; evaluation is interrupted on timeout or context cancellation.
- Added tests for JS evaluation, Promise jobs, infinite-loop interruption, and source syntax.
- Windows CI builds the static QuickJS library and runs the Go CGo tests. A passing CI result is still required before treating this phase as validated.
- The runtime remains opt-in and is not wired into the production music service.

### P2 — LX host API compatibility

Implement only APIs supported by source inspection and executable fixtures:
- `lx.currentScriptInfo`, `lx.version`, `lx.env`
- `lx.request`, abort/timeout behavior, and `lx.on`/`lx.send`
- `lx.utils.buffer`, `lx.utils.crypto`, and `lx.utils.zlib`
- Promise/microtask scheduling and required browser globals.

HTTP must be performed by Go. The JavaScript context must not receive unrestricted filesystem, process, module-loader, or native-library access.

### P3 — Real source compatibility gate (test fixtures added; execution evidence pending)

Pinned snapshots of these scripts are stored under `app/internal/music/lxruntime/testdata/sources`:
1. `lx/latest.js`
2. `xinglan/latest.js`
3. `helloworld/latest.js`

The fixture manifest records the upstream blob SHA and retrieval date. Current tests cover syntax compilation, host-shim initialization, the `inited` event, request-handler registration, and one deterministic HelloWorld `kw/320k` URL-resolution path using a mocked HTTP response. These are deliberately narrow compatibility probes, not full API parity. Additional actions, real network behavior, and live playback still require separate tests.

### P4 — Production integration decision

Only after P0–P3 pass:
- Propose a separate integration change for the music service.
- Keep the runtime lifecycle independent from the UI and playback engine.
- Add import validation, per-source isolation, resource quotas, request cancellation, and regression tests.
- Review licensing, dependency footprint, release packaging, and security before enabling by default.

## Security requirements

Imported scripts are untrusted code. Enforce memory and stack ceilings, execution interruption, bounded timers, bounded request concurrency, HTTP timeouts/cancellation, response-size limits, and deterministic runtime shutdown. Do not expose Node.js APIs or direct filesystem/process access.

## Acceptance criteria

The PoC is successful only when all of the following are evidenced:
- Windows x64 CI builds the pinned engine reproducibly.
- Go can create/use/close the engine without cross-thread access or leaks in repeated lifecycle tests.
- Promise jobs and asynchronous host callbacks behave deterministically.
- Resource limits stop an intentionally non-terminating script.
- The three pinned LX fixtures pass the defined compatibility cases.
- Final EXE size and memory impact are measured and acceptable.

A CI pass for P0 alone is not evidence that LX scripts are compatible. P0/P1/P2/P3 results must be reported separately.


## Verified LX desktop host contract (source audit, 2026-11-19)

The contract below was checked against the upstream desktop implementation at
`lyswhut/lx-music-desktop`, specifically
`src/main/modules/userApi/renderer/preload.js`,
`src/main/modules/userApi/rendererEvent/rendererEvent.ts`,
`src/main/modules/userApi/main.ts`, and
`src/common/types/user_api.d.ts`. This is the desktop contract, not the more
restricted mobile host API.

### Required public surface

- `lx.EVENT_NAMES`: `request`, `inited`, `updateAlert`.
- `lx.currentScriptInfo`: `name`, `description`, `version`, `author`,
  `homepage`, `rawScript`.
- `lx.version === '2.0.0'`; `lx.env === 'desktop'`.
- `lx.on('request', handler)`: registers the request handler. The handler must
  return a Promise. Unsupported event names reject.
- `lx.send('inited', data)`: one-time initialization; supported source keys
  are `kw`, `kg`, `tx`, `wy`, `mg`, `local`; each source is filtered
  against host-supported actions and qualities.
- `lx.send('updateAlert', data)`: one-time update notification with bounded
  log and optional URL.
- `lx.request(url, options, callback)`: callback signature is
  `(err, resp, body)`; options include `method`, `timeout`, `headers`,
  `body`, `form`, and `formData`. The returned function aborts the request.
  The response includes `statusCode`, `statusMessage`, `headers`, `bytes`,
  `raw`, and `body`. The host converts raw response bytes to a string, then
  attempts JSON parsing; `resp.body` and callback argument three contain the
  parsed value when parsing succeeds, otherwise the string.
- `lx.utils.crypto`: `aesEncrypt(buffer, mode, key, iv)`,
  `rsaEncrypt(buffer, key)`, `randomBytes(size)`, `md5(str)`.
  AES uses Node-compatible cipher names and cipher finalization. RSA uses
  `RSA_NO_PADDING` after left-padding the input to 128 bytes. MD5 returns
  lowercase hexadecimal.
- `lx.utils.buffer.from(...args)` follows Node `Buffer.from` semantics;
  `bufToString(buf, format)` follows Node Buffer string encodings.
- `lx.utils.zlib.inflate(buf)` and `deflate(data)` return Promises and perform
  actual zlib operations.

### Compatibility gaps confirmed in the current PoC

| Contract area | Current PoC status | Required correction |
|---|---|---|
| Persistent JS context | Basic persistent context exists | Keep all script and host calls on the runtime OS thread |
| `lx.on/send` | Only a test-only JS mock | Implement real event registration, validation, and host response routing |
| `lx.request` | Fixed response mock; callback shape is incomplete | Go HTTP transport, full response object, JSON/body behavior, timeout and abort |
| AES/MD5/RSA/random | Fixed placeholder outputs | Standard-library or audited crypto implementation with known-answer tests |
| Buffer | Partial Uint8Array helper | Node-compatible input and encoding semantics required by pinned scripts |
| Zlib | Identity transform | Real zlib inflate/deflate and round-trip/known-vector tests |
| Timers | IDs only; callbacks never run | Bounded timer queue integrated with the runtime event loop |
| Promise jobs | Immediate microtask drain only | Pump jobs after each host callback and while awaiting async operations |
| URL / URLSearchParams | Incomplete mock | Implement the subset used by pinned sources and test parsing/relative URL behavior |
| Source result validation | Not enforced | Match host validation: musicUrl must be an HTTP(S) string <= 2048 chars |

### Important runtime design constraint

The upstream desktop preload runs source scripts in a persistent Chromium renderer,
but its documented custom-source bridge is narrow. It does not require embedding
Node.js or Chromium wholesale. My-LiveHub can implement the documented `lx`
surface over QuickJS, with Go owning HTTP, crypto, zlib, timers, and request
lifecycle. However, QuickJS Promise jobs alone are insufficient: native host
callbacks must re-enter the JS context on its locked OS thread, and async
operations must keep their Promise resolvers alive until completion or
cancellation.

### Revised acceptance gates

1. P0/P1: engine build, CGo embedding, lifecycle and resource limits (CI passed).
2. P2a: exact host object shape, event validation, initialization metadata and
   source/action/quality filtering.
3. P2b: HTTP callback contract, response parsing, abort and timeout behavior.
4. P2c: crypto, Buffer and zlib known-answer tests.
5. P2d: timer scheduling, Promise job pumping and async host callback ordering.
6. P3: run pinned source initialization, search and musicUrl actions against
   deterministic HTTP fixtures; assert actual request URL/method/headers/body
   and returned URL. Live network checks are separate and cannot substitute for
   deterministic runtime tests.

A successful syntax compile or mocked URL return does not satisfy P2 or P3.
