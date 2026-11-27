//go:build lxquickjs || lxproduction

package lxruntime

import (
	"strings"
	"testing"
)

func cacheableObfuscatedFixture() string {
	return "(function(){var _0x5b6cec=[];while(!![]){try{const _0x3d2c58=parseInt(value);if(_0x3d2c58===target)break;else _0x5b6cec['push'](_0x5b6cec['shift']());}catch(_0x4bc586){_0x5b6cec['push'](_0x5b6cec['shift']());}}}(a0_0x223d,);})();"
}

func TestPrepareLXObfuscationCacheSourceMissInstrumentsRotationCount(t *testing.T) {
	source := cacheableObfuscatedFixture()
	prepared, hit, cacheable := prepareLXObfuscationCacheSource(source, &LXObfuscationCache{})
	if hit || !cacheable {
		t.Fatalf("cache miss result hit=%t cacheable=%t", hit, cacheable)
	}
	if !strings.Contains(prepared, "globalThis."+lxObfuscationCacheGlobal+"=0;while(!![])") {
		t.Fatalf("cache miss did not initialize rotation counter before loop: %s", prepared)
	}
	if strings.Count(prepared, "globalThis."+lxObfuscationCacheGlobal+"++;_0x5b6cec['push']") != 2 {
		t.Fatalf("cache miss must count both checksum and catch rotations: %s", prepared)
	}
}

func TestPrepareLXObfuscationCacheSourceHitReplaysOnlyArrayRotations(t *testing.T) {
	source := cacheableObfuscatedFixture()
	cache := &LXObfuscationCache{
		Version: lxObfuscationCacheVersion, SourceSHA256: lxSourceSHA256(source), RotationCount: 266,
	}
	prepared, hit, cacheable := prepareLXObfuscationCacheSource(source, cache)
	if !hit || !cacheable {
		t.Fatalf("cache hit result hit=%t cacheable=%t", hit, cacheable)
	}
	if !strings.Contains(prepared, "for(let __myLiveHubObfPreRotation=0;__myLiveHubObfPreRotation<266;__myLiveHubObfPreRotation++)") {
		t.Fatalf("cache hit did not replace decoder/check loop with cached rotations: %s", prepared)
	}
	if strings.Contains(prepared, "while(!![])") {
		t.Fatalf("cache hit retained the repeated obfuscation loop: %s", prepared)
	}
	if !strings.Contains(prepared, "for(let __myLiveHubObfVerify=0;__myLiveHubObfVerify<1;__myLiveHubObfVerify++)") ||
		!strings.Contains(prepared, "parseInt(value)") {
		t.Fatalf("cache hit must preserve one final checksum/decode pass: %s", prepared)
	}
}

func TestPrepareLXObfuscationCacheSourceInvalidatesChangedScript(t *testing.T) {
	source := cacheableObfuscatedFixture()
	cache := &LXObfuscationCache{
		Version: lxObfuscationCacheVersion, SourceSHA256: lxSourceSHA256(source), RotationCount: 266,
	}
	prepared, hit, cacheable := prepareLXObfuscationCacheSource(source+"// changed", cache)
	if hit || !cacheable {
		t.Fatalf("changed script must invalidate cache: hit=%t cacheable=%t", hit, cacheable)
	}
	if !strings.Contains(prepared, "globalThis."+lxObfuscationCacheGlobal+"=0;") {
		t.Fatal("changed script did not enter the normal validation path")
	}
}

func TestPrepareLXObfuscationCacheSourceLeavesUnknownShapeUntouched(t *testing.T) {
	source := "while (true) { break; }"
	prepared, hit, cacheable := prepareLXObfuscationCacheSource(source, &LXObfuscationCache{})
	if hit || cacheable || prepared != source {
		t.Fatalf("unknown source shape must remain untouched, got hit=%t cacheable=%t source=%q", hit, cacheable, prepared)
	}
}

func TestPrepareLXInitializationSourceProfileDoesNotBypassPersistentCache(t *testing.T) {
	source := cacheableObfuscatedFixture()

	prepared, hit, cacheable, profiled := prepareLXInitializationSource(source, &LXObfuscationCache{}, true)
	if hit || !cacheable || profiled {
		t.Fatalf("profiling must not bypass a managed cache: hit=%t cacheable=%t profiled=%t", hit, cacheable, profiled)
	}
	if !strings.Contains(prepared, "globalThis."+lxObfuscationCacheGlobal+"=0;") {
		t.Fatal("managed cache miss did not instrument the rotation counter")
	}

	cache := &LXObfuscationCache{Version: lxObfuscationCacheVersion, SourceSHA256: lxSourceSHA256(source), RotationCount: 266}
	prepared, hit, cacheable, profiled = prepareLXInitializationSource(source, cache, true)
	if !hit || !cacheable || profiled {
		t.Fatalf("profiling must preserve a valid cache hit: hit=%t cacheable=%t profiled=%t", hit, cacheable, profiled)
	}
	if !strings.Contains(prepared, "__myLiveHubObfPreRotation<266") || strings.Contains(prepared, "while(!![])") {
		t.Fatal("valid cached source did not use the fast path when profiling was requested")
	}
}

func TestPrepareLXInitializationSourceAllowsExplicitProfilingWithoutCache(t *testing.T) {
	source := lxObfuscationLoopStart + "0);if(1)break;else _0x5b6cec['push'](_0x5b6cec['shift']());" + lxObfuscationLoopEnd + "0);" + lxObfuscationDecodeMiss + "===null?0:1;})();"
	prepared, hit, cacheable, profiled := prepareLXInitializationSource(source, nil, true)
	if !profiled || hit || cacheable {
		t.Fatalf("explicit no-cache profiling result hit=%t cacheable=%t profiled=%t", hit, cacheable, profiled)
	}
	if !strings.Contains(prepared, "__myLiveHubLXObfProfile") {
		t.Fatal("explicit profiling did not instrument the source")
	}
}
