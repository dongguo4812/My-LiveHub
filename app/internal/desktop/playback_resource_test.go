package desktop

import (
	"context"
	"testing"

	"github.com/dongguo4812/My-LiveHub/app/internal/config"
	"github.com/dongguo4812/My-LiveHub/app/internal/music"
)

type playbackResourceTestRuntime struct { calls int }
func (r *playbackResourceTestRuntime) Start(context.Context) error { return nil }
func (r *playbackResourceTestRuntime) Close() error { return nil }
func (r *playbackResourceTestRuntime) Available() bool { return true }
func (r *playbackResourceTestRuntime) ResolvePlaybackURL(_ context.Context, source string, info any) (string,error) {
	r.calls++
	return "https://media.example.test/"+source+".mp3",nil
}

func TestMusicServiceResolvePlaybackResourceUsesSharedCache(t *testing.T) {
	service:=NewMusicServiceWithDependencies(nil,config.DefaultMusicConfig(),nil,&nextTrackTestEngine{})
	runtime:=&playbackResourceTestRuntime{}
	service.scriptRuntime=runtime
	track:=music.Track{ID:"tx_1",SourceID:"tx",LXSourceKey:"tx",Title:"Song",SourceData:map[string]string{"songmid":"1"},Qualities:[]music.TrackQuality{{Type:"320k"}}}
	first,err:=service.ResolvePlaybackResource(track,"")
	if err!=nil { t.Fatal(err) }
	second,err:=service.ResolvePlaybackResource(track,"")
	if err!=nil { t.Fatal(err) }
	if first.URL!=second.URL || runtime.calls!=1 { t.Fatalf("cache failed: first=%#v second=%#v calls=%d",first,second,runtime.calls) }
}
