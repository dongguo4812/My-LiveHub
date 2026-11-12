package bilibili

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/url"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	videoSearchEndpoint = "https://api.bilibili.com/x/web-interface/wbi/search/type"
	videoViewEndpoint = "https://api.bilibili.com/x/web-interface/view"
	videoPlayEndpoint = "https://api.bilibili.com/x/player/wbi/playurl"
	videoHomeEndpoint = "https://www.bilibili.com/"
	videoNavEndpoint = "https://api.bilibili.com/x/web-interface/nav"
)

type VideoSearchResult struct {
	BVID string `json:"bvid"`
	Title string `json:"title"`
	Author string `json:"author"`
	Cover string `json:"cover"`
	Duration string `json:"duration"`
	PlayCount int64 `json:"play_count"`
	DanmakuCount int64 `json:"danmaku_count"`
}

type VideoPage struct {
	CID int64 `json:"cid"`
	Page int `json:"page"`
	Part string `json:"part"`
	Duration int `json:"duration"`
}

type VideoDetail struct {
	BVID string `json:"bvid"`
	Title string `json:"title"`
	Author string `json:"author"`
	Cover string `json:"cover"`
	Duration int `json:"duration"`
	Pages []VideoPage `json:"pages"`
}

type VideoStream struct {
	URL string `json:"url"`
	Quality int `json:"quality"`
	QualityName string `json:"quality_name"`
}

type videoAPIEnvelope struct {
	Code int `json:"code"`
	Message string `json:"message"`
	Data json.RawMessage `json:"data"`
}

var videoSearchTagPattern = regexp.MustCompile(`<[^>]*>`)

type VideoClient struct {
	auth *AuthManager
	client *http.Client
}

func NewVideoClient(auth *AuthManager, client *http.Client) (*VideoClient, error) {
	if auth == nil { return nil, errors.New("Bilibili authentication manager is required") }
	if client == nil { client = &http.Client{Timeout: 18*time.Second} }
	return &VideoClient{auth:auth, client:client}, nil
}

func (c *VideoClient) credentials(ctx context.Context) (Credentials, error) {
	return c.auth.LoadCredentials(ctx)
}

func (c *VideoClient) request(ctx context.Context, endpoint string, query url.Values, cookie string) ([]byte, error) {
	u, err := url.Parse(endpoint)
	if err != nil { return nil, err }
	u.RawQuery = query.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil { return nil, err }
	setWebHeaders(req)
	req.Header.Set("Cookie", cookie)
	resp, err := c.client.Do(req)
	if err != nil { return nil, fmt.Errorf("请求 B 站接口失败：%w", err) }
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 { return nil, fmt.Errorf("B 站接口返回 HTTP %d", resp.StatusCode) }
	return io.ReadAll(io.LimitReader(resp.Body, 8<<20))
}

func (c *VideoClient) ensureBuvid(ctx context.Context, cookie string) (string, error) {
	if strings.Contains(cookie, "buvid3=") { return cookie, nil }
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, videoHomeEndpoint, nil)
	if err != nil { return "", err }
	setWebHeaders(req)
	req.Header.Set("Cookie", cookie)
	resp, err := c.client.Do(req)
	if err != nil { return "", fmt.Errorf("获取 B 站搜索 Cookie 失败：%w", err) }
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<20))
	for _, item := range resp.Cookies() {
		if item.Name == "buvid3" && item.Value != "" { return cookie + "; buvid3=" + item.Value, nil }
	}
	return cookie, nil
}

func (c *VideoClient) wbiKeys(ctx context.Context, cookie string) (string, string, error) {
	body, err := c.request(ctx, videoNavEndpoint, url.Values{}, cookie)
	if err != nil { return "", "", err }
	var envelope struct {
		Code int `json:"code"`
		Message string `json:"message"`
		Data struct {
			WBI struct { ImgURL string `json:"img_url"`; SubURL string `json:"sub_url"` } `json:"wbi_img"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil { return "", "", err }
	if envelope.Code != 0 { return "", "", fmt.Errorf("读取 B 站 WBI 密钥失败：%s", safeMessage(envelope.Message)) }
	return keyFromImageURL(envelope.Data.WBI.ImgURL), keyFromImageURL(envelope.Data.WBI.SubURL), nil
}

func keyFromImageURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil { return "" }
	name := path.Base(u.Path)
	return strings.TrimSuffix(name, path.Ext(name))
}

var wbiMixinTable = []int{
	46, 47, 18,  2, 53,  8, 23, 32, 15, 50, 10, 31, 58,  3, 45, 35,
	27, 43,  5, 49, 33,  9, 42, 19, 29, 28, 14, 39, 12, 38, 41, 13,
	37, 48,  7, 16, 24, 55, 40, 61, 26, 17,  0,  1, 60, 51, 30,  4,
	22, 25, 54, 21, 56, 59,  6, 63, 57, 62, 11, 36, 20, 34, 44, 52,
}

func mixinWBIKey(imgKey, subKey string) string {
	source := imgKey + subKey
	var result strings.Builder
	for _, index := range wbiMixinTable {
		if index < len(source) { result.WriteByte(source[index]) }
	}
	key := result.String()
	if len(key) > 32 { key = key[:32] }
	return key
}

func signWBI(params url.Values, imgKey, subKey string, now time.Time) url.Values {
	clean := make(url.Values, len(params)+2)
	for key, values := range params {
		if key == "w_rid" || key == "wts" { continue }
		for _, value := range values {
			value = strings.Map(func(r rune) rune {
				if strings.ContainsRune("!'()*", r) { return -1 }
				return r
			}, value)
			clean.Add(key, value)
		}
	}
	clean.Set("wts", strconv.FormatInt(now.Unix(), 10))
	keys := make([]string, 0, len(clean))
	for key := range clean { keys = append(keys, key) }
	sort.Strings(keys)
	sorted := make(url.Values, len(clean))
	var canonicalParts []string
	for _, key := range keys {
		sorted[key] = clean[key]
		for _, value := range clean[key] {
			encodedKey := strings.ReplaceAll(url.QueryEscape(key), "+", "%20")
			encodedValue := strings.ReplaceAll(url.QueryEscape(value), "+", "%20")
			canonicalParts = append(canonicalParts, encodedKey+"="+encodedValue)
		}
	}
	sum := md5.Sum([]byte(strings.Join(canonicalParts, "&") + mixinWBIKey(imgKey, subKey)))
	sorted.Set("w_rid", hex.EncodeToString(sum[:]))
	return sorted
}

func (c *VideoClient) Search(ctx context.Context, keyword string, page int) ([]VideoSearchResult, error) {
	keyword = strings.TrimSpace(keyword)
	if keyword == "" { return nil, errors.New("请输入视频关键词") }
	if page < 1 || page > 50 { return nil, errors.New("视频搜索页码必须在 1 到 50 之间") }
	credentials, err := c.credentials(ctx)
	if err != nil { return nil, err }
	cookie, err := c.ensureBuvid(ctx, credentials.Cookie)
	if err != nil { return nil, err }
	imgKey, subKey, err := c.wbiKeys(ctx, cookie)
	if err != nil { return nil, err }
	params := url.Values{
		"search_type":{"video"}, "keyword":{keyword}, "order":{"totalrank"},
		"duration":{"0"}, "tids":{"0"}, "page":{strconv.Itoa(page)},
	}
	body, err := c.request(ctx, videoSearchEndpoint, signWBI(params, imgKey, subKey, time.Now()), cookie)
	if err != nil { return nil, err }
	var envelope videoAPIEnvelope
	if err := json.Unmarshal(body, &envelope); err != nil { return nil, err }
	if envelope.Code != 0 { return nil, fmt.Errorf("B 站视频搜索失败（%d）：%s", envelope.Code, safeMessage(envelope.Message)) }
	var data struct { Result []struct {
		BVID string `json:"bvid"`
		Title string `json:"title"`
		Author string `json:"author"`
		Pic string `json:"pic"`
		Duration string `json:"duration"`
		Play json.Number `json:"play"`
		Danmaku json.Number `json:"danmaku"`
	} `json:"result"` }
	if err := json.Unmarshal(envelope.Data, &data); err != nil { return nil, err }
	results := make([]VideoSearchResult, 0, len(data.Result))
	for _, item := range data.Result {
		if item.BVID == "" { continue }
		play, _ := strconv.ParseInt(item.Play.String(), 10, 64)
		danmaku, _ := strconv.ParseInt(item.Danmaku.String(), 10, 64)
		cover := normalizeEmoticonURL(item.Pic)
		title := videoSearchTagPattern.ReplaceAllString(html.UnescapeString(item.Title), "")
		results = append(results, VideoSearchResult{BVID:item.BVID, Title:title, Author:item.Author, Cover:cover, Duration:item.Duration, PlayCount:play, DanmakuCount:danmaku})
	}
	return results, nil
}

func (c *VideoClient) Detail(ctx context.Context, bvid string) (VideoDetail, error) {
	bvid = strings.TrimSpace(bvid)
	if !validBVID(bvid) { return VideoDetail{}, errors.New("请输入有效的 B 站 BV 号或视频链接") }
	credentials, err := c.credentials(ctx)
	if err != nil { return VideoDetail{}, err }
	body, err := c.request(ctx, videoViewEndpoint, url.Values{"bvid":{bvid}}, credentials.Cookie)
	if err != nil { return VideoDetail{}, err }
	var envelope videoAPIEnvelope
	if err := json.Unmarshal(body, &envelope); err != nil { return VideoDetail{}, err }
	if envelope.Code != 0 { return VideoDetail{}, fmt.Errorf("获取 B 站视频信息失败（%d）：%s", envelope.Code, safeMessage(envelope.Message)) }
	var data struct {
		BVID string `json:"bvid"`
		Title string `json:"title"`
		Duration int `json:"duration"`
		Pic string `json:"pic"`
		Owner struct { Name string `json:"name"` } `json:"owner"`
		Pages []struct { CID int64 `json:"cid"`; Page int `json:"page"`; Part string `json:"part"`; Duration int `json:"duration"` } `json:"pages"`
	}
	if err := json.Unmarshal(envelope.Data, &data); err != nil { return VideoDetail{}, err }
	result := VideoDetail{BVID:data.BVID, Title:html.UnescapeString(data.Title), Author:data.Owner.Name, Cover:data.Pic, Duration:data.Duration, Pages:make([]VideoPage,0,len(data.Pages))}
	result.Cover = normalizeEmoticonURL(result.Cover)
	for _, item := range data.Pages { result.Pages = append(result.Pages, VideoPage{CID:item.CID,Page:item.Page,Part:html.UnescapeString(item.Part),Duration:item.Duration}) }
	if len(result.Pages) == 0 { return VideoDetail{}, errors.New("该视频没有可播放的分 P") }
	return result, nil
}

func (c *VideoClient) Resolve(ctx context.Context, bvid string, page int) (VideoStream, error) {
	detail, err := c.Detail(ctx, bvid)
	if err != nil { return VideoStream{}, err }
	if page < 1 || page > len(detail.Pages) { return VideoStream{}, fmt.Errorf("视频分 P 不存在：第 %d P", page) }
	credentials, err := c.credentials(ctx)
	if err != nil { return VideoStream{}, err }
	cookie, err := c.ensureBuvid(ctx, credentials.Cookie)
	if err != nil { return VideoStream{}, err }
	imgKey, subKey, err := c.wbiKeys(ctx, cookie)
	if err != nil { return VideoStream{}, err }
	cid := detail.Pages[page-1].CID
	var lastErr error
	for _, quality := range []int{80, 64, 32, 16} {
		params := url.Values{
			"bvid":{detail.BVID}, "cid":{strconv.FormatInt(cid,10)},
			"qn":{strconv.Itoa(quality)}, "fnval":{"1"}, "fnver":{"0"},
			"fourk":{"1"}, "otype":{"json"}, "platform":{"html5"},
			"high_quality":{"1"},
		}
		body, requestErr := c.request(ctx, videoPlayEndpoint, signWBI(params, imgKey, subKey, time.Now()), cookie)
		if requestErr != nil { lastErr = requestErr; continue }
		var envelope videoAPIEnvelope
		if err := json.Unmarshal(body, &envelope); err != nil { lastErr = err; continue }
		if envelope.Code != 0 { lastErr = fmt.Errorf("B 站解析播放地址失败（%d）：%s", envelope.Code, safeMessage(envelope.Message)); continue }
		var data struct {
			Quality int `json:"quality"`
			DURL []struct { URL string `json:"url"` } `json:"durl"`
		}
		if err := json.Unmarshal(envelope.Data, &data); err != nil { lastErr = err; continue }
		if len(data.DURL) == 0 || data.DURL[0].URL == "" { lastErr = errors.New("B 站没有返回可用的 MP4 播放地址"); continue }
		return VideoStream{URL:data.DURL[0].URL, Quality:data.Quality, QualityName:videoQualityName(data.Quality)}, nil
	}
	if lastErr == nil { lastErr = errors.New("B 站没有返回可用的视频流") }
	return VideoStream{}, lastErr
}

func videoQualityName(quality int) string {
	switch quality {
	case 127: return "8K"
	case 126: return "杜比视界"
	case 125: return "HDR"
	case 120: return "4K"
	case 116: return "1080P60"
	case 112: return "1080P+"
	case 80: return "1080P"
	case 74: return "720P60"
	case 64: return "720P"
	case 32: return "480P"
	case 16: return "360P"
	default: return fmt.Sprintf("%dP", quality)
	}
}

func validBVID(value string) bool {
	if len(value) != 12 || !strings.HasPrefix(value, "BV") { return false }
	for _, r := range value[2:] {
		if !(r >= '0' && r <= '9' || r >= 'A' && r <= 'Z' || r >= 'a' && r <= 'z') { return false }
	}
	return true
}
