package desktop

import (
	"bytes"
	"compress/gzip"
	"context"
	crand "crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	mathrand "math/rand"
	"strconv"
	"strings"
	"time"

	"github.com/dongguo4812/My-LiveHub/app/internal/music"
)

const musicPlaylistStoreFile = "music_playlists.json"
const musicPlaylistStoreLimit int64 = 32 << 20
const musicPlaylistLimit = 200
const musicPlaylistTrackLimit = 5000

// MusicPlaylist is a user-owned playlist. Track metadata is stored locally;
// temporary playback URLs and resolved media resources are never persisted.
type MusicPlaylist struct {
	ID        string        `json:"id"`
	Name      string        `json:"name"`
	Tracks    []music.Track `json:"tracks"`
	CreatedAt time.Time     `json:"created_at"`
	UpdatedAt time.Time     `json:"updated_at"`
}

func newMusicPlaylistID() (string, error) {
	var raw [16]byte
	if _, err := crand.Read(raw[:]); err != nil { return "", fmt.Errorf("generate playlist ID: %w", err) }
	return hex.EncodeToString(raw[:]), nil
}

func cloneMusicPlaylist(value MusicPlaylist) MusicPlaylist {
	result := value
	result.Tracks = make([]music.Track, len(value.Tracks))
	for i, track := range value.Tracks {
		result.Tracks[i] = track
		if track.SourceData != nil {
			result.Tracks[i].SourceData = make(map[string]string, len(track.SourceData))
			for key, item := range track.SourceData { result.Tracks[i].SourceData[key] = item }
		}
		result.Tracks[i].Qualities = append([]music.TrackQuality(nil), track.Qualities...)
		for j := range result.Tracks[i].Qualities {
			if track.Qualities[j].SourceData != nil {
				result.Tracks[i].Qualities[j].SourceData = make(map[string]string, len(track.Qualities[j].SourceData))
				for key, item := range track.Qualities[j].SourceData { result.Tracks[i].Qualities[j].SourceData[key] = item }
			}
		}
	}
	if result.Tracks == nil { result.Tracks = []music.Track{} }
	return result
}

func (s *MusicService) loadPlaylistsLocked() ([]MusicPlaylist, error) {
	if s == nil || s.manager == nil { return nil, errors.New("music playlist storage is unavailable") }
	data, exists, err := s.manager.ReadLocalJSON(musicPlaylistStoreFile, musicPlaylistStoreLimit)
	if err != nil { return nil, fmt.Errorf("read music playlists: %w", err) }
	if !exists { return []MusicPlaylist{}, nil }
	var playlists []MusicPlaylist
	if err := json.Unmarshal(data, &playlists); err != nil { return nil, fmt.Errorf("decode music playlists: %w", err) }
	if len(playlists) > musicPlaylistLimit { return nil, fmt.Errorf("music playlist store exceeds %d playlists", musicPlaylistLimit) }
	for i := range playlists {
		if strings.TrimSpace(playlists[i].ID) == "" || strings.TrimSpace(playlists[i].Name) == "" {
			return nil, fmt.Errorf("music playlist entry %d has an empty ID or name", i)
		}
		if len(playlists[i].Tracks) > musicPlaylistTrackLimit { return nil, fmt.Errorf("playlist %q exceeds %d tracks", playlists[i].Name, musicPlaylistTrackLimit) }
		if playlists[i].Tracks == nil { playlists[i].Tracks = []music.Track{} }
	}
	return playlists, nil
}

func (s *MusicService) savePlaylistsLocked(playlists []MusicPlaylist) error {
	if s == nil || s.manager == nil { return errors.New("music playlist storage is unavailable") }
	if len(playlists) > musicPlaylistLimit { return fmt.Errorf("最多保存 %d 个歌单", musicPlaylistLimit) }
	data, err := json.MarshalIndent(playlists, "", "  ")
	if err != nil { return fmt.Errorf("encode music playlists: %w", err) }
	if err := s.manager.WriteLocalJSON(musicPlaylistStoreFile, data, musicPlaylistStoreLimit); err != nil {
		return fmt.Errorf("save music playlists: %w", err)
	}
	return nil
}

func (s *MusicService) ListPlaylists() ([]MusicPlaylist, error) {
	if s == nil { return nil, errors.New("music service is unavailable") }
	s.playlistMu.Lock()
	defer s.playlistMu.Unlock()
	playlists, err := s.loadPlaylistsLocked()
	if err != nil { return nil, err }
	result := make([]MusicPlaylist, len(playlists))
	for i := range playlists { result[i] = cloneMusicPlaylist(playlists[i]) }
	return result, nil
}

func (s *MusicService) GetPlaylist(id string) (MusicPlaylist, error) {
	if s == nil { return MusicPlaylist{}, errors.New("music service is unavailable") }
	s.playlistMu.Lock()
	defer s.playlistMu.Unlock()
	playlists, err := s.loadPlaylistsLocked()
	if err != nil { return MusicPlaylist{}, err }
	for _, playlist := range playlists {
		if playlist.ID == id { return cloneMusicPlaylist(playlist), nil }
	}
	return MusicPlaylist{}, fmt.Errorf("歌单不存在：%s", id)
}

func (s *MusicService) CreatePlaylist(name string) (MusicPlaylist, error) {
	if s == nil { return MusicPlaylist{}, errors.New("music service is unavailable") }
	name = strings.TrimSpace(name)
	if name == "" { return MusicPlaylist{}, errors.New("歌单名称不能为空") }
	if len([]rune(name)) > 80 { return MusicPlaylist{}, errors.New("歌单名称不能超过 80 个字符") }
	s.playlistMu.Lock()
	defer s.playlistMu.Unlock()
	playlists, err := s.loadPlaylistsLocked()
	if err != nil { return MusicPlaylist{}, err }
	if len(playlists) >= musicPlaylistLimit { return MusicPlaylist{}, fmt.Errorf("最多创建 %d 个歌单", musicPlaylistLimit) }
	id, err := newMusicPlaylistID()
	if err != nil { return MusicPlaylist{}, err }
	now := time.Now().UTC()
	playlist := MusicPlaylist{ID:id, Name:name, Tracks:[]music.Track{}, CreatedAt:now, UpdatedAt:now}
	playlists = append(playlists, playlist)
	if err := s.savePlaylistsLocked(playlists); err != nil { return MusicPlaylist{}, err }
	return cloneMusicPlaylist(playlist), nil
}

func (s *MusicService) RenamePlaylist(id, name string) error {
	name = strings.TrimSpace(name)
	if name == "" { return errors.New("歌单名称不能为空") }
	if len([]rune(name)) > 80 { return errors.New("歌单名称不能超过 80 个字符") }
	return s.updatePlaylist(id, func(playlist *MusicPlaylist) error { playlist.Name = name; return nil })
}

func (s *MusicService) DeletePlaylist(id string) error {
	if s == nil { return errors.New("music service is unavailable") }
	s.playlistMu.Lock()
	defer s.playlistMu.Unlock()
	playlists, err := s.loadPlaylistsLocked()
	if err != nil { return err }
	for i := range playlists {
		if playlists[i].ID != id { continue }
		playlists = append(playlists[:i], playlists[i+1:]...)
		return s.savePlaylistsLocked(playlists)
	}
	return fmt.Errorf("歌单不存在：%s", id)
}

func (s *MusicService) updatePlaylist(id string, update func(*MusicPlaylist) error) error {
	if s == nil { return errors.New("music service is unavailable") }
	s.playlistMu.Lock()
	defer s.playlistMu.Unlock()
	playlists, err := s.loadPlaylistsLocked()
	if err != nil { return err }
	for i := range playlists {
		if playlists[i].ID != id { continue }
		if err := update(&playlists[i]); err != nil { return err }
		playlists[i].UpdatedAt = time.Now().UTC()
		return s.savePlaylistsLocked(playlists)
	}
	return fmt.Errorf("歌单不存在：%s", id)
}

func (s *MusicService) AddPlaylistTrack(playlistID string, track music.Track) error {
	if err := validatePlaylistTrack(track); err != nil { return err }
	return s.updatePlaylist(playlistID, func(playlist *MusicPlaylist) error {
		if len(playlist.Tracks) >= musicPlaylistTrackLimit { return fmt.Errorf("歌单最多保存 %d 首歌曲", musicPlaylistTrackLimit) }
		for _, existing := range playlist.Tracks {
			if existing.SourceID == track.SourceID && existing.ID == track.ID { return errors.New("这首歌曲已在歌单中") }
		}
		playlist.Tracks = append(playlist.Tracks, sanitizePlaylistTrack(track))
		return nil
	})
}

func (s *MusicService) RemovePlaylistTrack(playlistID, trackID string) error {
	return s.updatePlaylist(playlistID, func(playlist *MusicPlaylist) error {
		for i := range playlist.Tracks {
			if playlist.Tracks[i].ID == trackID {
				playlist.Tracks = append(playlist.Tracks[:i], playlist.Tracks[i+1:]...)
				return nil
			}
		}
		return fmt.Errorf("歌曲不存在：%s", trackID)
	})
}

func (s *MusicService) MovePlaylistTrack(playlistID, trackID string, targetIndex int) error {
	return s.updatePlaylist(playlistID, func(playlist *MusicPlaylist) error {
		if targetIndex < 0 || targetIndex >= len(playlist.Tracks) { return fmt.Errorf("目标位置 %d 超出歌单范围", targetIndex) }
		from := -1
		for i := range playlist.Tracks { if playlist.Tracks[i].ID == trackID { from = i; break } }
		if from < 0 { return fmt.Errorf("歌曲不存在：%s", trackID) }
		item := playlist.Tracks[from]
		if from < targetIndex { copy(playlist.Tracks[from:targetIndex], playlist.Tracks[from+1:targetIndex+1]) } else if from > targetIndex { copy(playlist.Tracks[targetIndex+1:from+1], playlist.Tracks[targetIndex:from]) }
		playlist.Tracks[targetIndex] = item
		return nil
	})
}

func (s *MusicService) ReplacePlaylistTrack(playlistID, oldTrackID string, replacement music.Track) error {
	if err := validatePlaylistTrack(replacement); err != nil { return err }
	return s.updatePlaylist(playlistID, func(playlist *MusicPlaylist) error {
		for i := range playlist.Tracks {
			if playlist.Tracks[i].ID != oldTrackID { continue }
			for j, existing := range playlist.Tracks {
				if j != i && existing.SourceID == replacement.SourceID && existing.ID == replacement.ID { return errors.New("目标歌曲已存在于当前歌单") }
			}
			playlist.Tracks[i] = sanitizePlaylistTrack(replacement)
			return nil
		}
		return fmt.Errorf("待替换歌曲不存在：%s", oldTrackID)
	})
}

// PlayPlaylist starts a playlist from the selected track. Pending audience requests
// always take precedence; playlist mode only advances this playlist's own tracks.
func (s *MusicService) PlayPlaylist(playlistID, startTrackID string) error {
	playlist, err := s.GetPlaylist(playlistID)
	if err != nil { return err }
	if len(playlist.Tracks) == 0 { return errors.New("歌单为空，无法播放") }
	start := 0
	if startTrackID != "" {
		start = -1
		for i := range playlist.Tracks { if playlist.Tracks[i].ID == startTrackID { start = i; break } }
		if start < 0 { return fmt.Errorf("歌曲不存在：%s", startTrackID) }
	}
	s.mu.Lock()
	mode := s.playlistPlaybackMode
	if mode == "" { mode = "sequential" }
	s.playlistPlaybackActive = true
	s.playlistPlaybackID = playlist.ID
	s.playlistPlaybackName = playlist.Name
	s.playlistPlaybackTracks = append([]music.Track(nil), playlist.Tracks...)
	s.playlistPlaybackIndex = start
	s.playlistPlaybackMode = mode
	s.mu.Unlock()

	current := s.queue.Snapshot().Current
	// Never interrupt a song requested by an audience member. The playlist
	// starts as soon as the complete audience queue has been served.
	if current != nil && current.RequestedBy != playlistPlayMarker &&
		current.RequestedBy != "" && current.RequestedBy != searchPlayMarker {
		return nil
	}
	s.cancelPlaybackWatcher()
	s.queue.FinishCurrent()
	return s.PlayNext()
}

func (s *MusicService) PlayPlaylistTrack(playlistID, trackID string) error {
	return s.PlayPlaylist(playlistID, trackID)
}

func (s *MusicService) hasActivePlaylistPlayback() bool {
	if s == nil { return false }
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.playlistPlaybackActive && len(s.playlistPlaybackTracks) > 0
}

// GetPlaylistPlaybackActive exposes whether the playlist scheduler can advance.
// The UI uses this alongside the audience queue to enable the shared Next button.
func (s *MusicService) GetPlaylistPlaybackActive() bool {
	return s.hasActivePlaylistPlayback()
}

func (s *MusicService) takeNextPlaylistTrack() (music.QueueItem, bool) {
	if s == nil { return music.QueueItem{}, false }
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.playlistPlaybackActive || len(s.playlistPlaybackTracks) == 0 {
		return music.QueueItem{}, false
	}
	index := s.playlistPlaybackIndex
	if index >= len(s.playlistPlaybackTracks) {
		switch s.playlistPlaybackMode {
		case "loop":
			index = 0
		case "random":
			index = mathrand.Intn(len(s.playlistPlaybackTracks))
		default:
			s.playlistPlaybackActive = false
			return music.QueueItem{}, false
		}
	}
	if index < 0 || index >= len(s.playlistPlaybackTracks) {
		s.playlistPlaybackActive = false
		return music.QueueItem{}, false
	}
	track := s.playlistPlaybackTracks[index]
	switch s.playlistPlaybackMode {
	case "random":
		if len(s.playlistPlaybackTracks) == 1 { s.playlistPlaybackIndex = 0 } else {
			next := mathrand.Intn(len(s.playlistPlaybackTracks)-1)
			if next >= index { next++ }
			s.playlistPlaybackIndex = next
		}
	default:
		s.playlistPlaybackIndex = index + 1
	}
	return music.QueueItem{
		ID: fmt.Sprintf("playlist-%d", time.Now().UnixNano()),
		Track: track, RequestedBy: playlistPlayMarker, AddedAt: time.Now().UTC(),
	}, true
}

// SetPlaylistPlaybackMode changes only the active playlist session.
func (s *MusicService) SetPlaylistPlaybackMode(mode string) error {
	switch mode {
	case "sequential", "loop", "random":
	default:
		return fmt.Errorf("不支持的歌单播放模式：%s", mode)
	}
	if s == nil { return errors.New("music service is unavailable") }
	s.mu.Lock()
	s.playlistPlaybackMode = mode
	s.mu.Unlock()
	return nil
}

func (s *MusicService) GetPlaylistPlaybackMode() string {
	if s == nil { return "sequential" }
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.playlistPlaybackMode == "" { return "sequential" }
	return s.playlistPlaybackMode
}

func validatePlaylistTrack(track music.Track) error {
	if strings.TrimSpace(track.ID) == "" || strings.TrimSpace(track.SourceID) == "" || strings.TrimSpace(track.Title) == "" {
		return errors.New("歌曲缺少必要的 ID、音源或歌名")
	}
	if len(track.Title) > 512 || len(track.Artist) > 512 || len(track.Album) > 512 { return errors.New("歌曲元数据字段过长") }
	return nil
}

// sanitizePlaylistTrack prevents process-local media URLs from leaking into the persistent playlist.
func sanitizePlaylistTrack(track music.Track) music.Track {
	result := track
	result.PlaybackResource = nil
	result.ResolvedQuality = ""
	result.SourceData = make(map[string]string, len(track.SourceData))
	for key, value := range track.SourceData {
		lower := strings.ToLower(key)
		switch lower {
		case "url", "play_url", "audio_url", "media_url", "stream_url", "access_token", "token", "cookie", "authorization":
			continue
		}
		result.SourceData[key] = value
	}
	result.Qualities = append([]music.TrackQuality(nil), track.Qualities...)
	for i := range result.Qualities {
		result.Qualities[i].SourceData = nil
	}
	return result
}

// ImportLXPlaylist imports one 洛雪 playlist. For multi-playlist exports it returns the first imported list.
func (s *MusicService) ImportLXPlaylist(raw string) (MusicPlaylist, error) {
	playlists, err := s.ImportLXPlaylists(raw)
	if err != nil { return MusicPlaylist{}, err }
	if len(playlists) == 0 { return MusicPlaylist{}, errors.New("洛雪文件中没有可导入的歌单") }
	return playlists[0], nil
}

// ImportLXPlaylists imports both single-list exports and full playList_v2 exports.
// 洛雪 .lxmc files are GZIP-compressed JSON; plain JSON exports remain supported.
func (s *MusicService) ImportLXPlaylists(raw string) ([]MusicPlaylist, error) {
	if s == nil { return nil, errors.New("music service is unavailable") }
	if len(raw) == 0 || len(raw) > 24<<20 { return nil, errors.New("歌单文件为空或超过 16 MiB") }
	payload := []byte(raw)
	if strings.HasPrefix(raw, "base64:") {
		decoded, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(raw, "base64:"))
		if err != nil { return nil, fmt.Errorf("读取洛雪歌单文件失败：Base64 数据无效：%w", err) }
		if len(decoded) > 16<<20 { return nil, errors.New("洛雪歌单文件超过 16 MiB") }
		payload = decoded
	}
	if len(payload) >= 2 && payload[0] == 0x1f && payload[1] == 0x8b {
		reader, err := gzip.NewReader(bytes.NewReader(payload))
		if err != nil { return nil, fmt.Errorf("解压洛雪歌单文件失败：%w", err) }
		decompressed, readErr := io.ReadAll(io.LimitReader(reader, (16<<20)+1))
		closeErr := reader.Close()
		if readErr != nil { return nil, fmt.Errorf("解压洛雪歌单文件失败：%w", readErr) }
		if closeErr != nil { return nil, fmt.Errorf("读取洛雪歌单文件失败：%w", closeErr) }
		if len(decompressed) > 16<<20 { return nil, errors.New("解压后的洛雪歌单超过 16 MiB") }
		payload = decompressed
	}
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.UseNumber()
	var file map[string]any
	if err := decoder.Decode(&file); err != nil { return nil, fmt.Errorf("解析洛雪歌单文件失败：%w", err) }
	kind, _ := file["type"].(string)
	var entries []map[string]any
	switch kind {
	case "playListPart_v2", "playListPart":
		data, ok := file["data"].(map[string]any)
		if !ok { return nil, errors.New("洛雪歌单文件缺少 data 对象") }
		entries = append(entries, data)
	case "playList_v2":
		data, ok := file["data"].([]any)
		if !ok { return nil, errors.New("洛雪完整歌单文件缺少 data 数组") }
		for _, rawEntry := range data {
			if entry, ok := rawEntry.(map[string]any); ok { entries = append(entries, entry) }
		}
	default:
		return nil, fmt.Errorf("不支持的洛雪文件类型 %q；请导出洛雪歌单文件（.lxmc）", kind)
	}
	if len(entries) == 0 { return nil, errors.New("洛雪文件中没有歌单") }
	if len(entries) > musicPlaylistLimit { return nil, fmt.Errorf("洛雪文件包含的歌单超过 %d 个", musicPlaylistLimit) }
	imported := make([]MusicPlaylist, 0, len(entries))
	for _, entry := range entries {
		playlist, err := s.importLXPlaylistData(entry)
		if err != nil {
			for _, created := range imported { _ = s.DeletePlaylist(created.ID) }
			return nil, err
		}
		if playlist.ID != "" { imported = append(imported, playlist) }
	}
	if len(imported) == 0 { return nil, errors.New("洛雪文件中没有可导入的在线歌曲") }
	return imported, nil
}

func (s *MusicService) importLXPlaylistData(data map[string]any) (MusicPlaylist, error) {
	name := strings.TrimSpace(playlistLXString(data["name"]))
	if name == "" { name = "导入的洛雪歌单" }
	if len([]rune(name)) > 80 { name = string([]rune(name)[:80]) }
	rawTracks, ok := data["list"].([]any)
	if !ok { return MusicPlaylist{}, errors.New("洛雪歌单文件缺少歌曲列表 data.list") }
	if len(rawTracks) > musicPlaylistTrackLimit { return MusicPlaylist{}, fmt.Errorf("洛雪歌单超过 %d 首歌曲", musicPlaylistTrackLimit) }
	tracks := make([]music.Track, 0, len(rawTracks))
	seen := make(map[string]bool)
	skipped := 0
	for _, rawTrack := range rawTracks {
		item, ok := rawTrack.(map[string]any)
		if !ok { skipped++; continue }
		track, err := parseLXExportTrack(item)
		if err != nil { skipped++; continue }
		key := track.SourceID + "\x00" + track.ID
		if seen[key] { continue }
		seen[key] = true
		tracks = append(tracks, sanitizePlaylistTrack(track))
	}
	if len(tracks) == 0 {
		if len(rawTracks) == 0 { return MusicPlaylist{}, nil }
		return MusicPlaylist{}, fmt.Errorf("歌单“%s”中没有可导入的在线歌曲（共 %d 条无效或不支持）", name, skipped)
	}
	playlist, err := s.CreatePlaylist(name)
	if err != nil { return MusicPlaylist{}, err }
	err = s.updatePlaylist(playlist.ID, func(target *MusicPlaylist) error { target.Tracks = tracks; return nil })
	if err != nil { _ = s.DeletePlaylist(playlist.ID); return MusicPlaylist{}, err }
	playlist.Tracks = tracks
	playlist.UpdatedAt = time.Now().UTC()
	return cloneMusicPlaylist(playlist), nil
}

func parseLXExportTrack(item map[string]any) (music.Track, error) {
	meta, _ := item["meta"].(map[string]any)
	source := playlistLXString(item["source"])
	if source == "" { source = playlistLXString(meta["source"]) }
	if source == "" { return music.Track{}, errors.New("missing source") }
	title := playlistLXString(item["name"])
	artist := playlistLXString(item["singer"])
	album := playlistLXString(item["albumName"])
	cover := playlistLXString(item["img"])
	songID := playlistLXString(item["songmid"])
	if len(meta) > 0 {
		if album == "" { album = playlistLXString(meta["albumName"]) }
		if cover == "" { cover = playlistLXString(meta["picUrl"]) }
		if songID == "" { songID = playlistLXString(meta["songId"]) }
	}
	if title == "" { title = playlistLXString(item["title"]) }
	if title == "" || songID == "" { return music.Track{}, errors.New("missing title or song ID") }
	id := playlistLXString(item["id"])
	if id == "" { id = source + "_" + songID }
	sourceData := map[string]string{"songmid":songID}
	albumID := playlistLXString(item["albumId"])
	if albumID == "" { albumID = playlistLXString(meta["albumId"]) }
	if albumID != "" { sourceData["album_id"] = albumID }
	if source == "kg" {
		hash := playlistLXString(item["hash"]); if hash == "" { hash = playlistLXString(meta["hash"]) }
		if hash != "" { sourceData["hash"] = hash }
		albumAudioID := playlistLXString(item["albumAudioId"]); if albumAudioID == "" { albumAudioID = playlistLXString(meta["albumAudioId"]) }
		if albumAudioID != "" { sourceData["album_audio_id"] = albumAudioID }
	}
	if source == "tx" {
		mediaMID := playlistLXString(item["strMediaMid"]); if mediaMID == "" { mediaMID = playlistLXString(meta["strMediaMid"]) }
		if mediaMID != "" { sourceData["media_mid"] = mediaMID }
		albumMID := playlistLXString(item["albumMid"]); if albumMID == "" { albumMID = playlistLXString(meta["albumMid"]) }
		if albumMID != "" { sourceData["album_mid"] = albumMID }
		songNativeID := playlistLXString(item["songId"]); if songNativeID == "" { songNativeID = playlistLXString(meta["id"]) }
		if songNativeID != "" { sourceData["song_id"] = songNativeID }
	}
	if source == "mg" {
		for from, to := range map[string]string{"copyrightId":"copyright_id","lrcUrl":"lrc_url","mrcUrl":"mrc_url","trcUrl":"trc_url"} {
			value := playlistLXString(item[from]); if value == "" { value = playlistLXString(meta[from]) }; if value != "" { sourceData[to] = value }
		}
	}
	qualityValues := item["types"]
	if qualityValues == nil { qualityValues = meta["qualitys"] }
	qualities := make([]music.TrackQuality, 0)
	if values, ok := qualityValues.([]any); ok {
		for _, raw := range values {
			entry, ok := raw.(map[string]any); if !ok { continue }
			kind := playlistLXString(entry["type"]); if kind == "" { continue }
			qualities = append(qualities, music.TrackQuality{Type:kind, Size:playlistLXString(entry["size"])})
		}
	}
	if len(qualities) == 0 {
		if formats, ok := meta["_qualitys"].(map[string]any); ok {
			for kind, value := range formats { if value != nil { qualities = append(qualities, music.TrackQuality{Type:kind}) } }
		}
	}
	return music.Track{
		ID:id, SourceID:source, LXSourceKey:source, SourceData:sourceData,
		Title:title, Artist:artist, Album:album, DurationMS:parseLXInterval(playlistLXString(item["interval"])),
		CoverURL:cover, Qualities:qualities,
	}, nil
}

func parseLXInterval(value string) int64 {
	parts := strings.Split(strings.TrimSpace(value), ":")
	if len(parts) != 2 && len(parts) != 3 { return 0 }
	total := int64(0)
	for _, part := range parts {
		n, err := strconv.ParseInt(part, 10, 32)
		if err != nil || n < 0 { return 0 }
		total = total*60 + n
	}
	return total * 1000
}

func playlistLXString(value any) string {
	switch v := value.(type) {
	case string: return strings.TrimSpace(v)
	case json.Number: return v.String()
	case float64: return strconv.FormatInt(int64(v), 10)
	case int: return strconv.Itoa(v)
	case int64: return strconv.FormatInt(v, 10)
	default: return ""
	}
}

// SearchAllSourcesPage intentionally searches every registered provider,
// independent of the normal search-priority toggles. It powers manual
// replacement so the user can inspect all available platform candidates.
func (s *MusicService) SearchAllSourcesPage(keyword string, page int) (MusicSearchResponse, error) {
	if s == nil || s.searcher == nil { return MusicSearchResponse{}, errors.New("洛雪搜索 SDK 未初始化") }
	if strings.TrimSpace(keyword) == "" { return MusicSearchResponse{}, errors.New("请输入歌曲名称或歌手") }
	if page < 1 { return MusicSearchResponse{}, errors.New("搜索页码必须大于 0") }
	sourceIDs := make([]string, 0)
	for _, source := range s.searcher.Sources() { sourceIDs = append(sourceIDs, source.ID) }
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	s.mu.RLock()
	searcher, resolver := s.searcher, s.scriptRuntime
	s.mu.RUnlock()
	result, sourceErrors, err := music.NewUnifiedSourceService(scriptAwareSourceSearcher{base:searcher, service:s}, resolver).Search(ctx, keyword, page, sourceIDs)
	response := MusicSearchResponse{Tracks:result.Tracks, Errors:sourceErrors, Page:result.Page, PageSize:result.PageSize, Total:result.Total, MaxPage:result.MaxPage}
	if response.Tracks == nil { response.Tracks = []music.Track{} }
	return response, err
}
