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
	service:=NewMusicServiceWithDependencies(nil,config.DefaultMusicConfig(),&nextTrackTestEngine{})
	runtime:=&playbackResourceTestRuntime{}
	service.scriptRuntime=runtime
	track:=music.Track{ID:"tx_1",SourceID:"tx",LXSourceKey:"tx",Title:"Song",SourceData:map[string]string{"songmid":"1"},Qualities:[]music.TrackQuality{{Type:"320k"}}}
	first,err:=service.ResolvePlaybackResource(track,"")
	if err!=nil { t.Fatal(err) }
	second,err:=service.ResolvePlaybackResource(track,"")
	if err!=nil { t.Fatal(err) }
	if first.URL!=second.URL || runtime.calls!=1 { t.Fatalf("cache failed: first=%#v second=%#v calls=%d",first,second,runtime.calls) }
}


func TestMusicServiceMediaFailureRefreshIsIdempotent(t *testing.T) {
	service := NewMusicServiceWithDependencies(nil, config.DefaultMusicConfig(), &nextTrackTestEngine{})
	runtime := &playbackResourceTestRuntime{}
	service.scriptRuntime = runtime
	track := music.Track{ID: "tx_failure", SourceID: "tx", LXSourceKey: "tx", Title: "Song", SourceData: map[string]string{"songmid": "failure-song"}, Qualities: []music.TrackQuality{{Type: "320k"}}}
	failed, err := service.ResolvePlaybackResource(track, "320k")
	if err != nil { t.Fatal(err) }
	first, err := service.HandlePlaybackResourceFailure(track, failed)
	if err != nil { t.Fatal(err) }
	second, err := service.HandlePlaybackResourceFailure(track, failed)
	if err != nil { t.Fatal(err) }
	if runtime.calls != 2 { t.Fatalf("resolver calls=%d, want initial resolve + one refresh", runtime.calls) }
	if first.URL != second.URL || first.ResolvedAt != second.ResolvedAt {
		t.Fatalf("repeated media error did not reuse the same refresh result: first=%#v second=%#v", first, second)
	}
}

func TestMusicServiceMediaFailureRefreshRejectsMismatchedResource(t *testing.T) {
	service := NewMusicServiceWithDependencies(nil, config.DefaultMusicConfig(), &nextTrackTestEngine{})
	service.scriptRuntime = &playbackResourceTestRuntime{}
	track := music.Track{ID: "tx_expected", SourceID: "tx", LXSourceKey: "tx", Title: "Song"}
	failed := music.PlaybackResource{URL: "https://media.example.test/fail.mp3", SourceID: "kg", TrackID: track.ID, Quality: "320k"}
	if _, err := service.HandlePlaybackResourceFailure(track, failed); err == nil {
		t.Fatal("mismatched source resource was accepted for refresh")
	}
}
