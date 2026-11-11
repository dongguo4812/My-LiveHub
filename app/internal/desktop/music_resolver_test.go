package desktop

import (
	"encoding/json"
	"testing"

	"github.com/dongguo4812/My-LiveHub/app/internal/music"
)

func TestChooseLXTrackQualityPrefersBestMutuallySupported(t *testing.T) {
	track := music.Track{Qualities: []music.TrackQuality{{Type:"128k"},{Type:"320k"},{Type:"flac"}}}
	got, err := chooseLXTrackQuality(track, music.LXDeclaredSource{Qualitys:[]string{"128k","320k"}})
	if err != nil { t.Fatal(err) }
	if got.Type != "320k" { t.Fatalf("selected %q, want 320k", got.Type) }
}

func TestChooseLXTrackQualityHonorsExplicitSelection(t *testing.T) {
	track := music.Track{SelectedQuality:"128k", Qualities:[]music.TrackQuality{{Type:"128k"},{Type:"flac"}}}
	got, err := chooseLXTrackQuality(track, music.LXDeclaredSource{Qualitys:[]string{"128k","flac"}})
	if err != nil { t.Fatal(err) }
	if got.Type != "128k" { t.Fatalf("selected %q, want 128k", got.Type) }
	if _, err := chooseLXTrackQuality(track, music.LXDeclaredSource{Qualitys:[]string{"flac"}}); err == nil { t.Fatal("expected unsupported selected quality error") }
}

func TestBuildLXMusicURLRequestPreservesPlatformFields(t *testing.T) {
	track := music.Track{
		ID:"tx:mid-1", SourceID:"tx", LXSourceKey:"tx", Title:"歌曲", Artist:"歌手", Album:"专辑",
		SourceData:map[string]string{"songmid":"mid-1","song_id":"123","media_mid":"media-1","album_mid":"album-1"},
		Qualities:[]music.TrackQuality{{Type:"128k"},{Type:"320k"}},
	}
	payload, err := buildLXMusicURLRequest(track, music.TrackQuality{Type:"320k"})
	if err != nil { t.Fatal(err) }
	var request struct {
		Source string `json:"source"`
		Action string `json:"action"`
		Info map[string]any `json:"info"`
	}
	if err := json.Unmarshal(payload,&request); err != nil { t.Fatal(err) }
	if request.Source!="tx" || request.Action!="musicUrl" { t.Fatalf("unexpected request: %#v",request) }
	if request.Info["songmid"]!="mid-1" || request.Info["strMediaMid"]!="media-1" || request.Info["songId"]!="123" { t.Fatalf("native QQ fields missing: %#v",request.Info) }
	if request.Info["type"]!="320k" { t.Fatalf("wrong quality: %#v",request.Info["type"]) }
	types, ok := request.Info["_types"].(map[string]any)
	if !ok || types["320k"]==nil { t.Fatalf("quality metadata missing: %#v",request.Info["_types"]) }
}

func TestBuildLXMusicURLRequestMapsMiguCopyrightID(t *testing.T) {
	track:=music.Track{ID:"mg:1",SourceID:"mg",Title:"歌",SourceData:map[string]string{"songmid":"1","copyright_id":"copyright-1"}}
	payload,err:=buildLXMusicURLRequest(track,music.TrackQuality{Type:"128k"})
	if err!=nil{t.Fatal(err)}
	var request struct{Info map[string]any `json:"info"`}
	if err:=json.Unmarshal(payload,&request);err!=nil{t.Fatal(err)}
	if request.Info["copyrightId"]!="copyright-1"{t.Fatalf("copyrightId missing: %#v",request.Info)}
}

func TestBuildLXMusicURLRequestPreservesFivePlatformNativeFields(t *testing.T) {
	tests := []struct {
		name string
		track music.Track
		selected music.TrackQuality
		want map[string]string
	}{
		{"kuwo", music.Track{ID:"kw:kw-id", SourceID:"kw", LXSourceKey:"kw", Title:"song", SourceData:map[string]string{"songmid":"kw-id","album_id":"kw-album"}}, music.TrackQuality{Type:"flac"}, map[string]string{"songmid":"kw-id","albumId":"kw-album"}},
		{"kugou", music.Track{ID:"kg:kg-id", SourceID:"kg", LXSourceKey:"kg", Title:"song", SourceData:map[string]string{"songmid":"kg-id","hash":"base-hash","album_audio_id":"audio-id"}, Qualities:[]music.TrackQuality{{Type:"320k",SourceData:map[string]string{"hash":"320-hash"}}}}, music.TrackQuality{Type:"320k",SourceData:map[string]string{"hash":"320-hash"}}, map[string]string{"songmid":"kg-id","hash":"320-hash","albumAudioId":"audio-id"}},
		{"qq", music.Track{ID:"tx:tx-id", SourceID:"tx", LXSourceKey:"tx", Title:"song", SourceData:map[string]string{"songmid":"tx-id","song_id":"123","media_mid":"media-id","album_mid":"album-mid"}}, music.TrackQuality{Type:"320k"}, map[string]string{"songmid":"tx-id","songId":"123","strMediaMid":"media-id","albumMid":"album-mid"}},
		{"netease", music.Track{ID:"wy:wy-id", SourceID:"wy", LXSourceKey:"wy", Title:"song", SourceData:map[string]string{"songmid":"wy-id","album_id":"wy-album"}}, music.TrackQuality{Type:"flac"}, map[string]string{"songmid":"wy-id","albumId":"wy-album"}},
		{"migu", music.Track{ID:"mg:mg-id", SourceID:"mg", LXSourceKey:"mg", Title:"song", SourceData:map[string]string{"songmid":"mg-id","copyright_id":"copyright","album_id":"mg-album","lrc_url":"lrc","mrc_url":"mrc","trc_url":"trc"}}, music.TrackQuality{Type:"128k"}, map[string]string{"songmid":"mg-id","copyrightId":"copyright","albumId":"mg-album","lrcUrl":"lrc","mrcUrl":"mrc","trcUrl":"trc"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			raw, err := buildLXMusicURLRequest(test.track, test.selected)
			if err != nil { t.Fatal(err) }
			var request struct { Info map[string]any `json:"info"` }
			if err := json.Unmarshal(raw, &request); err != nil { t.Fatal(err) }
			for key, want := range test.want {
				if got, _ := request.Info[key].(string); got != want { t.Errorf("%s = %q, want %q", key, got, want) }
			}
			if request.Info["interval"] == nil || request.Info["otherSource"] != nil || request.Info["typeUrl"] == nil {
				t.Errorf("missing LX track metadata fields: %#v", request.Info)
			}
		})
	}
}
