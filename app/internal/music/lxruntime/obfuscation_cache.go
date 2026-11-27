//go:build lxquickjs || lxproduction

package lxruntime

import (
	"crypto/sha256"
	"encoding/hex"
	"regexp"
	"strconv"
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

var (
	lxCacheLoopStartPattern = regexp.MustCompile(`while\(!!\[\]\)\{try\{const\s+[A-Za-z_$][\w$]*\s*=\s*parseInt\(`)
	lxCacheLoopEndPattern = regexp.MustCompile(`catch\(([A-Za-z_$][\w$]*)\)\{([A-Za-z_$][\w$]*)\['push'\]\(([A-Za-z_$][\w$]*)\['shift'\]\(\)\);\}\}\}\(([A-Za-z_$][\w$]*),`)
)

func lxSourceSHA256(source string) string {
	sum := sha256.Sum256([]byte(source))
	return hex.EncodeToString(sum[:])
}

// prepareLXObfuscationCacheSource recognizes the common LX obfuscator rotation
// loop while capturing its local identifiers. Unknown or structurally
// different scripts are evaluated unchanged.
func prepareLXObfuscationCacheSource(source string, cache *LXObfuscationCache) (string, bool, bool) {
	if cache == nil {
		return source, false, false
	}
	startMatches := lxCacheLoopStartPattern.FindAllStringIndex(source, 2)
	endMatches := lxCacheLoopEndPattern.FindAllStringSubmatchIndex(source, 2)
	if len(startMatches) != 1 || len(endMatches) != 1 {
		return source, false, false
	}
	start := startMatches[0][0]
	end := endMatches[0]
	if end[0] < startMatches[0][1] {
		return source, false, false
	}
	catchName := source[end[2]:end[3]]
	arrayName := source[end[4]:end[5]]
	shiftArrayName := source[end[6]:end[7]]
	invokedFunction := source[end[8]:end[9]]
	if arrayName != shiftArrayName || catchName == "" || invokedFunction == "" {
		return source, false, false
	}

	digest := lxSourceSHA256(source)
	if cache.Version == lxObfuscationCacheVersion &&
		cache.SourceSHA256 == digest &&
		cache.RotationCount >= 0 &&
		cache.RotationCount <= 1_000_000 {
		replacement := "for(var __myLiveHubObfRotation=0;__myLiveHubObfRotation<" +
			strconv.Itoa(cache.RotationCount) + ";__myLiveHubObfRotation++){" +
			arrayName + "['push'](" + arrayName + "['shift']());}}(" + invokedFunction + ","
		return source[:start] + replacement + source[end[0]:end[1]*0+end[0]] + source[end[1]:], true, true
	}

	// Count actual catch/rotation executions during the first successful eval.
	instrumented := source[:start] + "globalThis." + lxObfuscationCacheGlobal + "=0;" + source[start:end[0]] +
		"catch(" + catchName + "){globalThis." + lxObfuscationCacheGlobal + "++;" +
		arrayName + "['push'](" + arrayName + "['shift']());}}}(" + invokedFunction + "," +
		source[end[1]:]
	return instrumented, false, true
}
