package music

import (
	"bytes"
	"compress/zlib"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

// TestNativeProviderLyricsAdapters exercises each non-QQ provider's complete
// HTTP adapter with deterministic responses. The fixtures intentionally use
// provider-specific metadata and wire formats so a provider cannot silently
// fall through to another source's lyric implementation.
func TestNativeProviderLyricsAdapters(t *testing.T) {
	t.Run("Kuwo", func(t *testing.T) {
		plain := []byte("[00:01.000]<0,500>酷我<500,500>歌词")
		key := []byte("yeelion")
		for i := range plain { plain[i] ^= key[i%len(key)] }
		encoded := base64.StdEncoding.EncodeToString(plain)
		var compressed bytes.Buffer
		zw := zlib.NewWriter(&compressed)
		if _, err := io.WriteString(zw, encoded); err != nil { t.Fatal(err) }
		if err := zw.Close(); err != nil { t.Fatal(err) }
		client := &http.Client{Transport: lyricRoundTripper(func(r *http.Request) (*http.Response, error) {
			if r.URL.Host != "mlyric.kuwo.cn" || r.URL.Query().Get("rid") != "kw-song-7" {
				t.Fatalf("Kuwo request did not use provider songmid: %s", r.URL.String())
			}
			body := append([]byte("tp=content\r\n\r\n"), compressed.Bytes()...)
			return lyricHTTPResponse(200, body), nil
		})}
		got, err := GetKuwoMusicLyrics(context.Background(), client, Track{SourceID:"kw", SourceData:map[string]string{"songmid":"kw-song-7"}})
		if err != nil { t.Fatal(err) }
		if !strings.Contains(got, "[00:01.000]酷我歌词") { t.Fatalf("Kuwo lyric decode/sync failed: %q", got) }
	})

	t.Run("NetEase", func(t *testing.T) {
		client := &http.Client{Transport: lyricRoundTripper(func(r *http.Request) (*http.Response, error) {
			if r.URL.Host != "interface3.music.163.com" || r.URL.Path != "/eapi/song/lyric/v1" { t.Fatalf("unexpected NetEase endpoint: %s", r.URL) }
			if err := r.ParseForm(); err != nil { t.Fatal(err) }
			if strings.TrimSpace(r.Form.Get("params")) == "" { t.Fatal("NetEase EAPI encrypted params are missing") }
			return lyricHTTPResponse(200, []byte(`{"code":200,"lrc":{"lyric":"[00:01.00]网易原文"},"tlyric":{"lyric":"[00:01.00]NetEase translation"}}`)), nil
		})}
		got, err := GetNeteaseMusicLyrics(context.Background(), client, Track{SourceID:"wy", ID:"wy_1500569811"})
		if err != nil { t.Fatal(err) }
		for _, want := range []string{"[00:01.00]网易原文", "[00:01.00]NetEase translation"} {
			if !strings.Contains(got, want) { t.Fatalf("NetEase lyric/translation missing %q: %q", want, got) }
		}
	})

	t.Run("Migu", func(t *testing.T) {
		client := &http.Client{Transport: lyricRoundTripper(func(r *http.Request) (*http.Response, error) {
			if r.URL.String() != "https://fixture.invalid/migu.lrc" { t.Fatalf("Migu adapter ignored Migu lyric URL: %s", r.URL) }
			return lyricHTTPResponse(200, []byte("[00:01.00]咪咕原文")), nil
		})}
		got, err := GetMiguMusicLyrics(context.Background(), client, Track{SourceID:"mg", SourceData:map[string]string{"lrc_url":"https://fixture.invalid/migu.lrc"}})
		if err != nil { t.Fatal(err) }
		if !strings.Contains(got, "[00:01.00]咪咕原文") { t.Fatalf("Migu lyric retrieval failed: %q", got) }
	})

	t.Run("provider metadata is mandatory", func(t *testing.T) {
		tests := []struct{name string; call func() (string,error); want string}{
			{"Kuwo", func()(string,error){ return GetKuwoMusicLyrics(context.Background(), nil, Track{SourceID:"kw"}) }, "songmid"},
			{"NetEase", func()(string,error){ return GetNeteaseMusicLyrics(context.Background(), nil, Track{SourceID:"wy"}) }, "songmid"},
			{"Migu", func()(string,error){ return GetMiguMusicLyrics(context.Background(), nil, Track{SourceID:"mg"}) }, "歌曲 ID"},
		}
		for _, tt := range tests { t.Run(tt.name, func(t *testing.T) { _,err:=tt.call(); if err==nil || !strings.Contains(err.Error(),tt.want) { t.Fatalf("expected explicit metadata error containing %q, got %v",tt.want,err) } }) }
	})
}

func lyricHTTPResponse(status int, body []byte) *http.Response {
	return &http.Response{StatusCode:status, Header:make(http.Header), Body:io.NopCloser(bytes.NewReader(body))}
}

// Keep this compile-time assertion close to the wire tests: form encoding is
// also used by the NetEase adapter and must remain available to its fixture.
var _ = url.Values{}
var _ = json.Valid
