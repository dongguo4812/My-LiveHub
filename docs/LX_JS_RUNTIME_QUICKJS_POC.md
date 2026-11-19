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

### P1 — Go/CGo embedding proof

- Add a narrow C wrapper around the QuickJS C API; do not adopt an unverified third-party Go binding.
- Create and destroy one runtime/context on a dedicated locked OS thread.
- Evaluate source, retrieve values/errors, and close idempotently.
- Verify Promise job draining and callbacks across the Go/C boundary.
- Verify memory, stack, and interrupt limits.
- Measure the added executable size and baseline/idle/peak memory.
- Keep this package opt-in until Windows CI and local Windows tests pass.

### P2 — LX host API compatibility

Implement only APIs supported by source inspection and executable fixtures:
- `lx.currentScriptInfo`, `lx.version`, `lx.env`
- `lx.request`, abort/timeout behavior, and `lx.on`/`lx.send`
- `lx.utils.buffer`, `lx.utils.crypto`, and `lx.utils.zlib`
- Promise/microtask scheduling and required browser globals.

HTTP must be performed by Go. The JavaScript context must not receive unrestricted filesystem, process, module-loader, or native-library access.

### P3 — Real source compatibility gate

Use pinned copies of these three representative scripts as fixtures, with source URL, retrieval date, and SHA-256 recorded:
1. `lx/latest.js`
2. `xinglan/latest.js`
3. `helloworld/latest.js`

For each fixture, test import/metadata parsing, initialization, source registration, search, music URL resolution, and error/timeout behavior where the script exposes those actions. A script that only parses or initializes is not counted as playback-compatible.

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
