package desktop

import (
 "context"
 "encoding/json"
 "errors"
 "fmt"
 "net/url"
 "strings"
 "sync"
 "time"

 "github.com/dongguo4812/My-LiveHub/app/internal/music"
)

type lxScriptActionRuntime interface {
 HasExtensionAction(string, string) bool
 DispatchExtensionAction(context.Context, string, string, any) (any, error)
}

type LXScriptActionError struct {
 Source string
 Action string
 Kind string
 Err error
}
func (e *LXScriptActionError) Error() string { return fmt.Sprintf("LX script %s action for %s failed (%s): %v",e.Action,e.Source,e.Kind,e.Err) }
func (e *LXScriptActionError) Unwrap() error { return e.Err }


func (s *MusicService) dispatchLXExtension(ctx context.Context, source, action string, info any) (any, bool, error) {
 if s == nil { return nil, false, errors.New("music service is unavailable") }
 s.mu.RLock(); runtime := s.scriptRuntime; s.mu.RUnlock()
 dispatcher, ok := runtime.(lxScriptActionRuntime)
 if !ok || !dispatcher.HasExtensionAction(source, action) { return nil, false, nil }
 if ctx == nil { ctx = context.Background() }
 callCtx, cancel := context.WithTimeout(ctx, 18*time.Second); defer cancel()
 value, err := dispatcher.DispatchExtensionAction(callCtx, source, action, info)
 if err != nil {
  var unsupported interface { LXExtensionSourceUnsupported() bool }
  if errors.As(err, &unsupported) && unsupported.LXExtensionSourceUnsupported() { return nil, false, nil }
  kind := "execution"
  if errors.Is(err, context.DeadlineExceeded) { kind = "timeout" } else if errors.Is(err, context.Canceled) { kind = "cancelled" }
  return nil, true, &LXScriptActionError{Source:source,Action:action,Kind:kind,Err:err}
 }
 return value, true, nil
}

// scriptAwareSourceSearcher routes explicitly declared community actions to
// imported scripts; native provider implementations remain a separate fallback.
type scriptAwareSourceSearcher struct { base *music.LXSearchClient; service *MusicService }
func (a scriptAwareSourceSearcher) Sources() []music.SourceSnapshot { return a.base.Sources() }
func (a scriptAwareSourceSearcher) SearchProvider(ctx context.Context, source, keyword string, page int) (music.SearchPage, error) {
 value, handled, err := a.service.dispatchLXExtension(ctx, source, "search", map[string]any{"keyword":keyword,"page":page,"pageSize":30})
 if handled { if err != nil { return music.SearchPage{},err }; return normalizeLXScriptSearchResult(source,keyword,page,value) }
 return a.base.SearchProvider(ctx,source,keyword,page)
}
func (a scriptAwareSourceSearcher) SearchSources(ctx context.Context, keyword string, page int, sourceIDs []string) (music.SearchPage, map[string]string, error) {
 if ctx==nil { ctx=context.Background() }
 type outcome struct { page music.SearchPage; err error }
 outcomes:=make([]outcome,len(sourceIDs))
 var wg sync.WaitGroup
 for i,source:=range sourceIDs { wg.Add(1); go func(index int,id string){ defer wg.Done(); outcomes[index].page,outcomes[index].err=a.SearchProvider(ctx,id,keyword,page) }(i,source) }
 wg.Wait()
 result:=music.SearchPage{Tracks:[]music.Track{},Page:page,PageSize:30,MaxPage:1}
 failures:=map[string]string{}
 for i,source:=range sourceIDs { if outcomes[i].err!=nil { failures[source]=outcomes[i].err.Error(); continue }; item:=outcomes[i].page; result.Tracks=append(result.Tracks,item.Tracks...); if item.Total>result.Total { result.Total=item.Total }; if item.MaxPage>result.MaxPage { result.MaxPage=item.MaxPage } }
 if err:=ctx.Err();err!=nil { return result,failures,err }
 result.Tracks=dedupeScriptTracks(result.Tracks)
 return result,failures,nil
}
func dedupeScriptTracks(tracks []music.Track) []music.Track {
 seen:=map[string]bool{};out:=make([]music.Track,0,len(tracks))
 for _,track:=range tracks{key:=track.SourceID+"\x00"+track.ID;if !seen[key]{seen[key]=true;out=append(out,track)}};return out
}
func normalizeLXScriptSearchResult(source, keyword string, page int, value any) (music.SearchPage,error) {
 raw,err:=json.Marshal(value);if err!=nil{return music.SearchPage{},fmt.Errorf("encode %s script search result: %w",source,err)}
 var entries []json.RawMessage
 var envelope struct{Tracks []json.RawMessage `json:"tracks"`;Data []json.RawMessage `json:"data"`;Total int64 `json:"total"`;Page int `json:"page"`;PageSize int `json:"page_size"`;MaxPage int `json:"max_page"`}
 if len(raw)>0&&raw[0]=='[' { if err=json.Unmarshal(raw,&entries);err!=nil{return music.SearchPage{},fmt.Errorf("decode %s script search list: %w",source,err)} } else {
  if err=json.Unmarshal(raw,&envelope);err!=nil{return music.SearchPage{},fmt.Errorf("decode %s script search result: %w",source,err)}
  entries=envelope.Tracks;if entries==nil{entries=envelope.Data}
 }
 tracks:=make([]music.Track,0,len(entries))
 for i,entry:=range entries{
  var track music.Track;if err:=json.Unmarshal(entry,&track);err!=nil{return music.SearchPage{},fmt.Errorf("%s search track %d: %w",source,i+1,err)}
  var fields map[string]any;_=json.Unmarshal(entry,&fields)
  nativeID:=scriptString(fields,"songmid","song_id","hash","rid","id")
  if track.ID==""{track.ID=nativeID};if track.Title==""{track.Title=scriptString(fields,"title","name","songname","song_name")}
  if track.Artist==""{track.Artist=scriptString(fields,"artist","singer","author")};if track.Album==""{track.Album=scriptString(fields,"album","albumName")}
  if track.CoverURL==""{track.CoverURL=scriptString(fields,"cover","pic","picUrl","albumPic","img")}
  if track.DurationMS==0{d:=scriptInt(fields,"duration_ms","duration","interval","dt");if d>0&&d<100000{d*=1000};track.DurationMS=d}
  if track.SourceID==""{track.SourceID=source};if track.LXSourceKey==""{track.LXSourceKey=source}
  if track.SourceData==nil{track.SourceData=map[string]string{}}
  for _,key:=range []string{"songmid","song_id","songId","songid","hash","filehash","FileHash","rid","album_id","media_mid","copyright_id","mrc_url","lrc_url","trc_url"}{if v:=scriptString(fields,key);v!=""{track.SourceData[key]=v}}
  // Preserve provider-native identifiers even when a community script only
  // returns its conventional id field. Native lyric adapters need these
  // values after the search result has crossed the JS/Go boundary.
  if source=="tx" && track.SourceData["song_id"]=="" && track.SourceData["songmid"]=="" {
   id:=nativeID
   if strings.HasPrefix(id,"tx_"){id=strings.TrimPrefix(id,"tx_")}
   if id!=""{track.SourceData["song_id"]=id}
  }
  if (source=="kw"||source=="wy") && track.SourceData["songmid"]=="" && nativeID!="" {
   id:=strings.TrimPrefix(nativeID,source+"_")
   track.SourceData["songmid"]=id
  }
  if source=="kg" && track.SourceData["hash"]=="" {
   track.SourceData["hash"]=firstNonEmpty(track.SourceData["FileHash"],track.SourceData["filehash"])
  }
  if source=="mg" && track.SourceData["copyright_id"]=="" && nativeID!="" {
   track.SourceData["copyright_id"]=nativeID
  }
  if track.ID==""||track.Title==""{continue}
  if nativeID!=""&&track.ID==nativeID&&source!="kg"&&!strings.HasPrefix(track.ID,source+"_"){track.ID=source+"_"+track.ID}
  tracks=append(tracks,track)
 }
 pageValue:=envelope.Page;if pageValue<=0{pageValue=page};size:=envelope.PageSize;if size<=0{size=30};max:=envelope.MaxPage
 if max<=0&&envelope.Total>0{max=int((envelope.Total+int64(size)-1)/int64(size))};if max<=0{max=1}
 return music.SearchPage{Tracks:tracks,Total:envelope.Total,Page:pageValue,PageSize:size,MaxPage:max,Source:source},nil
}
func scriptString(m map[string]any,keys ...string)string{for _,k:=range keys{switch v:=m[k].(type){case string:if strings.TrimSpace(v)!=""{return strings.TrimSpace(v)};case float64:return fmt.Sprintf("%.0f",v)}};return ""}
func scriptInt(m map[string]any,keys ...string)int64{for _,k:=range keys{switch v:=m[k].(type){case float64:return int64(v);case string:var n int64;_,_=fmt.Sscan(v,&n);if n>0{return n}}};return 0}

func (s *MusicService) GetLXTrackDetail(track music.Track)(music.Track,error){
 source:=firstNonEmpty(track.LXSourceKey,track.SourceID)
 value,handled,err:=s.dispatchLXExtension(context.Background(),source,"detail",map[string]any{"musicInfo":track,"track":track})
 if !handled{return music.Track{},errors.New("当前音源未声明 My-LiveHub detail 扩展 action")}
 if err!=nil{return music.Track{},err}
 raw,err:=json.Marshal(value);if err!=nil{return music.Track{},fmt.Errorf("encode LX detail result: %w",err)}
 var detail music.Track
 if err=json.Unmarshal(raw,&detail);err!=nil{return music.Track{},fmt.Errorf("decode LX detail result: %w",err)}
 var fields map[string]any;_=json.Unmarshal(raw,&fields)
 if detail.ID==""{detail.ID=scriptString(fields,"songmid","song_id","hash","rid","id");if detail.ID==""{detail.ID=track.ID}}
 if detail.Title==""{detail.Title=scriptString(fields,"title","name","songname","song_name");if detail.Title==""{detail.Title=track.Title}}
 if detail.Artist==""{detail.Artist=scriptString(fields,"artist","singer","author");if detail.Artist==""{detail.Artist=track.Artist}}
 if detail.Album==""{detail.Album=scriptString(fields,"album","albumName");if detail.Album==""{detail.Album=track.Album}}
 if detail.SourceID==""{detail.SourceID=track.SourceID};if detail.LXSourceKey==""{detail.LXSourceKey=firstNonEmpty(track.LXSourceKey,track.SourceID)}
 if detail.DurationMS==0{detail.DurationMS=track.DurationMS};if detail.CoverURL==""{detail.CoverURL=scriptString(fields,"cover","pic","picUrl","albumPic","img");if detail.CoverURL==""{detail.CoverURL=track.CoverURL}}
 if detail.SourceData==nil{detail.SourceData=map[string]string{}}
 for key,value:=range track.SourceData{if _,ok:=detail.SourceData[key];!ok{detail.SourceData[key]=value}}
 if len(detail.Qualities)==0{detail.Qualities=track.Qualities}
 if detail.SelectedQuality==""{detail.SelectedQuality=track.SelectedQuality}
 return detail,nil
}
func (s *MusicService) GetLXTrackLyrics(track music.Track)(string,error){
 source:=firstNonEmpty(track.LXSourceKey,track.SourceID);value,handled,err:=s.dispatchLXExtension(context.Background(),source,"lyric",map[string]any{"musicInfo":track,"track":track})
 if !handled{return "",errors.New("当前音源未声明 My-LiveHub lyric 扩展 action")};if err!=nil{return "",err};text,ok:=value.(string);if !ok{return "",errors.New("LX lyric action must return a string")};return text,nil
}
func (s *MusicService) GetLXTrackCover(track music.Track)(string,error){
 source:=firstNonEmpty(track.LXSourceKey,track.SourceID);value,handled,err:=s.dispatchLXExtension(context.Background(),source,"pic",map[string]any{"musicInfo":track,"track":track})
 if !handled{return "",errors.New("当前音源未声明 My-LiveHub pic 扩展 action")};if err!=nil{return "",err}
 cover,ok:=value.(string);if !ok{return "",errors.New("LX pic action must return a string")}
 parsed,err:=url.Parse(strings.TrimSpace(cover));if err!=nil||parsed.Host==""||(parsed.Scheme!="http"&&parsed.Scheme!="https"){return "",errors.New("LX pic action returned an invalid HTTP(S) cover URL")}
 return parsed.String(),nil
}
