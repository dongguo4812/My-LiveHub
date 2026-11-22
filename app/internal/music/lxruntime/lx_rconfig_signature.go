package lxruntime

import (
	"crypto/md5"
	"encoding/hex"
	"strings"
	"unicode/utf16"
)

const lxRConfigPrefixMod int64 = 145788374
const lxRConfigPrefixBias int64 = 183

var lxRConfigPrefixMultipliers = [...]int64{
	152, 143, 12, 5, 220, 44, 14, 222,
	197, 17, 134, 185, 91, 229, 252, 114,
}

// lxRConfigDynamicPrefix reproduces the V7 source's 16-byte time-derived
// prefix. The source reduces the millisecond timestamp, adds 183, multiplies
// each byte by its fixed coefficient, and keeps the low 8 bits.
func lxRConfigDynamicPrefix(timestamp int64) string {
	seed := timestamp%lxRConfigPrefixMod + lxRConfigPrefixBias
	var prefix [len(lxRConfigPrefixMultipliers)]byte
	for i, multiplier := range lxRConfigPrefixMultipliers {
		prefix[i] = byte((seed * multiplier) % 256)
	}
	return hex.EncodeToString(prefix[:])
}

// lxRConfigScriptDigest reproduces the source integrity half of si: lowercase
// the complete source, insert U+200B between UTF-16 code units, then MD5 it.
func lxRConfigScriptDigest(source string) string {
	units := utf16.Encode([]rune(strings.ToLower(source)))
	var transformed strings.Builder
	transformed.Grow(len(source) * 4)
	for i, unit := range units {
		if i != 0 {
			transformed.WriteRune('\u200b')
		}
		transformed.WriteRune(rune(unit))
	}
	sum := md5.Sum([]byte(transformed.String()))
	return hex.EncodeToString(sum[:])
}

// lxRConfigSignature calculates si as MD5 of the ASCII hex prefix followed by
// the ASCII hex script digest (64 bytes total, not the raw 32-byte values).
func lxRConfigSignature(timestamp int64, source string) string {
	preimage := lxRConfigDynamicPrefix(timestamp) + lxRConfigScriptDigest(source)
	sum := md5.Sum([]byte(preimage))
	return hex.EncodeToString(sum[:])
}
