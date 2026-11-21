# N5: deterministic LX source protocol matrix

Every protocol integration test executes the checked-in JavaScript fixture in QuickJS.
Outbound HTTP is intercepted by a deterministic Go `http.RoundTripper`; tests must
never contact upstream services.

## Fixture matrix

| Fixture | Initialization | Source-level protocol paths covered | Remaining gap / boundary |
|---|---|---|---|
| `lx.js` | Real LX Host issues its server-managed `/rconfig` request, but initialization is deliberately expected to remain incomplete when the mock returns an invalid empty object. | `TestLXSourceRequestsServerConfigurationAgainstRealLXHost` asserts GET, HTTPS, `/rconfig`, version parameter, exact query-key/header sets, empty request body, the pinned V7 fingerprint (`42bc92aaf1a30247e40eab9523ca0713`), and hexadecimal encoding/length of the dynamic `e`, `to`, `s2`, and `si` fields. CI log capture observed one concrete request with all six query values. `TestLXSourceDoesNotInitializeWhenRConfigFails` additionally checks HTTP 403 rejection, transport failure, HTTP 200 malformed JSON, and HTTP 200 empty-object responses; in every case the pre-registered request handler remains present but `initialized` stays false. Separate syntax and shim-based registration tests exist, but do not prove a valid server handshake. | Successful `/rconfig` response parsing and post-handshake actions remain unverified. The response schema must be derived from the pinned source/server contract before adding a success fixture; do not invent a config response. |
| `xinglan.js` | Real LX Host initialization and source registration. | Migu music URL resolution, JSON POST payload, response parsing, 320k URL rewrite/probe; QQ v2→v3 fallback; 13-provider all-fail aggregation; concurrent head-provider success and sequential-tail suppression. Netease and Kugou provider pools are checked for complete 9-provider and 11-provider all-fail aggregation by `TestXinglanNeteaseAndKugouBackendPoolsReportAllFailures`. The Netease official EAPI POST contract validates the encrypted form envelope, headers, full URL extraction, and rejection of trial-only URLs; its later Canxiang GET fallback validates query mapping and `data.url` extraction after all preceding providers fail. The Kugou HaiTang primary POST validates its JSON payload and `data.url` extraction; its later Nianxin GET fallback validates hash/quality mapping and direct URL extraction after preceding providers fail. Migu seven-provider pool all-fail aggregation with deterministic HTTP 503 responses; `TestXinglanMiguXinghaiSequentialFallbackGETContract` also verifies the later Xinghai Migu GET query mapping, quality mapping, and direct URL extraction after preceding providers fail. QS primary HTTP 503 with backup success, request metadata contract, and URL key query encoding; `TestXinglanQsBackendPoolReportsAllFailures` also verifies both providers' complete negative-response aggregation. Kuwo now has primary-success tail suppression and full ten-provider all-fail aggregation (`TestXinglanKuwoPrimarySuccessSkipsSequentialFallbacks`, `TestXinglanKuwoBackendPoolReportsAllFailures`) in addition to individual fallback success contracts. Kuwo locally constructed stream URL routing for the four tiers in `KW_HIGH_QUALITY` (`flac24bit`, `atmos`, `atmos_plus`, `master`) with zero outbound resolver requests (`TestXinglanKuwoHighQualityUsesStreamBackendOnly`); the `flac24bit` high-quality routing gap was fixed by including it in `KW_HIGH_QUALITY`. Standard tiers (`128k`, `192k`, `320k`, `flac`, `hires`) correctly use the ordinary provider pool and are not asserted to be stream-only by this test. The `320k` standard-tier provider-pool route is exercised separately: Xinghai primary failure, HelloWorld backup POST payload/headers, and returned URL are asserted by `TestXinglanKuwoStandardQualityUsesProviderPool`. The next sequential fallback is also covered: after Xinghai and HelloWorld backup fail, the Kuwo official GET contract asserts `rid`, `br`, `source`, `type`, `surl`, mobile User-Agent, and `data.surl` extraction via `TestXinglanKuwoOfficialFallbackGETContract`. The following sequential fallback is covered by `TestXinglanKuwoMobileFallbackGETContract`: Xinghai, HelloWorld backup, and Kuwo official return deterministic HTTP 503 responses; the mobile GET asserts `/mobi.s`, `rid`, `br`, `f`, `user`, `type`, Android User-Agent, and `data.url` extraction. The next sequential fallback is covered by `TestXinglanHYWMusicKuwoGETContract`: after deterministic failures from Xinghai, HelloWorld backup, Kuwo official, Kuwo mobile, and Nianxin (including the `qualityToLevel` mapping to `standard` for 128k), it asserts HYWmusic's GET query mapping (song identifiers, metadata, platform/source, quality, card key), JSON headers, and `data.url` extraction. `TestXinglanKuwoYunmgeXinghaiAndFFAPIFallbackContracts` then verifies the remaining yunmge, Xinghai Kuwo (`api.xinghai.com`), and FFAPI GET contracts, bitrate selection, page URL encoding, response parsing, and that each successful provider stops later fallback calls. The inline `HelloWorld KW 主API` entry is intentionally classified as dormant: its level map only accepts `atmos`/`atmos_plus`/`master`, but `pickBackends` routes those high-quality tiers exclusively to the local stream backend; for ordinary tiers it rejects the quality before issuing HTTP. No supported Kuwo route currently reaches that endpoint. | These are deterministic mocks. Additional platform-specific live service behavior is not established. |
| `helloworld.js` | Real LX Host initialization and source registration. | QQ music URL resolution; pinned source's native Buffer token construction with captured token decoding; five-API FLAC failure followed by 320k quality retry; Kuwo master-quality JSON POST endpoint, headers, exact payload, and `data.url` response parsing (`TestHelloWorldKuwoMasterPOSTContract`); standard 128k GET `convert_url_with_sign` fallback, exact query/header mapping, and direct URL response (`TestHelloWorldKuwoStandardQualityGETContract`). | QQ 320k ordered fallback through the first card-key GET API, the YY tokenized GET API, then the tang `music_open_api.php?mid=` GET API; exact query/header contracts and `song_play_url_hq` response extraction are asserted by `TestHelloWorldQQTangFallbackGETContract`. Other API templates and real upstream behavior are not proven. |
| `huanyin.js` | Real LX Host initialization and source registration. | Local platform URL mapping; Migu search; search-result-to-playback flow; metadata mismatch retry/cache; album→singer→title negative-response fallback; transport-error fallback; three sequential source-level timeout/cancellation attempts. | QQ/Kuwo/Netease coverage currently checks local URL construction, not remote playback. Migu HTTP behavior is deterministic mock-only. |


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

## Cross-cutting Host/runtime coverage

- Promise microtask draining, timer lifecycle/order, in-flight HTTP cancellation, and interruption of an infinite loop.
- Node Buffer identity and byte/encoding behavior, including latin1/binary/ascii and base64/base64url cases.
- Tests that use the lightweight JavaScript shim are explicitly shim tests; they do not substitute for real LX Host integration tests.

## Completion boundary

N5 does not mean all four fixtures have equal coverage. In particular, the pinned
`lx.js` fixture's valid server-configuration handshake remains an explicit blocker.
The real-host request-shape test is not an initialization-success test.

A passing test proves only the deterministic source/Host contract asserted by that test.
It does not prove live endpoint availability, account entitlements, VIP bypass, or
guaranteed playback. No test in this matrix should access live third-party services.
