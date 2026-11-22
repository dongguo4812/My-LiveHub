# N5: deterministic LX source protocol matrix

Every protocol integration test executes the checked-in JavaScript fixture in QuickJS.
Deterministic provider-contract tests intercept outbound HTTP with a Go
`http.RoundTripper`. The separately gated `LX_RCONFIG_LIVE=1` V7 handshake test is
the explicit exception: it contacts the LX configuration service to verify the live
signed `/rconfig` contract; it does not call music-provider playback endpoints.

## Fixture matrix

| Fixture | Initialization | Source-level protocol paths covered | Remaining gap / boundary |
|---|---|---|---|
| `lx.js` | Real LX Host performs the signed V7 `/rconfig` handshake; a deterministic valid advisory payload reporting server version/minVersion 8 allows V7 initialization. | `TestLXSourceAcceptsLiveV7RConfigAdvisory` verifies GET `/rconfig`, `v=7`, and successful source initialization with the observed `code:0`, `enabled:true`, `version:8`, `minVersion:8`, and update URL response shape. `TestLXSourceRequestsServerConfigurationAgainstRealLXHost` plus live handshake diagnostics verify GET, HTTPS, `/rconfig`, exact query-key/header sets, empty request body, pinned V7 fingerprint (`42bc92aaf1a30247e40eab9523ca0713`), and hexadecimal encoding/length of dynamic `e`, `to`, `s2`, and `si` fields. CI log capture records the live HTTP 200 response and V7 accepting the V8 update advisory. `TestLXSourceDoesNotInitializeWhenRConfigFails` checks HTTP 403 rejection, transport failure, malformed JSON, and empty-object responses; initialization remains false in each failure case. | V8 source compatibility is not verified (the pinned fixture remains V7). Post-handshake playback actions and live third-party playback are not established. |
| `xinglan.js` | Real LX Host initialization and source registration. | Migu music URL resolution, JSON POST payload, response parsing, 320k URL rewrite/probe; QQ v2→v3 fallback; 13-provider all-fail aggregation; concurrent head-provider success and sequential-tail suppression. Netease and Kugou provider pools are checked for complete 9-provider and 11-provider all-fail aggregation by `TestXinglanNeteaseAndKugouBackendPoolsReportAllFailures`. The Netease official EAPI POST contract validates the encrypted form envelope, headers, full URL extraction, and rejection of trial-only URLs; its later Canxiang GET fallback validates query mapping and `data.url` extraction after all preceding providers fail. The Kugou HaiTang primary POST validates its JSON payload and `data.url` extraction; its later Nianxin GET fallback validates hash/quality mapping and direct URL extraction after preceding providers fail. Migu seven-provider pool all-fail aggregation with deterministic HTTP 503 responses; `TestXinglanMiguXinghaiSequentialFallbackGETContract` also verifies the later Xinghai Migu GET query mapping, quality mapping, and direct URL extraction after preceding providers fail. QS primary HTTP 503 with backup success, request metadata contract, and URL key query encoding; `TestXinglanQsBackendPoolReportsAllFailures` also verifies both providers' complete negative-response aggregation. Kuwo now has primary-success tail suppression and full ten-provider all-fail aggregation (`TestXinglanKuwoPrimarySuccessSkipsSequentialFallbacks`, `TestXinglanKuwoBackendPoolReportsAllFailures`) in addition to individual fallback success contracts. Kuwo locally constructed stream URL routing for the four tiers in `KW_HIGH_QUALITY` (`flac24bit`, `atmos`, `atmos_plus`, `master`) with zero outbound resolver requests (`TestXinglanKuwoHighQualityUsesStreamBackendOnly`); the `flac24bit` high-quality routing gap was fixed by including it in `KW_HIGH_QUALITY`. Standard tiers (`128k`, `192k`, `320k`, `flac`, `hires`) correctly use the ordinary provider pool and are not asserted to be stream-only by this test. The `320k` standard-tier provider-pool route is exercised separately: Xinghai primary failure, HelloWorld backup POST payload/headers, and returned URL are asserted by `TestXinglanKuwoStandardQualityUsesProviderPool`. The next sequential fallback is also covered: after Xinghai and HelloWorld backup fail, the Kuwo official GET contract asserts `rid`, `br`, `source`, `type`, `surl`, mobile User-Agent, and `data.surl` extraction via `TestXinglanKuwoOfficialFallbackGETContract`. The following sequential fallback is covered by `TestXinglanKuwoMobileFallbackGETContract`: Xinghai, HelloWorld backup, and Kuwo official return deterministic HTTP 503 responses; the mobile GET asserts `/mobi.s`, `rid`, `br`, `f`, `user`, `type`, Android User-Agent, and `data.url` extraction. The next sequential fallback is covered by `TestXinglanHYWMusicKuwoGETContract`: after deterministic failures from Xinghai, HelloWorld backup, Kuwo official, Kuwo mobile, and Nianxin (including the `qualityToLevel` mapping to `standard` for 128k), it asserts HYWmusic's GET query mapping (song identifiers, metadata, platform/source, quality, card key), JSON headers, and `data.url` extraction. `TestXinglanKuwoYunmgeXinghaiAndFFAPIFallbackContracts` then verifies the remaining yunmge, Xinghai Kuwo (`api.xinghai.com`), and FFAPI GET contracts, bitrate selection, page URL encoding, response parsing, and that each successful provider stops later fallback calls. The inline `HelloWorld KW 主API` entry is intentionally classified as dormant: its level map only accepts `atmos`/`atmos_plus`/`master`, but `pickBackends` routes those high-quality tiers exclusively to the local stream backend; for ordinary tiers it rejects the quality before issuing HTTP. No supported Kuwo route currently reaches that endpoint. | These are deterministic mocks. Additional platform-specific live service behavior is not established. |
| `helloworld.js` | Real LX Host initialization and source registration. | All 17 configured backend entries across Kuwo (2), Kugou (3), QQ (5), Netease (5), and Migu (2) are executed with intercepted HTTP by `TestHelloWorldAllConfiguredBackendContracts`; each case asserts endpoint host/path, HTTP method, configured query or JSON body fields, key headers where applicable, and response URL extraction. This includes Kuwo master POST and standard GET, Kugou card-key/YY-token/Haitang, QQ card-key/YY-token/Tang/Haitang/search, Netease wwwweb/Haitang/YY-token/Nianxin/card-key, and Migu YY-token/OIAPI. `TestHelloWorldPlatformQualityFallbackContracts` verifies FLAC→320k quality mapping and fallback success on all five platforms; `TestHelloWorldEveryPlatformBackendPoolAggregatesFailures` verifies full provider-pool exhaustion on all five platforms; `TestHelloWorldSuccessfulPlaybackURLIsCached` verifies a repeated successful request is served from cache. Existing tests additionally cover native Buffer token construction and decoding, QQ FLAC→320k fallback ordering, QQ Tang `song_play_url_hq` extraction, and Kuwo master/standard request contracts. | These are deterministic intercepted-HTTP tests. Live upstream availability, credentials/entitlements, and guaranteed playback are not established. The fallback suite covers FLAC→320k on all platforms; other tier combinations remain governed by the source's `FALLBACK` table and configured per-API quality maps. |
| `huanyin.js` | Real LX Host initialization and source registration. | Native QQ/Kuwo/Netease URL mapping and quality defaults; Migu search request/result mapping; search-result-to-playback flow; all three album→singer→title playback match paths on success; mismatch, missing URL, explicit negative code, HTTP 503, transport failure, and malformed-JSON fallback/rejection; three-page search with negative/malformed page skipping; empty keyword and all-pages-empty rejection; successful playback URL cache. Coverage is in `TestHuanyinSourceInitializesAndDispatchesMusicURL`, `TestHuanyinSearchResultFlowsIntoMusicURLResolution`, `TestHuanyinMiguPlaybackRejectsNegativeResponsesAcrossMatchFallbacks`, `TestHuanyinPlatformURLMappingCoversAllNativePlatforms`, and `huanyin_protocol_test.go`. | All Migu HTTP behavior is deterministic intercepted-HTTP coverage. The QQ/Kuwo/Netease resolver URL is constructed locally; these tests do not prove remote playback or live third-party availability. |


## Xinglan stage acceptance

The Xinglan stage acceptance suite is green on the feature branch at
`2e4f0e38272d62f21073099338c09d49fa27b879` (CI run
[44847125767](https://github.com/dongguo4812/My-LiveHub/actions/runs/44847125767)).
The CI log records all Xinglan tests and subtests passing, including:

- QQ: v2→v3 fallback, 13-provider all-fail aggregation, primary success, and tail suppression.
- Netease: 9-provider all-fail aggregation, official EAPI success/trial rejection, and Canxiang sequential fallback.
- Kuwo: stream-vs-provider routing, ordinary provider fallback chain, all-fail aggregation, primary success, and tail suppression.
- Kugou: 11-provider all-fail aggregation, HaiTang primary POST, and Nianxin sequential fallback.
- Migu: 7-provider all-fail aggregation, HaiTang resolver/quality rewrite, and Xinghai sequential fallback.
- QS: primary-to-backup fallback, key encoding, and all-fail aggregation.

This acceptance is for deterministic QuickJS + real LX Host tests with intercepted
HTTP. It confirms the source/Host request and response contracts represented above;
it does not establish live third-party endpoint availability or guaranteed playback.


## HelloWorld stage acceptance

The HelloWorld stage acceptance suite is green on the feature branch at
`f2e0fce61c59e8603ddfd590c82b045c900fbec2` (CI run
[44849785175](https://github.com/dongguo4812/My-LiveHub/actions/runs/44849785175)).
CI output confirms all 17 configured provider contract subtests passed, along with
FLAC→320k fallback on all five platforms, all-provider failure aggregation on all
five platforms, successful-result caching, QQ quality fallback, native Buffer token
construction, QQ Tang response extraction, and Kuwo POST/GET contracts.

The suite uses deterministic intercepted HTTP with the real LX Host and pinned
HelloWorld source. It validates source request/response behavior, not live third-party
service availability or guaranteed playback.

## Cross-cutting Host/runtime coverage

- Promise microtask draining, timer lifecycle/order and callback-turn caps; in-flight HTTP cancellation on caller-context cancellation and runtime shutdown; active JavaScript evaluation interruption on runtime shutdown; native Crypto/Zlib Promise ordering relative to timers.
- Node Buffer identity and byte/encoding behavior, including latin1/binary/ascii and base64/base64url cases.
- Tests that use the lightweight JavaScript shim are explicitly shim tests; they do not substitute for real LX Host integration tests.

## Completion boundary

N5 does not mean all four fixtures have equal coverage. In particular, the pinned `lx.js` fixture is V7: its signed `/rconfig` handshake and acceptance of the observed V8 update advisory are now tested. V8 source compatibility and post-handshake playback remain outside this acceptance boundary.

A passing test proves only the deterministic source/Host contract asserted by that test.
It does not prove live endpoint availability, account entitlements, VIP bypass, or
guaranteed playback. No deterministic provider-contract test in this matrix should access live third-party services; the explicitly gated LX configuration handshake is the sole live-network exception.


## Huanyin stage acceptance

Huanyin protocol acceptance is recorded at the feature-branch commit after the CI run succeeds. The dedicated protocol suite checks each Migu match route, HTTP/transport/JSON negative-response handling, three-page search behavior, empty-result rejection, and successful-result caching. Existing integration tests cover the real LX Host registration, all native platform URL mappings, search-to-playback, and metadata mismatch / missing-URL / negative-code fallback sequence.

Provider HTTP is intercepted by deterministic test transports. The separate live V7 `/rconfig` handshake contacts only the LX configuration service; it does not establish music-provider availability or guaranteed playback.


## N3: event-loop and runtime lifecycle acceptance

N3 lifecycle acceptance is green on feature-branch commit
`8abecba7174f95caab683363d7733a72b55aa85d` (CI run
[44990973767](https://github.com/dongguo4812/My-LiveHub/actions/runs/44990973767)).
The Windows x64 QuickJS-NG CGo job completed successfully, and its log confirms:

- Native Crypto remains synchronous; Zlib Promise jobs are drained before the
  dispatch action is observed complete, while due timers preserve the asserted order.
- Async HTTP callback and its queued Promise microtask complete in the expected
  order relative to timer callbacks and the source action continuation.
- Caller-context cancellation aborts an in-flight HTTP request.
- Runtime shutdown cancels an in-flight dispatch and its HTTP request.
- Runtime shutdown interrupts an active non-terminating JavaScript evaluation
  rather than waiting for the configured evaluation timeout.
- Timer clearing, interval rescheduling, and the per-pump 1,000-callback cap pass.

This verifies the tested lifecycle and scheduling contracts on the Windows x64
QuickJS-NG CGo target. It is not a general proof of race freedom on every platform;
other operating systems and architectures still require their own native CI runs.
