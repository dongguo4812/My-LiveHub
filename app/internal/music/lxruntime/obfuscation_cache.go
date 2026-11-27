//go:build lxquickjs || lxproduction

package lxruntime

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"strconv"
)

// LXObfuscationCache stores the result needed to bypass a previously
// successful obfuscation rotation loop for the exact source bytes.
type LXObfuscationCache struct {
	Version       int    `json:"version"`
	SourceSHA256  string `json:"source_sha256"`
	RotationCount int    `json:"rotation_count"`
}

const lxObfuscationCacheVersion = 1
const lxObfuscationCacheGlobal = "__myLiveHubLXObfuscationCacheRotations"

func lxSourceSHA256(source string) string {
	sum := sha256.Sum256([]byte(source))
	return hex.EncodeToString(sum[:])
}

// prepareLXObfuscationCacheSource only rewrites the explicitly recognized
// rotation-loop shape. Unknown obfuscators are always evaluated unchanged.
func prepareLXObfuscationCacheSource(source string, cache *LXObfuscationCache) (string, bool, bool) {
	if cache == nil ||
		strings.Count(source, lxObfuscationLoopStart) != 1 ||
		strings.Count(source, lxObfuscationLoopEnd) != 1 {
		return source, false, false
	}

	digest := lxSourceSHA256(source)
	if cache.Version == lxObfuscationCacheVersion &&
		cache.SourceSHA256 == digest &&
		cache.RotationCount >= 0 &&
		cache.RotationCount <= 1_000_000 {
		replacement := "for(var __lxObfRotation=0;__lxObfRotation<" +
			strconv.Itoa(cache.RotationCount) +
			";__lxObfRotation++){_0x5b6cec['push'](_0x5b6cec['shift']());}}(a0_0x223d,"
		start := strings.Index(source, lxObfuscationLoopStart)
		end := strings.Index(source, lxObfuscationLoopEnd)
		if start < 0 || end < start {
			return source, false, false
		}
		end += len(lxObfuscationLoopEnd)
		return source[:start] + replacement + source[end:], true, true
	}

	// Count actual catch/rotation executions during the first successful eval.
	start := strings.Index(source, lxObfuscationLoopStart)
	if start < 0 { return source, false, false }
	instrumented := source[:start] + "globalThis." + lxObfuscationCacheGlobal + "=0;" + source[start:]
	rotation := "catch(_0x4bc586){globalThis." + lxObfuscationCacheGlobal +
		"++;_0x5b6cec['push'](_0x5b6cec['shift']());}}}(a0_0x223d,"
	instrumented = strings.Replace(instrumented, lxObfuscationLoopEnd, rotation, 1)
	return instrumented, false, true
}
