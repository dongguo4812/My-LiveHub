package music

import (
	"context"
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
	for _, want := range []string{"[ti:晴天]","[ar:周杰伦]","[00:01.00]第一句 / First line","[00:02.45]第二句"} {
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
