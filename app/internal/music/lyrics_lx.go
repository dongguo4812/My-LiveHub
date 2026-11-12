package music

import (
	"context"
	"compress/zlib"
	"encoding/base64"
	"encoding/json"
	"io"
	"regexp"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	qrc "github.com/dongguo4812/My-LiveHub/app/internal/music/lxqrc"
)

const qqMusicLyricEndpoint = "https://u.y.qq.com/cgi-bin/musicu.fcg"

// GetQQMusicLyrics follows LX Music Desktop's QQ provider:
// PlayLyricInfo -> QRC decrypt/decompress -> timestamped lyric lines.
func GetQQMusicLyrics(parent context.Context, client *http.Client, track Track) (string, error) {
	if parent == nil { parent = context.Background() }
	if client == nil { client = &http.Client{Timeout: 15 * time.Second} }
	songID := strings.TrimSpace(track.SourceData["song_id"])
	songMID := strings.TrimSpace(track.SourceData["songmid"])
	if songID == "" && songMID == "" {
		return "", errors.New("QQ 歌词请求缺少 song_id/songmid")
	}
	var songIDValue any = songID
	if songID == "" { songIDValue = 0 } else if n := intValue(songID); n > 0 { songIDValue = n }
	payload := map[string]any{
		"comm": map[string]any{"ct":"19","cv":"1859","uin":"0"},
		"req": map[string]any{
			"method":"GetPlayLyricInfo",
			"module":"music.musichallSong.PlayLyricInfo",
			"param": map[string]any{
				"format":"json","crypt":1,"ct":19,"cv":1873,"interval":0,
				"lrc_t":0,"qrc":1,"qrc_t":0,"roma":1,"roma_t":0,
				"songID":songIDValue,"songMID":songMID,"trans":1,"trans_t":0,"type":-1,
			},
		},
	}
	body, err := jsonBody(payload)
	if err != nil { return "", fmt.Errorf("构建 QQ 歌词请求失败：%w", err) }
	raw, err := doLXJSON(parent, client, http.MethodPost, qqMusicLyricEndpoint,
		map[string]string{"Referer":"https://y.qq.com","User-Agent":"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 Chrome/120.0.0.0 Safari/537.36","Content-Type":"application/json"},
		strings.NewReader(string(body)))
	if err != nil { return "", fmt.Errorf("请求 QQ 歌词失败：%w", err) }
	req := mapValue(raw["req"])
	if len(req) == 0 || intValue(req["code"]) != 0 || intValue(raw["code"]) != 0 {
		return "", fmt.Errorf("QQ 歌词接口返回异常：%s", stringValue(req["message"]))
	}
	data := mapValue(req["data"])
	if strings.TrimSpace(stringValue(data["lyric"])) == "" {
		// QQ 返回成功但没有 lyric 字段，代表该歌曲在当前平台没有可用歌词；
		// 只有接口/解密失败才返回 error，前端据此区分“暂无歌词”和“获取失败”。
		return "", nil
	}
	primary, err := decodeQQLyricField(stringValue(data["lyric"]))
	if err != nil { return "", fmt.Errorf("解码 QQ 原文歌词失败：%w", err) }
	if primary == nil || len(primary.Lines) == 0 { return "", errors.New("QQ 音乐歌词响应无法解析") }
	translated, _ := decodeQQLyricField(stringValue(data["trans"]))
	romanized, _ := decodeQQLyricField(stringValue(data["roma"]))
	return formatQQLyricAsLRC(track, primary, translated, romanized), nil
}

func decodeQQLyricField(value string) (*qrc.Lyric, error) {
	value = strings.TrimSpace(value)
	if value == "" { return nil, nil }
	decoded, err := qrc.Decrypt([]byte(value))
	if err != nil { return nil, err }
	lyric, err := qrc.Parse(decoded)
	if err != nil { return nil, err }
	return lyric, nil
}

func formatQQLyricAsLRC(track Track, primary, translated, romanized *qrc.Lyric) string {
	lines := make(map[int][]string)
	seen := make(map[int]map[string]bool)
	add := func(at int, text string) {
		text = strings.TrimSpace(text)
		if text == "" { return }
		if seen[at] == nil { seen[at] = make(map[string]bool) }
		if seen[at][text] { return }
		seen[at][text] = true
		lines[at] = append(lines[at], text)
	}
	for _, line := range primary.Lines { add(line.Start, line.Text) }
	for _, line := range primary.Translation { add(line.Start, line.Text) }
	if translated != nil {
		for _, line := range translated.Lines { add(line.Start, line.Text) }
	}
	if romanized != nil {
		for _, line := range romanized.Lines { add(line.Start, line.Text) }
	}
	times := make([]int, 0, len(lines))
	for at := range lines { times = append(times, at) }
	sort.Ints(times)
	var out strings.Builder
	fmt.Fprintf(&out, "[ti:%s]\n[ar:%s]\n[al:%s]\n[offset:0]\n", track.Title, track.Artist, track.Album)
	for _, at := range times {
		minutes, seconds, centiseconds := at/60000, (at/1000)%60, (at%1000)/10
		fmt.Fprintf(&out, "[%02d:%02d.%02d]%s\n", minutes, seconds, centiseconds, strings.Join(lines[at], " / "))
	}
	return strings.TrimSpace(out.String())
}


// GetKugouMusicLyrics follows LX Music Desktop's Kugou lyric flow:
// lyrics.kugou.com/search -> lyrics.kugou.com/download -> KRC decrypt/decompress.
func GetKugouMusicLyrics(parent context.Context, client *http.Client, track Track) (string, error) {
	if parent == nil { parent = context.Background() }
	if client == nil { client = &http.Client{Timeout: 15 * time.Second} }
	hash := strings.TrimSpace(track.SourceData["hash"])
	if hash == "" { return "", errors.New("酷狗歌词请求缺少 FileHash") }
	duration := track.DurationMS / 1000
	query := url.Values{}
	query.Set("ver", "1")
	query.Set("man", "yes")
	query.Set("client", "pc")
	query.Set("keyword", track.Title)
	query.Set("hash", hash)
	query.Set("timelength", fmt.Sprintf("%d", duration))
	query.Set("lrctxt", "1")
	searchURL := "https://lyrics.kugou.com/search?" + query.Encode()
	headers := map[string]string{
		"KG-RC": "1",
		"KG-THash": "expand_search_manager.cpp:852736169:451",
		"User-Agent": "KuGou2012-9020-ExpandSearchManager",
	}
	search, err := doLXJSON(parent, client, http.MethodGet, searchURL, headers, nil)
	if err != nil { return "", fmt.Errorf("搜索酷狗歌词失败：%w", err) }
	if intValue(search["status"]) != 200 { return "", fmt.Errorf("酷狗歌词搜索返回异常状态：%s", stringValue(search["info"])) }
	candidates := sliceValue(search["candidates"])
	if len(candidates) == 0 { return "", nil }
	candidate := mapValue(candidates[0])
	id := stringValue(candidate["id"])
	accessKey := stringValue(candidate["accesskey"])
	if id == "" || accessKey == "" { return "", errors.New("酷狗歌词候选结果缺少下载凭据") }
	format := "lrc"
	if intValue(candidate["krctype"]) == 1 && intValue(candidate["contenttype"]) != 1 { format = "krc" }
	download := url.Values{}
	download.Set("ver", "1")
	download.Set("client", "pc")
	download.Set("id", id)
	download.Set("accesskey", accessKey)
	download.Set("fmt", format)
	download.Set("charset", "utf8")
	result, err := doLXJSON(parent, client, http.MethodGet, "https://lyrics.kugou.com/download?"+download.Encode(), headers, nil)
	if err != nil { return "", fmt.Errorf("下载酷狗歌词失败：%w", err) }
	if intValue(result["status"]) != 200 { return "", fmt.Errorf("酷狗歌词下载失败：%s", stringValue(result["info"])) }
	content := stringValue(result["content"])
	if content == "" { return "", nil }
	switch strings.ToLower(stringValue(result["fmt"])) {
	case "krc":
		return decodeKugouKRC(content, track)
	case "lrc":
		decoded, err := base64.StdEncoding.DecodeString(content)
		if err != nil { return "", fmt.Errorf("解码酷狗 LRC 失败：%w", err) }
		return strings.TrimSpace(string(decoded)), nil
	default:
		return "", fmt.Errorf("酷狗返回未知歌词格式：%s", stringValue(result["fmt"]))
	}
}

var kugouKRCLineTime = regexp.MustCompile(`^\\[(\\d+),(\\d+)\\]`)
var kugouKRCWordTime = regexp.MustCompile(`<\\d+,\\d+(?:,\\d+)?>`)
var kugouKRCHeader = regexp.MustCompile(`(?m)^.*\\[id:\\$[A-Za-z0-9]+\\]\\r?\\n`)

func decodeKugouKRC(encoded string, track Track) (string, error) {
	raw, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil { return "", fmt.Errorf("KRC Base64 解码失败：%w", err) }
	if len(raw) <= 4 { return "", errors.New("KRC 数据长度无效") }
	key := []byte{0x40,0x47,0x61,0x77,0x5e,0x32,0x74,0x47,0x51,0x36,0x31,0x2d,0xce,0xd2,0x6e,0x69}
	cipherText := append([]byte(nil), raw[4:]...)
	for i := range cipherText { cipherText[i] ^= key[i%len(key)] }
	reader, err := zlib.NewReader(strings.NewReader(string(cipherText)))
	if err != nil { return "", fmt.Errorf("KRC 解压失败：%w", err) }
	plain, readErr := io.ReadAll(io.LimitReader(reader, 4<<20))
	closeErr := reader.Close()
	if readErr != nil { return "", readErr }
	if closeErr != nil { return "", closeErr }
	return parseKugouKRC(string(plain), track), nil
}

func parseKugouKRC(content string, track Track) string {
	content = strings.ReplaceAll(content, "\\r", "")
	content = kugouKRCHeader.ReplaceAllString(content, "")
	lines := strings.Split(content, "\\n")
	var out strings.Builder
	fmt.Fprintf(&out, "[ti:%s]\\n[ar:%s]\\n[al:%s]\\n[offset:0]\\n", track.Title, track.Artist, track.Album)
	for _, line := range lines {
		match := kugouKRCLineTime.FindStringSubmatch(line)
		if len(match) != 3 { continue }
		start, _ := strconv.Atoi(match[1])
		minutes, seconds, centiseconds := start/60000, (start/1000)%60, (start%1000)/10
		text := kugouKRCLineTime.ReplaceAllString(line, "")
		text = kugouKRCWordTime.ReplaceAllString(text, "")
		text = strings.TrimSpace(text)
		if text == "" { continue }
		fmt.Fprintf(&out, "[%02d:%02d.%02d]%s\\n", minutes, seconds, centiseconds, text)
	}
	return strings.TrimSpace(out.String())
}
