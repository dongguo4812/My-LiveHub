package desktop

import (
	"context"
	"testing"

	"github.com/dongguo4812/My-LiveHub/app/internal/music"
	"github.com/dongguo4812/My-LiveHub/app/internal/config"
)

func TestSameLXTrackIdentityUsesOnlyTitleAndArtist(t *testing.T) {
	original := music.Track{Title: "稻香", Artist: "周杰伦", Album: "魔杰座", DurationMS: 223000, SourceID: "tx"}
	candidate := music.Track{Title: " 稻香 ", Artist: "周杰伦", Album: "其他专辑", DurationMS: 180000, SourceID: "kw"}
	if !sameLXTrackIdentity(original, candidate) {
		t.Fatal("tracks with matching title and artist should match despite album, duration, and source differences")
	}
}

func TestSameLXTrackIdentityNormalizesPunctuationAndWhitespace(t *testing.T) {
	a := music.Track{Title: "稻香（Live）", Artist: "周杰伦、方文山"}
	b := music.Track{Title: "稻香 Live", Artist: "周杰伦 方文山"}
	if !sameLXTrackIdentity(a, b) {
		t.Fatal("track identity should ignore punctuation and whitespace")
	}
}

func TestSameLXTrackIdentityRequiresBothTitleAndArtist(t *testing.T) {
	original := music.Track{Title: "稻香", Artist: "周杰伦"}
	wrongTitle := music.Track{Title: "晴天", Artist: "周杰伦"}
	wrongArtist := music.Track{Title: "稻香", Artist: "林俊杰"}
	if sameLXTrackIdentity(original, wrongTitle) {
		t.Fatal("different titles must not match")
	}
	if sameLXTrackIdentity(original, wrongArtist) {
		t.Fatal("different artists must not match")
	}
}


type resourceAwareQueueTestEngine struct {
	*nextTrackTestEngine
	loadedTrack music.Track
	resource music.PlaybackResource
	loadResourceCalls int
}
func (e *resourceAwareQueueTestEngine) LoadResource(_ context.Context, track music.Track, resource music.PlaybackResource) error {
	e.loadedTrack = track
	e.resource = resource
	e.loadResourceCalls++
	return nil
}

func TestQueuePlaybackPassesStructuredResourceToResourceAwareEngine(t *testing.T) {
	engine := &resourceAwareQueueTestEngine{nextTrackTestEngine: &nextTrackTestEngine{}}
	service := NewMusicServiceWithDependencies(nil, config.DefaultMusicConfig(), engine)
	runtime := &playbackResourceTestRuntime{}
	service.scriptRuntime = runtime
	track := music.Track{
		ID: "tx_queue_resource", SourceID: "tx", LXSourceKey: "tx",
		Title: "Queue Resource", Artist: "Test Artist",
		SourceData: map[string]string{"songmid": "queue-resource-song"},
		Qualities: []music.TrackQuality{{Type: "320k"}},
	}
	loaded, err := service.resolveAndStartLXTrack(context.Background(), track, false)
	if err != nil { t.Fatal(err) }
	if engine.loadResourceCalls != 1 {
		t.Fatalf("LoadResource calls=%d, want 1", engine.loadResourceCalls)
	}
	if engine.loadedTrack.ID != track.ID || engine.resource.URL == "" || loaded.PlaybackResource == nil {
		t.Fatalf("structured resource was not passed through queue playback: loaded=%#v resource=%#v", engine.loadedTrack, engine.resource)
	}
	if runtime.calls != 1 { t.Fatalf("URL resolver calls=%d, want 1", runtime.calls) }
}
