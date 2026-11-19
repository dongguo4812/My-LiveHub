# Pinned LX source compatibility fixtures

These snapshots are test inputs, not trusted code. They must only be evaluated by the QuickJS test host with memory and execution limits and with network access replaced by a deterministic mock.

| Fixture | Upstream | Ref | Upstream blob SHA | Retrieved | Size |
|---|---|---|---|---|---:|
| `lx.js` | `pdone/lx-music-source/lx/latest.js` | `main` | `ce12f2160fc479998aec09d3e890efa1c2e9db08` | 2026-11-19 | 58,760 chars |
| `xinglan.js` | `pdone/lx-music-source/xinglan/latest.js` | `main` | `f12581f4d2b05b6585480fd3436595836f91040a` | 2026-11-19 | 71,469 chars |
| `helloworld.js` | `pdone/lx-music-source/helloworld/latest.js` | `main` | `9072550b4b65d8a5135201a86c404e7873719202` | 2026-11-19 | 21,148 chars |

## Test coverage

- All three snapshots: JavaScript syntax compilation.
- All three snapshots: host shim installation, source initialization, `inited` event, and request handler registration.
- HelloWorld: `kw` / `320k` music URL resolution against a deterministic mocked HTTP response.
- Engine: Promise microtask drain and interruption of an infinite loop.

Passing these tests proves only the cases above. It does not prove full API parity, live network behavior, all source actions, or VIP entitlement bypass. Live service responses and playback must be tested separately.
