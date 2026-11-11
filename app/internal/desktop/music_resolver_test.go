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
