//go:build lxquickjs || lxproduction

package lxruntime

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
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
		replacement := "for(let __lxObfRotation=0;__lxObfRotation<" +
			strings.TrimSpace(strings.Repeat(" ", 0)) +
			"0;__lxObfRotation++){_0x5b6cec['push'](_0x5b6cec['shift']());}}(a0_0x223d,"
		replacement = strings.Replace(replacement, "<0;", "<"+itoa(cache.RotationCount)+";", 1)
		start := strings.Index(source, lxObfuscationLoopStart)
		end := strings.Index(source, lxObfuscationLoopEnd)
		if start < 0 || end < start {
			return source, false, false
		}
		end += len(lxObfuscationLoopEnd)
		return source[:start] + replacement + source[end:], true, true
	}

	// Count actual catch/rotation executions during the first successful eval.
	instrumented := "globalThis." + lxObfuscationCacheGlobal + "=0;" + source
	rotation := "catch(_0x4bc586){globalThis." + lxObfuscationCacheGlobal +
		"++;_0x5b6cec['push'](_0x5b6cec['shift']());}}}(a0_0x223d,"
	instrumented = strings.Replace(instrumented, lxObfuscationLoopEnd, rotation, 1)
	return instrumented, false, true
}

func itoa(value int) string {
	if value == 0 { return "0" }
	var buf [20]byte
	i := len(buf)
	for value > 0 {
		i--
		buf[i] = byte('0' + value%10)
		value /= 10
	}
	return string(buf[i:])
}
