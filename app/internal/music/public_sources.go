package music

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const (
	qqMusicSearchURL = "https://c.y.qq.com/soso/fcgi-bin/client_search_cp"
	kugouSearchURL   = "https://songsearch.kugou.com/song_search_v2"
	kuwoSearchURL    = "http://search.kuwo.cn/r.s"
)

func sourceHTTPClient(client *http.Client) *http.Client {
	if client != nil {
		return client
	}
	return &http.Client{Timeout: 15 * time.Second}
}

func requestSourceJSON(ctx context.Context, client *http.Client, endpoint string, headers map[string]string, target any) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return err
	}
	for key, value := range headers {
		request.Header.Set(key, value)
	}
	response, err := client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("music source returned HTTP %d", response.StatusCode)
	}
	if err := json.NewDecoder(response.Body).Decode(target); err != nil {
		return fmt.Errorf("decode music source response: %w", err)
	}
	return nil
}

func sourcePageCount(total int64, page, limit, returned int) (int64, int) {
	if total == 0 && returned > 0 {
		total = int64((page-1)*limit + returned)
	}
	maxPage := 1
	if total > 0 {
		maxPage = int((total + int64(limit) - 1) / int64(limit))
	}
	return total, maxPage
}

// QQMusicSource searches QQ Music's public desktop search endpoint.
// The search adapter does not require a QQ account. Playback URL resolution is
// intentionally reported as unsupported until a public, non-authenticated
// resolver can be verified for the selected track.
type QQMusicSource struct {
	client  *http.Client
	baseURL string
}

func NewQQMusicSource(client *http.Client, baseURL string) *QQMusicSource {
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if baseURL == "" {
		baseURL = qqMusicSearchURL
	}
	return &QQMusicSource{client: sourceHTTPClient(client), baseURL: baseURL}
}

func (*QQMusicSource) ID() string   { return "qq_music" }
func (*QQMusicSource) Name() string { return "QQ 音乐" }
func (*QQMusicSource) Capabilities() SourceCapabilities {
	return SourceCapabilities{Search: true, Resolve: false}
}
func (s *QQMusicSource) Search(ctx context.Context, keyword string, limit int) ([]Track, error) {
	page, err := s.SearchPage(ctx, keyword, 1, limit)
	return page.Tracks, err
}
func (s *QQMusicSource) SearchPage(ctx context.Context, keyword string, page, limit int) (SourceSearchPage, error) {
	keyword, err := normalizeSearchKeyword(keyword)
	if err != nil {
		return SourceSearchPage{}, err
	}
	limit = normalizeSearchLimit(limit)
	if page < 1 {
		page = 1
	}
	endpoint, err := url.Parse(s.baseURL)
	if err != nil {
		return SourceSearchPage{}, err
	}
	query := endpoint.Query()
	query.Set("w", keyword)
	query.Set("p", strconv.Itoa(page))
	query.Set("n", strconv.Itoa(limit))
	query.Set("format", "json")
	query.Set("inCharset", "utf8")
	query.Set("outCharset", "utf-8")
	endpoint.RawQuery = query.Encode()

	var payload struct {
		Code int `json:"code"`
		Data struct {
			Song struct {
				Total int64 `json:"totalnum"`
				List []struct {
					ID       int64  `json:"id"`
					Mid      string `json:"mid"`
					Title    string `json:"title"`
					Name     string `json:"name"`
					Interval int64  `json:"interval"`
					Singers  []struct {
						Name string `json:"name"`
					} `json:"singer"`
					Album struct {
						Name string `json:"name"`
						Mid  string `json:"mid"`
					} `json:"album"`
				} `json:"list"`
			} `json:"song"`
		} `json:"data"`
	}
	err = requestSourceJSON(ctx, s.client, endpoint.String(), map[string]string{
		"Referer": "https://y.qq.com/",
		"User-Agent": "Mozilla/5.0",
	}, &payload)
	if err != nil {
		return SourceSearchPage{}, fmt.Errorf("search QQ Music: %w", err)
	}
	if payload.Code != 0 {
		return SourceSearchPage{}, fmt.Errorf("QQ Music search returned code %d", payload.Code)
	}
	tracks := make([]Track, 0, len(payload.Data.Song.List))
	for _, item := range payload.Data.Song.List {
		id := strings.TrimSpace(item.Mid)
		if id == "" && item.ID > 0 {
			id = strconv.FormatInt(item.ID, 10)
		}
		title := strings.TrimSpace(item.Title)
		if title == "" {
			title = strings.TrimSpace(item.Name)
		}
		if id == "" || title == "" {
			continue
		}
		artists := make([]string, 0, len(item.Singers))
		for _, singer := range item.Singers {
			if name := strings.TrimSpace(singer.Name); name != "" {
				artists = append(artists, name)
			}
		}
		cover := ""
		if item.Album.Mid != "" {
			cover = "https://y.gtimg.cn/music/photo_new/T002R500x500M000" + item.Album.Mid + ".jpg"
		}
		tracks = append(tracks, Track{
			ID: id, SourceID: s.ID(), Title: title, Artist: strings.Join(artists, " / "),
			Album: strings.TrimSpace(item.Album.Name), DurationMS: item.Interval * 1000, CoverURL: cover,
		})
	}
	total, maxPage := sourcePageCount(payload.Data.Song.Total, page, limit, len(tracks))
	return SourceSearchPage{Tracks: tracks, Total: total, MaxPage: maxPage, PageSize: limit}, nil
}
func (*QQMusicSource) Resolve(context.Context, Track) (PlaybackResource, error) {
	return PlaybackResource{}, errors.New("QQ Music search is available; playback URL resolution is not implemented")
}

// KugouSource uses Kugou's public song search endpoint. It keeps the provider's
// song ID and metadata but does not claim playback support until a playable
// public URL can be obtained without bypassing platform restrictions.
type KugouSource struct {
	client  *http.Client
	baseURL string
}

func NewKugouSource(client *http.Client, baseURL string) *KugouSource {
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if baseURL == "" {
		baseURL = kugouSearchURL
	}
	return &KugouSource{client: sourceHTTPClient(client), baseURL: baseURL}
}

func (*KugouSource) ID() string   { return "kugou" }
func (*KugouSource) Name() string { return "酷狗音乐" }
func (*KugouSource) Capabilities() SourceCapabilities {
	return SourceCapabilities{Search: true, Resolve: false}
}
func (s *KugouSource) Search(ctx context.Context, keyword string, limit int) ([]Track, error) {
	page, err := s.SearchPage(ctx, keyword, 1, limit)
	return page.Tracks, err
}
func (s *KugouSource) SearchPage(ctx context.Context, keyword string, page, limit int) (SourceSearchPage, error) {
	keyword, err := normalizeSearchKeyword(keyword)
	if err != nil {
		return SourceSearchPage{}, err
	}
	limit = normalizeSearchLimit(limit)
	if page < 1 {
		page = 1
	}
	endpoint, err := url.Parse(s.baseURL)
	if err != nil {
		return SourceSearchPage{}, err
	}
	query := endpoint.Query()
	query.Set("platform", "AndroidFilter")
	query.Set("iscorrection", "1")
	query.Set("keyword", keyword)
	query.Set("hifiquality", "0")
	query.Set("pagesize", strconv.Itoa(limit))
	query.Set("PrivilegeFilter", "0")
	query.Set("page", strconv.Itoa(page))
	endpoint.RawQuery = query.Encode()

	var payload struct {
		ErrorCode int `json:"error_code"`
		Data struct {
			Total int64 `json:"total"`
			Lists []struct {
				AudioID     int64  `json:"Audioid"`
				FileHash    string `json:"FileHash"`
				SongName    string `json:"SongName"`
				OriSongName string `json:"OriSongName"`
				Suffix      string `json:"Suffix"`
				AlbumName   string `json:"AlbumName"`
				Duration    int64  `json:"Duration"`
				Singers     []struct {
					Name string `json:"name"`
				} `json:"Singers"`
			} `json:"lists"`
		} `json:"data"`
	}
	err = requestSourceJSON(ctx, s.client, endpoint.String(), map[string]string{
		"Referer": "https://www.kugou.com/",
		"User-Agent": "Mozilla/5.0",
	}, &payload)
	if err != nil {
		return SourceSearchPage{}, fmt.Errorf("search Kugou: %w", err)
	}
	if payload.ErrorCode != 0 {
		return SourceSearchPage{}, fmt.Errorf("Kugou search returned code %d", payload.ErrorCode)
	}
	tracks := make([]Track, 0, len(payload.Data.Lists))
	for _, item := range payload.Data.Lists {
		id := strconv.FormatInt(item.AudioID, 10)
		title := strings.TrimSpace(item.SongName)
		if title == "" {
			title = strings.TrimSpace(item.OriSongName + " " + item.Suffix)
		}
		title = html.UnescapeString(title)
		if item.AudioID <= 0 || title == "" {
			continue
		}
		artists := make([]string, 0, len(item.Singers))
		for _, singer := range item.Singers {
			if name := strings.TrimSpace(singer.Name); name != "" {
				artists = append(artists, html.UnescapeString(name))
			}
		}
		tracks = append(tracks, Track{
			ID: id, SourceID: s.ID(), Title: title, Artist: strings.Join(artists, " / "),
			Album: html.UnescapeString(strings.TrimSpace(item.AlbumName)), DurationMS: item.Duration * 1000,
		})
	}
	total, maxPage := sourcePageCount(payload.Data.Total, page, limit, len(tracks))
	return SourceSearchPage{Tracks: tracks, Total: total, MaxPage: maxPage, PageSize: limit}, nil
}
func (*KugouSource) Resolve(context.Context, Track) (PlaybackResource, error) {
	return PlaybackResource{}, errors.New("Kugou search is available; playback URL resolution is not implemented")
}

// KuwoSource uses Kuwo's public search endpoint and its zero-based page index.
type KuwoSource struct {
	client  *http.Client
	baseURL string
}

func NewKuwoSource(client *http.Client, baseURL string) *KuwoSource {
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if baseURL == "" {
		baseURL = kuwoSearchURL
	}
	return &KuwoSource{client: sourceHTTPClient(client), baseURL: baseURL}
}

func (*KuwoSource) ID() string   { return "kuwo" }
func (*KuwoSource) Name() string { return "酷我音乐" }
func (*KuwoSource) Capabilities() SourceCapabilities {
	return SourceCapabilities{Search: true, Resolve: false}
}
func (s *KuwoSource) Search(ctx context.Context, keyword string, limit int) ([]Track, error) {
	page, err := s.SearchPage(ctx, keyword, 1, limit)
	return page.Tracks, err
}
func (s *KuwoSource) SearchPage(ctx context.Context, keyword string, page, limit int) (SourceSearchPage, error) {
	keyword, err := normalizeSearchKeyword(keyword)
	if err != nil {
		return SourceSearchPage{}, err
	}
	limit = normalizeSearchLimit(limit)
	if page < 1 {
		page = 1
	}
	endpoint, err := url.Parse(s.baseURL)
	if err != nil {
		return SourceSearchPage{}, err
	}
	query := endpoint.Query()
	query.Set("client", "kt")
	query.Set("all", keyword)
	query.Set("pn", strconv.Itoa(page-1))
	query.Set("rn", strconv.Itoa(limit))
	query.Set("uid", "794762570")
	query.Set("ver", "kwplayer_ar_9.2.2.1")
	query.Set("vipver", "1")
	query.Set("show_copyright_off", "1")
	query.Set("newver", "1")
	query.Set("ft", "music")
	query.Set("cluster", "0")
	query.Set("strategy", "2012")
	query.Set("encoding", "utf8")
	query.Set("rformat", "json")
	query.Set("vermerge", "1")
	query.Set("mobi", "1")
	query.Set("issubtitle", "1")
	endpoint.RawQuery = query.Encode()

	var payload struct {
		Total string `json:"TOTAL"`
		AbsList []struct {
			MusicRID string `json:"MUSICRID"`
			SongName string `json:"SONGNAME"`
			Artist string `json:"ARTIST"`
			Duration string `json:"DURATION"`
			AlbumID string `json:"ALBUMID"`
			Album string `json:"ALBUM"`
		} `json:"abslist"`
	}
	err = requestSourceJSON(ctx, s.client, endpoint.String(), map[string]string{
		"Referer": "https://www.kuwo.cn/",
		"User-Agent": "Mozilla/5.0",
	}, &payload)
	if err != nil {
		return SourceSearchPage{}, fmt.Errorf("search Kuwo: %w", err)
	}
	total, _ := strconv.ParseInt(strings.TrimSpace(payload.Total), 10, 64)
	tracks := make([]Track, 0, len(payload.AbsList))
	for _, item := range payload.AbsList {
		id := strings.TrimPrefix(strings.TrimSpace(item.MusicRID), "MUSIC_")
		title := html.UnescapeString(strings.TrimSpace(item.SongName))
		if id == "" || title == "" {
			continue
		}
		duration, _ := strconv.ParseInt(strings.TrimSpace(item.Duration), 10, 64)
		tracks = append(tracks, Track{
			ID: id, SourceID: s.ID(), Title: title,
			Artist: html.UnescapeString(strings.TrimSpace(item.Artist)),
			Album: html.UnescapeString(strings.TrimSpace(item.Album)),
			DurationMS: duration * 1000,
		})
	}
	total, maxPage := sourcePageCount(total, page, limit, len(tracks))
	return SourceSearchPage{Tracks: tracks, Total: total, MaxPage: maxPage, PageSize: limit}, nil
}
func (*KuwoSource) Resolve(context.Context, Track) (PlaybackResource, error) {
	return PlaybackResource{}, errors.New("Kuwo search is available; playback URL resolution is not implemented")
}

var _ Source = (*QQMusicSource)(nil)
var _ PagedSource = (*QQMusicSource)(nil)
var _ Source = (*KugouSource)(nil)
var _ PagedSource = (*KugouSource)(nil)
var _ Source = (*KuwoSource)(nil)
var _ PagedSource = (*KuwoSource)(nil)
