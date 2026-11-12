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
