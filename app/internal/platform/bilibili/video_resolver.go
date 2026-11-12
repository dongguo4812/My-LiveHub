package bilibili

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	videoViewURL = "https://api.bilibili.com/x/web-interface/view"
	videoNavURL = "https://api.bilibili.com/x/web-interface/nav"
	videoPlayURL = "https://api.bilibili.com/x/player/wbi/playurl"
	videoReferer = "https://www.bilibili.com/"
	videoUserAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 Chrome/132.0.0.0 Safari/537.36"
)

type VideoPlayback struct {
	BVID string
	Title string
	ResourceURL string
	Headers map[string]string
	Quality int
}

type VideoCredentialProvider interface { LoadCredentials(context.Context) (Credentials, error) }

type VideoResolver struct {
	auth VideoCredentialProvider
	client *http.Client
	now func() time.Time
	viewURL string
	navURL string
	playURL string
}

func NewVideoResolver(auth VideoCredentialProvider, client *http.Client) (*VideoResolver, error) {
	if auth == nil { return nil, errors.New("B 站登录管理器不能为空") }
	if client == nil { client = &http.Client{Timeout: 20 * time.Second} }
	return &VideoResolver{auth:auth, client:client, now:time.Now, viewURL:videoViewURL, navURL:videoNavURL, playURL:videoPlayURL}, nil
}

func (r *VideoResolver) Resolve(ctx context.Context, rawBVID string) (VideoPlayback, error) {
	bvid := strings.TrimSpace(rawBVID)
	if !validVideoBVID(bvid) { return VideoPlayback{}, errors.New("请输入有效的 B 站 BV 号") }
	credentials, err := r.auth.LoadCredentials(ctx)
	if err != nil { return VideoPlayback{}, err }

	detailQuery := url.Values{"bvid":{bvid}}
	detailBody, err := r.get(ctx, r.viewURL, detailQuery, credentials.Cookie)
	if err != nil { return VideoPlayback{}, fmt.Errorf("获取 B 站视频信息失败：%w", err) }
	var detail struct {
		Code int `json:"code"`
		Message string `json:"message"`
		Data struct {
			BVID string `json:"bvid"`
			Title string `json:"title"`
			CID int64 `json:"cid"`
			Pages []struct { CID int64 `json:"cid"`; Page int `json:"page"` } `json:"pages"`
		} `json:"data"`
	}
	if err := json.Unmarshal(detailBody, &detail); err != nil { return VideoPlayback{}, fmt.Errorf("解析 B 站视频信息失败：%w", err) }
	if detail.Code != 0 { return VideoPlayback{}, fmt.Errorf("B 站视频信息接口错误（%d）：%s", detail.Code, detail.Message) }
	cid := detail.Data.CID
	if cid == 0 && len(detail.Data.Pages) > 0 { cid = detail.Data.Pages[0].CID }
	if cid <= 0 { return VideoPlayback{}, errors.New("B 站视频没有可播放的 CID") }

	navBody, err := r.get(ctx, r.navURL, nil, credentials.Cookie)
	if err != nil { return VideoPlayback{}, fmt.Errorf("获取 B 站播放签名密钥失败：%w", err) }
	var nav struct {
		Code int `json:"code"`
		Message string `json:"message"`
		Data struct { WBI struct { ImgURL string `json:"img_url"`; SubURL string `json:"sub_url"` } `json:"wbi_img"` } `json:"data"`
	}
	if err := json.Unmarshal(navBody, &nav); err != nil { return VideoPlayback{}, err }
	if nav.Code != 0 { return VideoPlayback{}, fmt.Errorf("读取 B 站 WBI 密钥失败：%s", nav.Message) }
	imgKey, subKey := wbiKey(nav.Data.WBI.ImgURL), wbiKey(nav.Data.WBI.SubURL)
	if len(imgKey) < 32 || len(subKey) < 32 { return VideoPlayback{}, errors.New("B 站没有返回有效的 WBI 密钥") }

	// qn=127 asks Bilibili for the highest quality available to this account.
	// The API may downgrade it according to the video's encodings and account
	// permissions; Data.Quality below records the quality actually returned.
	// Keep the interleaved MP4/DURL request so mpv needs no external muxer.
	params := url.Values{
		"bvid":{bvid}, "cid":{strconv.FormatInt(cid,10)}, "qn":{"127"},
		"fnval":{"1"}, "fnver":{"0"}, "fourk":{"1"}, "platform":{"html5"},
	}
	signed := signVideoWBI(params, imgKey, subKey, r.now())
	playBody, err := r.get(ctx, r.playURL, signed, credentials.Cookie)
	if err != nil { return VideoPlayback{}, fmt.Errorf("获取 B 站 MP4 播放地址失败：%w", err) }
	var play struct {
		Code int `json:"code"`
		Message string `json:"message"`
		Data struct {
			Quality int `json:"quality"`
			DURL []struct { URL string `json:"url"` } `json:"durl"`
		} `json:"data"`
	}
	if err := json.Unmarshal(playBody, &play); err != nil { return VideoPlayback{}, fmt.Errorf("解析 B 站播放地址失败：%w", err) }
	if play.Code != 0 { return VideoPlayback{}, fmt.Errorf("B 站播放地址接口错误（%d）：%s", play.Code, play.Message) }
	if len(play.Data.DURL) == 0 || strings.TrimSpace(play.Data.DURL[0].URL) == "" {
		return VideoPlayback{}, errors.New("B 站没有返回 MP4 格式视频流；本次最小验证暂不处理 DASH 分离音视频")
	}
	return VideoPlayback{
		BVID:bvid, Title:strings.TrimSpace(detail.Data.Title), ResourceURL:play.Data.DURL[0].URL,
		Headers:map[string]string{"Cookie":credentials.Cookie,"Referer":videoReferer,"User-Agent":videoUserAgent},
		Quality:play.Data.Quality,
	}, nil
}

func (r *VideoResolver) get(ctx context.Context, endpoint string, params url.Values, cookie string) ([]byte,error) {
	u, err := url.Parse(endpoint); if err != nil { return nil,err }
	if params != nil { u.RawQuery = params.Encode() }
	req, err := http.NewRequestWithContext(ctx,http.MethodGet,u.String(),nil); if err != nil { return nil,err }
	req.Header.Set("User-Agent",videoUserAgent)
	req.Header.Set("Referer",videoReferer)
	req.Header.Set("Accept","application/json, text/plain, */*")
	req.Header.Set("Cookie",cookie)
	resp,err:=r.client.Do(req); if err!=nil{return nil,err}; defer resp.Body.Close()
	if resp.StatusCode<200 || resp.StatusCode>=300 { return nil,fmt.Errorf("HTTP %d",resp.StatusCode) }
	return io.ReadAll(io.LimitReader(resp.Body,8<<20))
}

func validVideoBVID(value string) bool {
	if len(value)!=12 || !strings.HasPrefix(value,"BV") { return false }
	for _,ch:=range value[2:] { if !(ch>='0'&&ch<='9'||ch>='A'&&ch<='Z'||ch>='a'&&ch<='z'){return false} }
	return true
}

func wbiKey(raw string) string {
	u,err:=url.Parse(raw); if err!=nil{return ""}
	return strings.TrimSuffix(path.Base(u.Path),path.Ext(u.Path))
}

var videoWBIMixin = []int{46,47,18,2,53,8,23,32,15,50,10,31,58,3,45,35,27,43,5,49,33,9,42,19,29,28,14,39,12,38,41,13,37,48,7,16,24,55,40,61,26,17,0,1,60,51,30,4,22,25,54,21,56,59,6,63,57,62,11,36,20,34,44,52}

func signVideoWBI(params url.Values, imgKey, subKey string, now time.Time) url.Values {
	clean:=url.Values{}
	for key,values:=range params { for _,value:=range values {
		value=strings.Map(func(ch rune)rune{if strings.ContainsRune("!'()*",ch){return -1};return ch},value)
		clean.Add(key,value)
	} }
	clean.Set("wts",strconv.FormatInt(now.Unix(),10))
	keys:=make([]string,0,len(clean)); for key:=range clean{keys=append(keys,key)}; sort.Strings(keys)
	var parts []string
	for _,key:=range keys { for _,value:=range clean[key] {
		k:=strings.ReplaceAll(url.QueryEscape(key),"+","%20")
		v:=strings.ReplaceAll(url.QueryEscape(value),"+","%20")
		parts=append(parts,k+"="+v)
	} }
	source:=imgKey+subKey; var mixed strings.Builder
	for _,index:=range videoWBIMixin { if index<len(source){mixed.WriteByte(source[index])} }
	mix:=mixed.String(); if len(mix)>32{mix=mix[:32]}
	sum:=md5.Sum([]byte(strings.Join(parts,"&")+mix))
	clean.Set("w_rid",hex.EncodeToString(sum[:]))
	return clean
}
