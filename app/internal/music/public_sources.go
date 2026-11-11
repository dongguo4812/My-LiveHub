package music

import (
	"context"
	"crypto/md5"
	"encoding/hex"
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
	miguSearchURL    = "https://jadeite.migu.cn/music_search/v3/search/searchAll"
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

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}
	return ""
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
					ID        int64  `json:"id"`
					SongID    int64  `json:"songid"`
					Mid       string `json:"mid"`
					SongMid   string `json:"songmid"`
					Title     string `json:"title"`
					Name      string `json:"name"`
					SongName  string `json:"songname"`
					Interval  int64  `json:"interval"`
					Singers   []struct {
						Name string `json:"name"`
					} `json:"singer"`
					AlbumName string `json:"albumname"`
					AlbumMid  string `json:"albummid"`
					Album     struct {
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
		id := strings.TrimSpace(item.SongMid)
		if id == "" {
			id = strings.TrimSpace(item.Mid)
		}
		if id == "" && item.SongID > 0 {
			id = strconv.FormatInt(item.SongID, 10)
		}
		if id == "" && item.ID > 0 {
			id = strconv.FormatInt(item.ID, 10)
		}
		title := strings.TrimSpace(item.SongName)
		if title == "" {
			title = strings.TrimSpace(item.Title)
		}
		if title == "" {
			title = strings.TrimSpace(item.Name)
		}
		albumName := strings.TrimSpace(item.AlbumName)
		if albumName == "" {
			albumName = strings.TrimSpace(item.Album.Name)
		}
		albumMid := strings.TrimSpace(item.AlbumMid)
		if albumMid == "" {
			albumMid = strings.TrimSpace(item.Album.Mid)
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
		if albumMid != "" {
			cover = "https://y.gtimg.cn/music/photo_new/T002R500x500M000" + albumMid + ".jpg"
		}
		tracks = append(tracks, Track{
			ID: id, SourceID: s.ID(), LXSourceKey: "tx", Title: title, Artist: strings.Join(artists, " / "),
			Album: albumName, DurationMS: item.Interval * 1000, CoverURL: cover,
			SourceData: map[string]string{
				"id": strconv.FormatInt(item.ID, 10), "songid": strconv.FormatInt(item.SongID, 10),
				"mid": firstNonEmpty(item.Mid, item.SongMid), "songmid": firstNonEmpty(item.SongMid, item.Mid),
				"name": title, "title": title, "songname": title, "singer": strings.Join(artists, " / "),
				"artist": strings.Join(artists, " / "), "album": albumName, "albumname": albumName,
				"albummid": albumMid,
				"interval": strconv.FormatInt(item.Interval, 10), "duration": strconv.FormatInt(item.Interval, 10),
			},
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
			ID: id, SourceID: s.ID(), LXSourceKey: "kg", Title: title, Artist: strings.Join(artists, " / "),
			Album: html.UnescapeString(strings.TrimSpace(item.AlbumName)), DurationMS: item.Duration * 1000,
			SourceData: map[string]string{
				"id": id, "audioid": id, "Audioid": id, "hash": item.FileHash, "fileHash": item.FileHash, "FileHash": item.FileHash,
				"name": title, "songname": title, "title": title, "singer": strings.Join(artists, " / "),
				"artist": strings.Join(artists, " / "), "album": html.UnescapeString(strings.TrimSpace(item.AlbumName)),
				"duration": strconv.FormatInt(item.Duration, 10), "interval": strconv.FormatInt(item.Duration, 10),
			},
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
			ID: id, SourceID: s.ID(), LXSourceKey: "kw", Title: title,
			Artist: html.UnescapeString(strings.TrimSpace(item.Artist)),
			Album: html.UnescapeString(strings.TrimSpace(item.Album)),
			DurationMS: duration * 1000,
			SourceData: map[string]string{
				"id": id, "rid": id, "MUSICRID": strings.TrimSpace(item.MusicRID), "musicrid": strings.TrimSpace(item.MusicRID),
				"name": title, "songname": title, "title": title, "artist": html.UnescapeString(strings.TrimSpace(item.Artist)),
				"album": html.UnescapeString(strings.TrimSpace(item.Album)), "duration": strconv.FormatInt(duration, 10),
				"interval": strconv.FormatInt(duration, 10),
			},
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

type MiguSource struct { client *http.Client; baseURL string; now func() time.Time }
func NewMiguSource(client *http.Client,baseURL string)*MiguSource{baseURL=strings.TrimRight(strings.TrimSpace(baseURL),"/");if baseURL==""{baseURL=miguSearchURL};return &MiguSource{client:sourceHTTPClient(client),baseURL:baseURL,now:time.Now}}
func(*MiguSource)ID()string{return "migu"}
func(*MiguSource)Name()string{return "咪咕音乐"}
func(*MiguSource)Capabilities()SourceCapabilities{return SourceCapabilities{Search:true,Resolve:false}}
func(s *MiguSource)Search(ctx context.Context,keyword string,limit int)([]Track,error){p,e:=s.SearchPage(ctx,keyword,1,limit);return p.Tracks,e}
func miguSignature(keyword,timestamp string)(string,string){const device="963B7AA0D21511ED807EE5846EC87D20";const salt="6cdc72a439cef99a3418d2a78aa28c73";const app="yyapp2d16148780a1dcc7408e06336b98cfd50";sum:=md5.Sum([]byte(keyword+salt+app+device+timestamp));return hex.EncodeToString(sum[:]),device}
func miguRawString(raw json.RawMessage)string{if len(raw)==0||string(raw)=="null"{return ""};var v string;if json.Unmarshal(raw,&v)==nil{return v};return strings.TrimSpace(string(raw))}
func(s *MiguSource)SearchPage(ctx context.Context,keyword string,page,limit int)(SourceSearchPage,error){
 keyword,err:=normalizeSearchKeyword(keyword);if err!=nil{return SourceSearchPage{},err};limit=normalizeSearchLimit(limit);if page<1{page=1}
 now:=time.Now;if s.now!=nil{now=s.now};stamp:=strconv.FormatInt(now().UnixMilli(),10);sign,device:=miguSignature(keyword,stamp)
 endpoint,err:=url.Parse(s.baseURL);if err!=nil{return SourceSearchPage{},err};q:=endpoint.Query()
 q.Set("isCorrect","0");q.Set("isCopyright","1");q.Set("searchSwitch","{\"song\":1,\"album\":0,\"singer\":0,\"tagSong\":1,\"mvSong\":0,\"bestShow\":1,\"songlist\":0,\"lyricSong\":0}")
 q.Set("pageSize",strconv.Itoa(limit));q.Set("text",keyword);q.Set("pageNo",strconv.Itoa(page));q.Set("sort","0");q.Set("sid","USS");endpoint.RawQuery=q.Encode()
 var payload struct{Code string `json:"code"`;Info string `json:"info"`;SongResultData struct{TotalCount json.RawMessage `json:"totalCount"`;ResultList [][]map[string]json.RawMessage `json:"resultList"`} `json:"songResultData"`}
 err=requestSourceJSON(ctx,s.client,endpoint.String(),map[string]string{"uiVersion":"A_music_3.6.1","deviceId":device,"timestamp":stamp,"sign":sign,"channel":"0146921","User-Agent":"Mozilla/5.0 (Linux; U; Android 11.0.0; zh-cn; MI 11 Build/OPR1.170623.032) AppleWebKit/534.30 (KHTML, like Gecko) Version/4.0 Mobile Safari/534.30"},&payload)
 if err!=nil{return SourceSearchPage{},fmt.Errorf("search Migu Music: %w",err)};if payload.Code!="000000"{return SourceSearchPage{},fmt.Errorf("Migu Music search returned %s: %s",payload.Code,strings.TrimSpace(payload.Info))}
 tracks:=make([]Track,0,limit);seen:=map[string]struct{}{}
 for _,group:=range payload.SongResultData.ResultList{for _,item:=range group{
  id:=miguRawString(item["songId"]);copyright:=miguRawString(item["copyrightId"]);title:=miguRawString(item["name"]);if id==""||copyright==""||title==""{continue};if _,ok:=seen[copyright];ok{continue};seen[copyright]=struct{}{}
  var singers []map[string]json.RawMessage;_ = json.Unmarshal(item["singerList"],&singers);names:=[]string{};for _,a:=range singers{if n:=miguRawString(a["name"]);n!=""{names=append(names,n)}}
  album:=miguRawString(item["album"]);duration,_:=strconv.ParseInt(miguRawString(item["duration"]),10,64);cover:=firstNonEmpty(miguRawString(item["img3"]),miguRawString(item["img2"]),miguRawString(item["img1"]));if cover!=""&&!strings.Contains(cover,"http:")&&!strings.Contains(cover,"https:"){cover="http://d.musicapp.migu.cn"+cover}
  formats:=item["audioFormats"];if len(formats)==0{formats=json.RawMessage("[]")};artist:=strings.Join(names,"、")
  tracks=append(tracks,Track{ID:id,SourceID:s.ID(),LXSourceKey:"mg",Title:title,Artist:artist,Album:album,DurationMS:duration*1000,CoverURL:cover,SourceData:map[string]string{"id":id,"songmid":id,"name":title,"singer":artist,"artist":artist,"album":album,"albumName":album,"albumId":miguRawString(item["albumId"]),"copyrightId":copyright,"duration":strconv.FormatInt(duration,10),"interval":formatMiguPlayTime(duration),"img":cover,"lrcUrl":miguRawString(item["lrcUrl"]),"mrcUrl":miguRawString(item["mrcurl"]),"trcUrl":miguRawString(item["trcUrl"]),"audio_formats_json":string(formats)}})
  if len(tracks)>=limit{break}
 };if len(tracks)>=limit{break}}
 total,_:=strconv.ParseInt(miguRawString(payload.SongResultData.TotalCount),10,64);total,max:=sourcePageCount(total,page,limit,len(tracks));return SourceSearchPage{Tracks:tracks,Total:total,MaxPage:max,PageSize:limit},nil
}
func(*MiguSource)Resolve(context.Context,Track)(PlaybackResource,error){return PlaybackResource{},errors.New("Migu Music search is available; playback URL resolution requires a compatible LX musicUrl source")}
var _ Source=(*MiguSource)(nil)
var _ PagedSource=(*MiguSource)(nil)

func formatMiguPlayTime(seconds int64) string {
	if seconds <= 0 { return "" }
	return fmt.Sprintf("%02d:%02d", seconds/60, seconds%60)
}
