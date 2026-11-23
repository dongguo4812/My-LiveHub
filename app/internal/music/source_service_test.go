package music

import (
	"context"
	"errors"
	"testing"
)

type unifiedTestSearcher struct {
	tracks map[string]Track
}

func (s unifiedTestSearcher) Sources() []SourceSnapshot {
	return []SourceSnapshot{
		{ID: "tx", Name: "QQ", Capabilities: SourceCapabilities{Search: true}},
		{ID: "kg", Name: "Kugou", Capabilities: SourceCapabilities{Search: true}},
		{ID: "mg", Name: "Migu", Capabilities: SourceCapabilities{Search: true}},
	}
}
func (s unifiedTestSearcher) SearchSources(_ context.Context, keyword string, page int, sourceIDs []string) (SearchPage, map[string]string, error) {
	tracks := make([]Track, 0, len(sourceIDs))
	for _, id := range sourceIDs {
		if track, ok := s.tracks[id]; ok { tracks = append(tracks, track) }
	}
	return SearchPage{Tracks: tracks, Page: page, PageSize: 30, MaxPage: 1}, map[string]string{}, nil
}
func (s unifiedTestSearcher) SearchProvider(_ context.Context, source, _ string, page int) (SearchPage, error) {
	track, ok := s.tracks[source]
	if !ok { return SearchPage{}, errors.New("unknown source") }
	return SearchPage{Tracks: []Track{track}, Page: page, PageSize: 30, MaxPage: 1}, nil
}

type unifiedTestResolver struct {
	calls []struct { source string; info map[string]any }
	url string
	err error
}
func (r *unifiedTestResolver) ResolvePlaybackURL(_ context.Context, source string, raw any) (string, error) {
	info, _ := raw.(map[string]any)
	r.calls = append(r.calls, struct { source string; info map[string]any }{source, info})
	return r.url, r.err
}

func TestUnifiedSourceServiceSearchAndResolveAcrossProviders(t *testing.T) {
	tracks := map[string]Track{
		"tx": {ID:"tx_101", SourceID:"tx", LXSourceKey:"tx", Title:"Song", Artist:"Singer", SelectedQuality:"128k", SourceData:map[string]string{"songmid":"101","media_mid":"MEDIA101"}, Qualities:[]TrackQuality{{Type:"320k",Size:"5 MiB"}}},
		"kg": {ID:"HASH202", SourceID:"kg", LXSourceKey:"kg", Title:"Song", Artist:"Singer", SourceData:map[string]string{"hash":"HASH202"}, Qualities:[]TrackQuality{{Type:"flac",Size:"8 MiB"}}},
		"mg": {ID:"mg_303", SourceID:"mg", LXSourceKey:"mg", Title:"Song", Artist:"Singer", SourceData:map[string]string{"songmid":"303","copyright_id":"COPY303"}, Qualities:[]TrackQuality{{Type:"flac24bit",Size:"12 MiB"}}},
	}
	resolver := &unifiedTestResolver{url:"https://cdn.example.test/song.mp3"}
	service := NewUnifiedSourceService(unifiedTestSearcher{tracks:tracks}, resolver)
	page, sourceErrors, err := service.Search(context.Background(), "Song", 1, []string{"tx","kg","mg"})
	if err != nil { t.Fatal(err) }
	if len(sourceErrors) != 0 || len(page.Tracks) != 3 { t.Fatalf("search result = %#v, errors = %#v", page, sourceErrors) }
	for _, track := range page.Tracks {
		got, err := service.ResolvePlaybackURL(context.Background(), track, "")
		if err != nil { t.Fatalf("resolve %s: %v", track.SourceID, err) }
		if got != resolver.url { t.Fatalf("%s URL = %q", track.SourceID, got) }
	}
	if len(resolver.calls) != 3 { t.Fatalf("resolver calls = %d, want 3", len(resolver.calls)) }
	for _, call := range resolver.calls {
		if call.info["type"] == nil { t.Errorf("%s missing selected quality", call.source) }
		if call.source == "tx" && call.info["type"] != "128k" { t.Errorf("selected track quality = %v, want 128k", call.info["type"]) }
		musicInfo, _ := call.info["musicInfo"].(map[string]any)
		if musicInfo == nil || musicInfo["songmid"] == "" { t.Errorf("%s missing native song identity: %#v", call.source, call.info) }
	}
}

func TestUnifiedSourceServiceRejectsInvalidPlaybackURL(t *testing.T) {
	track := Track{ID:"tx_1",SourceID:"tx",Title:"Song",SourceData:map[string]string{"songmid":"1"}}
	for _, raw := range []string{"javascript:alert(1)", "https:///missing-host", "not a url"} {
		resolver := &unifiedTestResolver{url:raw}
		service := NewUnifiedSourceService(unifiedTestSearcher{}, resolver)
		if _, err := service.ResolvePlaybackURL(context.Background(), track, "320k"); err == nil {
			t.Errorf("invalid URL %q was accepted", raw)
		}
	}
}

func TestUnifiedSourceServiceRequiresNativeSongIdentity(t *testing.T) {
	service := NewUnifiedSourceService(unifiedTestSearcher{}, &unifiedTestResolver{url:"https://cdn.example.test/song.mp3"})
	_, err := service.ResolvePlaybackURL(context.Background(), Track{ID:"tx_",SourceID:"tx",Title:"Song"}, "320k")
	if err == nil { t.Fatal("expected missing native identity to fail") }
}
