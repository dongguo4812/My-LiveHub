package desktop

import (
	"encoding/json"
	"testing"
	"testing/fstest"

	"github.com/dongguo4812/My-LiveHub/app/internal/config"
	"github.com/dongguo4812/My-LiveHub/app/internal/music"
)

func newPlaylistTestService(t *testing.T) *MusicService {
	t.Helper()
	defaults, err := json.Marshal(config.DefaultMusicConfig())
	if err != nil { t.Fatal(err) }
	manager, err := config.NewManager(fstest.MapFS{"music.json": &fstest.MapFile{Data:defaults}}, t.TempDir())
	if err != nil { t.Fatal(err) }
	return NewMusicService(manager, config.DefaultMusicConfig())
}

func TestLXPlaylistImportV2PreservesNativePlaybackMetadata(t *testing.T) {
	service := newPlaylistTestService(t)
	raw := `{"type":"playListPart_v2","data":{"id":"lx-list-1","name":"周杰伦精选","list":[{"id":"tx_001","source":"tx","name":"稻香","singer":"周杰伦","interval":"03:42","meta":{"songId":"001","id":"native-001","strMediaMid":"media-001","albumMid":"album-001","albumName":"魔杰座","picUrl":"https://example.test/cover.jpg","qualitys":[{"type":"320k","size":"8 MiB"}],"_qualitys":{"320k":true}}}]}}`
	playlist, err := service.ImportLXPlaylist(raw)
	if err != nil { t.Fatal(err) }
	if playlist.Name != "周杰伦精选" || len(playlist.Tracks) != 1 { t.Fatalf("unexpected imported playlist: %#v", playlist) }
	track := playlist.Tracks[0]
	if track.SourceID != "tx" || track.LXSourceKey != "tx" || track.ID != "tx_001" { t.Fatalf("track identity not preserved: %#v", track) }
	if track.SourceData["songmid"] != "001" || track.SourceData["song_id"] != "native-001" || track.SourceData["media_mid"] != "media-001" || track.SourceData["album_mid"] != "album-001" {
		t.Fatalf("QQ native metadata missing: %#v", track.SourceData)
	}
	if track.DurationMS != 222000 || track.Album != "魔杰座" || track.CoverURL != "https://example.test/cover.jpg" {
		t.Fatalf("track display metadata mismatch: %#v", track)
	}
	if len(track.Qualities) != 1 || track.Qualities[0].Type != "320k" { t.Fatalf("quality metadata missing: %#v", track.Qualities) }
}

func TestLXPlaylistImportLegacyAndUnsupportedType(t *testing.T) {
	service := newPlaylistTestService(t)
	legacy := `{"type":"playListPart","data":{"name":"旧歌单","list":[{"id":"kg_123_hash","source":"kg","songmid":"123","hash":"hash","name":"晴天","singer":"周杰伦","albumName":"叶惠美","interval":"04:29","types":[{"type":"320k","size":"9 MiB"}]}]}}`
	playlist, err := service.ImportLXPlaylist(legacy)
	if err != nil { t.Fatal(err) }
	if len(playlist.Tracks) != 1 || playlist.Tracks[0].SourceData["hash"] != "hash" {
		t.Fatalf("legacy Kugou metadata not imported: %#v", playlist)
	}
	if _, err := service.ImportLXPlaylist(`{"type":"allData_v2","data":{}}`); err == nil {
		t.Fatal("full backup should not be accepted as a single-list import")
	}
}

func TestPlaylistCRUDAndReplacePreserveTrackPosition(t *testing.T) {
	service := newPlaylistTestService(t)
	playlist, err := service.CreatePlaylist("收藏")
	if err != nil { t.Fatal(err) }
	first := music.Track{ID:"tx_old",SourceID:"tx",LXSourceKey:"tx",Title:"稻香",Artist:"周杰伦"}
	second := music.Track{ID:"wy_second",SourceID:"wy",LXSourceKey:"wy",Title:"晴天",Artist:"周杰伦"}
	if err := service.AddPlaylistTrack(playlist.ID, first); err != nil { t.Fatal(err) }
	if err := service.AddPlaylistTrack(playlist.ID, second); err != nil { t.Fatal(err) }
	replacement := music.Track{ID:"kg_new_hash",SourceID:"kg",LXSourceKey:"kg",Title:"稻香",Artist:"周杰伦",SourceData:map[string]string{"hash":"hash"}}
	if err := service.ReplacePlaylistTrack(playlist.ID, first.ID, replacement); err != nil { t.Fatal(err) }
	got, err := service.GetPlaylist(playlist.ID)
	if err != nil { t.Fatal(err) }
	if len(got.Tracks) != 2 || got.Tracks[0].ID != replacement.ID || got.Tracks[1].ID != second.ID {
		t.Fatalf("replace did not retain the original position: %#v", got.Tracks)
	}
	if err := service.RenamePlaylist(playlist.ID, "我的收藏"); err != nil { t.Fatal(err) }
	if err := service.RemovePlaylistTrack(playlist.ID, second.ID); err != nil { t.Fatal(err) }
	if err := service.DeletePlaylist(playlist.ID); err != nil { t.Fatal(err) }
	if _, err := service.GetPlaylist(playlist.ID); err == nil { t.Fatal("deleted playlist is still present") }
}

func TestPlaylistPersistenceDoesNotStoreTemporaryPlaybackResource(t *testing.T) {
	service := newPlaylistTestService(t)
	playlist, err := service.CreatePlaylist("资源安全")
	if err != nil { t.Fatal(err) }
	track := music.Track{ID:"wy_1",SourceID:"wy",Title:"歌曲",Artist:"歌手",PlaybackResource:&music.PlaybackResource{URL:"https://example.test/signed.mp3"},SourceData:map[string]string{"songmid":"1","play_url":"https://example.test/signed.mp3","lrc_url":"https://example.test/lyric.lrc"}}
	if err := service.AddPlaylistTrack(playlist.ID, track); err != nil { t.Fatal(err) }
	got, err := service.GetPlaylist(playlist.ID)
	if err != nil { t.Fatal(err) }
	saved := got.Tracks[0]
	if saved.PlaybackResource != nil || saved.SourceData["play_url"] != "" || saved.SourceData["lrc_url"] == "" {
		t.Fatalf("temporary playback data was persisted or provider metadata was lost: %#v", saved)
	}
}
