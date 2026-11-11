package music

import (
	"context"
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
	endpoint, err := url.Parse(s.baseURL + "/api/search/get/web")
	if err != nil {
		return SourceSearchPage{}, fmt.Errorf("invalid NetEase API base URL: %w", err)
	}
	query := endpoint.Query()
	query.Set("s", keyword)
	query.Set("type", "1")
	query.Set("limit", strconv.Itoa(limit))
	query.Set("offset", strconv.Itoa((page-1)*limit))
	query.Set("total", "true")
	endpoint.RawQuery = query.Encode()

	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return SourceSearchPage{}, fmt.Errorf("create NetEase search request: %w", err)
	}
	request.Header.Set("Referer", "https://music.163.com/")
	request.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 Chrome/132.0.0.0 Safari/537.36")
	response, err := s.client.Do(request)
	if err != nil {
		return SourceSearchPage{}, fmt.Errorf("search NetEase music: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return SourceSearchPage{}, fmt.Errorf("NetEase search returned HTTP %d", response.StatusCode)
	}
	var payload struct {
		Code int `json:"code"`
		Result struct {
			SongCount int64 `json:"songCount"`
			Songs []struct {
				ID int64 `json:"id"`
				Name string `json:"name"`
				Duration int64 `json:"duration"`
				Artists []struct {
					Name string `json:"name"`
				} `json:"artists"`
				Album struct {
					Name string `json:"name"`
					PicURL string `json:"picUrl"`
				} `json:"album"`
			} `json:"songs"`
		} `json:"result"`
	}
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		return SourceSearchPage{}, fmt.Errorf("decode NetEase search response: %w", err)
	}
	if payload.Code != 200 {
		return SourceSearchPage{}, fmt.Errorf("NetEase search returned code %d", payload.Code)
	}
	tracks := make([]Track, 0, len(payload.Result.Songs))
	for _, song := range payload.Result.Songs {
		if song.ID <= 0 || strings.TrimSpace(song.Name) == "" {
			continue
		}
		artists := make([]string, 0, len(song.Artists))
		for _, artist := range song.Artists {
			if name := strings.TrimSpace(artist.Name); name != "" {
				artists = append(artists, name)
			}
		}
		tracks = append(tracks, Track{
			ID: strconv.FormatInt(song.ID, 10), SourceID: s.ID(), LXSourceKey: "wy", Title: strings.TrimSpace(song.Name),
			Artist: strings.Join(artists, " / "), Album: strings.TrimSpace(song.Album.Name),
			DurationMS: song.Duration, CoverURL: strings.TrimSpace(song.Album.PicURL),
			SourceData: map[string]string{
				"id": strconv.FormatInt(song.ID, 10), "songId": strconv.FormatInt(song.ID, 10),
				"name": strings.TrimSpace(song.Name), "title": strings.TrimSpace(song.Name),
				"artist": strings.Join(artists, " / "), "album": strings.TrimSpace(song.Album.Name),
				"duration": strconv.FormatInt(song.Duration/1000, 10), "interval": strconv.FormatInt(song.Duration/1000, 10),
			},
		})
	}
	total := payload.Result.SongCount
	if total == 0 && len(tracks) > 0 {
		total = int64((page-1)*limit + len(tracks))
	}
	maxPage := 1
	if total > 0 {
		maxPage = int((total + int64(limit) - 1) / int64(limit))
	}
	return SourceSearchPage{Tracks: tracks, Total: total, MaxPage: maxPage, PageSize: limit}, nil
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
