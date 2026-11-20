# N5: deterministic LX source protocol matrix

This matrix is an integration-test inventory for the four pinned JavaScript fixtures.
Every test must execute the checked-in source file in QuickJS. Network traffic must
be intercepted by a deterministic Go `http.RoundTripper`; tests must never contact
the upstream service.

## Fixture matrix

| Fixture | Initialization protocol | Action protocol exercised | Existing deterministic coverage | Current boundary |
|---|---|---|---|---|
| `lx.js` | Server-managed `/rconfig` request before `inited` | Configuration request method, HTTPS URL, version parameter, headers/body shape | `TestLXSourceRequestsServerConfigurationAgainstRealLXHost` | The current fixture test intentionally returns an invalid empty config and asserts the exact request; successful config parsing and post-init action dispatch are not yet proven. |
| `xinglan.js` | Local initialization and source registration | Migu music URL resolution, JSON POST payload, response URL parsing, 320k URL rewrite/probe | `TestPinnedSourcesInitializeAgainstRealLXHost`, `TestXinglanRealSourceResolvesMiguURLThroughNativeLXRequest` | One deterministic Migu route is covered; other platform backends and failure/fallback combinations remain open. |
| `helloworld.js` | Local initialization and source registration | QQ music URL fallback, native Buffer token construction, HTTP response URL parsing | `TestPinnedSourcesInitializeAgainstRealLXHost`, `TestHelloWorldSourceBuildsRequestTokenThroughNativeLXBuffer` | The token-bearing Migu route is exercised; remaining API templates, quality fallback ordering, and Buffer edge cases need additional source-level cases. |
| `huanyin.js` | Local initialization and source registration | Migu search, search-result-to-playback flow, metadata mismatch retry, cache, platform URL mapping | `TestHuanyinSourceInitializesAndDispatchesMusicURL`, `TestHuanyinMiguMusicURLRetriesMetadataMismatchAndCachesResult`, `TestHuanyinMiguSearchStopsWhenRequestedLimitIsReached`, `TestHuanyinSearchResultFlowsIntoMusicURLResolution`, `TestHuanyinPlatformURLMappingCoversAllNativePlatforms` | QQ/Kuwo/Netease URL construction is local; Migu HTTP is mocked. More negative response and timeout cases remain open. |

## Protocol assertions required for N5

1. **Initialization**: execute the real pinned source, assert whether it sends `inited`,
   verify advertised source/action/quality metadata, and verify request-handler registration.
2. **Request contract**: assert method, URL scheme/host/path/query, relevant headers,
   body encoding, and request count. Do not accept a request merely because it returned 200.
3. **Response contract**: provide deterministic success, malformed, empty, and non-2xx
   responses where the source has corresponding branches; assert the actual source result
   or rejection.
4. **Async lifecycle**: exercise callbacks/promises and timers through the real QuickJS
   dispatch loop, not a JavaScript-only mock of the source handler.
5. **Buffer/crypto contract**: assert the real source invokes native host methods and
   validate resulting bytes/payloads at the Go HTTP boundary.
6. **Isolation**: fail the test on any unrecognized outbound request; never use live
   credentials, service responses, or third-party network access.

## Completion criteria

N5 is not complete merely because all four scripts parse or initialize. Each fixture
must have a deterministic, source-level protocol path with explicit request and response
assertions. The `lx.js` successful server-config handshake is a known gap and must be
resolved from the pinned source's actual config schema, not by inventing a response.
A passing matrix proves only the listed deterministic contracts; it does not prove
live service availability, entitlement bypass, or guaranteed playback.
