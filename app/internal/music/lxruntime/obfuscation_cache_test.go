//go:build lxquickjs || lxproduction

package lxruntime

import (
	"strings"
	"testing"
)

func cacheableObfuscatedFixture() string {
	return "(function(){var _0x5b6cec=[];while(!![]){try{const _0x3d2c58=parseInt(value);break;}catch(_0x4bc586){_0x5b6cec['push'](_0x5b6cec['shift']());}}}(a0_0x223d,);})();"
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
	if !strings.Contains(prepared, "globalThis."+lxObfuscationCacheGlobal+"++;_0x5b6cec['push']") {
		t.Fatalf("cache miss did not count each array rotation: %s", prepared)
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
	if !strings.Contains(prepared, "for(var __lxObfRotation=0;__lxObfRotation<266;__lxObfRotation++)") {
		t.Fatalf("cache hit did not replace decoder/check loop with cached rotations: %s", prepared)
	}
	if strings.Contains(prepared, "while(!![])") || strings.Contains(prepared, "parseInt(value)") {
		t.Fatalf("cache hit retained expensive obfuscation validation: %s", prepared)
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
