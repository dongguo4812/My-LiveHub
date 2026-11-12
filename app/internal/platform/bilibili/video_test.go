package bilibili

import (
	"net/url"
	"testing"
	"time"
)

func TestValidBVID(t *testing.T) {
	for _, value := range []string{"BV1xx411c7mD", "BV1Q541167Qg"} {
		if !validBVID(value) { t.Errorf("expected valid BVID: %s", value) }
	}
	for _, value := range []string{"", "av123", "BV123", "BV1xx411c7mD-extra", "BV1xx411c7m!"} {
		if validBVID(value) { t.Errorf("expected invalid BVID: %s", value) }
	}
}

func TestMixinWBIKey(t *testing.T) {
	got := mixinWBIKey("0123456789abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ", "abcdefghijklmnopqrstuvwxyz0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZ")
	if len(got) != 32 { t.Fatalf("mixin key length = %d, want 32", len(got)) }
}

func TestSignWBI(t *testing.T) {
	params := url.Values{"keyword":{"测试 视频"}, "search_type":{"video"}, "wts":{"old"}, "w_rid":{"old"}}
	got := signWBI(params, "img-key", "sub-key", time.Unix(1700000000, 0))
	if got.Get("wts") != "1700000000" { t.Fatalf("unexpected wts: %s", got.Get("wts")) }
	if got.Get("w_rid") == "" || got.Get("w_rid") == "old" { t.Fatalf("signature was not generated: %s", got.Get("w_rid")) }
	if got.Get("keyword") != "测试 视频" { t.Fatalf("keyword changed: %q", got.Get("keyword")) }
}
