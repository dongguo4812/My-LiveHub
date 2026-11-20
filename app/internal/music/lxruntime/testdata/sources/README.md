# Pinned LX source compatibility fixtures

These snapshots are test inputs, not trusted code. They must only be evaluated by the QuickJS test host with memory and execution limits and with network access replaced by a deterministic mock.

| Fixture | Upstream | Ref | Upstream blob SHA | Retrieved | Size |
|---|---|---|---|---|---:|
| `lx.js` | `pdone/lx-music-source/lx/latest.js` | `main` | `ce12f2160fc479998aec09d3e890efa1c2e9db08` | 2026-11-19 | 58,760 chars |
| `xinglan.js` | `pdone/lx-music-source/xinglan/latest.js` | `main` | `f12581f4d2b05b6585480fd3436595836f91040a` | 2026-11-19 | 71,469 chars |
| `helloworld.js` | `pdone/lx-music-source/helloworld/latest.js` | `main` | `9072550b4b65d8a5135201a86c404e7873719202` | 2026-11-19 | 21,148 chars |
| `huanyin.js` | Local pinned compatibility fixture | project snapshot | `e4970d8cdb6445cf307308e37b42a06d8373e332` | 2026-11-20 | 7,316 bytes |

## Existing test coverage

- `lx.js`: syntax compilation; its actual `/rconfig` HTTPS request is captured and checked against the real QuickJS LX host. The test intentionally returns an invalid empty config and expects initialization not to complete. Successful config parsing and post-init actions are not yet proven.
- `xinglan.js`: real LX host initialization and deterministic Migu music URL resolution, including the JSON POST contract and 320k URL rewrite/probe.
- `helloworld.js`: real LX host initialization and deterministic music URL resolution. The token-bearing API path executes the pinned source's real `lx.utils.buffer.from` / `bufToString` calls; the captured token is decoded and checked.
- `huanyin.js`: real LX host initialization, local platform URL mapping, deterministic Migu search and playback, search-result-to-playback flow, metadata mismatch retry, and cache behavior.
- Engine: Promise microtask drain, timer lifecycle, HTTP cancellation, and interruption of an infinite loop.

The four fixtures do not yet have equal protocol coverage. See [SOURCE_PROTOCOL_MATRIX.md](../../SOURCE_PROTOCOL_MATRIX.md) for the N5 coverage inventory and explicit completion criteria.

Passing these tests proves only the deterministic cases listed above. It does not prove full API parity, live network behavior, live service availability, entitlement bypass, or guaranteed playback. Live service responses and playback must be tested separately.
