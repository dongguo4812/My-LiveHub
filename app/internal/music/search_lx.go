package music

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/md5"
	"crypto/rand"
	"crypto/sha1"
	"encoding/base64"
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
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	LXSearchKW = "kw"
	LXSearchKG = "kg"
	LXSearchTX = "tx"
	LXSearchWY = "wy"
	LXSearchMG = "mg"
)

type TrackQuality struct {
	Type string            `json:"type"`
	Size string            `json:"size,omitempty"`
	SourceData map[string]string `json:"source_data,omitempty"`
}

type SearchPage struct {
	Tracks []Track `json:"tracks"`
	Total int64 `json:"total"`
	Page int `json:"page"`
	PageSize int `json:"page_size"`
	MaxPage int `json:"max_page"`
	Source string `json:"source"`
}

type SearchProvider interface {
	ID() string
	Name() string
	Search(context.Context, string, int) (SearchPage, error)
}

type LXSearchClient struct {
	httpClient *http.Client
	providers []SearchProvider
}

type LXSearchEndpoints struct {
	Kuwo string
	Kugou string
	QQ string
	Netease string
	Migu string
}

func DefaultLXSearchEndpoints() LXSearchEndpoints {
	return LXSearchEndpoints{
		Kuwo: "http://search.kuwo.cn/r.s",
		Kugou: "http://songsearch.kugou.com/song_search_v2",
		QQ: "https://u.y.qq.com/cgi-bin/musics.fcg",
		Netease: "http://interface.music.163.com/eapi/batch",
		Migu: "https://jadeite.migu.cn/music_search/v3/search/searchAll",
	}
}

func NewLXSearchClient(client *http.Client) *LXSearchClient {
	return NewLXSearchClientWithEndpoints(client, DefaultLXSearchEndpoints())
}

func NewLXSearchClientWithEndpoints(client *http.Client, endpoints LXSearchEndpoints) *LXSearchClient {
	if client == nil {
		client = &http.Client{Timeout: 15 * time.Second}
	}
	return &LXSearchClient{
		httpClient: client,
		providers: []SearchProvider{
			kuwoSearchProvider{client: client, endpoint: endpoints.Kuwo},
			kugouSearchProvider{client: client, endpoint: endpoints.Kugou},
			qqSearchProvider{client: client, endpoint: endpoints.QQ},
			neteaseSearchProvider{client: client, endpoint: endpoints.Netease},
			miguSearchProvider{client: client, endpoint: endpoints.Migu},
		},
	}
}

func (c *LXSearchClient) Sources() []SourceSnapshot {
	if c == nil { return []SourceSnapshot{} }
	result := make([]SourceSnapshot, 0, len(c.providers))
	for _, provider := range c.providers {
		result = append(result, SourceSnapshot{
			ID: provider.ID(), Name: provider.Name(),
			Capabilities: SourceCapabilities{Search: true},
			Enabled: true, Available: true,
		})
	}
	return result
}

// Search runs all five LX desktop providers concurrently. Results retain the
// provider order used by LX's SDK; one provider failure does not discard others.
func (c *LXSearchClient) Search(ctx context.Context, keyword string, page int) (SearchPage, map[string]string, error) {
	keyword = strings.TrimSpace(keyword)
	if keyword == "" { return SearchPage{}, nil, errors.New("请输入歌曲名称或歌手") }
	if page < 1 { return SearchPage{}, nil, errors.New("搜索页码必须大于 0") }
	if c == nil || len(c.providers) == 0 { return SearchPage{}, nil, errors.New("洛雪搜索 SDK 未初始化") }
	if ctx == nil { ctx = context.Background() }

	type outcome struct { page SearchPage; err error }
	outcomes := make([]outcome, len(c.providers))
	var wg sync.WaitGroup
	for i, provider := range c.providers {
		wg.Add(1)
		go func(index int, p SearchProvider) {
			defer wg.Done()
			outcomes[index].page, outcomes[index].err = p.Search(ctx, keyword, page)
		}(i, provider)
	}
	wg.Wait()

	result := SearchPage{Tracks: []Track{}, Page: page, PageSize: 20}
	errs := make(map[string]string)
	successes := 0
	for i, provider := range c.providers {
		outcome := outcomes[i]
		if outcome.err != nil {
			errs[provider.ID()] = outcome.err.Error()
			continue
		}
		successes++
		result.Tracks = append(result.Tracks, outcome.page.Tracks...)
		result.Total += outcome.page.Total
		if outcome.page.MaxPage > result.MaxPage { result.MaxPage = outcome.page.MaxPage }
	}
	if successes == 0 {
		if len(errs) == 0 { return result, errs, errors.New("五个平台均未返回搜索结果") }
		return result, errs, fmt.Errorf("五个平台搜索均失败")
	}
	if result.MaxPage == 0 { result.MaxPage = 1 }
	return result, errs, nil
}

func (c *LXSearchClient) SearchProvider(ctx context.Context, source, keyword string, page int) (SearchPage, error) {
	if c == nil { return SearchPage{}, errors.New("洛雪搜索 SDK 未初始化") }
	for _, provider := range c.providers {
		if provider.ID() == source { return provider.Search(ctx, keyword, page) }
	}
	return SearchPage{}, fmt.Errorf("不支持的音乐来源：%s", source)
}

func doLXJSON(ctx context.Context, client *http.Client, method, rawURL string, headers map[string]string, body io.Reader) (map[string]any, error) {
	req, err := http.NewRequestWithContext(ctx, method, rawURL, body)
	if err != nil { return nil, err }
	req.Header.Set("User-Agent", "lx-music request")
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
	if size <= 0 { return "" }
	const unit = 1024
	if size < unit { return fmt.Sprintf("%d B", size) }
	div, exp := int64(unit), 0
	for n := size / unit; n >= unit; n /= unit { div *= unit; exp++ }
	return fmt.Sprintf("%.1f %ciB", float64(size)/float64(div), "KMGTPE"[exp])
}
func newLXTrack(source, nativeID, title, artist, album string, duration int64, cover string, data map[string]string, qualities []TrackQuality) Track {
	if data == nil { data = map[string]string{} }
	return Track{
		ID: source + ":" + nativeID, SourceID: source, LXSourceKey: source,
		SourceData: data, Title: decodeLXName(title), Artist: decodeLXName(artist),
		Album: decodeLXName(album), DurationMS: duration, CoverURL: cover,
		Qualities: qualities,
	}
}
func quality(kind string, size int64, data map[string]string) TrackQuality {
	return TrackQuality{Type: kind, Size: formatLXSize(size), SourceData: data}
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
func (p kuwoSearchProvider) Search(ctx context.Context, keyword string, page int) (SearchPage, error) {
	u, _ := url.Parse(p.endpoint)
	q := u.Query()
	q.Set("client", "kt"); q.Set("all", keyword); q.Set("pn", strconv.Itoa(page-1)); q.Set("rn", "30")
	q.Set("uid", "794762570"); q.Set("ver", "kwplayer_ar_9.2.2.1"); q.Set("vipver", "1")
	q.Set("show_copyright_off", "1"); q.Set("newver", "1"); q.Set("ft", "music"); q.Set("cluster", "0")
	q.Set("strategy", "2012"); q.Set("encoding", "utf8"); q.Set("rformat", "json"); q.Set("vermerge", "1"); q.Set("mobi", "1"); q.Set("issubtitle", "1")
	u.RawQuery = q.Encode()
	raw, err := doLXJSON(ctx, p.client, http.MethodGet, u.String(), nil, nil)
	if err != nil { return SearchPage{}, err }
	total, _ := strconv.ParseInt(stringValue(raw["TOTAL"]), 10, 64)
	list := make([]Track, 0)
	for _, entry := range sliceValue(raw["abslist"]) {
		item := mapValue(entry)
		id := strings.TrimPrefix(stringValue(item["MUSICRID"]), "MUSIC_")
		name := stringValue(item["SONGNAME"])
		if id == "" || name == "" { continue }
		types := []TrackQuality{}
		for _, token := range strings.Split(stringValue(item["N_MINFO"]), ";") {
			match := kuwoInfoPattern.FindStringSubmatch(token)
			if len(match) != 5 { continue }
			size, _ := strconv.ParseFloat(match[4], 64)
			var kind string
			switch match[2] { case "4000": kind="flac24bit"; case "2000": kind="flac"; case "320": kind="320k"; case "128": kind="128k" }
			if kind != "" { types = append(types, TrackQuality{Type:kind, Size:match[4]}) }
			_ = size
		}
		for i,j:=0,len(types)-1;i<j;i,j=i+1,j-1 { types[i],types[j]=types[j],types[i] }
		duration, _ := strconv.ParseInt(stringValue(item["DURATION"]),10,64)
		data := map[string]string{"songmid":id,"album_id":stringValue(item["ALBUMID"])}
		list = append(list,newLXTrack("kw",id,stringValue(item["SONGNAME"]),stringValue(item["ARTIST"]),stringValue(item["ALBUM"]),formatLXDuration(duration),"",data,types))
	}
	return SearchPage{Tracks:list,Total:total,Page:page,PageSize:30,MaxPage:pageCount(total,30),Source:"kw"},nil
}

// Kugou (kg)
type kugouSearchProvider struct { client *http.Client; endpoint string }
func (p kugouSearchProvider) ID() string { return LXSearchKG }
func (p kugouSearchProvider) Name() string { return "酷狗音乐" }
func (p kugouSearchProvider) Search(ctx context.Context, keyword string, page int) (SearchPage,error) {
	u,_:=url.Parse(p.endpoint); q:=u.Query()
	q.Set("platform","AndroidFilter");q.Set("iscorrection","1");q.Set("keyword",keyword);q.Set("hifiquality","0")
	q.Set("pagesize","30");q.Set("PrivilegeFilter","0");q.Set("page",strconv.Itoa(page));u.RawQuery=q.Encode()
	raw,err:=doLXJSON(ctx,p.client,http.MethodGet,u.String(),nil,nil);if err!=nil{return SearchPage{},err}
	if intValue(raw["error_code"])!=0{return SearchPage{},fmt.Errorf("酷狗搜索失败，错误码：%s",stringValue(raw["error_code"]))}
	data:=mapValue(raw["data"]);items:=sliceValue(data["lists"]);total:=intValue(data["total"])
	list:=make([]Track,0)
	seen:=map[string]bool{}
	var add func(map[string]any)
	add=func(item map[string]any){
		id:=stringValue(item["Audioid"]);hash:=stringValue(item["FileHash"]);key:=id+":"+hash
		if id==""||seen[key]{return};seen[key]=true
		name:=stringValue(item["OriSongName"]);if suffix:=stringValue(item["Suffix"]);suffix!=""{name+=" "+suffix}
		types:=[]TrackQuality{}
		for _,spec:=range []struct{field,sizeField,kind string}{{"FileHash","FileSize","128k"},{"HQFileHash","HQFileSize","320k"},{"SQFileHash","SQFileSize","flac"},{"ResFileHash","ResFileSize","flac24bit"}}{
			sz:=intValue(item[spec.sizeField]);if sz>0{types=append(types,quality(spec.kind,sz,map[string]string{"hash":stringValue(item[spec.field])}))}
		}
		artists:=[]string{};for _,v:=range sliceValue(item["Singers"]){if n:=stringValue(mapValue(v)["name"]);n!=""{artists=append(artists,n)}}
		artist:=strings.Join(artists,"、")
		dur:=intValue(item["Duration"])
		sourceData:=map[string]string{"songmid":id,"hash":hash,"album_id":stringValue(item["AlbumID"]),"album_audio_id":stringValue(item["MixSongID"])}
		list=append(list,newLXTrack("kg",id,name,artist,stringValue(item["AlbumName"]),formatLXDuration(dur),"",sourceData,types))
	}
	for _,entry:=range items{item:=mapValue(entry);add(item);for _,child:=range sliceValue(item["Grp"]){add(mapValue(child))}}
	return SearchPage{Tracks:list,Total:total,Page:page,PageSize:30,MaxPage:pageCount(total,30),Source:"kg"},nil
}

// QQ Music (tx)
type qqSearchProvider struct { client *http.Client; endpoint string }
func (p qqSearchProvider) ID()string{return LXSearchTX}
func (p qqSearchProvider) Name()string{return "QQ 音乐"}
type qqSearchRequest struct {
	Comm struct {
		ChannelID string `json:"_channelid"`
		OSVersion string `json:"_os_version"`
		CT string `json:"ct"`
		CV string `json:"cv"`
		GUID string `json:"guid"`
		Patch string `json:"patch"`
		Expires int `json:"psrf_access_token_expiresAt"`
		AccessToken string `json:"psrf_qqaccess_token"`
		OpenID string `json:"psrf_qqopenid"`
		UnionID string `json:"psrf_qqunionid"`
		AppID string `json:"tmeAppID"`
		LoginType int `json:"tmeLoginType"`
		UIN string `json:"uin"`
		WID string `json:"wid"`
	} `json:"comm"`
	Service struct {
		Module string `json:"module"`
		Method string `json:"method"`
		Param struct {
			Group int `json:"grp"`
			Limit int `json:"num_per_page"`
			Page int `json:"page_num"`
			Query string `json:"query"`
			Remote string `json:"remoteplace"`
			Type int `json:"search_type"`
			SearchID string `json:"searchid"`
		} `json:"param"`
	} `json:"music.search.SearchCgiService"`
}
func qqSignature(text string) string {
	sum:=sha1.Sum([]byte(text));h:=hex.EncodeToString(sum[:])
	p1:=[]int{23,14,6,36,16,40,7,19};p2:=[]int{16,1,32,12,19,27,8,5}
	var a,b strings.Builder;for _,i:=range p1{a.WriteByte(h[i])};for _,i:=range p2{b.WriteByte(h[i])}
	scramble:=[]byte{89,39,179,150,218,82,58,252,177,52,186,123,120,64,242,133,143,161,121,179}
	raw:=make([]byte,len(scramble));for i,v:=range scramble{n,_:=strconv.ParseUint(h[i*2:i*2+2],16,8);raw[i]=v^byte(n)}
	mid:=base64.StdEncoding.EncodeToString(raw);mid=strings.NewReplacer("/","","+","","=","").Replace(mid)
	return strings.ToLower("zzc"+a.String()+mid+b.String())
}
func (p qqSearchProvider) Search(ctx context.Context,keyword string,page int)(SearchPage,error){
	var payload qqSearchRequest
	payload.Comm.ChannelID="0";payload.Comm.OSVersion="6.2.9200-2";payload.Comm.CT="19";payload.Comm.CV="2151"
	payload.Comm.GUID="1F70E520B2EAA7D25E11760783C53CA9";payload.Comm.Patch="118";payload.Comm.AppID="qqmusic";payload.Comm.WID="7223299733393904640";payload.Comm.UIN="0"
	payload.Service.Module="music.search.SearchCgiService";payload.Service.Method="DoSearchForQQMusicDesktop"
	payload.Service.Param.Group=1;payload.Service.Param.Limit=50;payload.Service.Param.Page=page;payload.Service.Param.Query=keyword;payload.Service.Param.Remote="txt.newclient.top";payload.Service.Param.Type=0
	var random [16]byte;_,_=rand.Read(random[:])
	payload.Service.Param.SearchID=strings.ToUpper(hex.EncodeToString(random[:]))+fmt.Sprintf("%05d",time.Now().UnixNano()%100000)
	body,err:=jsonBody(payload);if err!=nil{return SearchPage{},err}
	sign:=qqSignature(string(body))
	raw,err:=doLXJSON(ctx,p.client,http.MethodPost,p.endpoint+"?sign="+url.QueryEscape(sign),map[string]string{"User-Agent":"QQMusic 14090508(android 12)","Content-Type":"application/json"},bytes.NewReader(body))
	if err!=nil{return SearchPage{},err}
	req:=mapValue(raw["music.search.SearchCgiService"]);if len(req)==0{req=mapValue(raw["req"])}
	if len(req)==0||intValue(raw["code"])!=0||intValue(req["code"])!=0{return SearchPage{},errors.New("QQ 音乐搜索响应状态异常")}
	data:=mapValue(req["data"]);resultBody:=mapValue(data["body"]);song:=mapValue(resultBody["song"]);meta:=mapValue(data["meta"])
	items:=sliceValue(song["list"]);tracks:=make([]Track,0,len(items))
	for _,entry:=range items{
		item:=mapValue(entry);file:=mapValue(item["file"]);media:=stringValue(file["media_mid"]);if media==""{continue}
		types:=[]TrackQuality{}
		for _,spec:=range []struct{key,kind string}{{"size_128mp3","128k"},{"size_320mp3","320k"},{"size_flac","flac"},{"size_hires","flac24bit"}}{sz:=intValue(file[spec.key]);if sz>0{types=append(types,quality(spec.kind,sz,nil))}}
		album:=mapValue(item["album"]);albumID:=stringValue(album["mid"]);albumName:=stringValue(album["name"])
		artists:=[]string{};for _,v:=range sliceValue(item["singer"]){if n:=stringValue(mapValue(v)["name"]);n!=""{artists=append(artists,n)}}
		id:=stringValue(item["mid"]);if id==""{id=stringValue(item["id"])}
		cover:="";if albumID!=""{cover="https://y.gtimg.cn/music/photo_new/T002R500x500M000"+albumID+".jpg"}
		dur:=intValue(item["interval"])*1000
		dataMap:=map[string]string{"songmid":id,"song_id":stringValue(item["id"]),"media_mid":media,"album_mid":albumID}
		tracks=append(tracks,newLXTrack("tx",id,stringValue(item["title"]),strings.Join(artists,"、"),albumName,dur,cover,dataMap,types))
	}
	total:=intValue(meta["sum"]);if total==0{total=intValue(song["totalnum"])}
	return SearchPage{Tracks:tracks,Total:total,Page:page,PageSize:50,MaxPage:pageCount(total,50),Source:"tx"},nil
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
func(p neteaseSearchProvider)Search(ctx context.Context,keyword string,page int)(SearchPage,error){
	path:="/api/search/song/list/page"
	payload:=struct{
		Keyword string `json:"keyword"`
		NeedCorrect string `json:"needCorrect"`
		Channel string `json:"channel"`
		Offset int `json:"offset"`
		Scene string `json:"scene"`
		Total bool `json:"total"`
		Limit int `json:"limit"`
	}{keyword,"1","typing",30*(page-1),"normal",page==1,30}
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
		if maxLevel=="hires"{hr:=mapValue(item["hr"]);types=append(types,quality("flac24bit",intValue(hr["size"]),nil))}
		if maxBR>=999000{sq:=mapValue(item["sq"]);types=append(types,quality("flac",intValue(sq["size"]),nil))}
		if maxBR>=320000{h:=mapValue(item["h"]);types=append(types,quality("320k",intValue(h["size"]),nil))}
		if maxBR>=128000{l:=mapValue(item["l"]);types=append(types,quality("128k",intValue(l["size"]),nil))}
		artists:=[]string{};for _,v:=range sliceValue(item["ar"]){if n:=stringValue(mapValue(v)["name"]);n!=""{artists=append(artists,n)}}
		id:=stringValue(item["id"]);albumID:=stringValue(album["id"])
		tracks=append(tracks,newLXTrack("wy",id,stringValue(item["name"]),strings.Join(artists,"、"),stringValue(album["name"]),intValue(item["dt"]),stringValue(album["picUrl"]),map[string]string{"songmid":id,"album_id":albumID},types))
	}
	total:=intValue(data["totalCount"])
	return SearchPage{Tracks:tracks,Total:total,Page:page,PageSize:30,MaxPage:pageCount(total,30),Source:"wy"},nil
}

// Migu (mg): request signing follows the LX desktop SDK's MD5 signature.
type miguSearchProvider struct{client *http.Client;endpoint string}
func(p miguSearchProvider)ID()string{return LXSearchMG}
func(p miguSearchProvider)Name()string{return "咪咕音乐"}
func(p miguSearchProvider)Search(ctx context.Context,keyword string,page int)(SearchPage,error){
	now:=strconv.FormatInt(time.Now().UnixMilli(),10)
	device:="963B7AA0D21511ED807EE5846EC87D20"
	signatureMD5:="6cdc72a439cef99a3418d2a78aa28c73"
	signature:=md5.Sum([]byte(keyword+signatureMD5+"yyapp2d16148780a1dcc7408e06336b98cfd50"+device+now))
	u,_:=url.Parse(p.endpoint);q:=u.Query()
	q.Set("isCorrect","0");q.Set("isCopyright","1");q.Set("searchSwitch",`{"song":1,"album":0,"singer":0,"tagSong":1,"mvSong":0,"bestShow":1,"songlist":0,"lyricSong":0}`)
	q.Set("pageSize","20");q.Set("text",keyword);q.Set("pageNo",strconv.Itoa(page));q.Set("sort","0");q.Set("sid","USS");u.RawQuery=q.Encode()
	headers:=map[string]string{"uiVersion":"A_music_3.6.1","deviceId":device,"timestamp":now,"sign":hex.EncodeToString(signature[:]),"channel":"0146921","User-Agent":"Mozilla/5.0 (Linux; U; Android 11.0.0; zh-cn; MI 11 Build/OPR1.170623.032) AppleWebKit/534.30 (KHTML, like Gecko) Version/4.0 Mobile Safari/534.30"}
	raw,err:=doLXJSON(ctx,p.client,http.MethodGet,u.String(),headers,nil);if err!=nil{return SearchPage{},err}
	if stringValue(raw["code"])!="000000"{return SearchPage{},fmt.Errorf("咪咕搜索失败：%s",stringValue(raw["info"]))}
	data:=mapValue(raw["songResultData"]);items:=sliceValue(data["resultList"]);tracks:=make([]Track,0);seen:=map[string]bool{}
	for _,group:=range items{for _,entry:=range sliceValue(group){
		item:=mapValue(entry);id:=stringValue(item["songId"]);copyright:=stringValue(item["copyrightId"])
		if id==""||copyright==""||seen[copyright]{continue};seen[copyright]=true
		types:=[]TrackQuality{}
		for _,v:=range sliceValue(item["audioFormats"]){f:=mapValue(v);kind:="";switch stringValue(f["formatType"]){case "PQ":kind="128k";case "HQ":kind="320k";case "SQ":kind="flac";case "ZQ24":kind="flac24bit"};if kind!=""{types=append(types,quality(kind,func() int64 { if n:=intValue(f["asize"]); n>0 { return n }; return intValue(f["isize"]) }(),nil))}}
		img:=stringValue(item["img3"]);if img==""{img=stringValue(item["img2"])};if img==""{img=stringValue(item["img1"])};if img!=""&&!strings.HasPrefix(img,"http"){img="http://d.musicapp.migu.cn"+img}
		artists:=[]string{};for _,v:=range sliceValue(item["singerList"]){if n:=stringValue(mapValue(v)["name"]);n!=""{artists=append(artists,n)}}
		dataMap:=map[string]string{"songmid":id,"copyright_id":copyright,"album_id":stringValue(item["albumId"]),"lrc_url":stringValue(item["lrcUrl"]),"mrc_url":stringValue(item["mrcurl"]),"trc_url":stringValue(item["trcUrl"])}
		tracks=append(tracks,newLXTrack("mg",id,stringValue(item["name"]),strings.Join(artists,"、"),stringValue(item["album"]),formatLXDuration(intValue(item["duration"])),img,dataMap,types))
	}}
	total:=intValue(data["totalCount"])
	return SearchPage{Tracks:tracks,Total:total,Page:page,PageSize:20,MaxPage:pageCount(total,20),Source:"mg"},nil
}
