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
		meta, _ := musicInfo["meta"].(map[string]any)
		if musicInfo == nil || meta == nil || meta["songId"] == "" { t.Errorf("%s missing native LX song identity: %#v", call.source, call.info) }
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


func TestUnifiedSourceServicePreservesNativeProviderPlaybackMetadata(t *testing.T) {
	tests := []struct {
		name, source, songID, quality string
		sourceData map[string]string
		qualityData map[string]string
		wantMusicFields map[string]string
	}{
		{
			name: "Kuwo", source: "kw", songID: "kw-song-1", quality: "320k",
			sourceData: map[string]string{"songmid": "kw-song-1", "album_id": "kw-album-1"},
			wantMusicFields: map[string]string{"albumId": "kw-album-1"},
		},
		{
			name: "QQ", source: "tx", songID: "qq-song-1", quality: "320k",
			sourceData: map[string]string{"songmid": "qq-song-1", "media_mid": "qq-media-1", "album_mid": "qq-album-1"},
			qualityData: map[string]string{"file": "qq-file-320"},
			wantMusicFields: map[string]string{"strMediaMid": "qq-media-1", "albumMid": "qq-album-1"},
		},
		{
			name: "Kugou", source: "kg", songID: "kg-file-hash", quality: "flac",
			sourceData: map[string]string{"hash": "kg-file-hash"},
			qualityData: map[string]string{"hash": "kg-file-hash"},
			wantMusicFields: map[string]string{"hash": "kg-file-hash"},
		},
		{
			name: "Migu", source: "mg", songID: "mg-song-1", quality: "flac24bit",
			sourceData: map[string]string{"songmid": "mg-song-1", "copyright_id": "mg-copy-1"},
			qualityData: map[string]string{"copyrightId": "mg-copy-1"},
			wantMusicFields: map[string]string{"copyrightId": "mg-copy-1"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			track := Track{
				ID: tt.source + "_" + tt.songID, SourceID: tt.source, LXSourceKey: tt.source,
				Title: "Song", Artist: "Singer", SourceData: tt.sourceData,
				Qualities: []TrackQuality{{Type: tt.quality, SourceData: tt.qualityData}},
			}
			resolver := &unifiedTestResolver{url: "https://cdn.example.test/" + tt.source + ".mp3"}
			service := NewUnifiedSourceService(unifiedTestSearcher{}, resolver)
			if _, err := service.ResolvePlaybackURL(context.Background(), track, ""); err != nil {
				t.Fatal(err)
			}
			if len(resolver.calls) != 1 {
				t.Fatalf("resolver calls = %d, want 1", len(resolver.calls))
			}
			info := resolver.calls[0].info
			if info["type"] != tt.quality {
				t.Errorf("selected quality = %#v, want %q", info["type"], tt.quality)
			}
			musicInfo, ok := info["musicInfo"].(map[string]any)
			if !ok {
				t.Fatalf("musicInfo = %#v", info["musicInfo"])
			}
			meta, ok := musicInfo["meta"].(map[string]any)
			if !ok { t.Fatalf("musicInfo.meta = %#v", musicInfo["meta"]) }
			if meta["songId"] != tt.songID {
				t.Errorf("native song ID = %#v, want %q", meta["songId"], tt.songID)
			}
			for key, want := range tt.wantMusicFields {
				if meta[key] != want {
					t.Errorf("musicInfo.meta.%s = %#v, want %q", key, meta[key], want)
				}
			}
			qualitys, ok := meta["qualitys"].([]map[string]any)
			if !ok || len(qualitys) != 1 { t.Fatalf("quality metadata = %#v", meta["qualitys"]) }
			indexed, ok := meta["_qualitys"].(map[string]any)
			if !ok || indexed[tt.quality] == nil { t.Fatalf("indexed quality metadata = %#v", meta["_qualitys"]) }
			for key, want := range tt.qualityData {
				if qualitys[0][key] != want { t.Errorf("qualitys[0].%s = %#v, want %q", key, qualitys[0][key], want) }
			}
		})
	}
}
