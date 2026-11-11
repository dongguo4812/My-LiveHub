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
	native["albumId"] = firstNonEmpty(track.SourceData["album_id"], track.SourceData["albumId"])
	native["interval"] = formatLXInterval(track.DurationMS)
	native["img"] = track.CoverURL
	native["lrc"] = firstNonEmpty(track.SourceData["lrc_url"], track.SourceData["lrcUrl"])
	native["otherSource"] = nil
	native["typeUrl"] = map[string]string{}
	types := make([]map[string]string, 0, len(track.Qualities))
	typeMap := make(map[string]map[string]string, len(track.Qualities))
	for _, item := range track.Qualities {
		value := map[string]string{"type": item.Type}
		if item.Size != "" { value["size"] = item.Size }
		for key, field := range item.SourceData { value[key] = field }
		types = append(types, value)
		meta := make(map[string]string, len(value))
		for key, field := range value { if key != "type" { meta[key] = field } }
		typeMap[item.Type] = meta
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

func formatLXInterval(durationMS int64) string {
	if durationMS < 0 { durationMS = 0 }
	seconds := durationMS / 1000
	return fmt.Sprintf("%02d:%02d", seconds/60, seconds%60)
}

func parseLXMusicURLResponse(raw []byte, expectedSource, expectedQuality string) (string, error) {
	var result struct {
		Source string `json:"source"`
		Action string `json:"action"`
		Data struct {
			URL string `json:"url"`
			Type string `json:"type"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		return "", errors.New("自定义源返回的播放结果格式无效")
	}
	if result.Source != expectedSource || result.Action != "musicUrl" {
		return "", errors.New("自定义源返回的播放结果与当前歌曲不匹配")
	}
	if result.Data.Type != "" && result.Data.Type != expectedQuality {
		return "", errors.New("自定义源返回的音质与所选音质不匹配")
	}
	if err := music.ValidateLXPlaybackURL(result.Data.URL); err != nil {
		return "", errors.New("自定义源未返回有效的 HTTP(S) 播放地址")
	}
	return result.Data.URL, nil
}
