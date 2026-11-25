package desktop

import (
	"bytes"
	"compress/gzip"
	"encoding/base64"
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


func TestLXPlaylistImportGzipFullExportImportsAllLists(t *testing.T) {
	service := newPlaylistTestService(t)
	plain := "{\"type\":\"playList_v2\",\"data\":[{\"id\":\"default\",\"name\":\"默认列表\",\"list\":[{\"id\":\"wy_1\",\"source\":\"wy\",\"name\":\"歌曲一\",\"singer\":\"歌手一\",\"interval\":\"03:00\",\"meta\":{\"songId\":1}}]},{\"id\":\"wy_favorites\",\"name\":\"我的收藏\",\"list\":[{\"id\":\"kg_2_hash\",\"source\":\"kg\",\"name\":\"歌曲二\",\"singer\":\"歌手二\",\"interval\":\"04:00\",\"meta\":{\"songId\":2,\"hash\":\"hash\"}}]},{\"id\":\"empty\",\"name\":\"空列表\",\"list\":[]}]}"
	var compressed bytes.Buffer
	writer := gzip.NewWriter(&compressed)
	if _, err := writer.Write([]byte(plain)); err != nil { t.Fatal(err) }
	if err := writer.Close(); err != nil { t.Fatal(err) }
	playlists, err := service.ImportLXPlaylists("base64:" + base64.StdEncoding.EncodeToString(compressed.Bytes()))
	if err != nil { t.Fatal(err) }
	if len(playlists) != 2 { t.Fatalf("expected 2 non-empty playlists, got %d", len(playlists)) }
	if playlists[0].Name != "默认列表" || len(playlists[0].Tracks) != 1 || playlists[1].Name != "我的收藏" || len(playlists[1].Tracks) != 1 {
		t.Fatalf("unexpected imported playlists: %#v", playlists)
	}
	if playlists[1].Tracks[0].SourceData["hash"] != "hash" { t.Fatalf("native source metadata lost: %#v", playlists[1].Tracks[0]) }
}


func TestPlaylistPlaybackModesAdvanceOnlyPlaylistTracks(t *testing.T) {
	service := newPlaylistTestService(t)
	service.playlistPlaybackActive = true
	service.playlistPlaybackTracks = []music.Track{
		{ID:"one", SourceID:"tx", Title:"One"},
		{ID:"two", SourceID:"tx", Title:"Two"},
	}
	service.playlistPlaybackMode = "sequential"
	first, ok := service.takeNextPlaylistTrack()
	if !ok || first.Track.ID != "one" || first.RequestedBy != playlistPlayMarker {
		t.Fatalf("first sequential track = %#v, %v", first, ok)
	}
	second, ok := service.takeNextPlaylistTrack()
	if !ok || second.Track.ID != "two" {
		t.Fatalf("second sequential track = %#v, %v", second, ok)
	}
	if _, ok := service.takeNextPlaylistTrack(); ok || service.hasActivePlaylistPlayback() {
		t.Fatal("sequential mode should finish after the last playlist track")
	}

	service.playlistPlaybackActive = true
	service.playlistPlaybackIndex = 2
	service.playlistPlaybackMode = "loop"
	looped, ok := service.takeNextPlaylistTrack()
	if !ok || looped.Track.ID != "one" {
		t.Fatalf("loop mode did not restart at the first track: %#v, %v", looped, ok)
	}

	service.playlistPlaybackActive = true
	service.playlistPlaybackIndex = 0
	service.playlistPlaybackMode = "random"
	randomFirst, ok := service.takeNextPlaylistTrack()
	if !ok || randomFirst.Track.ID != "one" {
		t.Fatalf("random mode did not start from selected track: %#v, %v", randomFirst, ok)
	}
	randomNext, ok := service.takeNextPlaylistTrack()
	if !ok || randomNext.Track.ID == randomFirst.Track.ID {
		t.Fatalf("random mode should choose another track when possible: %#v, %v", randomNext, ok)
	}
}

func TestPlaylistPlaybackModeSettingIsValidated(t *testing.T) {
	service := newPlaylistTestService(t)
	for _, mode := range []string{"sequential", "loop", "random"} {
		if err := service.SetPlaylistPlaybackMode(mode); err != nil {
			t.Fatalf("SetPlaylistPlaybackMode(%q): %v", mode, err)
		}
		if got := service.GetPlaylistPlaybackMode(); got != mode {
			t.Fatalf("mode = %q, want %q", got, mode)
		}
	}
	if err := service.SetPlaylistPlaybackMode("audience-first-random"); err == nil {
		t.Fatal("unsupported playlist playback mode should be rejected")
	}
}


func TestPlaylistPlaybackDefersToAudienceQueue(t *testing.T) {
	service := newPlaylistTestService(t)
	engine := &queueAdvanceTestEngine{}
	service.engine = engine
	service.value.Enabled = true
	defer service.cancelPlaybackWatcher()

	playlist, err := service.CreatePlaylist("优先级测试")
	if err != nil { t.Fatal(err) }
	playlistTrack := music.Track{ID:"playlist-track", SourceID:"tx", LXSourceKey:"tx", Title:"歌单歌曲", Artist:"歌手"}
	if err := service.AddPlaylistTrack(playlist.ID, playlistTrack); err != nil { t.Fatal(err) }
	audienceTrack := music.Track{ID:"audience-track", SourceID:"tx", LXSourceKey:"tx", Title:"观众点播", Artist:"观众"}
	if _, err := service.queue.Enqueue(audienceTrack, "viewer"); err != nil { t.Fatal(err) }

	if err := service.PlayPlaylist(playlist.ID, ""); err != nil {
		t.Fatalf("PlayPlaylist returned error: %v", err)
	}
	current := service.queue.Snapshot().Current
	if current == nil || current.Track.ID != audienceTrack.ID {
		t.Fatalf("audience request did not take priority: %#v", current)
	}

	service.cancelPlaybackWatcher()
	service.queue.FinishCurrent()
	if err := service.PlayNext(); err != nil {
		t.Fatalf("start playlist after audience request: %v", err)
	}
	current = service.queue.Snapshot().Current
	if current == nil || current.Track.ID != playlistTrack.ID || current.RequestedBy != playlistPlayMarker {
		t.Fatalf("playlist did not resume after audience queue drained: %#v", current)
	}
}
