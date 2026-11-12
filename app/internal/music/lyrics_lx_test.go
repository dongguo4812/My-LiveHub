package music

import (
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	qrc "github.com/dongguo4812/My-LiveHub/app/internal/music/lxqrc"
)

type lyricRoundTripper func(*http.Request) (*http.Response, error)
func (f lyricRoundTripper) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestFormatQQLyricAsLRC(t *testing.T) {
	primary := &qrc.Lyric{Lines: []qrc.Line{{Start: 1000, Text: "第一句"},{Start: 2450, Text: "第二句"}}}
	translated := &qrc.Lyric{Lines: []qrc.Line{{Start: 1000, Text: "First line"}}}
	got := formatQQLyricAsLRC(Track{Title:"晴天",Artist:"周杰伦"}, primary, translated, nil)
	for _, want := range []string{"[ti:晴天]","[ar:周杰伦]","[00:01.00]第一句", "[00:01.00]First line","[00:02.45]第二句"} {
		if !strings.Contains(got, want) { t.Fatalf("formatted lyrics missing %q:\n%s", want, got) }
	}
}

func TestGetQQMusicLyricsUsesNativePlayLyricInfo(t *testing.T) {
	client := &http.Client{Transport: lyricRoundTripper(func(r *http.Request) (*http.Response, error) {
		if r.URL.String() != qqMusicLyricEndpoint { t.Fatalf("unexpected endpoint: %s", r.URL) }
		return &http.Response{StatusCode:200,Header:make(http.Header),Body:io.NopCloser(strings.NewReader(`{"code":0,"req":{"code":0,"data":{"lyric":"<QrcInfos><QrcHeadInfo Version=\"100\"/><LyricInfo><Lyric_1 LyricContent=\"[1000,1000]测试歌词(1000,1000)\"/></LyricInfo></QrcInfos>"}}}`))},nil
	})}
	got, err := GetQQMusicLyrics(context.Background(), client, Track{ID:"tx_1",SourceID:"tx",LXSourceKey:"tx",SourceData:map[string]string{"song_id":"123","songmid":"abc"},Title:"测试",Artist:"歌手"})
	if err != nil { t.Fatal(err) }
	if !strings.Contains(got,"[00:01.00]测试歌词") { t.Fatalf("unexpected lyric result: %s",got) }
}

func TestParseKugouKRCLyric(t *testing.T) {
	got := parseKugouKRC("[id:$00000000]\n[1000,2000]<1000,500>春风<1500,500>十里\n[2500,1000]<2500,1000>不如你", Track{Title:"春风十里", Artist:"鹿先森乐队"})
	for _, want := range []string{"[ti:春风十里]", "[ar:鹿先森乐队]", "[00:01.00]春风十里", "[00:02.50]不如你"} {
		if !strings.Contains(got, want) { t.Fatalf("missing %q in KRC output:\n%s", want, got) }
	}
}

func TestGetKugouMusicLyricsUsesLXSearchAndDownloadFlow(t *testing.T) {
	lrc := "[00:01.00]第一句\n[00:02.50]第二句"
	encoded := base64.StdEncoding.EncodeToString([]byte(lrc))
	client := &http.Client{Transport: lyricRoundTripper(func(r *http.Request) (*http.Response, error) {
		if !strings.Contains(r.URL.Host, "lyrics.kugou.com") { t.Fatalf("unexpected host: %s", r.URL.Host) }
		switch r.URL.Path {
		case "/search":
			if r.URL.Query().Get("hash") != "abc123" { t.Fatalf("missing Kugou hash: %s", r.URL.RawQuery) }
			return &http.Response{StatusCode:200,Header:make(http.Header),Body:io.NopCloser(strings.NewReader(`{"status":200,"candidates":[{"id":"42","accesskey":"secret","krctype":0,"contenttype":1}]}`))},nil
		case "/download":
			if r.URL.Query().Get("id") != "42" || r.URL.Query().Get("fmt") != "lrc" { t.Fatalf("unexpected download query: %s", r.URL.RawQuery) }
			return &http.Response{StatusCode:200,Header:make(http.Header),Body:io.NopCloser(strings.NewReader(fmt.Sprintf(`{"status":200,"fmt":"lrc","content":%q}`,encoded)))},nil
		default:
			t.Fatalf("unexpected path: %s",r.URL.Path)
			return nil,nil
		}
	})}
	got, err := GetKugouMusicLyrics(context.Background(),client,Track{
		SourceID:"kg",LXSourceKey:"kg",Title:"测试歌曲",Artist:"测试歌手",DurationMS:180000,
		SourceData:map[string]string{"hash":"abc123"},
	})
	if err != nil { t.Fatal(err) }
	if !strings.Contains(got,"[00:01.00]第一句") || !strings.Contains(got,"[00:02.50]第二句") { t.Fatalf("unexpected LRC result: %s",got) }
}

func TestParseLXPlainLyricRemovesWordTiming(t *testing.T) {
	got := parseLXPlainLyric("[00:01.000]<0,200>春<200,300>风")
	if !strings.Contains(got, "[00:01.000]春风") { t.Fatalf("unexpected Kuwo lyric: %q", got) }
}
func TestParseMiguLyricConvertsMillisecondTimestamps(t *testing.T) {
	got := parseMiguLyric("[1000,2000](1000,500)春风(1500,500)十里")
	if !strings.Contains(got, "[00:01.000]春风十里") { t.Fatalf("unexpected Migu lyric: %q", got) }
}
func TestDecryptMiguLyricLeavesPlainShortPayload(t *testing.T) {
	got, err := decryptMiguLyric("plain")
	if err != nil { t.Fatal(err) }
	if got != "plain" { t.Fatalf("got %q", got) }
}

func TestNeteaseLyricSongIDFallsBackToLXPlaylistTrackID(t *testing.T) {
 track := Track{ID: "wy_1500569811", SourceID: "wy"}
 if got := neteaseLyricSongID(track); got != "1500569811" {
  t.Fatalf("netease lyric ID = %q, want %q", got, "1500569811")
 }
}

func TestNeteaseLyricSongIDPrefersProviderMetadata(t *testing.T) {
 track := Track{ID: "wy_1500569811", SourceID: "wy", SourceData: map[string]string{"songmid": "987654"}}
 if got := neteaseLyricSongID(track); got != "987654" {
  t.Fatalf("netease lyric ID = %q, want metadata value %q", got, "987654")
 }
}
