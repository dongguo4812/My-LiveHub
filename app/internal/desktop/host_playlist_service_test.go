package desktop

import (
	"encoding/json"
	"testing"
)

func TestLXBackupTrackPreservesNumericProviderIDs(t *testing.T) {
	var song map[string]json.RawMessage
	if err := json.Unmarshal([]byte(`{
		"id": 123456,
		"source": "wy",
		"name": "测试歌曲",
		"meta": {"songmid": 123456, "album_id": 789}
	}`), &song); err != nil {
		t.Fatal(err)
	}
	track, ok := lxBackupTrack(song)
	if !ok {
		t.Fatal("expected backup song to be imported")
	}
	if got := track.SourceData["songmid"]; got != "123456" {
		t.Fatalf("songmid = %q, want %q", got, "123456")
	}
	if got := track.SourceData["album_id"]; got != "789" {
		t.Fatalf("album_id = %q, want %q", got, "789")
	}
}

func TestLXBackupTrackPreservesNumericTopLevelIDs(t *testing.T) {
	var song map[string]json.RawMessage
	if err := json.Unmarshal([]byte(`{
		"id": 123456,
		"source": "wy",
		"name": "测试歌曲",
		"songmid": 123456
	}`), &song); err != nil {
		t.Fatal(err)
	}
	track, ok := lxBackupTrack(song)
	if !ok {
		t.Fatal("expected backup song to be imported")
	}
	if got := track.SourceData["songmid"]; got != "123456" {
		t.Fatalf("songmid = %q, want %q", got, "123456")
	}
}

func TestLXBackupTrackNormalizesMetaSongIDForLyrics(t *testing.T) {
	var song map[string]json.RawMessage
	if err := json.Unmarshal([]byte(`{
		"id": 123456,
		"source": "wy",
		"name": "测试歌曲",
		"meta": {"songId": 987654, "album_id": 789}
	}`), &song); err != nil {
		t.Fatal(err)
	}
	track, ok := lxBackupTrack(song)
	if !ok {
		t.Fatal("expected backup song to be imported")
	}
	if got := track.SourceData["songId"]; got != "987654" {
		t.Fatalf("songId = %q, want %q", got, "987654")
	}
	if got := track.SourceData["songmid"]; got != "987654" {
		t.Fatalf("normalized songmid = %q, want %q", got, "987654")
	}
}

func TestLXBackupTrackMatchesRealNetEaseLXPlaylistShape(t *testing.T) {
 var song map[string]json.RawMessage
 if err := json.Unmarshal([]byte(`{
  "id": "wy_1500569811",
  "source": "wy",
  "name": "阿拉斯加海湾",
  "singer": "蓝心羽",
  "meta": {"songId": 1500569811, "albumName": "阿拉斯加海湾"}
 }`), &song); err != nil {
  t.Fatal(err)
 }
 track, ok := lxBackupTrack(song)
 if !ok {
  t.Fatal("expected real NetEase LX backup song to be imported")
 }
 if track.ID != "wy_1500569811" {
  t.Fatalf("track ID = %q, want %q", track.ID, "wy_1500569811")
 }
 if got := track.SourceData["songmid"]; got != "1500569811" {
  t.Fatalf("normalized songmid = %q, want %q", got, "1500569811")
 }
}
