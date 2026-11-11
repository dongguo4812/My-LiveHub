package music

import (
 "context"
 "crypto/rand"
 "crypto/sha1"
 "encoding/base64"
 "encoding/hex"
 "encoding/json"
 "errors"
 "fmt"
 "io"
 "net/http"
 "net/url"
 "math/big"
 "strconv"
 "strings"
 "time"
)

const lxQQSearchURL = "https://u.y.qq.com/cgi-bin/musics.fcg"
var lxQQScramble = []byte{89,39,179,150,218,82,58,252,177,52,186,123,120,64,242,133,143,161,121,179}
var lxQQPart1 = []int{23,14,6,36,16,40,7,19}
var lxQQPart2 = []int{16,1,32,12,19,27,8,5}

func lxQQSign(body []byte) string {
 sum:=sha1.Sum(body); h:=hex.EncodeToString(sum[:])
 pick:=func(indexes []int) string {var b strings.Builder;for _,i:=range indexes {if i>=0&&i<len(h){b.WriteByte(h[i])}};return b.String()}
 mixed:=make([]byte,len(lxQQScramble));for i,v:=range lxQQScramble {n,_:=strconv.ParseUint(h[i*2:i*2+2],16,8);mixed[i]=v^byte(n)}
 encoded:=base64.StdEncoding.EncodeToString(mixed)
 encoded=strings.NewReplacer("/","","+","","=","").Replace(encoded)
 return strings.ToLower("zzc"+pick(lxQQPart1)+encoded+pick(lxQQPart2))
}
func lxQQSearchID() string {
 var b [16]byte
 if _,err:=rand.Read(b[:]);err!=nil{return strings.Repeat("0",32)+"00000"}
 suffix,err:=rand.Int(rand.Reader,big.NewInt(100000));if err!=nil{return strings.ToUpper(hex.EncodeToString(b[:]))+"00000"};return strings.ToUpper(hex.EncodeToString(b[:]))+fmt.Sprintf("%05d",suffix.Int64())
}
type lxQQComm struct {
 ChannelID string `json:"_channelid"`; OSVersion string `json:"_os_version"`; CT string `json:"ct"`; CV string `json:"cv"`; GUID string `json:"guid"`; Patch string `json:"patch"`
 TokenExpires int `json:"psrf_access_token_expiresAt"`; QQAccessToken string `json:"psrf_qqaccess_token"`; QQOpenID string `json:"psrf_qqopenid"`; QQUnionID string `json:"psrf_qqunionid"`; TMEAppID string `json:"tmeAppID"`; TMELoginType int `json:"tmeLoginType"`; UIN string `json:"uin"`; WID string `json:"wid"`
}
type lxQQParam struct { Group int `json:"grp"`; Limit int `json:"num_per_page"`; Page int `json:"page_num"`; Query string `json:"query"`; RemotePlace string `json:"remoteplace"`; SearchType int `json:"search_type"`; SearchID string `json:"searchid"` }
type lxQQServiceRequest struct { Module string `json:"module"`; Method string `json:"method"`; Param lxQQParam `json:"param"` }
type lxQQRequest struct { Comm lxQQComm `json:"comm"`; Service lxQQServiceRequest `json:"music.search.SearchCgiService"` }
type lxQQSong struct {
 ID int64 `json:"id"`
 Mid string `json:"mid"`
 Title string `json:"title"`
 Interval int64 `json:"interval"`
 Singer []struct { Name string `json:"name"`; Mid string `json:"mid"` } `json:"singer"`
 Album struct { ID int64 `json:"id"`; Name string `json:"name"`; Mid string `json:"mid"` } `json:"album"`
 File struct { MediaMid string `json:"media_mid"`; Size128 int64 `json:"size_128mp3"`; Size320 int64 `json:"size_320mp3"`; SizeFlac int64 `json:"size_flac"`; SizeHiRes int64 `json:"size_hires"` } `json:"file"`
}
type lxQQSearchService struct {
 Code json.RawMessage `json:"code"`
 Data struct {
  Body struct { Song struct { List []lxQQSong `json:"list"` } `json:"song"` } `json:"body"`
  Meta struct { Sum int64 `json:"sum"` } `json:"meta"`
 } `json:"data"`
}
type lxQQSearchEnvelope struct {
 Code json.RawMessage `json:"code"`
 Service lxQQSearchService `json:"music.search.SearchCgiService"`
 Req lxQQSearchService `json:"req"`
}

type LXQQMusicSource struct { client *http.Client; baseURL string }
func NewLXQQMusicSource(client *http.Client,baseURL string)*LXQQMusicSource {
 if client==nil {client=&http.Client{Timeout:15*time.Second}}
 baseURL=strings.TrimSpace(baseURL);if baseURL==""{baseURL=lxQQSearchURL}
 return &LXQQMusicSource{client:client,baseURL:baseURL}
}
func (*LXQQMusicSource) ID()string{return "qq_music"}
func (*LXQQMusicSource) Name()string{return "QQ 音乐"}
func (*LXQQMusicSource) Capabilities()SourceCapabilities{return SourceCapabilities{Search:true,Resolve:false}}
func(s *LXQQMusicSource)Search(ctx context.Context,keyword string,limit int)([]Track,error){p,e:=s.SearchPage(ctx,keyword,1,limit);return p.Tracks,e}
func(s *LXQQMusicSource)SearchPage(ctx context.Context,keyword string,page,limit int)(SourceSearchPage,error){
 keyword,err:=normalizeSearchKeyword(keyword);if err!=nil{return SourceSearchPage{},err}
 limit=normalizeSearchLimit(limit);if page<1{page=1}
 var lastErr error
 // LX desktop creates a fresh searchid and signature for every retry.
 // Reusing the previous signed payload is not equivalent to its retry path.
 for attempt:=0;attempt<6;attempt++{
  payload:=lxQQRequest{
   Comm:lxQQComm{ChannelID:"0",OSVersion:"6.2.9200-2",CT:"19",CV:"2151",GUID:"1F70E520B2EAA7D25E11760783C53CA9",Patch:"118",TokenExpires:0,QQAccessToken:"",QQOpenID:"",QQUnionID:"",TMEAppID:"qqmusic",TMELoginType:0,UIN:"0",WID:"7223299733393904640"},
   Service:lxQQServiceRequest{Module:"music.search.SearchCgiService",Method:"DoSearchForQQMusicDesktop",Param:lxQQParam{Group:1,Limit:limit,Page:page,Query:keyword,RemotePlace:"txt.newclient.top",SearchType:0,SearchID:lxQQSearchID()}},
  }
  body,err:=json.Marshal(payload);if err!=nil{return SourceSearchPage{},err}
  endpoint,err:=url.Parse(s.baseURL);if err!=nil{return SourceSearchPage{},err}
  q:=endpoint.Query();q.Set("sign",lxQQSign(body));endpoint.RawQuery=q.Encode()
  req,e:=http.NewRequestWithContext(ctx,http.MethodPost,endpoint.String(),strings.NewReader(string(body)));if e!=nil{return SourceSearchPage{},e}
  req.Header.Set("Content-Type","application/json");req.Header.Set("User-Agent","QQMusic 14090508(android 12)")
  resp,e:=s.client.Do(req);if e!=nil{return SourceSearchPage{},e}
  raw,e:=io.ReadAll(io.LimitReader(resp.Body,8<<20));resp.Body.Close();if e!=nil{return SourceSearchPage{},e}
  if resp.StatusCode<200||resp.StatusCode>=300{return SourceSearchPage{},fmt.Errorf("QQ Music HTTP %d",resp.StatusCode)}
  var envelope lxQQSearchEnvelope
  if e=json.Unmarshal(raw,&envelope);e!=nil{return SourceSearchPage{},fmt.Errorf("decode QQ Music response: %w",e)}
  service:=envelope.Service
  if len(service.Code)==0 { service=envelope.Req }
  if len(service.Code)==0 {lastErr=fmt.Errorf("QQ Music response missing service envelope (body.code=%s)",lxQQCodeLabel(envelope.Code));continue}
  if !lxQQCodeOK(envelope.Code)||!lxQQCodeOK(service.Code){
   lastErr=fmt.Errorf("QQ Music search response code is not successful (body.code=%s, service.code=%s)",lxQQCodeLabel(envelope.Code),lxQQCodeLabel(service.Code))
   continue
  }
  tracks:=make([]Track,0,len(service.Data.Body.Song.List))
  for _,it:=range service.Data.Body.Song.List{
   id,title,mediaMid:=strings.TrimSpace(it.Mid),strings.TrimSpace(it.Title),strings.TrimSpace(it.File.MediaMid);if id==""||title==""||mediaMid==""{continue}
   artists:=make([]string,0,len(it.Singer));for _,a:=range it.Singer{if strings.TrimSpace(a.Name)!=""{artists=append(artists,strings.TrimSpace(a.Name))}}
   cover:="";if it.Album.Mid!=""&&it.Album.Mid!="空"{cover="https://y.gtimg.cn/music/photo_new/T002R500x500M000"+it.Album.Mid+".jpg"}else if len(it.Singer)>0&&strings.TrimSpace(it.Singer[0].Mid)!=""{cover="https://y.gtimg.cn/music/photo_new/T001R500x500M000"+it.Singer[0].Mid+".jpg"}
   tracks=append(tracks,Track{ID:id,SourceID:s.ID(),LXSourceKey:"tx",Title:title,Artist:strings.Join(artists,"、"),Album:it.Album.Name,DurationMS:it.Interval*1000,CoverURL:cover,SourceData:map[string]string{
    "id":strconv.FormatInt(it.ID,10),"mid":id,"songmid":id,"name":title,"title":title,"songname":title,"singer":strings.Join(artists,"、"),"artist":strings.Join(artists,"、"),
    "album":it.Album.Name,"albumname":it.Album.Name,"albumid":strconv.FormatInt(it.Album.ID,10),"albummid":it.Album.Mid,"media_mid":it.File.MediaMid,"interval":strconv.FormatInt(it.Interval,10),
    "size_128mp3":strconv.FormatInt(it.File.Size128,10),"size_320mp3":strconv.FormatInt(it.File.Size320,10),"size_flac":strconv.FormatInt(it.File.SizeFlac,10),"size_hires":strconv.FormatInt(it.File.SizeHiRes,10),
   }})
  }
  total,maxPage:=sourcePageCount(service.Data.Meta.Sum,page,limit,len(tracks))
  return SourceSearchPage{Tracks:tracks,Total:total,MaxPage:maxPage,PageSize:limit},nil
 }
 return SourceSearchPage{},fmt.Errorf("search QQ Music using LX desktop API after 6 attempts: %w",lastErr)
}
func(*LXQQMusicSource)Resolve(context.Context,Track)(PlaybackResource,error){return PlaybackResource{},errors.New("QQ Music search is available; playback URL resolution requires a compatible LX musicUrl source")}
func lxQQCodeLabel(raw json.RawMessage) string {
 if len(raw)==0 { return "<missing>" }
 return string(raw)
}

// LX desktop compares response codes with JavaScript's loose numeric equality.
func lxQQCodeOK(raw json.RawMessage) bool {
 if len(raw)==0 { return false }
 var value any
 if err:=json.Unmarshal(raw,&value);err!=nil { return false }
 switch v:=value.(type) {
 case float64: return v==0
 case string:
  n,err:=strconv.ParseFloat(strings.TrimSpace(v),64)
  return err==nil && n==0
 case bool: return !v
 default: return false
 }
}

var _ Source=(*LXQQMusicSource)(nil)
var _ PagedSource=(*LXQQMusicSource)(nil)
