# Pinned LX source compatibility fixtures

These snapshots are test inputs, not trusted code. They are evaluated only by the QuickJS test host with memory and execution limits and with outbound network replaced by deterministic mocks.

| Fixture | Upstream | Ref | Upstream blob SHA | Retrieved | Size |
|---|---|---|---|---|---:|
| `lx.js` | `pdone/lx-music-source/lx/latest.js` | `main` | `ce12f2160fc479998aec09d3e890efa1c2e9db08` | 2026-11-19 | 58,760 chars |
| `xinglan.js` | `pdone/lx-music-source/xinglan/latest.js` | `main` | `f12581f4d2b05b6585480fd3436595836f91040a` | 2026-11-19 | 71,469 chars |
| `helloworld.js` | `pdone/lx-music-source/helloworld/latest.js` | `main` | `9072550b4b65d8a5135201a86c404e7873719202` | 2026-11-19 | 21,148 chars |
| `huanyin.js` | Local pinned compatibility fixture | project snapshot | `e4970d8cdb6445cf307308e37b42a06d8373e332` | 2026-11-20 | 7,316 bytes |

## Current coverage summary

- `lx.js`: syntax compilation and real LX Host `/rconfig` request-shape verification. The request test deliberately returns an invalid empty configuration and expects initialization not to complete. Successful configuration parsing and post-handshake actions are not yet proven.
- `xinglan.js`: real LX Host initialization; deterministic Migu music URL resolution; JSON POST contract and 320k URL rewrite/probe; QQ v2→v3 fallback; QQ provider-pool all-fail aggregation; concurrent head-provider behavior.
- `helloworld.js`: real LX Host initialization and deterministic QQ music URL resolution; native LX Buffer token construction and captured-token verification; FLAC-to-320k quality fallback.
- `huanyin.js`: real LX Host initialization; local platform URL mapping; deterministic Migu search/playback and search-result flow; metadata mismatch/cache behavior; negative responses; transport failures; timeout and request cancellation across three matching fallbacks.
- Runtime/Host: Promise jobs, timers, HTTP cancellation, infinite-loop interruption, Buffer methods and encoding compatibility.

See [SOURCE_PROTOCOL_MATRIX.md](../../SOURCE_PROTOCOL_MATRIX.md) for test-level coverage and explicit limitations.

Passing these tests proves only the deterministic cases listed above. It does not prove full API parity, live network behavior, live service availability, account entitlement, VIP bypass, or guaranteed playback. The `lx.js` valid server-configuration handshake remains unverified and must not be represented as complete.
