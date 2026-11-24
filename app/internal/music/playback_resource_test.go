package music

import (
	"context"
	"errors"
	"strings"
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

func TestPlaybackResourceCacheOverwriteDoesNotEvictOtherEntries(t *testing.T) {
	cache := NewPlaybackResourceCache(time.Minute, 2)
	trackA := Track{ID: "a", SourceID: "tx"}
	trackB := Track{ID: "b", SourceID: "tx"}
	resourceA := PlaybackResource{URL: "https://media.example.test/a.mp3", SourceID: "tx", TrackID: "a", Quality: "320k"}
	resourceB := PlaybackResource{URL: "https://media.example.test/b.mp3", SourceID: "tx", TrackID: "b", Quality: "320k"}
	cache.Put(trackA, "320k", resourceA)
	cache.Put(trackB, "320k", resourceB)
	resourceA.URL = "https://media.example.test/a-refresh.mp3"
	cache.Put(trackA, "320k", resourceA)
	if got, ok := cache.Get(trackB, "320k"); !ok || got.URL != resourceB.URL {
		t.Fatalf("overwriting an existing key evicted another entry: %#v, %v", got, ok)
	}
	if got, ok := cache.Get(trackA, "320k"); !ok || got.URL != resourceA.URL {
		t.Fatalf("replacement was not stored: %#v, %v", got, ok)
	}
}

func TestPlaybackResourceRejectsCredentialsFragmentsAndOversizedHeaders(t *testing.T) {
	base := PlaybackResource{URL: "https://media.example.test/a.mp3", SourceID: "tx", TrackID: "1"}
	base.URL = "https://user:secret@media.example.test/a.mp3"
	if err := validatePlaybackResource(base); err == nil { t.Fatal("credential-bearing URL accepted") }
	base.URL = "https://media.example.test/a.mp3#fragment"
	if err := validatePlaybackResource(base); err == nil { t.Fatal("fragment-bearing URL accepted") }
	base.URL = "https://media.example.test/a.mp3"
	base.Headers = map[string]string{"Bad Header": "value"}
	if err := validatePlaybackResource(base); err == nil { t.Fatal("invalid header name accepted") }
	base.Headers = map[string]string{"X-Large": strings.Repeat("x", (64<<10)+1)}
	if err := validatePlaybackResource(base); err == nil { t.Fatal("oversized headers accepted") }
}


func TestResolvePlaybackResourceQualityPrecedenceAndDeduplication(t *testing.T) {
	track := Track{
		ID: "song-order", SourceID: "tx", LXSourceKey: "tx",
		SourceData: map[string]string{"songmid": "native-song-order"},
		Title: "Song", SelectedQuality: "320k",
		Qualities: []TrackQuality{{Type: "320k"}, {Type: "128k"}, {Type: "128k"}, {Type: "flac"}},
	}
	resolver := &qualityResolver{failures: map[string]bool{"flac": true}}
	service := NewUnifiedSourceService(nil, resolver)
	resource, err := service.ResolvePlaybackResource(context.Background(), track, "flac", NewPlaybackResourceCache(time.Minute, 8))
	if err != nil { t.Fatal(err) }
	if resource.Quality != "320k" { t.Fatalf("quality=%q, want selected fallback 320k", resource.Quality) }
	want := []string{"flac", "320k"}
	if len(resolver.calls) != len(want) { t.Fatalf("resolver calls=%v, want %v", resolver.calls, want) }
	for i := range want { if resolver.calls[i] != want[i] { t.Fatalf("resolver calls=%v, want %v", resolver.calls, want) } }
}

func TestPlaybackResourceCacheHonorsSourceExpiryAndCapacity(t *testing.T) {
	now := time.Date(2026, 11, 23, 12, 0, 0, 0, time.UTC)
	cache := NewPlaybackResourceCache(time.Hour, 2)
	cache.now = func() time.Time { return now }
	a := Track{ID: "a", SourceID: "tx"}
	b := Track{ID: "b", SourceID: "tx"}
	c := Track{ID: "c", SourceID: "tx"}
	d := Track{ID: "d", SourceID: "tx"}
	makeResource := func(id string, expiry time.Time) PlaybackResource {
		return PlaybackResource{URL: "https://media.example.test/" + id + ".mp3", SourceID: "tx", TrackID: id, Quality: "320k", ExpiresAt: expiry}
	}
	cache.Put(a, "320k", makeResource("a", now.Add(10*time.Second)))
	cache.Put(b, "320k", makeResource("b", now.Add(30*time.Second)))
	if _, ok := cache.Get(a, "320k"); !ok { t.Fatal("source resource should be available before source expiry") }
	now = now.Add(11*time.Second)
	if _, ok := cache.Get(a, "320k"); ok { t.Fatal("resource was served after source expiry") }
	cache.Put(c, "320k", makeResource("c", now.Add(2*time.Minute)))
	cache.Put(d, "320k", makeResource("d", now.Add(3*time.Minute)))
	if _, ok := cache.Get(b, "320k"); ok { t.Fatal("capacity eviction retained the earliest-expiring entry") }
	if _, ok := cache.Get(c, "320k"); !ok { t.Fatal("later-expiring entry was evicted unexpectedly") }
	if _, ok := cache.Get(d, "320k"); !ok { t.Fatal("new entry missing after bounded eviction") }
}

func TestRefreshPlaybackResourceResolvesOnlyOnceAfterInvalidation(t *testing.T) {
	track := Track{ID: "refresh-song", SourceID: "tx", LXSourceKey: "tx", SourceData: map[string]string{"songmid": "native-refresh-song"}, Title: "Song", Qualities: []TrackQuality{{Type: "320k"}}}
	resolver := &qualityResolver{}
	service := NewUnifiedSourceService(nil, resolver)
	cache := NewPlaybackResourceCache(time.Minute, 4)
	first, err := service.ResolvePlaybackResource(context.Background(), track, "320k", cache)
	if err != nil { t.Fatal(err) }
	refreshed, err := service.RefreshPlaybackResource(context.Background(), track, first, cache)
	if err != nil { t.Fatal(err) }
	if len(resolver.calls) != 2 { t.Fatalf("resolver called %d times, want initial + exactly one refresh", len(resolver.calls)) }
	if refreshed.URL != first.URL { t.Fatalf("unexpected fixture URL change: %q != %q", refreshed.URL, first.URL) }
}


func TestPlaybackResourceCacheRejectsIdentityMismatch(t *testing.T) {
	cache := NewPlaybackResourceCache(time.Minute, 4)
	track := Track{ID: "expected-track", SourceID: "tx", LXSourceKey: "tx"}
	cases := []struct {
		name string
		resource PlaybackResource
		quality string
	}{
		{name: "source", quality: "320k", resource: PlaybackResource{URL: "https://media.example.test/a.mp3", SourceID: "kg", TrackID: "expected-track", Quality: "320k"}},
		{name: "track", quality: "320k", resource: PlaybackResource{URL: "https://media.example.test/a.mp3", SourceID: "tx", TrackID: "other-track", Quality: "320k"}},
		{name: "quality", quality: "320k", resource: PlaybackResource{URL: "https://media.example.test/a.mp3", SourceID: "tx", TrackID: "expected-track", Quality: "128k"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cache.Put(track, tc.quality, tc.resource)
			if _, ok := cache.Get(track, tc.quality); ok { t.Fatal("mismatched playback resource was cached") }
		})
	}
}


func TestPlaybackResourceCacheClonesMediaHeadersOnWriteAndRead(t *testing.T) {
	cache := NewPlaybackResourceCache(time.Minute, 4)
	track := Track{ID: "header-clone", SourceID: "tx"}
	resource := PlaybackResource{
		URL: "https://media.example.test/header-clone.mp3", SourceID: "tx",
		TrackID: track.ID, Quality: "320k", Headers: map[string]string{"Referer": "https://source.example/"},
	}
	cache.Put(track, "320k", resource)
	resource.Headers["Referer"] = "https://mutated-before-read.example/"
	first, ok := cache.Get(track, "320k")
	if !ok { t.Fatal("expected cached resource") }
	if first.Headers["Referer"] != "https://source.example/" { t.Fatalf("cache retained caller's mutable header map: %#v", first.Headers) }
	first.Headers["Referer"] = "https://mutated-after-read.example/"
	second, ok := cache.Get(track, "320k")
	if !ok { t.Fatal("expected cached resource after read mutation") }
	if second.Headers["Referer"] != "https://source.example/" { t.Fatalf("read result mutated cached headers: %#v", second.Headers) }
}


func TestPlaybackResourceCacheEvictsLexicallyFirstEntryWhenExpiryTies(t *testing.T) {
	cache := NewPlaybackResourceCache(time.Minute, 2)
	now := time.Date(2026, 11, 23, 12, 0, 0, 0, time.UTC)
	cache.now = func() time.Time { return now }
	a := Track{ID: "a", SourceID: "tx"}
	b := Track{ID: "b", SourceID: "tx"}
	c := Track{ID: "c", SourceID: "tx"}
	put := func(track Track) {
		cache.Put(track, "320k", PlaybackResource{
			URL: "https://media.example.test/" + track.ID + ".mp3",
			SourceID: "tx", TrackID: track.ID, Quality: "320k",
		})
	}
	put(b)
	put(a)
	put(c)
	if _, ok := cache.Get(a, "320k"); ok { t.Fatal("lexically first equal-expiry entry should be evicted") }
	if _, ok := cache.Get(b, "320k"); !ok { t.Fatal("other equal-expiry entry should remain") }
	if _, ok := cache.Get(c, "320k"); !ok { t.Fatal("new cache entry should remain") }
}


func TestURLOnlyPlaybackResolverDoesNotInventMediaRequestContext(t *testing.T) {
	track := Track{
		ID: "url-only-song", SourceID: "tx", LXSourceKey: "tx",
		SourceData: map[string]string{"songmid": "url-only-native-id"},
		Title: "URL Only", Qualities: []TrackQuality{{Type: "320k"}},
	}
	service := NewUnifiedSourceService(nil, &qualityResolver{})
	resource, err := service.ResolvePlaybackResource(context.Background(), track, "", NewPlaybackResourceCache(time.Minute, 4))
	if err != nil { t.Fatal(err) }
	if len(resource.Headers) != 0 || resource.MIMEType != "" || resource.Codec != "" ||
		!resource.ExpiresAt.IsZero() || resource.RequiresProxy {
		t.Fatalf("URL-only native musicUrl protocol invented media context: %#v", resource)
	}
}

func TestResolvePlaybackResourceAutomaticallyPrefersHighestKnownQuality(t *testing.T) {
	track := Track{ID:"auto", SourceID:"tx", LXSourceKey:"tx", SourceData:map[string]string{"songmid":"native-auto"}, Title:"Song",
		Qualities:[]TrackQuality{{Type:"128k"},{Type:"320k"},{Type:"flac"},{Type:"hires"}}}
	resolver := &qualityResolver{failures:map[string]bool{"hires":true}}
	service := NewUnifiedSourceService(nil,resolver)
	resource, err := service.ResolvePlaybackResource(context.Background(),track,"",NewPlaybackResourceCache(time.Minute,8))
	if err != nil { t.Fatal(err) }
	if resource.Quality != "flac" { t.Fatalf("quality=%q, want flac after hires fallback",resource.Quality) }
	want := []string{"hires","flac"}
	if len(resolver.calls)!=len(want) { t.Fatalf("calls=%v, want %v",resolver.calls,want) }
	for i := range want { if resolver.calls[i]!=want[i] { t.Fatalf("calls=%v, want %v",resolver.calls,want) } }
}
func TestExplicitQualityRemainsHigherPriorityThanAutomaticRanking(t *testing.T) {
	track := Track{ID:"explicit", SourceID:"tx", LXSourceKey:"tx", SourceData:map[string]string{"songmid":"native-explicit"}, Title:"Song",
		SelectedQuality:"320k", Qualities:[]TrackQuality{{Type:"128k"},{Type:"hires"},{Type:"flac"}}}
	resolver := &qualityResolver{}
	service := NewUnifiedSourceService(nil,resolver)
	resource, err := service.ResolvePlaybackResource(context.Background(),track,"",NewPlaybackResourceCache(time.Minute,8))
	if err != nil { t.Fatal(err) }
	if resource.Quality!="320k" { t.Fatalf("quality=%q, want explicit 320k",resource.Quality) }
	if len(resolver.calls)!=1 || resolver.calls[0]!="320k" { t.Fatalf("calls=%v, want [320k]",resolver.calls) }
}

type staticMediaURLResolver struct {
	url string
	calls []string
}
func (r *staticMediaURLResolver) ResolvePlaybackURL(_ context.Context, _ string, raw any) (string, error) {
	info := raw.(map[string]any)
	r.calls = append(r.calls, info["type"].(string))
	return r.url, nil
}
func TestMiguURLQualityDowngradeIsDetectedAndFallsBackTruthfully(t *testing.T) {
	track := Track{
		ID: "migu-daoxiang", SourceID: "mg", LXSourceKey: "mg",
		SourceData: map[string]string{"songmid": "60054702010165016", "copyright_id": "COPY"},
		Title: "稻香", Artist: "周杰伦",
		Qualities: []TrackQuality{{Type: "flac"}, {Type: "128k"}},
	}
	resolver := &staticMediaURLResolver{
		url: "https://freetyst.nf.migu.cn/public/product/MP3_128_16_Stero/60054702010165016.mp3",
	}
	service := NewUnifiedSourceService(nil, resolver)
	resource, err := service.ResolvePlaybackResource(context.Background(), track, "flac", NewPlaybackResourceCache(time.Minute, 8))
	if err != nil { t.Fatal(err) }
	if resource.Quality != "128k" { t.Fatalf("resolved quality=%q, want truthful 128k fallback", resource.Quality) }
	want := []string{"flac", "128k"}
	if len(resolver.calls) != len(want) { t.Fatalf("resolver calls=%v, want %v", resolver.calls, want) }
	for i := range want { if resolver.calls[i] != want[i] { t.Fatalf("resolver calls=%v, want %v", resolver.calls, want) } }
}
func TestMiguURLQualityDetectionDoesNotGuessOpaqueURLs(t *testing.T) {
	if got := detectPlaybackURLQuality("mg", "https://cdn.example.test/audio?id=1"); got != "" {
		t.Fatalf("opaque media URL was guessed as %q", got)
	}
	if got := detectPlaybackURLQuality("kg", "https://cdn.example.test/MP3_128_16_Stero/song.mp3"); got != "" {
		t.Fatalf("non-Migu source URL was incorrectly classified as %q", got)
	}
}

func TestPlaybackQualityRankMatchesXinghaiFallbackOrder(t *testing.T) {
	ordered := []string{"jymaster", "sky", "jyeffect", "hires", "flac24bit", "spatial", "master", "flac", "320k", "192k", "128k"}
	for i := 0; i < len(ordered)-1; i++ {
		if playbackQualityRank(ordered[i]) <= playbackQualityRank(ordered[i+1]) {
			t.Fatalf("quality rank order mismatch: %s=%d must outrank %s=%d",
				ordered[i], playbackQualityRank(ordered[i]), ordered[i+1], playbackQualityRank(ordered[i+1]))
		}
	}
	if playbackQualityRank("atmos") != playbackQualityRank("master") ||
		playbackQualityRank("atmos_plus") != playbackQualityRank("master") {
		t.Fatal("Atmos aliases must share Xinghai's master quality tier")
	}
}
