package desktop

import (
	"encoding/json"
	"testing"

	"github.com/dongguo4812/My-LiveHub/app/internal/music"
)

func TestChooseLXTrackQualityUsesLXDefault128k(t *testing.T) {
	track := music.Track{Qualities: []music.TrackQuality{{Type:"128k"},{Type:"320k"},{Type:"flac"}}}
	got, err := chooseLXTrackQuality(track, music.LXDeclaredSource{Qualitys:[]string{"128k","320k"}})
	if err != nil { t.Fatal(err) }
	if got.Type != "128k" { t.Fatalf("selected %q, want LX default 128k", got.Type) }
}

func TestChooseLXTrackQualityHonorsExplicitSelection(t *testing.T) {
	track := music.Track{SelectedQuality:"128k", Qualities:[]music.TrackQuality{{Type:"128k"},{Type:"flac"}}}
	got, err := chooseLXTrackQuality(track, music.LXDeclaredSource{Qualitys:[]string{"128k","flac"}})
	if err != nil { t.Fatal(err) }
	if got.Type != "128k" { t.Fatalf("selected %q, want 128k", got.Type) }
	if _, err := chooseLXTrackQuality(track, music.LXDeclaredSource{Qualitys:[]string{"flac"}}); err == nil { t.Fatal("expected unsupported selected quality error") }
}

func decodeLXRequest(t *testing.T, track music.Track, selected music.TrackQuality) (map[string]any, map[string]any) {
	t.Helper()
	payload, err := buildLXMusicURLRequest(track, selected)
	if err != nil { t.Fatal(err) }
	var request map[string]any
	if err := json.Unmarshal(payload, &request); err != nil { t.Fatal(err) }
	info, ok := request["info"].(map[string]any)
	if !ok { t.Fatalf("LX request info envelope missing: %#v", request) }
	musicInfo, ok := info["musicInfo"].(map[string]any)
	if !ok { t.Fatalf("LX request info.musicInfo missing: %#v", info) }
	if info["type"] != selected.Type { t.Fatalf("selected type = %#v, want %q", info["type"], selected.Type) }
	if request["action"] != "musicUrl" || request["source"] != track.SourceID { t.Fatalf("wrong request envelope: %#v", request) }
	if musicInfo["source"] != track.SourceID || musicInfo["name"] != track.Title || musicInfo["singer"] != track.Artist {
		t.Fatalf("wrong legacy LX MusicInfo fields: %#v", musicInfo)
	}
	if _, exists := musicInfo["meta"]; exists { t.Fatalf("source script contract must be legacy top-level fields, got meta: %#v", musicInfo) }
	if musicInfo["typeUrl"] == nil || musicInfo["types"] == nil || musicInfo["_types"] == nil {
		t.Fatalf("legacy typeUrl/types/_types fields missing: %#v", musicInfo)
	}
	return info, musicInfo
}

func TestBuildLXMusicURLRequestUsesLegacySourceScriptContract(t *testing.T) {
	track := music.Track{
		ID:"tx_mid-1", SourceID:"tx", LXSourceKey:"tx", Title:"歌曲", Artist:"歌手", Album:"专辑", CoverURL:"https://img.test/a.jpg", DurationMS:182000,
		SourceData:map[string]string{"songmid":"mid-1","song_id":"123","media_mid":"media-1","album_mid":"album-1","album_id":"album-1"},
		Qualities:[]music.TrackQuality{{Type:"128k"},{Type:"320k"}},
	}
	_, info := decodeLXRequest(t, track, music.TrackQuality{Type:"320k"})
	if info["songmid"] != "mid-1" || info["strMediaMid"] != "media-1" || info["songId"] != float64(123) || info["albumMid"] != "album-1" {
		t.Fatalf("native QQ fields must be top-level for LX source scripts: %#v", info)
	}
	if info["interval"] != "03:02" || info["albumName"] != "专辑" || info["img"] != "https://img.test/a.jpg" {
		t.Fatalf("legacy common fields missing: %#v", info)
	}
	if _, exists := info["id"]; exists { t.Fatalf("legacy toOldMusicInfo does not expose id: %#v", info) }
	qualityMap, ok := info["_types"].(map[string]any)
	if !ok || qualityMap["320k"] == nil { t.Fatalf("LX _types metadata missing: %#v", info["_types"]) }
	qualityList, ok := info["types"].([]any)
	if !ok || len(qualityList) != 2 { t.Fatalf("LX types list missing: %#v", info["types"]) }
}

func TestLXMusicInfoIntervalMatchesSourceSpecificNullBehavior(t *testing.T) {
	track := music.Track{ID:"tx_zero", SourceID:"tx", LXSourceKey:"tx", SourceData:map[string]string{"songmid":"zero"}}
	_, info := decodeLXRequest(t, track, music.TrackQuality{Type:"128k"})
	if info["interval"] != nil { t.Fatalf("QQ zero interval should be null, got %#v", info["interval"]) }
	track.SourceID = "kw"; track.LXSourceKey = "kw"; track.ID = "kw_zero"
	_, info = decodeLXRequest(t, track, music.TrackQuality{Type:"128k"})
	if info["interval"] != "--/--" { t.Fatalf("non-QQ zero interval should use LX placeholder, got %#v", info["interval"]) }
}

func TestBuildLXMusicURLRequestMapsMiguCopyrightAndLyricsAtTopLevel(t *testing.T) {
	track := music.Track{
		ID:"mg_1", SourceID:"mg", LXSourceKey:"mg", Title:"歌", Album:"专辑",
		SourceData:map[string]string{"songmid":"1","copyright_id":"copyright-1","album_id":"album-1","lrc_url":"lrc","mrc_url":"mrc","trc_url":"trc"},
		Qualities:[]music.TrackQuality{{Type:"128k"}},
	}
	_, info := decodeLXRequest(t, track, music.TrackQuality{Type:"128k"})
	for key, want := range map[string]string{"copyrightId":"copyright-1","albumId":"album-1","lrcUrl":"lrc","mrcUrl":"mrc","trcUrl":"trc"} {
		if info[key] != want { t.Errorf("%s = %#v, want %q", key, info[key], want) }
	}
}

func TestBuildLXMusicURLRequestPreservesPlatformNativeFieldsAndHashes(t *testing.T) {
	tests := []struct {
		name string
		track music.Track
		selected music.TrackQuality
		want map[string]string
	}{
		{"kuwo", music.Track{ID:"kw_kw-id",SourceID:"kw",LXSourceKey:"kw",Title:"song",SourceData:map[string]string{"songmid":"kw-id","album_id":"kw-album"},Qualities:[]music.TrackQuality{{Type:"flac",Size:"5.0mb"}}}, music.TrackQuality{Type:"flac"}, map[string]string{"songmid":"kw-id","albumId":"kw-album"}},
		{"kugou", music.Track{ID:"kg-id_base-hash",SourceID:"kg",LXSourceKey:"kg",Title:"song",SourceData:map[string]string{"songmid":"kg-id","hash":"base-hash","album_id":"kg-album","album_audio_id":"audio-id"},Qualities:[]music.TrackQuality{{Type:"320k",SourceData:map[string]string{"hash":"320-hash"}}}}, music.TrackQuality{Type:"320k",SourceData:map[string]string{"hash":"320-hash"}}, map[string]string{"songmid":"kg-id","hash":"base-hash","albumId":"kg-album","albumAudioId":"audio-id"}},
		{"qq", music.Track{ID:"tx_tx-id",SourceID:"tx",LXSourceKey:"tx",Title:"song",SourceData:map[string]string{"songmid":"tx-id","song_id":"123","media_mid":"media-id","album_mid":"album-mid","album_id":"album-mid"}}, music.TrackQuality{Type:"320k"}, map[string]string{"songmid":"tx-id","strMediaMid":"media-id","albumMid":"album-mid"}},
		{"netease", music.Track{ID:"wy_wy-id",SourceID:"wy",LXSourceKey:"wy",Title:"song",SourceData:map[string]string{"songmid":"wy-id","album_id":"wy-album"}}, music.TrackQuality{Type:"flac"}, map[string]string{"songmid":"wy-id","albumId":"wy-album"}},
		{"migu", music.Track{ID:"mg_mg-id",SourceID:"mg",LXSourceKey:"mg",Title:"song",SourceData:map[string]string{"songmid":"mg-id","copyright_id":"copyright","album_id":"mg-album","lrc_url":"lrc","mrc_url":"mrc","trc_url":"trc"}}, music.TrackQuality{Type:"128k"}, map[string]string{"songmid":"mg-id","copyrightId":"copyright","albumId":"mg-album","lrcUrl":"lrc","mrcUrl":"mrc","trcUrl":"trc"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, info := decodeLXRequest(t, test.track, test.selected)
			for key, want := range test.want {
				if got, _ := info[key].(string); got != want { t.Errorf("musicInfo.%s = %q, want %q", key, got, want) }
			}
			if test.name == "kugou" && info["hash"] != "base-hash" { t.Errorf("top-level hash = %#v, want base-hash", info["hash"]) }
			if test.name == "kuwo" {
				qualityMap := info["_types"].(map[string]any)
				if qualityMap["flac"].(map[string]any)["size"] != "5.0MB" { t.Errorf("Kuwo _types size must be uppercased: %#v", qualityMap["flac"]) }
				qualityList := info["types"].([]any)
				if qualityList[0].(map[string]any)["size"] != "5.0mb" { t.Errorf("Kuwo types size must retain original case: %#v", qualityList[0]) }
			}
		})
	}
}
