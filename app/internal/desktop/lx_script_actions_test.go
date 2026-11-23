package desktop

import (
 "testing"
 "context"
 "errors"
 "github.com/dongguo4812/My-LiveHub/app/internal/music"
)

func TestNormalizeLXScriptSearchResultMapsCommunityTrackShape(t *testing.T){
 got,err:=normalizeLXScriptSearchResult("kw","fixture",1,[]any{map[string]any{"id":"native-1","name":"Fixture Song","singer":"Fixture Artist","album":"Fixture Album","interval":212,"pic":"https://cdn.example.test/cover.jpg"}})
 if err!=nil{t.Fatal(err)}
 if len(got.Tracks)!=1{t.Fatalf("tracks=%d",len(got.Tracks))}
 track:=got.Tracks[0]
 if track.ID!="kw_native-1"||track.SourceID!="kw"||track.LXSourceKey!="kw"||track.Title!="Fixture Song"||track.Artist!="Fixture Artist"||track.DurationMS!=212000||track.CoverURL!="https://cdn.example.test/cover.jpg"{t.Fatalf("unexpected mapped track: %#v",track)}
}
func TestNormalizeLXScriptSearchResultPreservesUnifiedTrack(t *testing.T){
 got,err:=normalizeLXScriptSearchResult("kg","fixture",2,map[string]any{"tracks":[]music.Track{{ID:"track-2",SourceID:"kg",Title:"Song"}},"total":61,"page":2,"page_size":30,"max_page":3})
 if err!=nil{t.Fatal(err)}
 if len(got.Tracks)!=1||got.Page!=2||got.Total!=61||got.MaxPage!=3{t.Fatalf("unexpected page: %#v",got)}
}

type fakeLXActionRuntime struct { available bool; value any; err error }
func (f fakeLXActionRuntime) Available() bool { return f.available }
func (f fakeLXActionRuntime) Start(context.Context) error { return nil }
func (f fakeLXActionRuntime) ResolvePlaybackURL(context.Context,string,any)(string,error) { return "",nil }
func (f fakeLXActionRuntime) Close() error { return nil }
func (f fakeLXActionRuntime) HasExtensionAction(string, string) bool { return f.available }
func (f fakeLXActionRuntime) DispatchExtensionAction(context.Context,string,string,any)(any,error) { return f.value,f.err }

func TestDispatchLXExtensionClassifiesTimeout(t *testing.T) {
 service:=&MusicService{scriptRuntime:fakeLXActionRuntime{available:true,err:context.DeadlineExceeded}}
 _,handled,err:=service.dispatchLXExtension(context.Background(),"kw","search",map[string]any{"keyword":"x"})
 var actionErr *LXScriptActionError
 if !handled||!errors.As(err,&actionErr)||actionErr.Kind!="timeout"||!errors.Is(err,context.DeadlineExceeded){t.Fatalf("timeout classification = %#v, handled=%v, err=%v",actionErr,handled,err)}
}
func TestDispatchLXExtensionClassifiesCancellation(t *testing.T) {
 service:=&MusicService{scriptRuntime:fakeLXActionRuntime{available:true,err:context.Canceled}}
 _,handled,err:=service.dispatchLXExtension(context.Background(),"kg","lyric",nil)
 var actionErr *LXScriptActionError
 if !handled||!errors.As(err,&actionErr)||actionErr.Kind!="cancelled"||!errors.Is(err,context.Canceled){t.Fatalf("cancel classification = %#v, handled=%v, err=%v",actionErr,handled,err)}
}
