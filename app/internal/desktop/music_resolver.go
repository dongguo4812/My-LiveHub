package desktop

import (
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
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
	songID := track.SourceData["songmid"]
	if songID == "" {
		if source == "kg" { songID = strings.SplitN(track.ID, "_", 2)[0] } else { songID = strings.TrimPrefix(track.ID, source+"_") }
	}

	qualityList := make([]map[string]any, 0, len(track.Qualities))
	qualityMap := make(map[string]map[string]any, len(track.Qualities))
	for _, item := range track.Qualities {
		size := any(nil)
		if item.Size != "" { size = item.Size }
		value := map[string]any{"type": item.Type, "size": size}
		metaSize := size
		if source == "kw" && item.Size != "" { metaSize = strings.ToUpper(item.Size) }
		meta := map[string]any{"size": metaSize}
		for key, field := range item.SourceData { value[key] = field; meta[key] = field }
		qualityList = append(qualityList, value)
		qualityMap[item.Type] = meta
	}

	meta := map[string]any{
		"songId": songID, "albumName": track.Album, "picUrl": nullableLXString(track.CoverURL),
		"qualitys": qualityList, "_qualitys": qualityMap,
	}
	if value := firstNonEmpty(track.SourceData["album_id"], track.SourceData["albumId"]); value != "" { meta["albumId"] = value }
	switch source {
	case "kg":
		meta["hash"] = track.SourceData["hash"]
		if value := track.SourceData["album_audio_id"]; value != "" { meta["albumAudioId"] = value }
	case "tx":
		meta["strMediaMid"] = track.SourceData["media_mid"]
		if value := track.SourceData["song_id"]; value != "" { if number, err := strconv.ParseInt(value, 10, 64); err == nil { meta["id"] = number } else { meta["id"] = value } }
		if value := track.SourceData["album_mid"]; value != "" { meta["albumMid"] = value }
	case "mg":
		meta["copyrightId"] = track.SourceData["copyright_id"]
		for from, to := range map[string]string{"lrc_url":"lrcUrl", "mrc_url":"mrcUrl", "trc_url":"trcUrl"} {
			if value := track.SourceData[from]; value != "" { meta[to] = value }
		}
	}
	// LX converts its current MusicInfo model back to the legacy source-script
	// contract before dispatching user API actions (toOldMusicInfo in tools.ts).
	// Source scripts therefore read songmid/hash/types at the top level, not meta.*.
	musicInfo := map[string]any{
		"name": track.Title,
		"singer": track.Artist,
		"source": source,
		"songmid": songID,
		"interval": lxMusicInfoInterval(source, track.DurationMS),
		"albumName": track.Album,
		"img": nullableLXString(track.CoverURL),
		"typeUrl": map[string]any{},
		"types": qualityList,
		"_types": qualityMap,
	}
	if value := firstNonEmpty(track.SourceData["album_id"], track.SourceData["albumId"]); value != "" {
		musicInfo["albumId"] = value
	}
	switch source {
	case "kg":
		if value := track.SourceData["hash"]; value != "" { musicInfo["hash"] = value }
		if value := track.SourceData["album_audio_id"]; value != "" { musicInfo["albumAudioId"] = value }
	case "tx":
		if value := track.SourceData["media_mid"]; value != "" { musicInfo["strMediaMid"] = value }
		if value := track.SourceData["song_id"]; value != "" {
			if number, err := strconv.ParseInt(value, 10, 64); err == nil { musicInfo["songId"] = number } else { musicInfo["songId"] = value }
		}
		if value := track.SourceData["album_mid"]; value != "" { musicInfo["albumMid"] = value }
	case "mg":
		if value := track.SourceData["copyright_id"]; value != "" { musicInfo["copyrightId"] = value }
		for from, to := range map[string]string{"lrc_url":"lrcUrl", "mrc_url":"mrcUrl", "trc_url":"trcUrl"} {
			if value := track.SourceData[from]; value != "" { musicInfo[to] = value }
		}
	}
	info := map[string]any{"type": selected.Type, "musicInfo": musicInfo}
	return json.Marshal(map[string]any{"source": source, "action": "musicUrl", "info": info})
}

func nullableLXString(value string) any {
	if strings.TrimSpace(value) == "" { return nil }
	return value
}

func lxMusicInfoInterval(source string, durationMS int64) any {
	if source == "tx" && durationMS == 0 { return nil }
	return formatLXInterval(durationMS)
}

func firstNonEmpty(values ...string) string {
	for _, value := range values { if strings.TrimSpace(value) != "" { return value } }
	return ""
}

func formatLXInterval(durationMS int64) string {
	if durationMS <= 0 { return "--/--" }
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
