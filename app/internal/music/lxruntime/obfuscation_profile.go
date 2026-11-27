//go:build lxquickjs || lxproduction

package lxruntime

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"
)

const lxObfuscationProfileEnv = "MYLIVEHUB_LX_PROFILE_OBFUSCATION"

const (
	lxObfuscationLoopStart = "while(!![]){try{const _0x3d2c58=parseInt("
	lxObfuscationLoopEnd = "catch(_0x4bc586){_0x5b6cec['push'](_0x5b6cec['shift']());}}}(a0_0x223d,"
	lxObfuscationDecodeMiss = "return _0x14f9f3===undefined?(a0_0x54c8['OldavH']===undefined"
)

type lxObfuscationProfile struct {
	Iterations       int     `json:"iterations"`
	DecodeCalls      int     `json:"decodeCalls"`
	LoopDecodeCalls  int     `json:"loopDecodeCalls"`
	LoopDurationMS   float64 `json:"loopDurationMs"`
}

// instrumentLXObfuscationProfile instruments only the known obfuscation
// bootstrap shape. It changes no source file on disk and is opt-in via env.
func instrumentLXObfuscationProfile(source string) (string, bool) {
	if strings.Count(source, lxObfuscationLoopStart) != 1 ||
		strings.Count(source, lxObfuscationLoopEnd) != 1 ||
		strings.Count(source, lxObfuscationDecodeMiss) != 1 {
		return source, false
	}

	start := "globalThis.__myLiveHubLXObfProfile={started:Date.now(),iterations:0,decodeCalls:0,loopDecodeCalls:0,loopDurationMs:0};" +
		"while(!![]){globalThis.__myLiveHubLXObfProfile.iterations++;try{const _0x3d2c58=parseInt("
	end := "catch(_0x4bc586){_0x5b6cec['push'](_0x5b6cec['shift']());}}" +
		"globalThis.__myLiveHubLXObfProfile.loopDecodeCalls=globalThis.__myLiveHubLXObfProfile.decodeCalls;" +
		"globalThis.__myLiveHubLXObfProfile.loopDurationMs=Date.now()-globalThis.__myLiveHubLXObfProfile.started;" +
		"}(a0_0x223d,"
	decode := "return _0x14f9f3===undefined?(globalThis.__myLiveHubLXObfProfile.decodeCalls++," +
		"a0_0x54c8['OldavH']===undefined"

	source = strings.Replace(source, lxObfuscationLoopStart, start, 1)
	source = strings.Replace(source, lxObfuscationLoopEnd, end, 1)
	source = strings.Replace(source, lxObfuscationDecodeMiss, decode, 1)
	return source, true
}

func (r *Runtime) logLXObfuscationProfile(ctx context.Context, filename string) {
	value, err := r.Eval(ctx, "JSON.stringify(globalThis.__myLiveHubLXObfProfile || null)", "lx-obfuscation-profile.js")
	if err != nil {
		log.Printf("[LX-Obfuscation] script=%q profile_read=error error=%q", filename, err.Error())
		return
	}
	var profile lxObfuscationProfile
	if value == "null" || json.Unmarshal([]byte(value), &profile) != nil {
		log.Printf("[LX-Obfuscation] script=%q profile=unavailable", filename)
		return
	}
	log.Printf("[LX-Obfuscation] script=%q loop_iterations=%d loop_decode_calls=%d total_decode_calls=%d loop_duration_ms=%.3f",
		filename, profile.Iterations, profile.LoopDecodeCalls, profile.DecodeCalls, profile.LoopDurationMS)
}

var _ = fmt.Sprintf
