package music

import (
	"context"
	"crypto/aes"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const netEaseBaseURL = "https://music.163.com"

type NetEaseSource struct {
	client *http.Client
	baseURL string
}

func NewNetEaseSource(client *http.Client, baseURL string) *NetEaseSource {
	if client == nil { client = &http.Client{Timeout:15*time.Second} }
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if baseURL == "" { baseURL = netEaseBaseURL }
	return &NetEaseSource{client:client, baseURL:baseURL}
}

func (*NetEaseSource) ID() string { return "netease" }
func (*NetEaseSource) Name() string { return "网易云音乐" }
func (*NetEaseSource) Capabilities() SourceCapabilities { return SourceCapabilities{Search:true, Resolve:true} }

func (s *NetEaseSource) Search(ctx context.Context, keyword string, limit int) ([]Track, error) {
	page, err := s.SearchPage(ctx, keyword, 1, limit)
	return page.Tracks, err
}

func (s *NetEaseSource) SearchPage(ctx context.Context, keyword string, page, limit int) (SourceSearchPage, error) {
	if s == nil || s.client == nil {
		return SourceSearchPage{}, errors.New("NetEase source is unavailable")
	}
	keyword, err := normalizeSearchKeyword(keyword)
	if err != nil {
		return SourceSearchPage{}, err
	}
	limit = normalizeSearchLimit(limit)
	if page < 1 {
		page = 1
	}
	const apiPath = "/api/search/song/list/page"
	endpoint, err := url.Parse(s.baseURL + "/eapi/batch")
	if err != nil { return SourceSearchPage{}, fmt.Errorf("invalid NetEase API base URL: %w", err) }
	requestData := map[string]any{
		"keyword": keyword, "needCorrect": "1", "channel": "typing",
		"offset": (page-1)*limit, "scene": "normal", "total": page == 1, "limit": limit,
	}
	formValue, err := netEaseEAPIForm(apiPath, requestData)
	if err != nil { return SourceSearchPage{}, fmt.Errorf("encrypt NetEase search request: %w", err) }
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint.String(), strings.NewReader(formValue.Encode()))
	if err != nil { return SourceSearchPage{}, fmt.Errorf("create NetEase search request: %w", err) }
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.Header.Set("Referer", "https://music.163.com")
	request.Header.Set("Origin", "https://music.163.com")
	request.Header.Set("User-Agent", "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/60.0.3112.90 Safari/537.36")
	response, err := s.client.Do(request)
	if err != nil { return SourceSearchPage{}, fmt.Errorf("search NetEase music: %w", err) }
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 { return SourceSearchPage{}, fmt.Errorf("NetEase search returned HTTP %d", response.StatusCode) }
	var payload struct {
		Code int `json:"code"`
		Data struct {
			TotalCount int64 `json:"totalCount"`
			Resources []struct {
				BaseInfo struct {
					SimpleSongData struct {
						ID int64 `json:"id"`
						Name string `json:"name"`
						Duration int64 `json:"dt"`
						Artists []struct { Name string `json:"name"` } `json:"ar"`
						Album struct { ID int64 `json:"id"`; Name string `json:"name"`; PicURL string `json:"picUrl"` } `json:"al"`
						Privilege json.RawMessage `json:"privilege"`
						Low json.RawMessage `json:"l"`
						High json.RawMessage `json:"h"`
						SQ json.RawMessage `json:"sq"`
						HR json.RawMessage `json:"hr"`
					} `json:"simpleSongData"`
				} `json:"baseInfo"`
			} `json:"resources"`
		} `json:"data"`
	}
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil { return SourceSearchPage{}, fmt.Errorf("decode NetEase search response: %w", err) }
	if payload.Code != 200 { return SourceSearchPage{}, fmt.Errorf("NetEase search returned code %d", payload.Code) }
	tracks := make([]Track, 0, len(payload.Data.Resources))
	for _, resource := range payload.Data.Resources {
		song := resource.BaseInfo.SimpleSongData
		if song.ID <= 0 || strings.TrimSpace(song.Name) == "" { continue }
		artists := make([]string, 0, len(song.Artists))
		for _, artist := range song.Artists { if name := strings.TrimSpace(artist.Name); name != "" { artists = append(artists, name) } }
		id := strconv.FormatInt(song.ID, 10)
		artist := strings.Join(artists, "、")
		albumName := strings.TrimSpace(song.Album.Name)
		data := map[string]string{
			"id":id, "songId":id, "name":strings.TrimSpace(song.Name), "title":strings.TrimSpace(song.Name),
			"artist":artist, "singer":artist, "album":albumName, "albumName":albumName,
			"albumId":strconv.FormatInt(song.Album.ID,10), "duration":strconv.FormatInt(song.Duration/1000,10),
			"interval":strconv.FormatInt(song.Duration/1000,10),
			"wy_privilege":string(song.Privilege), "wy_l":string(song.Low), "wy_h":string(song.High),
			"wy_sq":string(song.SQ), "wy_hr":string(song.HR),
		}
		tracks = append(tracks, Track{ID:id,SourceID:s.ID(),LXSourceKey:"wy",Title:strings.TrimSpace(song.Name),
			Artist:artist,Album:albumName,DurationMS:song.Duration,CoverURL:strings.TrimSpace(song.Album.PicURL),SourceData:data})
	}
	total := payload.Data.TotalCount
	if total == 0 && len(tracks) > 0 {
		total = int64((page-1)*limit + len(tracks))
	}
	maxPage := 1
	if total > 0 {
		maxPage = int((total + int64(limit) - 1) / int64(limit))
	}
	return SourceSearchPage{Tracks: tracks, Total: total, MaxPage: maxPage, PageSize: limit}, nil
}


// netEaseEAPIForm reproduces LX Music's EAPI request envelope (AES-128-ECB + MD5).
func netEaseEAPIForm(apiPath string, value any) (url.Values, error) {
	plain, err := json.Marshal(value)
	if err != nil { return nil, err }
	message := "nobody" + apiPath + "use" + string(plain) + "md5forencrypt"
	digest := fmt.Sprintf("%x", md5.Sum([]byte(message)))
	data := apiPath + "-36cd479b6b5-" + string(plain) + "-36cd479b6b5-" + digest
	block, err := aes.NewCipher([]byte("e82ckenh8dichen8"))
	if err != nil { return nil, err }
	padding := block.BlockSize() - len(data)%block.BlockSize()
	padded := append([]byte(data), bytesRepeat(byte(padding), padding)...)
	ciphertext := make([]byte, len(padded))
	for offset := 0; offset < len(padded); offset += block.BlockSize() {
		block.Encrypt(ciphertext[offset:offset+block.BlockSize()], padded[offset:offset+block.BlockSize()])
	}
	values := url.Values{}
	values.Set("params", strings.ToUpper(hex.EncodeToString(ciphertext)))
	return values, nil
}

func bytesRepeat(value byte, count int) []byte {
	result := make([]byte, count)
	for i := range result { result[i] = value }
	return result
}

func decodeNetEaseQuality(raw json.RawMessage) (any, bool) {
	if len(raw) == 0 || string(raw) == "null" { return nil, false }
	var item struct { Size json.RawMessage `json:"size"` }
	if json.Unmarshal(raw, &item) != nil || len(item.Size) == 0 || string(item.Size) == "null" { return nil, true }
	size, err := strconv.ParseInt(strings.Trim(string(item.Size), `"`), 10, 64)
	if err != nil || size <= 0 { return nil, true }
	return formatNetEaseFileSize(size), true
}

func formatNetEaseFileSize(size int64) string {
	units := []string{"B","KiB","MiB","GiB","TiB"}
	value := float64(size); unit := 0
	for value >= 1024 && unit < len(units)-1 { value /= 1024; unit++ }
	return fmt.Sprintf("%.2f %s",value,units[unit])
}

func buildLXNetEaseQualities(data map[string]string) ([]any, map[string]any) {
	var privilege struct { MaxBR int64 `json:"maxbr"`; MaxBRLevel string `json:"maxBrLevel"` }
	_ = json.Unmarshal([]byte(data["wy_privilege"]), &privilege)
	fields := make([]struct{name,key string},0,4)
	if privilege.MaxBRLevel == "hires" { fields=append(fields,struct{name,key string}{"flac24bit","wy_hr"}) }
	switch privilege.MaxBR {
	case 999000: fields=append(fields,struct{name,key string}{"flac","wy_sq"}); fallthrough
	case 320000: fields=append(fields,struct{name,key string}{"320k","wy_h"}); fallthrough
	case 192000,128000: fields=append(fields,struct{name,key string}{"128k","wy_l"})
	}
	types:=make([]any,0,len(fields)); available:=make(map[string]any,len(fields))
	for _,field:=range fields {
		size, present := decodeNetEaseQuality(json.RawMessage(data[field.key]))
		if !present { size=nil }
		types=append(types,map[string]any{"type":field.name,"size":size})
		available[field.name]=map[string]any{"size":size}
	}
	return types,available
}

// Resolve uses the platform's ordinary song URL redirect. It does not unlock
// greyed-out tracks or bypass account, region, subscription, or DRM controls.
func (s *NetEaseSource) Resolve(_ context.Context, track Track) (PlaybackResource, error) {
	if track.SourceID != s.ID() { return PlaybackResource{}, errors.New("track does not belong to NetEase source") }
	id, err := strconv.ParseInt(track.ID, 10, 64)
	if err != nil || id <= 0 { return PlaybackResource{}, errors.New("invalid NetEase song ID") }
	return PlaybackResource{URL:fmt.Sprintf("%s/song/media/outer/url?id=%d.mp3", s.baseURL, id), Headers:map[string]string{"Referer":"https://music.163.com/", "User-Agent":"Mozilla/5.0"}}, nil
}

var _ Source = (*NetEaseSource)(nil)
