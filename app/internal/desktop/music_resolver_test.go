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

func decodeLXRequest(t *testing.T, track music.Track, selected music.TrackQuality) (map[string]any, map[string]any, map[string]any) {
	t.Helper()
	payload, err := buildLXMusicURLRequest(track, selected)
	if err != nil { t.Fatal(err) }
	var request map[string]any
	if err := json.Unmarshal(payload, &request); err != nil { t.Fatal(err) }
	info, ok := request["info"].(map[string]any)
	if !ok { t.Fatalf("LX request info envelope missing: %#v", request) }
	musicInfo, ok := info["musicInfo"].(map[string]any)
	if !ok { t.Fatalf("LX request info.musicInfo missing: %#v", info) }
	meta, ok := musicInfo["meta"].(map[string]any)
	if !ok { t.Fatalf("LX musicInfo.meta missing: %#v", musicInfo) }
	if info["type"] != selected.Type { t.Fatalf("selected type = %#v, want %q", info["type"], selected.Type) }
	if request["action"] != "musicUrl" || request["source"] != track.SourceID { t.Fatalf("wrong request envelope: %#v", request) }
	if musicInfo["id"] != track.ID { t.Fatalf("LX MusicInfo id = %#v, want %q", musicInfo["id"], track.ID) }
	if musicInfo["name"] != track.Title || musicInfo["singer"] != track.Artist || musicInfo["source"] != track.SourceID {
		t.Fatalf("wrong LX MusicInfo base fields: %#v", musicInfo)
	}
	return info, musicInfo, meta
}

func TestBuildLXMusicURLRequestUsesNestedLXMusicInfoContract(t *testing.T) {
	track := music.Track{
		ID:"tx_mid-1", SourceID:"tx", LXSourceKey:"tx", Title:"歌曲", Artist:"歌手", Album:"专辑", DurationMS:182000,
		SourceData:map[string]string{"songmid":"mid-1","song_id":"123","media_mid":"media-1","album_mid":"album-1","album_id":"album-1"},
		Qualities:[]music.TrackQuality{{Type:"128k"},{Type:"320k"}},
	}
	_, musicInfo, meta := decodeLXRequest(t, track, music.TrackQuality{Type:"320k"})
	if musicInfo["interval"] != "03:02" { t.Fatalf("wrong interval: %#v", musicInfo["interval"]) }
	if meta["songId"] != "mid-1" || meta["strMediaMid"] != "media-1" || meta["id"] != float64(123) || meta["albumMid"] != "album-1" {
		t.Fatalf("native QQ metadata missing: %#v", meta)
	}
	qualityMap, ok := meta["_qualitys"].(map[string]any)
	if !ok || qualityMap["320k"] == nil { t.Fatalf("LX _qualitys metadata missing: %#v", meta["_qualitys"]) }
	qualityList, ok := meta["qualitys"].([]any)
	if !ok || len(qualityList) != 2 { t.Fatalf("LX qualitys list missing: %#v", meta["qualitys"]) }
}


func TestLXMusicInfoIntervalMatchesSourceSpecificNullBehavior(t *testing.T) {
	track := music.Track{ID:"tx_zero", SourceID:"tx", LXSourceKey:"tx", SourceData:map[string]string{"songmid":"zero"}}
	_, musicInfo, _ := decodeLXRequest(t, track, music.TrackQuality{Type:"128k"})
	if musicInfo["interval"] != nil { t.Fatalf("QQ zero interval should be null, got %#v", musicInfo["interval"]) }
	track.SourceID = "kw"; track.LXSourceKey = "kw"; track.ID = "kw_zero"
	_, musicInfo, _ = decodeLXRequest(t, track, music.TrackQuality{Type:"128k"})
	if musicInfo["interval"] != "--/--" { t.Fatalf("non-QQ zero interval should use LX placeholder, got %#v", musicInfo["interval"]) }
}

func TestBuildLXMusicURLRequestMapsMiguCopyrightAndLyrics(t *testing.T) {
	track := music.Track{
		ID:"mg_1", SourceID:"mg", Title:"歌", Album:"专辑",
		SourceData:map[string]string{"songmid":"1","copyright_id":"copyright-1","lrc_url":"lrc","mrc_url":"mrc","trc_url":"trc"},
		Qualities:[]music.TrackQuality{{Type:"128k"}},
	}
	_, _, meta := decodeLXRequest(t, track, music.TrackQuality{Type:"128k"})
	for key, want := range map[string]string{"copyrightId":"copyright-1","lrcUrl":"lrc","mrcUrl":"mrc","trcUrl":"trc"} {
		if meta[key] != want { t.Errorf("%s = %#v, want %q", key, meta[key], want) }
	}
}

func TestBuildLXMusicURLRequestPreservesPlatformNativeFieldsAndHashes(t *testing.T) {
	tests := []struct {
		name string
		track music.Track
		selected music.TrackQuality
		want map[string]string
	}{
		{"kuwo", music.Track{ID:"kw_kw-id",SourceID:"kw",LXSourceKey:"kw",Title:"song",SourceData:map[string]string{"songmid":"kw-id","album_id":"kw-album"},Qualities:[]music.TrackQuality{{Type:"flac",Size:"30 MB"}}}, music.TrackQuality{Type:"flac"}, map[string]string{"songId":"kw-id","albumId":"kw-album"}},
		{"kugou", music.Track{ID:"kg-id_base-hash",SourceID:"kg",LXSourceKey:"kg",Title:"song",SourceData:map[string]string{"songmid":"kg-id","hash":"base-hash","album_id":"kg-album","album_audio_id":"audio-id"},Qualities:[]music.TrackQuality{{Type:"320k",SourceData:map[string]string{"hash":"320-hash"}}}}, music.TrackQuality{Type:"320k",SourceData:map[string]string{"hash":"320-hash"}}, map[string]string{"songId":"kg-id","hash":"base-hash","albumId":"kg-album","albumAudioId":"audio-id"}},
		{"qq", music.Track{ID:"tx_tx-id",SourceID:"tx",LXSourceKey:"tx",Title:"song",SourceData:map[string]string{"songmid":"tx-id","song_id":"123","media_mid":"media-id","album_mid":"album-mid","album_id":"album-mid"}}, music.TrackQuality{Type:"320k"}, map[string]string{"songId":"tx-id","strMediaMid":"media-id","albumMid":"album-mid"}},
		{"netease", music.Track{ID:"wy_wy-id",SourceID:"wy",LXSourceKey:"wy",Title:"song",SourceData:map[string]string{"songmid":"wy-id","album_id":"wy-album"}}, music.TrackQuality{Type:"flac"}, map[string]string{"songId":"wy-id","albumId":"wy-album"}},
		{"migu", music.Track{ID:"mg_mg-id",SourceID:"mg",LXSourceKey:"mg",Title:"song",SourceData:map[string]string{"songmid":"mg-id","copyright_id":"copyright","album_id":"mg-album","lrc_url":"lrc","mrc_url":"mrc","trc_url":"trc"}}, music.TrackQuality{Type:"128k"}, map[string]string{"songId":"mg-id","copyrightId":"copyright","albumId":"mg-album","lrcUrl":"lrc","mrcUrl":"mrc","trcUrl":"trc"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, _, meta := decodeLXRequest(t, test.track, test.selected)
			for key, want := range test.want {
				if got, _ := meta[key].(string); got != want { t.Errorf("meta.%s = %q, want %q", key, got, want) }
				if test.name == "qq" && meta["id"] != float64(123) { t.Errorf("meta.id = %#v, want numeric song ID 123", meta["id"]) }
			}
			if meta["qualitys"] == nil || meta["_qualitys"] == nil { t.Errorf("missing LX quality metadata: %#v", meta) }
		})
	}
}
