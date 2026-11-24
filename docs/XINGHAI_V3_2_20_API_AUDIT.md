# XingHai v3.2.20 LX Host API Audit

> Target branch: `feat/lx-js-runtime-quickjs-poc`  
> Exact checked-in file: `lx-js/xinghai-music-sourcev2.3.20.js`  
> File SHA (Git blob): `661e26fd3088b9e1eaf503e75a2875087be92832`  
> Script header: `@version v3.2.20` (the filename's `v2.3.20` label is stale)  
> Reference host: `lyswhut/lx-music-desktop/src/main/modules/userApi/renderer/preload.js`

## 1. Script API inventory

The source destructures `EVENT_NAMES`, `request`, `on`, `send`, and `env` from `globalThis.lx`.

| API / global | Actual use in XingHai | LX desktop reference | My-LiveHub QuickJS implementation |
|---|---|---|---|
| `lx.EVENT_NAMES` | `request`, `inited`, `updateAlert` event names | Exposed by preload | Implemented |
| `lx.on(name, handler)` | Registers async `request` handler | Stores request handler and returns resolved Promise | Implemented; handler retained in host context |
| `lx.send(name, data)` | Sends `inited` and `updateAlert` | Promise-based event validation and init negotiation | Implemented; validates and filters advertised capabilities |
| `lx.request(url, options, callback)` | GET requests, callbacks, cancellation function, status/body inspection | Needle callback API; returns abort function; response includes status, headers, bytes, raw and body | Implemented as queued host request + callback completion + abort queue |
| `lx.env` | Reads `env.platform` defensively; actual host value is `'desktop'`, so device label is `unknown` | `env: 'desktop'` | Matches reference |
| `lx.currentScriptInfo` | Not directly read | Script metadata object | Implemented |
| `lx.utils.buffer.from` | Used by defensive base64 helper (helper currently has no call sites) | Node `Buffer.from` | Native QuickJS callback installed |
| `lx.utils.buffer.bufToString` | Converts Buffer response bodies to UTF-8 | Buffer conversion | Native QuickJS callback installed |
| `lx.utils.crypto.*` | No call sites in this script | MD5, AES, RSA, random bytes | Native callbacks installed; not required by this script's current path |
| `lx.utils.zlib.*` | No call sites in this script | Promise-based inflate/deflate | Native callbacks installed; not required by this script's current path |
| global `Buffer` | `Buffer.isBuffer`, `Buffer.from` fallbacks | Node Buffer | Native-installed Buffer shim |
| `setTimeout/clearTimeout` | 3.5-second domain-probe watchdog | Node/Electron timer globals | Implemented; dispatched timers are pumped between host I/O completions |
| `Promise`, `Promise.all`, `async/await`, `Map`, `Set`, `Date`, `Math`, `JSON` | Async token/request flow, parallel domain probes and in-memory cache | JavaScript runtime globals | QuickJS runtime |
| `console.log` | Probe diagnostics | Renderer console | No-op console fallback |

The script does not use Node module imports, `fetch`, `XMLHttpRequest`, WebSocket, filesystem, or process APIs. It does not declare a `search` action. Search is performed by My-LiveHub's native provider search service; XingHai handles `musicUrl`, `lyric`, and `pic`.

## 2. Asynchronous request contract

The real script starts token acquisition, four domain probes, IP lookup, and update checking without awaiting all of them in its initialization IIFE. Its music URL handler is async and awaits token acquisition and one or more HTTP requests. Domain fallback may enqueue a second request from the first request's callback.

The host therefore must preserve all of these behaviors:

1. A request callback is invoked asynchronously after Go network I/O completes.
2. The returned cancellation function removes queued work or aborts in-flight work.
3. Multiple requests can be in flight while timers continue to advance.
4. Promise jobs created by request callbacks and timer callbacks are drained before the next dispatch state is sampled.
5. A callback may enqueue another request (primary-to-backup host fallback).
6. HTTP response shape preserves `statusCode`, `statusMessage`, `headers`, `bytes`, `raw`, and parsed-or-text `body`.

Current implementation points: `host_contract.go` (`lx.request`, timers and completion hooks), `http_lifecycle.go` (off-thread cancellable network tasks), `http_request.go` (HTTP policy/response), and `dispatch.go` (Promise/timer/request pump). Existing `event_loop_test.go` covers async HTTP + timer ordering, cancellation, runtime shutdown, and promise jobs. The live XingHai Desktop integration remains the end-to-end gate; unit coverage alone is not a live-provider pass.

## 3. Provider and token behavior

- Script platform mapping: `tx -> qq`, `mg -> migu`, `kw -> kw`, `kg -> kg`, `qs -> qs`.
- Music URL action validates `musicInfo` and requested quality, maps to the platform's advertised quality set, then calls the configured backend.
- KW uses the XingHai backend endpoint; KG sends hash/album/song identifiers; MG uses the `migu` backend mapping.
- TX first tries ChkSz only when a user API key is configured, then falls back to the XingHai backend with `source=qq`.
- Server token is fetched from `/lx/api/token.php?device_id=...`; the script caches it for four minutes, sends it as `X-Token`, and retries once when the backend reports token mismatch/untrusted token. An optional `X-License` is sent only when `LICENSE_CONFIG.code` is populated.
- A backend response saying `该音源仅支持当前已授权的客户端访问` is a separate license/client authorization gate, not proof that token acquisition failed. A missing token must remain a hard error (`获取服务器令牌失败`) and must not be classified as a license gate.

## 4. Desktop end-to-end acceptance already wired

`TestProductionLXXinghaiDaoxiangPlaybackURL` lives in `app/internal/desktop/lx_script_runtime_production_test.go` and is enabled by `MYLIVEHUB_LX_XINGHAI_PLAYBACK_INTEGRATION=1`. It imports the exact checked-in file, asserts source/action negotiation, searches `稻香` by `周杰伦` through TX/KW/KG/MG, resolves playback beginning at `flac24bit`, verifies media bytes with a Range request, and writes one verified URL to the browser-test handoff file. The Desktop validation workflow then runs the real HTMLAudio Chromium smoke test against that handoff URL.

The integration test explicitly distinguishes the TX server-token path from the backend's separate license gate. A TX token-fetch error must fail the test; a correctly fetched token followed by the explicit license gate is reported separately because this checked-in script has an empty license code.

## 5. Remaining evidence required

- Run the Desktop validation workflow on this branch and inspect the real QuickJS, provider integration, and Chromium HTMLAudio results.
- If KW/KG/MG fail, fix the first failing host/API contract before changing provider logic.
- If TX reports token acquisition failure, capture the token endpoint status/body shape and repair the host request/response path or token parsing; do not bypass the server-issued token requirement or fabricate a token.
- Do not report actual playback complete unless the browser smoke test consumes the URL produced by this exact script and successfully plays it.
