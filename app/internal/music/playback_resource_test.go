package music

import (
	"context"
	"errors"
	"testing"
	"time"
)

type qualityResolver struct { calls []string; failures map[string]bool }
func (r *qualityResolver) ResolvePlaybackURL(_ context.Context, _ string, raw any) (string, error) {
	info := raw.(map[string]any)
	quality := info["type"].(string)
	r.calls = append(r.calls, quality)
	if r.failures[quality] { return "", errors.New("quality unavailable") }
	return "https://media.example.test/"+quality+".mp3", nil
}

func TestResolvePlaybackResourceFallsBackAcrossQualities(t *testing.T) {
	track := Track{ID:"song-1",SourceID:"tx",LXSourceKey:"tx",SourceData:map[string]string{"songmid":"native-song-1"},Title:"Song",Qualities:[]TrackQuality{{Type:"flac"},{Type:"320k"},{Type:"128k"}}}
	resolver := &qualityResolver{failures:map[string]bool{"flac":true,"320k":true}}
	service := NewUnifiedSourceService(nil,resolver)
	resource, err := service.ResolvePlaybackResource(context.Background(),track,"",NewPlaybackResourceCache(time.Minute,8))
	if err != nil { t.Fatal(err) }
	if resource.Quality!="128k" { t.Fatalf("quality=%q, want 128k",resource.Quality) }
	if len(resolver.calls)!=3 { t.Fatalf("resolver calls=%v",resolver.calls) }
}

func TestPlaybackResourceCacheHitExpiryAndInvalidation(t *testing.T) {
	cache:=NewPlaybackResourceCache(time.Minute,4)
	now:=time.Now()
	cache.now=func()time.Time{return now}
	track:=Track{ID:"1",SourceID:"tx"}
	resource:=PlaybackResource{URL:"https://media.example.test/a.mp3",SourceID:"tx",TrackID:"1",Quality:"320k"}
	cache.Put(track,"320k",resource)
	if _,ok:=cache.Get(track,"320k");!ok { t.Fatal("expected cache hit") }
	now=now.Add(2*time.Minute)
	if _,ok:=cache.Get(track,"320k");ok { t.Fatal("expired resource was served") }
	cache.Put(track,"320k",resource)
	cache.Invalidate(track,"320k")
	if _,ok:=cache.Get(track,"320k");ok { t.Fatal("invalidated resource was served") }
}

func TestPlaybackResourceRejectsUnsafeURLAndHeaders(t *testing.T) {
	base:=PlaybackResource{URL:"https://media.example.test/a.mp3",SourceID:"tx",TrackID:"1"}
	if err:=validatePlaybackResource(base);err!=nil { t.Fatal(err) }
	base.URL="javascript:alert(1)"
	if err:=validatePlaybackResource(base);err==nil { t.Fatal("javascript URL accepted") }
	base.URL="https://media.example.test/a.mp3"
	base.Headers=map[string]string{"Referer":"https://ok.example/\r\nInjected: yes"}
	if err:=validatePlaybackResource(base);err==nil { t.Fatal("CRLF header accepted") }
}
