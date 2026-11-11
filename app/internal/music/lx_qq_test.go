package music

import (
 "context"
 "encoding/json"
 "net/http"
 "net/http/httptest"
 "strings"
 "testing"
)

func TestLXQQMusicSourceUsesSignedDesktopSearch(t *testing.T) {
 server:=httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter,r *http.Request){
  if r.Method!=http.MethodPost {t.Errorf("method=%s",r.Method)}
  if r.URL.Path!="/cgi-bin/musics.fcg" {t.Errorf("path=%s",r.URL.Path)}
  if got:=r.URL.Query().Get("sign");got==""||!strings.HasPrefix(got,"zzc"){t.Errorf("invalid sign %q",got)}
  if r.Header.Get("User-Agent")!="QQMusic 14090508(android 12)" {t.Errorf("unexpected UA %q",r.Header.Get("User-Agent"))}
  var request map[string]json.RawMessage
  if err:=json.NewDecoder(r.Body).Decode(&request);err!=nil{t.Fatal(err)}
  var service struct{Module string `json:"module"`;Method string `json:"method"`;Param struct{Query string `json:"query"`;Page int `json:"page_num"`;Limit int `json:"num_per_page"`} `json:"param"`}
  if err:=json.Unmarshal(request["music.search.SearchCgiService"],&service);err!=nil{t.Fatal(err)}
  if service.Module!="music.search.SearchCgiService"||service.Method!="DoSearchForQQMusicDesktop"||service.Param.Query!="找自己 陶喆"||service.Param.Page!=2||service.Param.Limit!=10 {t.Errorf("unexpected request %#v",service)}
  _,_=w.Write([]byte(`{"code":0,"music.search.SearchCgiService":{"code":0,"data":{"body":{"song":{"list":[{"id":123,"mid":"song-mid","title":"找自己","interval":304,"singer":[{"name":"陶喆"}],"album":{"name":"I'm OK","mid":"album-mid"},"file":{"media_mid":"media-mid","size_128mp3":1234,"size_320mp3":2345,"size_flac":3456,"size_hires":4567}}]}},"meta":{"sum":21}}}}`))
 }))
 defer server.Close()
 source:=NewLXQQMusicSource(server.Client(),server.URL+"/cgi-bin/musics.fcg")
 result,err:=source.SearchPage(context.Background(),"找自己 陶喆",2,10)
 if err!=nil{t.Fatal(err)}
 if result.Total!=21||result.MaxPage!=3||len(result.Tracks)!=1{t.Fatalf("unexpected result %#v",result)}
 got:=result.Tracks[0]
 if got.ID!="song-mid"||got.LXSourceKey!="tx"||got.SourceData["media_mid"]!="media-mid"||got.SourceData["size_flac"]!="3456" {t.Fatalf("LX metadata lost %#v",got)}
}
