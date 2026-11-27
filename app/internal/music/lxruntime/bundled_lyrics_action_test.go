//go:build lxproduction

package lxruntime

import (
 "context"
 "encoding/json"
 "net/http"
 "net/http/httptest"
 "strings"
 "testing"
 "time"
)

func TestProductionLXBundledLyricsActionsUseQuickJSForAllFivePlatforms(t *testing.T) {
 var kugouDownloads int
 server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
  switch r.URL.Path {
  case "/kw":
   _ = json.NewEncoder(w).Encode(map[string]any{"data":map[string]any{"lrclist":[]any{map[string]any{"time":"00:01.00","lineLyric":"KW original"},map[string]any{"time":"00:02.00","lineLyric":"KW next"}}}})
  case "/kg-search":
   _ = json.NewEncoder(w).Encode(map[string]any{"candidates":[]any{map[string]any{"id":"kg-id","accesskey":"kg-key"}}})
  case "/kg-download":
   kugouDownloads++
   _ = json.NewEncoder(w).Encode(map[string]any{"content":"WzAwOjAxLjAwXUtH"})
  case "/tx":
   _ = json.NewEncoder(w).Encode(map[string]any{"lyric":"[00:01.00]TX original","tlyric":"[00:01.00]TX translation"})
  case "/wy":
   _ = json.NewEncoder(w).Encode(map[string]any{"lrc":map[string]any{"lyric":"[00:01.00]WY original"},"tlyric":map[string]any{"lyric":"[00:01.00]WY translation"}})
  case "/mg":
   _, _ = w.Write([]byte("[00:01.00]MG original\n[00:01.00]MG next"))
  case "/empty":
   _ = json.NewEncoder(w).Encode(map[string]any{"data":map[string]any{"lrclist":[]any{}}})
  default:
   http.NotFound(w,r)
  }
 }))
 defer server.Close()

 manager, err := NewSourceManagerWithScripts(server.Client(), []LXSourceScript{BundledLyricsAction()})
 if err != nil { t.Fatal(err) }
 defer manager.Close()
 ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
 defer cancel()
 if err := manager.Start(ctx); err != nil { t.Fatalf("initialize built-in lyrics: %v",err) }

 endpoints := map[string]string{
  "kw":server.URL+"/kw", "kgSearch":server.URL+"/kg-search", "kgDownload":server.URL+"/kg-download",
  "tx":server.URL+"/tx", "wy":server.URL+"/wy", "mg":server.URL+"/mg",
 }
 tracks := map[string]map[string]any{
  "kw":{"id":"kw-1","title":"KW","songmid":"kw-native"},
  "kg":{"id":"kg-1","title":"KG","artist":"Singer","duration_ms":180000,"source_data":map[string]any{"hash":"KG-HASH"}},
  "tx":{"id":"tx-1","title":"TX","songmid":"TX-MID","song_id":"123"},
  "wy":{"id":"wy_123","title":"WY"},
  "mg":{"id":"mg-1","title":"MG","source_data":map[string]any{"lrc_url":server.URL+"/mg"}},
 }
 for _, source := range []string{"tx","kg","kw","wy","mg"} {
  if !manager.HasExtensionAction(source,"lyric") { t.Fatalf("%s lyric action was not registered",source) }
  requestCtx, requestCancel := context.WithTimeout(context.Background(),5*time.Second)
  value, dispatchErr := manager.DispatchExtensionAction(requestCtx,source,"lyric",map[string]any{
   "musicInfo":tracks[source],"__testEndpoints":endpoints,
  })
  requestCancel()
  if dispatchErr != nil { t.Fatalf("%s lyric action failed: %v",source,dispatchErr) }
  raw, marshalErr := json.Marshal(value)
  if marshalErr != nil { t.Fatal(marshalErr) }
  var result struct { Lyric string `json:"lyric"`; Translation string `json:"tlyric"` }
  if err := json.Unmarshal(raw,&result); err != nil { t.Fatalf("%s lyric decode: %v (%s)",source,err,raw) }
  if !strings.Contains(result.Lyric,"[00:01.00]") { t.Errorf("%s lost LRC timeline: %#v",source,result) }
  if (source=="tx" || source=="wy") && result.Translation=="" { t.Errorf("%s lost translation: %#v",source,result) }
 }
 if kugouDownloads != 1 { t.Fatalf("Kugou lyric download requests=%d, want 1",kugouDownloads) }

 emptyEndpoints := map[string]string{"kw":server.URL+"/empty"}
 emptyCtx, emptyCancel := context.WithTimeout(context.Background(),5*time.Second)
 empty, emptyErr := manager.DispatchExtensionAction(emptyCtx,"kw","lyric",map[string]any{
  "musicInfo":map[string]any{"id":"kw-empty","songmid":"kw-empty"},"__testEndpoints":emptyEndpoints,
 })
 emptyCancel()
 if emptyErr != nil { t.Fatalf("empty lyric response should be valid: %v",emptyErr) }
 emptyJSON,_ := json.Marshal(empty)
 var emptyResult struct { Lyric string `json:"lyric"` }
 _ = json.Unmarshal(emptyJSON,&emptyResult)
 if emptyResult.Lyric!="" { t.Fatalf("empty lyric result = %q",emptyResult.Lyric) }
}
