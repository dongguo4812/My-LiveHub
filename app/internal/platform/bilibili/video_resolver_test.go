package bilibili

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type videoTestCredentials struct { cookie string }
func (p videoTestCredentials) LoadCredentials(context.Context) (Credentials,error) {
	return Credentials{Cookie:p.cookie},nil
}

func TestVideoResolverReturnsMP4AndRequiredHeaders(t *testing.T) {
	const cookie = "SESSDATA=test-cookie; bili_jct=test-csrf"
	const img = "0123456789abcdef0123456789abcdef"
	const sub = "abcdef0123456789abcdef0123456789"
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter,r *http.Request) {
		if r.Header.Get("Cookie") != cookie { t.Errorf("Cookie = %q",r.Header.Get("Cookie")) }
		if r.Header.Get("Referer") != videoReferer { t.Errorf("Referer = %q",r.Header.Get("Referer")) }
		if r.Header.Get("User-Agent") != videoUserAgent { t.Errorf("User-Agent = %q",r.Header.Get("User-Agent")) }
		w.Header().Set("Content-Type","application/json")
		switch r.URL.Path {
		case "/view":
			fmt.Fprint(w,`{"code":0,"data":{"bvid":"BV1xx411c7mD","title":"测试视频","cid":12345,"pages":[{"cid":12345,"page":1}]}}`)
		case "/nav":
			fmt.Fprintf(w,`{"code":0,"data":{"wbi_img":{"img_url":"https://i.example/%s.png","sub_url":"https://i.example/%s.png"}}}`,img,sub)
		case "/danmaku":
			if r.URL.Query().Get("oid") != "12345" { t.Errorf("danmaku oid = %q", r.URL.Query().Get("oid")) }
			w.Header().Set("Content-Type", "text/xml")
			fmt.Fprint(w, `<i><d p="1,1,25,16777215,0,0,0,0">测试弹幕</d></i>`)
		case "/playurl":
			if r.URL.Query().Get("bvid")!="BV1xx411c7mD" || r.URL.Query().Get("cid")!="12345" { t.Errorf("unexpected playurl query: %s",r.URL.RawQuery) }
			if r.URL.Query().Get("w_rid")=="" || r.URL.Query().Get("wts")=="" { t.Errorf("missing WBI signature: %s",r.URL.RawQuery) }
			if r.URL.Query().Get("fnval")!="1" { t.Errorf("fnval = %q, want MP4",r.URL.Query().Get("fnval")) }
			if r.URL.Query().Get("qn")!="127" { t.Errorf("qn = %q, want highest-quality request 127",r.URL.Query().Get("qn")) }
			fmt.Fprint(w,`{"code":0,"data":{"quality":80,"durl":[{"url":"https://cdn.example/video.mp4?expires=1"}]}}`)
		default:
			http.NotFound(w,r)
		}
	}))
	defer server.Close()
	resolver,err:=NewVideoResolver(videoTestCredentials{cookie},server.Client())
	if err!=nil {t.Fatal(err)}
	resolver.viewURL=server.URL+"/view"
	resolver.navURL=server.URL+"/nav"
	resolver.playURL=server.URL+"/playurl"
	resolver.danmakuURL=server.URL+"/danmaku"
	resolver.now=func()time.Time{return time.Unix(1700000000,0)}
	got,err:=resolver.Resolve(context.Background(),"BV1xx411c7mD")
	if err!=nil {t.Fatal(err)}
	if got.Title!="测试视频" || got.Quality!=80 {t.Fatalf("unexpected video metadata: %#v",got)}
	if !strings.Contains(got.DanmakuASS, "测试弹幕") { t.Fatalf("danmaku ASS missing converted comment: %s", got.DanmakuASS) }
	if got.ResourceURL!="https://cdn.example/video.mp4?expires=1" {t.Fatalf("resource URL = %q",got.ResourceURL)}
	for key,want:=range map[string]string{"Cookie":cookie,"Referer":videoReferer,"User-Agent":videoUserAgent} {
		if got.Headers[key]!=want {t.Errorf("playback header %s = %q, want %q",key,got.Headers[key],want)}
	}
}

func TestVideoResolverRejectsInvalidBVIDBeforeNetwork(t *testing.T) {
	resolver,_:=NewVideoResolver(videoTestCredentials{"SESSDATA=test"},nil)
	_,err:=resolver.Resolve(context.Background(),"not-a-bvid")
	if err==nil || !strings.Contains(err.Error(),"BV") {t.Fatalf("expected BV validation error, got %v",err)}
}
