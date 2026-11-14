package desktop

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync"
	"math"
	"net/http"
	"net/url"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/dongguo4812/My-LiveHub/app/internal/music"
)

// LXPlaybackEngine delegates playback to the installed 洛雪 desktop client.
// The custom URI launches a specific song; HTTP is used only for status and
// the documented play/pause controls.
type LXPlaybackEngine struct {
	client *http.Client
	mu sync.RWMutex
	endpoints []string
}

func NewLXPlaybackEngine(endpoints []string) *LXPlaybackEngine {
	return &LXPlaybackEngine{
		client: &http.Client{Timeout: 2 * time.Second},
		endpoints: normalizeLXAPIEndpoints(endpoints),
	}
}

func normalizeLXAPIEndpoints(endpoints []string) []string {
	result := make([]string, 0, len(endpoints))
	seen := make(map[string]bool)
	for _, raw := range endpoints {
		value := strings.TrimRight(strings.TrimSpace(raw), "/")
		if value == "" || seen[value] { continue }
		parsed, err := url.Parse(value)
		if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" { continue }
		seen[value] = true
		result = append(result, value)
	}
	return result
}

func (e *LXPlaybackEngine) SetEndpoints(endpoints []string) {
	if e == nil { return }
	e.mu.Lock()
	e.endpoints = normalizeLXAPIEndpoints(endpoints)
	e.mu.Unlock()
}

func (e *LXPlaybackEngine) Start(context.Context) error { return nil }
func (e *LXPlaybackEngine) Close() error { return nil }

func (e *LXPlaybackEngine) endpointRequest(ctx context.Context, paths []string) ([]byte, string, error) {
	if e == nil { return nil, "", errors.New("洛雪播放服务不可用") }
	e.mu.RLock()
	endpoints := append([]string(nil), e.endpoints...)
	e.mu.RUnlock()
	if len(endpoints) == 0 { return nil, "", errors.New("尚未配置洛雪 API 地址，请在音乐设置中添加地址") }
	var failures []string
	for _, base := range endpoints {
		for _, path := range paths {
			requestURL := base + path
			req, err := http.NewRequestWithContext(ctx, http.MethodGet, requestURL, nil)
			if err != nil { failures = append(failures, requestURL+": "+err.Error()); continue }
			if path == "/status" || path == "/api/status" {
				query := url.Values{}
				query.Set("filter", "status,playStatus,state,playing,isPlaying,paused,isPaused,name,singer,albumName,duration,progress,playbackRate,picUrl,img,coverUrl,cover_url,lyricLineText")
				req.URL.RawQuery = query.Encode()
			}
			resp, err := e.client.Do(req)
			if err != nil { failures = append(failures, requestURL+": "+err.Error()); continue }
			body, readErr := readLXResponse(resp)
			resp.Body.Close()
			if readErr != nil { failures = append(failures, requestURL+": "+readErr.Error()); continue }
			if resp.StatusCode < 200 || resp.StatusCode >= 300 {
				failures = append(failures, fmt.Sprintf("%s: HTTP %d", requestURL, resp.StatusCode))
				continue
			}
			return body, base, nil
		}
	}
	return nil, "", fmt.Errorf("洛雪 API 地址均不可用：%s", strings.Join(failures, "；"))
}

func readLXResponse(resp *http.Response) ([]byte, error) {
	if resp == nil || resp.Body == nil { return nil, errors.New("empty response") }
	body, err := io.ReadAll(io.LimitReader(resp.Body, (1<<20)+1))
	if err != nil { return nil, err }
	if len(body) > 1<<20 { return nil, errors.New("洛雪 API 响应超过 1 MB") }
	return body, nil
}

func (e *LXPlaybackEngine) requestControl(ctx context.Context, path string) error {
	_, _, err := e.endpointRequest(ctx, []string{path})
	return err
}

func (e *LXPlaybackEngine) Pause(ctx context.Context) error {
	if ctx == nil { ctx = context.Background() }
	return e.requestControl(ctx, "/pause")
}

func (e *LXPlaybackEngine) Resume(ctx context.Context) error {
	if ctx == nil { ctx = context.Background() }
	return e.requestControl(ctx, "/play")
}

// NextTrack advances the 洛雪 player's own playlist, including songs queued outside My-LiveHub.
func (e *LXPlaybackEngine) NextTrack(ctx context.Context) error {
	if ctx == nil { ctx = context.Background() }
	return e.requestControl(ctx, "/skip-next")
}

// Stop has no dedicated 洛雪 endpoint. Pausing is the documented safe action;
// the My-LiveHub queue is still finished locally.
func (e *LXPlaybackEngine) Stop(ctx context.Context) error { return e.Pause(ctx) }

func (e *LXPlaybackEngine) Snapshot(ctx context.Context) (music.PlaybackSnapshot, error) {
	if ctx == nil { ctx = context.Background() }
	body, _, err := e.endpointRequest(ctx, []string{"/status", "/api/status"})
	if err != nil { return music.PlaybackSnapshot{}, err }
	var value any
	if err := json.Unmarshal(body, &value); err != nil { return music.PlaybackSnapshot{}, fmt.Errorf("解析洛雪播放状态失败：%w", err) }
	return parseLXPlaybackSnapshot(value), nil
}

// GetCurrentLyrics reads the currently playing song lyrics from 洛雪's Open API.
// Prefer /lyric-all so the existing frontend can align original and translated LRC lines.
func (e *LXPlaybackEngine) GetCurrentLyrics(ctx context.Context) (string, error) {
	if ctx == nil { ctx = context.Background() }
	body, _, err := e.endpointRequest(ctx, []string{"/lyric-all"})
	if err == nil {
		var value map[string]any
		if json.Unmarshal(body, &value) == nil {
			original := strings.TrimSpace(lxString(value["lyric"]))
			translated := strings.TrimSpace(lxString(value["tlyric"]))
			if original != "" && translated != "" { return original + "\n" + translated, nil }
			if original != "" { return original, nil }
		}
	}
	// Older 洛雪 versions may not expose /lyric-all; fall back to plain LRC.
	plain, _, plainErr := e.endpointRequest(ctx, []string{"/lyric"})
	if plainErr == nil { return strings.TrimSpace(string(plain)), nil }
	if err != nil { return "", fmt.Errorf("读取洛雪完整歌词失败：%v；读取普通歌词失败：%w", err, plainErr) }
	return "", fmt.Errorf("解析洛雪完整歌词失败，且普通歌词不可用：%w", plainErr)
}

func (e *LXPlaybackEngine) Load(ctx context.Context, track music.Track) error {
	if ctx == nil { ctx = context.Background() }
	uri, err := buildLXPlayURI(track)
	if err != nil { return err }
	if err := launchLXURI(uri); err != nil { return fmt.Errorf("启动洛雪播放协议失败：%w", err) }
	deadline := time.NewTimer(6 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done(): return ctx.Err()
		case <-deadline.C: return fmt.Errorf("已调用洛雪播放协议，但 6 秒内未能从状态 API 确认播放：%s - %s", track.Title, track.Artist)
		case <-ticker.C:
			snapshot, statusErr := e.Snapshot(ctx)
			if statusErr != nil { continue }
			if snapshot.State == music.PlaybackError { return errors.New(firstNonEmpty(snapshot.Error, "洛雪报告播放失败")) }
			if snapshot.State != music.PlaybackPlaying { continue }
			if sameLXTrackIdentity(track, statusTrackIdentity(snapshot)) {
				// If the exact song was already playing before the request, it is
				// still a valid playback result; otherwise this confirms the switch.
				return nil
			}
		}
	}
}

func statusTrackIdentity(snapshot music.PlaybackSnapshot) music.Track {
	if snapshot.Track != nil { return *snapshot.Track }
	return music.Track{}
}

func buildLXPlayURI(track music.Track) (string, error) {
	source := firstNonEmpty(track.LXSourceKey, track.SourceID)
	songID := firstNonEmpty(track.SourceData["songmid"], track.SourceData["song_id"], track.SourceData["hash"], track.SourceData["id"])
	if songID == "" {
		if source == "kg" { songID = strings.SplitN(track.ID, "_", 2)[0] } else { songID = strings.TrimPrefix(track.ID, source+"_") }
	}
	if strings.TrimSpace(track.Title) == "" || strings.TrimSpace(track.Artist) == "" || source == "" || songID == "" {
		return "", errors.New("歌曲缺少洛雪播放所需的歌名、歌手、音源或歌曲 ID")
	}
	types := make([]map[string]any, 0, len(track.Qualities))
	for _, item := range track.Qualities {
		if strings.TrimSpace(item.Type) == "" { continue }
		var size any
		if item.Size != "" { size = item.Size }
		quality := map[string]any{"type": item.Type, "size": size}
		for key, value := range item.SourceData { quality[key] = value }
		types = append(types, quality)
	}
	if len(types) == 0 { types = append(types, map[string]any{"type":"128k", "size":nil}) }
	payload := map[string]any{
		"name": track.Title, "singer": track.Artist, "source": source,
		"songmid": songID, "types": types,
	}
	optional := map[string]string{
		"img": track.CoverURL,
		"albumName": track.Album,
		"albumId": firstNonEmpty(track.SourceData["album_id"], track.SourceData["albumId"]),
		"hash": track.SourceData["hash"],
		"strMediaMid": firstNonEmpty(track.SourceData["media_mid"], track.SourceData["strMediaMid"]),
		"albumMid": firstNonEmpty(track.SourceData["album_mid"], track.SourceData["albumMid"]),
		"copyrightId": firstNonEmpty(track.SourceData["copyright_id"], track.SourceData["copyrightId"]),
		"lrcUrl": firstNonEmpty(track.SourceData["lrc_url"], track.SourceData["lrcUrl"]),
	}
	if track.DurationMS > 0 { optional["interval"] = fmt.Sprintf("%02d:%02d", track.DurationMS/60000, (track.DurationMS/1000)%60) }
	for key, value := range optional { if strings.TrimSpace(value) != "" { payload[key] = value } }
	raw, err := json.Marshal(payload)
	if err != nil { return "", err }
	query := url.Values{}
	query.Set("data", string(raw))
	return "lxmusic://music/play?" + query.Encode(), nil
}

func launchLXURI(uri string) error {
	var command *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		command = exec.Command("rundll32.exe", "url.dll,FileProtocolHandler", uri)
	case "darwin":
		command = exec.Command("open", uri)
	default:
		command = exec.Command("xdg-open", uri)
	}
	if err := command.Start(); err != nil { return err }
	return command.Process.Release()
}

func parseLXPlaybackSnapshot(value any) music.PlaybackSnapshot {
	snapshot := music.PlaybackSnapshot{State: music.PlaybackIdle}
	for _, object := range flattenLXObjects(value) {
		status := strings.ToLower(firstNonEmpty(lxString(object["status"]), lxString(object["playStatus"]), lxString(object["state"])))
		paused := object["paused"] == true || object["isPaused"] == true
		playing := object["playing"] == true || object["isPlaying"] == true
		switch {
		case paused || status == "paused" || status == "pause" || status == "暂停" || status == "已暂停":
			snapshot.State = music.PlaybackPaused
		case playing || status == "playing" || status == "play" || status == "1" || status == "true":
			snapshot.State = music.PlaybackPlaying
		case status == "error" || status == "failed":
			snapshot.State = music.PlaybackError
			snapshot.Error = firstNonEmpty(lxString(object["error"]), lxString(object["message"]), "洛雪报告播放失败")
		case object["playing"] == false || status == "stopped" || status == "stop" || status == "0" || status == "false" || status == "ended" || status == "停止":
			snapshot.State = music.PlaybackStopped
		}
		if snapshot.Track == nil {
			title := firstNonEmpty(lxString(object["name"]), lxString(object["title"]), lxString(object["songName"]), lxString(object["musicName"]), lxString(object["trackName"]))
			artist := firstNonEmpty(lxString(object["singer"]), lxString(object["artist"]), lxString(object["artistName"]), lxString(object["singerName"]), lxString(object["author"]))
			if title != "" {
				snapshot.Track = &music.Track{Title:title, Artist:artist}
				snapshot.Track.CoverURL = firstNonEmpty(lxString(object["picUrl"]), lxString(object["img"]), lxString(object["coverUrl"]), lxString(object["cover_url"]))
			}
		}
		if snapshot.DurationMS == 0 {
			snapshot.DurationMS = lxTimeMS(object, []string{"duration_ms", "duration", "totalTime", "length"})
		}
		if snapshot.PositionMS == 0 {
			snapshot.PositionMS = lxTimeMS(object, []string{"progress_ms", "progress", "currentTime", "position"})
		}
	}
	return snapshot
}

func flattenLXObjects(value any) []map[string]any {
	result := []map[string]any{}
	var visit func(any)
	visit = func(item any) {
		switch v := item.(type) {
		case map[string]any:
			result = append(result, v)
			for _, child := range v { visit(child) }
		case []any:
			for _, child := range v { visit(child) }
		}
	}
	visit(value)
	return result
}

func lxString(value any) string {
	switch v := value.(type) {
	case string: return strings.TrimSpace(v)
	case float64: return strconv.FormatFloat(v, 'f', -1, 64)
	case float32: return strconv.FormatFloat(float64(v), 'f', -1, 32)
	case int: return strconv.Itoa(v)
	case int32: return strconv.FormatInt(int64(v), 10)
	case int64: return strconv.FormatInt(v, 10)
	case bool: return strconv.FormatBool(v)
	default: return ""
	}
}

func lxTimeMS(object map[string]any, keys []string) int64 {
	for _, key := range keys {
		value, exists := object[key]
		if !exists { continue }
		number, err := strconv.ParseFloat(lxString(value), 64)
		if err != nil || number < 0 || math.IsNaN(number) || math.IsInf(number, 0) { continue }
		if strings.HasSuffix(key, "_ms") { return int64(number) }
		return int64(number * 1000)
	}
	return 0
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" { return value }
	}
	return ""
}
