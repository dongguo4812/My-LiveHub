package desktop

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/dongguo4812/My-LiveHub/app/internal/music"
)

var lxQualityPreference = []string{"flac24bit", "flac", "320k", "128k"}

func chooseLXTrackQuality(track music.Track, declared music.LXDeclaredSource) (music.TrackQuality, error) {
	available := make(map[string]music.TrackQuality, len(track.Qualities))
	for _, item := range track.Qualities { if strings.TrimSpace(item.Type) != "" { available[item.Type] = item } }
	allowed := make(map[string]bool, len(declared.Qualitys))
	for _, item := range declared.Qualitys { allowed[item] = true }
	if selected := strings.TrimSpace(track.SelectedQuality); selected != "" {
		item, ok := available[selected]
		if !ok { return music.TrackQuality{}, fmt.Errorf("歌曲不支持所选音质：%s", selected) }
		if !allowed[selected] { return music.TrackQuality{}, fmt.Errorf("当前自定义音源不支持 %s 音质", selected) }
		return item, nil
	}
	for _, kind := range lxQualityPreference { if item, ok := available[kind]; ok && allowed[kind] { return item, nil } }
	return music.TrackQuality{}, errors.New("当前歌曲与自定义音源没有共同支持的音质")
}

func buildLXMusicURLRequest(track music.Track, selected music.TrackQuality) ([]byte, error) {
	source := track.LXSourceKey
	if source == "" { source = track.SourceID }
	native := make(map[string]any, len(track.SourceData)+20)
	for key, value := range track.SourceData {
		native[key] = value
		switch key {
		case "copyright_id": native["copyrightId"] = value
		case "album_id": native["albumId"] = value
		case "album_mid": native["albumMid"] = value
		case "song_id": native["songId"] = value
		case "media_mid": native["strMediaMid"] = value
		case "album_audio_id": native["albumAudioId"] = value
		case "lrc_url": native["lrcUrl"] = value
		case "mrc_url": native["mrcUrl"] = value
		case "trc_url": native["trcUrl"] = value
		}
	}
	native["source"] = source
	native["name"] = track.Title
	native["singer"] = track.Artist
	native["albumName"] = track.Album
	native["songmid"] = firstNonEmpty(track.SourceData["songmid"], strings.TrimPrefix(track.ID, source+":"))
	native["type"] = selected.Type
	types := make([]map[string]string, 0, len(track.Qualities))
	typeMap := make(map[string]map[string]string, len(track.Qualities))
	for _, item := range track.Qualities {
		value := map[string]string{"type": item.Type}
		if item.Size != "" { value["size"] = item.Size }
		for key, field := range item.SourceData { value[key] = field }
		types = append(types, value)
		typeMap[item.Type] = value
	}
	native["types"] = types
	native["_types"] = typeMap
	for key, value := range selected.SourceData { native[key] = value }
	info := make(map[string]any, len(native))
	for key, value := range native { info[key] = value }
	info["type"] = selected.Type
	return json.Marshal(map[string]any{"source": source, "action": "musicUrl", "info": info})
}

func firstNonEmpty(values ...string) string {
	for _, value := range values { if strings.TrimSpace(value) != "" { return value } }
	return ""
}
