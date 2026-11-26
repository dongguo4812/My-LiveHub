package music

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"math"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf16"
)

type SearchProvider interface {
	ID() string
	Name() string
	Search(context.Context, string, int, int) (SearchPage, error)
}

const lxSongSearchPageSize = 30

type LXSearchClient struct {
	httpClient *http.Client
	providers []SearchProvider
}

type LXSearchEndpoints struct {
	Kuwo string
	Kugou string
	QQ string
	QQLegacy string
	Netease string
	Migu string
	MiguFallback string
}

func DefaultLXSearchEndpoints() LXSearchEndpoints {
	return LXSearchEndpoints{
		Kuwo: "http://search.kuwo.cn/r.s",
		Kugou: "http://songsearch.kugou.com/song_search_v2",
		QQ: "https://u.y.qq.com/cgi-bin/musicu.fcg",
		QQLegacy: "https://c.y.qq.com/soso/fcgi-bin/client_search_cp",
		Netease: "http://interface.music.163.com/eapi/batch",
		Migu: "https://jadeite.migu.cn/music_search/v3/search/searchAll",
		MiguFallback: "https://c.musicapp.migu.cn/v1.0/content/search_all.do",
	}
}

func NewLXSearchClient(client *http.Client) *LXSearchClient {
	return NewLXSearchClientWithEndpoints(client, DefaultLXSearchEndpoints())
}

func NewLXSearchClientWithEndpoints(client *http.Client, endpoints LXSearchEndpoints) *LXSearchClient {
	if client == nil {
		client = &http.Client{Timeout: 15 * time.Second}
	}
	if strings.TrimSpace(endpoints.MiguFallback) == "" {
		endpoints.MiguFallback = DefaultLXSearchEndpoints().MiguFallback
	}
	return &LXSearchClient{
		httpClient: client,
		providers: []SearchProvider{
			qqSearchProvider{client: client, endpoint: endpoints.QQ, legacyEndpoint: endpoints.QQLegacy},
			kuwoSearchProvider{client: client, endpoint: endpoints.Kuwo},
			kugouSearchProvider{client: client, endpoint: endpoints.Kugou},
			neteaseSearchProvider{client: client, endpoint: endpoints.Netease},
			miguSearchProvider{client: client, endpoint: endpoints.Migu, fallbackEndpoint: endpoints.MiguFallback},
		},
	}
}

func (c *LXSearchClient) Sources() []SourceSnapshot {
	return SourceCatalog()
}

// Search runs all five LX desktop providers concurrently. Results retain the
// provider order used by LX's SDK; one provider failure does not discard others.
func (c *LXSearchClient) Search(ctx context.Context, keyword string, page int) (SearchPage, map[string]string, error) {
	if c == nil { return SearchPage{}, nil, errors.New("洛雪搜索 SDK 未初始化") }
	ids := make([]string, 0, len(c.providers))
	for _, provider := range c.providers { ids = append(ids, provider.ID()) }
	return c.SearchSources(ctx, keyword, page, ids)
}

// SearchSources searches only providers declared by the active LX user script.
// Provider implementations remain the actual source of search capability.
func (c *LXSearchClient) SearchSources(ctx context.Context, keyword string, page int, sourceIDs []string) (SearchPage, map[string]string, error) {
	keyword = strings.TrimSpace(keyword)
	if keyword == "" { return SearchPage{}, nil, errors.New("请输入歌曲名称或歌手") }
	if page < 1 { return SearchPage{}, nil, errors.New("搜索页码必须大于 0") }
	if c == nil || len(c.providers) == 0 { return SearchPage{}, nil, errors.New("洛雪搜索 SDK 未初始化") }
	if ctx == nil { ctx = context.Background() }
	allowed := make(map[string]bool, len(sourceIDs))
	for _, id := range sourceIDs { allowed[id] = true }
	providerByID := make(map[string]SearchProvider, len(c.providers))
	for _, provider := range c.providers { providerByID[provider.ID()] = provider }
	providers := make([]SearchProvider, 0, len(sourceIDs))
	seenProviders := make(map[string]bool, len(sourceIDs))
	for _, id := range sourceIDs {
		if !allowed[id] || seenProviders[id] { continue }
		if provider, ok := providerByID[id]; ok { providers = append(providers, provider); seenProviders[id] = true }
	}
	if len(providers) == 0 { return SearchPage{Tracks: []Track{}, Page: page, PageSize: lxSongSearchPageSize}, map[string]string{}, nil }

	type outcome struct { page SearchPage; err error }
	outcomes := make([]outcome, len(providers))
	var wg sync.WaitGroup
	for i, provider := range providers {
		wg.Add(1)
		go func(index int, p SearchProvider) {
			defer wg.Done()
			outcomes[index].page, outcomes[index].err = p.Search(ctx, keyword, page, lxSongSearchPageSize)
		}(i, provider)
	}
	wg.Wait()

	result := SearchPage{Tracks: []Track{}, Page: page, PageSize: lxSongSearchPageSize}
	errs := make(map[string]string)
	successes := 0
	for i, provider := range providers {
		outcome := outcomes[i]
		if outcome.err != nil { errs[provider.ID()] = outcome.err.Error(); continue }
		successes++
		if outcome.page.MaxPage < page { continue }
		result.Tracks = append(result.Tracks, outcome.page.Tracks...)
		if outcome.page.Total > result.Total { result.Total = outcome.page.Total }
		if outcome.page.MaxPage > result.MaxPage { result.MaxPage = outcome.page.MaxPage }
	}
	if successes == 0 {
		if len(errs) == 0 { return result, errs, nil }
		if page == 1 { result.MaxPage = 1 } else { result.Total = int64(result.PageSize * page) }
		return result, errs, nil
	}
	result.Tracks = deduplicateLXTracks(result.Tracks)
	// Cross-provider results are grouped by the user's configured source
	// priority. Each provider's native ranking is retained within its group.
	if page > 1 && (result.Total == 0 || len(result.Tracks) == 0) { result.Total = int64(result.PageSize * page) }
	return result, errs, nil
}

func (c *LXSearchClient) SearchProvider(ctx context.Context, source, keyword string, page int) (SearchPage, error) {
	if c == nil { return SearchPage{}, errors.New("洛雪搜索 SDK 未初始化") }
	for _, provider := range c.providers {
		if provider.ID() == source { pageResult, err := provider.Search(ctx, keyword, page, lxSongSearchPageSize)
			if err != nil { return SearchPage{}, err }
			pageResult.Tracks = deduplicateLXTracks(pageResult.Tracks)
			return pageResult, nil }
	}
	return SearchPage{}, fmt.Errorf("不支持的音乐来源：%s", source)
}

func doLXJSON(ctx context.Context, client *http.Client, method, rawURL string, headers map[string]string, body io.Reader) (map[string]any, error) {
	req, err := http.NewRequestWithContext(ctx, method, rawURL, body)
	if err != nil { return nil, err }
	req.Header.Set("User-Agent", "lx-music request")
	req.Header.Set("bHhtl", "bHhtl")
	for key, value := range headers { req.Header.Set(key, value) }
	resp, err := client.Do(req)
	if err != nil { return nil, fmt.Errorf("请求音乐平台失败：%w", err) }
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil { return nil, fmt.Errorf("读取音乐平台响应失败：%w", err) }
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("音乐平台返回 HTTP %d", resp.StatusCode)
	}
	var result map[string]any
	if err := json.Unmarshal(raw, &result); err != nil { return nil, fmt.Errorf("解析音乐平台响应失败：%w", err) }
	return result, nil
}

func jsonBody(value any) ([]byte, error) { return json.Marshal(value) }
func mapValue(value any) map[string]any { v, _ := value.(map[string]any); return v }
func sliceValue(value any) []any { v, _ := value.([]any); return v }
func stringValue(value any) string {
	switch v := value.(type) {
	case string: return v
	case json.Number: return v.String()
	case float64: return strconv.FormatInt(int64(v), 10)
	case int: return strconv.Itoa(v)
	case int64: return strconv.FormatInt(v, 10)
	default: return ""
	}
}
func intValue(value any) int64 {
	switch v := value.(type) {
	case float64: return int64(v)
	case json.Number: n, _ := v.Int64(); return n
	case string: n, _ := strconv.ParseInt(v, 10, 64); return n
	case int: return int64(v)
	case int64: return v
	default: return 0
	}
}
func nestedMap(value any, keys ...string) map[string]any {
	current := mapValue(value)
	for _, key := range keys { current = mapValue(current[key]) }
	return current
}
func decodeLXName(value string) string { return strings.TrimSpace(html.UnescapeString(value)) }
func formatLXDuration(seconds int64) int64 {
	if seconds < 0 { return 0 }
	return seconds * 1000
}
func formatLXSize(size int64) string {
	if size == 0 { return "0 B" }
	if size < 0 { return "" }
	units := []string{"B", "KiB", "MiB", "GiB", "TiB"}
	exponent := int(math.Floor(math.Log(float64(size)) / math.Log(1024)))
	if exponent >= len(units) { exponent = len(units)-1 }
	return fmt.Sprintf("%.2f %s", float64(size)/math.Pow(1024, float64(exponent)), units[exponent])
}
func newLXTrack(source, nativeID, title, artist, album string, duration int64, cover string, data map[string]string, qualities []TrackQuality) Track {
	if data == nil { data = map[string]string{} }
	id := source + "_" + nativeID
	if source == "kg" {
		hash := data["hash"]
		if hash == "" { hash = "undefined" }
		id = nativeID + "_" + hash
	}
	return Track{
		ID: id, SourceID: source, LXSourceKey: source,
		SourceData: data, Title: decodeLXName(title), Artist: decodeLXName(artist),
		Album: decodeLXName(album), DurationMS: duration, CoverURL: cover,
		Qualities: qualities,
	}
}
func quality(kind string, size int64, data map[string]string) TrackQuality {
	return TrackQuality{Type: kind, Size: formatLXSize(size), SourceData: data}
}
func appendUniqueQuality(qualities []TrackQuality, item TrackQuality) []TrackQuality {
	for _, existing := range qualities {
		if existing.Type == item.Type { return qualities }
	}
	return append(qualities, item)
}
func qualityFromOptionalObject(kind string, raw any, sizeKey string) TrackQuality {
	if raw == nil { return TrackQuality{Type: kind} }
	return quality(kind, intValue(mapValue(raw)[sizeKey]), nil)
}

func deduplicateLXTracks(tracks []Track) []Track {
	seen := make(map[string]struct{}, len(tracks))
	result := make([]Track, 0, len(tracks))
	for _, track := range tracks {
		if _, exists := seen[track.ID]; exists { continue }
		seen[track.ID] = struct{}{}
		result = append(result, track)
	}
	return result
}

func lxSimilarity(a, b string) float64 {
	ar, br := utf16.Encode([]rune(a)), utf16.Encode([]rune(b))
	if len(ar) == 0 || len(br) == 0 { return 0 }
	if len(ar) > len(br) { ar, br = br, ar }
	prev := make([]int, len(br)+1)
	for i := range prev { prev[i] = i }
	for _, ch := range ar {
		diag := prev[0]
		prev[0]++
		for j := 1; j <= len(br); j++ {
			old := prev[j]
			cost := 1
			if ch == br[j-1] { cost = 0 }
			prev[j] = lxMinInt(prev[j]+1, prev[j-1]+1, diag+cost)
			diag = old
		}
	}
	return 1 - float64(prev[len(br)])/float64(len(br))
}

func lxMinInt(values ...int) int {
	minimum := values[0]
	for _, value := range values[1:] { if value < minimum { minimum = value } }
	return minimum
}

func sortLXTracksByRelevance(tracks []Track, keyword string) []Track {
	type ranked struct { track Track; score float64 }
	items := make([]ranked, len(tracks))
	for i, track := range tracks {
		items[i] = ranked{track:track, score:lxSimilarity(keyword, track.Title+" "+track.Artist)}
	}
	sort.SliceStable(items, func(i,j int) bool { return items[i].score > items[j].score })
	result := make([]Track, len(items))
	for i, item := range items { result[i] = item.track }
	return result
}

func pageCount(total int64, size int) int {
	if total <= 0 || size <= 0 { return 1 }
	return int(math.Ceil(float64(total)/float64(size)))
}

// Kuwo (kw)
type kuwoSearchProvider struct { client *http.Client; endpoint string }
func (p kuwoSearchProvider) ID() string { return LXSearchKW }
func (p kuwoSearchProvider) Name() string { return "酷我音乐" }
var kuwoInfoPattern = regexp.MustCompile(`level:(\w+),bitrate:(\d+),format:(\w+),size:([\w.]+)`)
func (p kuwoSearchProvider) Search(ctx context.Context, keyword string, page, limit int) (SearchPage, error) {
	if limit <= 0 { limit = lxSongSearchPageSize }
	u, _ := url.Parse(p.endpoint)
	q := u.Query()
	q.Set("client", "kt"); q.Set("all", keyword); q.Set("pn", strconv.Itoa(page-1)); q.Set("rn", strconv.Itoa(limit))
	q.Set("uid", "794762570"); q.Set("ver", "kwplayer_ar_9.2.2.1"); q.Set("vipver", "1")
	q.Set("show_copyright_off", "1"); q.Set("newver", "1"); q.Set("ft", "music"); q.Set("cluster", "0")
	q.Set("strategy", "2012"); q.Set("encoding", "utf8"); q.Set("rformat", "json"); q.Set("vermerge", "1"); q.Set("mobi", "1"); q.Set("issubtitle", "1")
	u.RawQuery = q.Encode()
	raw, err := doLXJSON(ctx, p.client, http.MethodGet, u.String(), nil, nil)
	if err != nil { return SearchPage{}, err }
	total, _ := strconv.ParseInt(stringValue(raw["TOTAL"]), 10, 64)
	list := make([]Track, 0)
	if stringValue(raw["TOTAL"]) != "0" && stringValue(raw["SHOW"]) == "0" {
		return SearchPage{}, errors.New("酷我搜索未返回有效结果")
	}
	for _, entry := range sliceValue(raw["abslist"]) {
		item := mapValue(entry)
		if strings.TrimSpace(stringValue(item["N_MINFO"])) == "" {
			return SearchPage{}, errors.New("酷我搜索结果缺少音质元数据")
		}
		id := strings.TrimPrefix(stringValue(item["MUSICRID"]), "MUSIC_")
		name := stringValue(item["SONGNAME"])
		if id == "" || name == "" { continue }
		types := []TrackQuality{}
		for _, token := range strings.Split(stringValue(item["N_MINFO"]), ";") {
			match := kuwoInfoPattern.FindStringSubmatch(token)
			if len(match) != 5 { continue }
			var kind string
			switch match[2] { case "4000", "2000": kind="flac"; case "320": kind="320k"; case "128": kind="128k" }
			if kind != "" { types = appendUniqueQuality(types, TrackQuality{Type:kind, Size:match[4]}) }
		}
		for i,j:=0,len(types)-1;i<j;i,j=i+1,j-1 { types[i],types[j]=types[j],types[i] }
		duration, _ := strconv.ParseInt(stringValue(item["DURATION"]),10,64)
		cover := normalizeKuwoCoverURL(firstLXString(item, "web_albumpic_short", "ALBUMPIC", "albumpic", "albumPic", "pic", "cover"))
		data := map[string]string{"songmid":id,"album_id":decodeLXName(stringValue(item["ALBUMID"]))}
		if cover != "" { data["cover_url"] = cover }
		list = append(list,newLXTrack("kw",id,stringValue(item["SONGNAME"]),strings.ReplaceAll(decodeLXName(stringValue(item["ARTIST"])), "&", "、"),stringValue(item["ALBUM"]),formatLXDuration(duration),cover,data,types))
	}
	return SearchPage{Tracks:list,Total:total,Page:page,PageSize:limit,MaxPage:pageCount(total,limit),Source:"kw"},nil
}

// Kugou (kg)
type kugouSearchProvider struct { client *http.Client; endpoint string }
func (p kugouSearchProvider) ID() string { return LXSearchKG }
func (p kugouSearchProvider) Name() string { return "酷狗音乐" }
func (p kugouSearchProvider) Search(ctx context.Context, keyword string, page, limit int) (SearchPage, error) {
	if limit <= 0 {
		limit = lxSongSearchPageSize
	}
	u, err := url.Parse(p.endpoint)
	if err != nil {
		return SearchPage{}, fmt.Errorf("酷狗搜索地址无效：%w", err)
	}
	q := u.Query()
	// song_search_v2 is the WebFilter endpoint. AndroidFilter and the
	// incorrectly-cased PrivilegeFilter parameter can return an empty list
	// even for well-known songs such as 稻香.
	q.Set("platform", "WebFilter")
	q.Set("iscorrection", "1")
	q.Set("keyword", keyword)
	q.Set("hifiquality", "0")
	q.Set("pagesize", strconv.Itoa(limit))
	q.Set("privilege_filter", "0")
	q.Set("filter", "2")
	q.Set("tag", "em")
	q.Set("userid", "-1")
	q.Set("clientver", "")
	q.Set("page", strconv.Itoa(page))
	u.RawQuery = q.Encode()

	raw, err := doLXJSON(ctx, p.client, http.MethodGet, u.String(), map[string]string{
		"User-Agent": "Mozilla/5.0",
		"Referer": "https://www.kugou.com/",
		"Accept": "application/json, text/plain, */*",
	}, nil)
	if err != nil {
		return SearchPage{}, err
	}
	if code := intValue(raw["error_code"]); code != 0 {
		return SearchPage{}, fmt.Errorf("酷狗搜索失败，错误码：%d", code)
	}
	data := mapValue(raw["data"])
	items := sliceValue(data["lists"])
	total := intValue(data["total"])
	list := make([]Track, 0, len(items))
	seen := make(map[string]bool, len(items))
	for _, entry := range items {
		item := mapValue(entry)
		id := firstTrackValue(stringValue(item["Audioid"]), stringValue(item["ID"]), stringValue(item["MixSongID"]))
		hash := firstTrackValue(stringValue(item["FileHash"]), stringValue(item["Hash"]))
		key := id + ":" + hash
		if id == "" || seen[key] {
			continue
		}
		seen[key] = true

		// Kugou's current WebFilter response may use SongName/SingerName
		// instead of the older OriSongName/Singers fields. Search highlighting
		// wraps matched words in <em>, which must not leak into the Track model.
		name := cleanLXSearchText(firstTrackValue(stringValue(item["OriSongName"]), stringValue(item["SongName"]), stringValue(item["FileName"])))
		if suffix := cleanLXSearchText(stringValue(item["Suffix"])); suffix != "" {
			name = strings.TrimSpace(name + " " + suffix)
		}
		artists := make([]string, 0)
		for _, value := range sliceValue(item["Singers"]) {
			singer := mapValue(value)
			if artist := cleanLXSearchText(firstTrackValue(stringValue(singer["name"]), stringValue(singer["SingerName"]))); artist != "" {
				artists = append(artists, artist)
			}
		}
		if len(artists) == 0 {
			if artist := cleanLXSearchText(stringValue(item["SingerName"])); artist != "" {
				artists = append(artists, artist)
			}
		}
		if len(artists) == 0 {
			fileName := cleanLXSearchText(stringValue(item["FileName"]))
			if separator := strings.Index(fileName, " - "); separator > 0 {
				artists = append(artists, strings.TrimSpace(fileName[:separator]))
			}
		}

		types := make([]TrackQuality, 0, 4)
		for _, spec := range []struct{ field, sizeField, kind string }{
			{"FileHash", "FileSize", "128k"},
			{"HQFileHash", "HQFileSize", "320k"},
			{"SQFileHash", "SQFileSize", "flac"},
			{"ResFileHash", "ResFileSize", "hires"},
		} {
			if size := intValue(item[spec.sizeField]); size > 0 {
				types = appendUniqueQuality(types, quality(spec.kind, size, map[string]string{"hash": stringValue(item[spec.field])}))
			}
		}
		duration := intValue(item["Duration"])
		album := firstTrackValue(stringValue(item["AlbumName"]), stringValue(item["Album"]))
		cover := normalizeKugouCoverURL(firstLXString(item, "Image", "Img", "AlbumImg", "AlbumImage", "album_img", "cover", "Cover", "pic"))
		sourceData := map[string]string{
			"songmid": id,
			"hash": hash,
			"album_id": firstTrackValue(stringValue(item["AlbumID"]), stringValue(item["album_id"])),
			"album_audio_id": stringValue(item["MixSongID"]),
		}
		if cover != "" { sourceData["cover_url"] = cover }
		list = append(list, newLXTrack("kg", id, name, strings.Join(artists, "、"), album, formatLXDuration(duration), cover, sourceData, types))
	}
	return SearchPage{Tracks: list, Total: total, Page: page, PageSize: limit, MaxPage: pageCount(total, limit), Source: "kg"}, nil
}

// firstLXString returns the first non-empty string field from a provider payload.
func firstLXString(item map[string]any, keys ...string) string {
	for _, key := range keys {
		if value := strings.TrimSpace(stringValue(item[key])); value != "" { return value }
	}
	return ""
}

// normalizeKuwoCoverURL converts Kuwo's web_albumpic_short relative path into
// the public album-cover URL used by its desktop/mobile clients.
func normalizeKuwoCoverURL(value string) string {
	value = strings.TrimSpace(value)
	if value == "" { return "" }
	if strings.HasPrefix(value, "//") { return "https:" + value }
	if strings.HasPrefix(value, "http://") || strings.HasPrefix(value, "https://") {
		parsed, err := url.Parse(value)
		if err != nil { return "" }
		value = parsed.Path
	}
	parts := strings.Split(strings.Trim(value, "/"), "/")
	for i, part := range parts {
		if strings.EqualFold(part, "albumcover") {
			tail := parts[i+1:]
			if len(tail) == 0 { return "" }
			return "https://img3.kuwo.cn/star/albumcover/300/" + strings.Join(tail, "/")
		}
	}
	if len(parts) > 1 { parts = parts[1:] }
	if len(parts) == 0 { return "" }
	return "https://img3.kuwo.cn/star/albumcover/300/" + strings.Join(parts, "/")
}

// Kugou search responses commonly expose Image as a size-templated image URL.
func normalizeKugouCoverURL(value string) string {
	value = strings.TrimSpace(value)
	if value == "" { return "" }
	value = strings.ReplaceAll(value, "{size}", "400")
	value = strings.ReplaceAll(value, "{SIZE}", "400")
	if strings.HasPrefix(value, "//") { return "https:" + value }
	if strings.HasPrefix(value, "http://") || strings.HasPrefix(value, "https://") { return value }
	return ""
}

func cleanLXSearchText(value string) string {
	value = strings.ReplaceAll(value, "<em>", "")
	value = strings.ReplaceAll(value, "</em>", "")
	value = strings.ReplaceAll(value, "<strong>", "")
	value = strings.ReplaceAll(value, "</strong>", "")
	return decodeLXName(value)
}

// QQ Music (tx)
type qqSearchProvider struct {
	client *http.Client
	endpoint string
	legacyEndpoint string
}
func (p qqSearchProvider) ID() string { return LXSearchTX }
func (p qqSearchProvider) Name() string { return "QQ 音乐" }

type qqSearchRequest struct {
	Service struct {
		Module string `json:"module"`
		Method string `json:"method"`
		Param struct {
			Group int `json:"grp,omitempty"`
			Limit int `json:"num_per_page"`
			Page int `json:"page_num"`
			Query string `json:"query"`
			Remote string `json:"remoteplace,omitempty"`
			Type int `json:"search_type"`
			SearchID string `json:"searchid,omitempty"`
		} `json:"param"`
	} `json:"music.search.SearchCgiService"`
}

func qqTracksFromDesktop(items []any) []Track {
	tracks := make([]Track, 0, len(items))
	for _, entry := range items {
		item := mapValue(entry)
		file := mapValue(item["file"])
		media := stringValue(file["media_mid"])
		if media == "" { continue }
		types := []TrackQuality{}
		for _, spec := range []struct{ key, kind string }{{"size_128mp3", "128k"}, {"size_320mp3", "320k"}, {"size_flac", "flac"}, {"size_hires", "hires"}} {
			if size := intValue(file[spec.key]); size > 0 { types = appendUniqueQuality(types, quality(spec.kind, size, nil)) }
		}
		album := mapValue(item["album"])
		albumID, albumName := stringValue(album["mid"]), stringValue(album["name"])
		artists := []string{}
		for _, value := range sliceValue(item["singer"]) {
			if name := stringValue(mapValue(value)["name"]); name != "" { artists = append(artists, name) }
		}
		id := stringValue(item["mid"])
		if id == "" { id = stringValue(item["id"]) }
		cover := ""
		if albumID != "" && albumID != "空" {
			cover = "https://y.gtimg.cn/music/photo_new/T002R500x500M000" + albumID + ".jpg"
		} else if singers := sliceValue(item["singer"]); len(singers) > 0 {
			if singerMid := stringValue(mapValue(singers[0])["mid"]); singerMid != "" {
				cover = "https://y.gtimg.cn/music/photo_new/T001R500x500M000" + singerMid + ".jpg"
			}
		}
		dur := intValue(item["interval"]) * 1000
		data := map[string]string{"songmid": id, "song_id": stringValue(item["id"]), "media_mid": media, "album_mid": albumID}
		tracks = append(tracks, newLXTrack("tx", id, stringValue(item["title"]), strings.Join(artists, "、"), albumName, dur, cover, data, types))
	}
	return tracks
}

// qqLegacyTracksFromCGI adapts the legacy song-search payload to the same Track
// model as the Desktop RPC. It deliberately rejects an empty legacy payload:
// old CGI deployments have been observed returning structurally valid but
// permanently empty song lists after the endpoint was retired.
func qqLegacyTracksFromCGI(raw map[string]any, page, limit int) (SearchPage, error) {
	data := mapValue(raw["data"])
	song := mapValue(data["song"])
	if len(data) == 0 || len(song) == 0 {
		return SearchPage{}, errors.New("QQ 旧版歌曲搜索接口未返回有效 song 数据")
	}
	items := sliceValue(song["list"])
	tracks := make([]Track, 0, len(items))
	for _, entry := range items {
		item := mapValue(entry)
		id := stringValue(item["songmid"])
		if id == "" { id = stringValue(item["mid"]) }
		if id == "" { continue }
		media := stringValue(item["strMediaMid"])
		if media == "" { media = stringValue(mapValue(item["file"])["media_mid"]) }
		albumID := stringValue(item["albummid"])
		albumName := stringValue(item["albumname"])
		artists := []string{}
		for _, value := range sliceValue(item["singer"]) {
			artist := mapValue(value)
			name := stringValue(artist["name"])
			if name == "" { name = stringValue(artist["title"]) }
			if name != "" { artists = append(artists, name) }
		}
		file := mapValue(item["file"])
		types := []TrackQuality{}
		for _, spec := range []struct{ keys []string; kind string }{
			{[]string{"size128", "size_128mp3"}, "128k"},
			{[]string{"size320", "size_320mp3"}, "320k"},
			{[]string{"sizeflac", "size_flac"}, "flac"},
			{[]string{"sizeape"}, "ape"},
		} {
			var size int64
			for _, key := range spec.keys {
				size = intValue(item[key])
				if size == 0 { size = intValue(file[key]) }
				if size > 0 { break }
			}
			if size > 0 { types = append(types, quality(spec.kind, size, nil)) }
		}
		cover := ""
		if albumID != "" { cover = "https://y.gtimg.cn/music/photo_new/T002R500x500M000" + albumID + ".jpg" }
		duration := intValue(item["interval"]) * 1000
		sourceData := map[string]string{"songmid": id, "song_id": stringValue(item["songid"]), "media_mid": media, "album_mid": albumID}
		tracks = append(tracks, newLXTrack("tx", id, stringValue(item["songname"]), strings.Join(artists, "、"), albumName, duration, cover, sourceData, types))
	}
	total := intValue(song["totalnum"])
	if len(tracks) == 0 {
		return SearchPage{}, fmt.Errorf("QQ 旧版歌曲搜索接口返回空列表（total=%d），备用接口可能已失效", total)
	}
	return SearchPage{Tracks: tracks, Total: total, Page: page, PageSize: limit, MaxPage: pageCount(total, limit), Source: "tx"}, nil
}

func (p qqSearchProvider) searchLegacy(ctx context.Context, keyword string, page, limit int) (SearchPage, error) {
	endpoint := p.legacyEndpoint
	if endpoint == "" { endpoint = DefaultLXSearchEndpoints().QQLegacy }
	u, err := url.Parse(endpoint)
	if err != nil { return SearchPage{}, fmt.Errorf("QQ 旧版搜索地址无效：%w", err) }
	q := u.Query()
	params := map[string]string{
		"ct":"24", "qqmusic_ver":"1298", "remoteplace":"txt.yqq.song",
		"t":"0", "aggr":"1", "cr":"1", "lossless":"0", "flag_qc":"0",
		"p":strconv.Itoa(page), "n":strconv.Itoa(limit), "w":keyword,
		"g_tk":"5381", "loginUin":"0", "hostUin":"0", "format":"json",
		"inCharset":"utf8", "outCharset":"utf-8", "notice":"0",
		"platform":"yqq.json", "needNewCode":"0",
	}
	for key, value := range params { q.Set(key, value) }
	u.RawQuery = q.Encode()
	raw, err := doLXJSON(ctx, p.client, http.MethodGet, u.String(), map[string]string{
		"User-Agent":"Mozilla/5.0", "Referer":"https://y.qq.com/", "Accept":"application/json, text/plain, */*",
	}, nil)
	if err != nil { return SearchPage{}, fmt.Errorf("QQ 旧版歌曲搜索回退失败：%w", err) }
	if code := intValue(raw["code"]); code != 0 {
		return SearchPage{}, fmt.Errorf("QQ 旧版歌曲搜索返回错误码：%d", code)
	}
	result, err := qqLegacyTracksFromCGI(raw, page, limit)
	if err != nil { return SearchPage{}, err }
	return result, nil
}

func (p qqSearchProvider) Search(ctx context.Context, keyword string, page, limit int) (SearchPage, error) {
	if limit <= 0 { limit = lxSongSearchPageSize }
	var lastStatus string
	for attempt := 0; attempt <= 5; attempt++ {
		var payload qqSearchRequest
		payload.Service.Module = "music.search.SearchCgiService"
		payload.Service.Method = "DoSearchForQQMusicDesktop"
		payload.Service.Param.Limit = limit
		payload.Service.Param.Page = page
		payload.Service.Param.Query = keyword
		payload.Service.Param.Type = 0
		body, err := jsonBody(payload)
		if err != nil { return SearchPage{}, err }
		raw, err := doLXJSON(ctx, p.client, http.MethodPost, p.endpoint, map[string]string{
			"User-Agent":"Mozilla/5.0", "Content-Type":"application/json",
			"Referer":"https://y.qq.com/", "Origin":"https://y.qq.com",
		}, bytes.NewReader(body))
		if err != nil { return SearchPage{}, err }
		req := mapValue(raw["music.search.SearchCgiService"])
		if len(req) == 0 { req = mapValue(raw["req_1"]) }
		if len(req) > 0 && intValue(raw["code"]) == 0 && intValue(req["code"]) == 0 {
			data := mapValue(req["data"])
			resultBody := mapValue(data["body"])
			song := mapValue(resultBody["song"])
			meta := mapValue(data["meta"])
			tracks := qqTracksFromDesktop(sliceValue(song["list"]))
			total := intValue(meta["sum"])
			if total == 0 { total = intValue(song["totalnum"]) }
			return SearchPage{Tracks:tracks, Total:total, Page:page, PageSize:limit, MaxPage:pageCount(total,limit), Source:"tx"}, nil
		}
		providerCode := intValue(req["code"])
		if providerCode == 2001 {
			// 2001 is returned by the anonymous Desktop request. Repeating the same
			// unsigned payload cannot change its identity; switch to legacy CGI once.
			return p.searchLegacy(ctx, keyword, page, limit)
		}
		lastStatus = fmt.Sprintf("top-level code=%v, provider code=%v", raw["code"], req["code"])
		if attempt < 5 {
			select {
			case <-ctx.Done(): return SearchPage{}, ctx.Err()
			case <-time.After(time.Duration(attempt+1) * 150 * time.Millisecond):
			}
		}
	}
	return SearchPage{}, fmt.Errorf("QQ 音乐搜索响应状态异常（重试 6 次后仍失败：%s）", lastStatus)
}

// NetEase Cloud Music (wy): LX desktop uses the EAPI AES-128-ECB request.
type neteaseSearchProvider struct{client *http.Client;endpoint string}
func(p neteaseSearchProvider)ID()string{return LXSearchWY}
func(p neteaseSearchProvider)Name()string{return "网易云音乐"}
func eapiEncrypt(path string,data any)(string,error){
	plain,err:=json.Marshal(data);if err!=nil{return "",err}
	message:="nobody"+path+"use"+string(plain)+"md5forencrypt"
	sum:=md5.Sum([]byte(message));digest:=hex.EncodeToString(sum[:])
	text:=path+"-36cd479b6b5-"+string(plain)+"-36cd479b6b5-"+digest
	block,err:=aes.NewCipher([]byte("e82ckenh8dichen8"));if err!=nil{return "",err}
	padding:=aes.BlockSize-len(text)%aes.BlockSize;input:=append([]byte(text),bytes.Repeat([]byte{byte(padding)},padding)...)
	out:=make([]byte,len(input));for i:=0;i<len(input);i+=aes.BlockSize{block.Encrypt(out[i:i+aes.BlockSize],input[i:i+aes.BlockSize])}
	return strings.ToUpper(hex.EncodeToString(out)),nil
}
func(p neteaseSearchProvider)Search(ctx context.Context,keyword string,page,limit int)(SearchPage,error){
	if limit <= 0 { limit = lxSongSearchPageSize }
	path:="/api/search/song/list/page"
	payload:=struct{
		Keyword string `json:"keyword"`
		NeedCorrect string `json:"needCorrect"`
		Channel string `json:"channel"`
		Offset int `json:"offset"`
		Scene string `json:"scene"`
		Total bool `json:"total"`
		Limit int `json:"limit"`
	}{keyword,"1","typing",limit*(page-1),"normal",page==1,limit}
	params,err:=eapiEncrypt(path,payload);if err!=nil{return SearchPage{},err}
	form:=url.Values{"params":{params}}
	raw,err:=doLXJSON(ctx,p.client,http.MethodPost,p.endpoint,map[string]string{"User-Agent":"Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/60.0.3112.90 Safari/537.36","Origin":"https://music.163.com","Content-Type":"application/x-www-form-urlencoded"},strings.NewReader(form.Encode()))
	if err!=nil{return SearchPage{},err}
	if intValue(raw["code"])!=200{return SearchPage{},fmt.Errorf("网易云搜索失败，状态码：%s",stringValue(raw["code"]))}
	data:=mapValue(raw["data"]);resources:=sliceValue(data["resources"]);tracks:=make([]Track,0,len(resources))
	for _,entry:=range resources{
		base:=mapValue(mapValue(entry)["baseInfo"]);item:=mapValue(base["simpleSongData"]);if len(item)==0{continue}
		album:=mapValue(item["al"]);priv:=mapValue(item["privilege"])
		types:=[]TrackQuality{};maxLevel:=stringValue(priv["maxBrLevel"]);maxBR:=intValue(priv["maxbr"])
		if maxLevel=="hires"{types=append(types,qualityFromOptionalObject("hires",item["hr"],"size"))}
		if maxBR>=999000{types=append(types,qualityFromOptionalObject("flac",item["sq"],"size"))}
		if maxBR>=320000{types=append(types,qualityFromOptionalObject("320k",item["h"],"size"))}
		if maxBR>=128000{types=append(types,qualityFromOptionalObject("128k",item["l"],"size"))}
		for i,j:=0,len(types)-1;i<j;i,j=i+1,j-1 { types[i],types[j]=types[j],types[i] }
		artists:=[]string{};for _,v:=range sliceValue(item["ar"]){if n:=stringValue(mapValue(v)["name"]);n!=""{artists=append(artists,n)}}
		id:=stringValue(item["id"]);albumID:=stringValue(album["id"])
		tracks=append(tracks,newLXTrack("wy",id,stringValue(item["name"]),strings.Join(artists,"、"),stringValue(album["name"]),intValue(item["dt"]),stringValue(album["picUrl"]),map[string]string{"songmid":id,"album_id":albumID},types))
	}
	total:=intValue(data["totalCount"])
	return SearchPage{Tracks:tracks,Total:total,Page:page,PageSize:limit,MaxPage:pageCount(total,limit),Source:"wy"},nil
}

// Migu (mg): request signing follows the LX desktop SDK's MD5 signature.
type miguSearchProvider struct {
	client *http.Client
	endpoint string
	fallbackEndpoint string
}

func (p miguSearchProvider) ID() string { return LXSearchMG }
func (p miguSearchProvider) Name() string { return "咪咕音乐" }

func (p miguSearchProvider) Search(ctx context.Context, keyword string, page, limit int) (SearchPage, error) {
	if limit <= 0 { limit = lxSongSearchPageSize }
	now := strconv.FormatInt(time.Now().UnixMilli(), 10)
	device := "963B7AA0D21511ED807EE5846EC87D20"
	signatureMD5 := "6cdc72a439cef99a3418d2a78aa28c73"
	signature := md5.Sum([]byte(keyword + signatureMD5 + "yyapp2d16148780a1dcc7408e06336b98cfd50" + device + now))
	u, _ := url.Parse(p.endpoint)
	q := u.Query()
	q.Set("isCorrect", "0")
	q.Set("isCopyright", "1")
	q.Set("searchSwitch", `{"song":1,"album":0,"singer":0,"tagSong":1,"mvSong":0,"bestShow":1,"songlist":0,"lyricSong":0}`)
	q.Set("pageSize", strconv.Itoa(limit))
	q.Set("text", keyword)
	q.Set("pageNo", strconv.Itoa(page))
	q.Set("sort", "0")
	q.Set("sid", "USS")
	u.RawQuery = q.Encode()
	headers := map[string]string{
		"uiVersion": "A_music_3.6.1", "deviceId": device, "timestamp": now,
		"sign": hex.EncodeToString(signature[:]), "channel": "0146921",
		"User-Agent": "Mozilla/5.0 (Linux; U; Android 11.0.0; zh-cn; MI 11 Build/OPR1.170623.032) AppleWebKit/534.30 (KHTML, like Gecko) Version/4.0 Mobile Safari/534.30",
	}
	raw, primaryErr := doLXJSON(ctx, p.client, http.MethodGet, u.String(), headers, nil)
	if primaryErr == nil {
		if result, parseErr := parseMiguSearchResponse(raw, page, limit); parseErr == nil && len(result.Tracks) > 0 {
			return result, nil
		} else if parseErr != nil {
			primaryErr = parseErr
		} else {
			primaryErr = errors.New("主搜索接口未返回歌曲")
		}
	}
	fallbackURL, fallbackErr := buildMiguFallbackSearchURL(p.fallbackEndpoint, keyword, page, limit)
	if fallbackErr != nil {
		return SearchPage{}, fmt.Errorf("咪咕搜索主接口失败：%v；备用接口地址无效：%w", primaryErr, fallbackErr)
	}
	fallbackHeaders := map[string]string{
		"User-Agent": "Mozilla/5.0 (Linux; Android 11; zh-cn) AppleWebKit/537.36 Chrome/120.0.0.0 Mobile Safari/537.36",
		"channel": "0146921",
		"Accept": "application/json, text/plain, */*",
	}
	fallbackRaw, fallbackErr := doLXJSON(ctx, p.client, http.MethodGet, fallbackURL, fallbackHeaders, nil)
	if fallbackErr != nil {
		return SearchPage{}, fmt.Errorf("咪咕搜索主接口失败：%v；备用接口失败：%w", primaryErr, fallbackErr)
	}
	result, fallbackErr := parseMiguSearchResponse(fallbackRaw, page, limit)
	if fallbackErr != nil {
		return SearchPage{}, fmt.Errorf("咪咕搜索主接口失败：%v；备用接口解析失败：%w", primaryErr, fallbackErr)
	}
	if len(result.Tracks) == 0 {
		return SearchPage{}, fmt.Errorf("咪咕搜索主接口失败：%v；备用接口未返回歌曲", primaryErr)
	}
	return result, nil
}

func buildMiguFallbackSearchURL(endpoint, keyword string, page, limit int) (string, error) {
	target, err := url.Parse(strings.TrimSpace(endpoint))
	if err != nil || target.Scheme == "" || target.Host == "" {
		return "", errors.New("fallback endpoint must be an absolute HTTP(S) URL")
	}
	if target.Scheme != "http" && target.Scheme != "https" {
		return "", errors.New("fallback endpoint must use HTTP(S)")
	}
	query := target.Query()
	query.Set("ua", "Android_migu")
	query.Set("version", "5.0.1")
	query.Set("text", keyword)
	query.Set("pageNo", strconv.Itoa(page))
	query.Set("pageSize", strconv.Itoa(limit))
	query.Set("searchSwitch", `{"song":1,"album":0,"singer":0,"tagSong":0,"mvSong":0,"songlist":0,"bestShow":1}`)
	target.RawQuery = query.Encode()
	return target.String(), nil
}

func parseMiguSearchResponse(raw map[string]any, page, limit int) (SearchPage, error) {
	if stringValue(raw["code"]) != "000000" {
		return SearchPage{}, fmt.Errorf("咪咕搜索失败：%s", stringValue(raw["info"]))
	}
	data := mapValue(raw["songResultData"])
	items := sliceValue(data["resultList"])
	if len(items) == 0 { items = sliceValue(data["result"]) }
	if len(items) == 0 { items = sliceValue(data["musics"]) }
	tracks := make([]Track, 0, len(items))
	seen := map[string]bool{}
	for _, group := range items {
		entries := sliceValue(group)
		if len(entries) == 0 { entries = []any{group} }
		for _, entry := range entries {
			item := mapValue(entry)
			id := firstTrackValue(stringValue(item["songId"]), stringValue(item["contentId"]), stringValue(item["id"]))
			copyright := firstTrackValue(stringValue(item["copyrightId"]), id)
			if id == "" || seen[copyright] { continue }
			seen[copyright] = true
			types := []TrackQuality{}
			for _, value := range sliceValue(item["audioFormats"]) {
				format := mapValue(value)
				kind := ""
				switch stringValue(format["formatType"]) {
				case "PQ": kind = "128k"
				case "HQ": kind = "320k"
				case "SQ": kind = "flac"
				case "ZQ24": kind = "flac"
				}
				if kind != "" {
					size := intValue(format["asize"])
					if size == 0 { size = intValue(format["isize"]) }
					types = append(types, quality(kind, size, nil))
				}
			}
			if len(types) == 0 {
				if stringValue(item["mp3"]) != "" || stringValue(item["listenUrl"]) != "" || stringValue(item["lisQq"]) != "" {
					types = append(types, TrackQuality{Type: "128k"})
				}
			}
			artists := []string{}
			for _, value := range sliceValue(item["singerList"]) {
				if name := stringValue(mapValue(value)["name"]); name != "" { artists = append(artists, name) }
			}
			if len(artists) == 0 {
				for _, value := range sliceValue(item["singers"]) {
					if name := stringValue(mapValue(value)["name"]); name != "" { artists = append(artists, name) }
				}
			}
			artist := strings.Join(artists, "、")
			if artist == "" { artist = firstTrackValue(stringValue(item["artist"]), stringValue(item["singer"])) }
			album := firstTrackValue(stringValue(item["album"]), stringValue(item["albumName"]))
			if album == "" {
				albums := sliceValue(item["albums"])
				if len(albums) > 0 { album = stringValue(mapValue(albums[0])["name"]) }
			}
			img := firstTrackValue(stringValue(item["img3"]), stringValue(item["img2"]), stringValue(item["img1"]))
			if img == "" {
				imgItems := sliceValue(item["imgItems"])
				if len(imgItems) > 0 { img = firstTrackValue(stringValue(mapValue(imgItems[0])["img"]), stringValue(mapValue(imgItems[0])["url"])) }
			}
			if img != "" && !strings.HasPrefix(img, "http://") && !strings.HasPrefix(img, "https://") {
				img = "https://d.musicapp.migu.cn" + img
			}
			duration := intValue(item["duration"])
			if duration > 0 && duration <= 10000 { duration = formatLXDuration(duration) }
			dataMap := map[string]string{
				"songmid": id, "content_id": firstTrackValue(stringValue(item["contentId"]), id),
				"copyright_id": copyright, "album_id": firstTrackValue(stringValue(item["albumId"]), stringValue(item["albumid"])),
				"lrc_url": firstTrackValue(stringValue(item["lrcUrl"]), stringValue(item["lyricUrl"])),
				"mrc_url": stringValue(item["mrcurl"]), "trc_url": stringValue(item["trcUrl"]),
			}
			tracks = append(tracks, newLXTrack("mg", id,
				firstTrackValue(stringValue(item["name"]), stringValue(item["songName"])),
				artist, album, duration, img, dataMap, types))
		}
	}
	total := intValue(data["totalCount"])
	if total == 0 { total = intValue(data["total"]) }
	return SearchPage{Tracks: tracks, Total: total, Page: page, PageSize: limit, MaxPage: pageCount(total, limit), Source: "mg"}, nil
}
