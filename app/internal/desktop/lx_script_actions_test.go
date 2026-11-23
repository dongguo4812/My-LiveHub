package desktop

import (
 "testing"
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
