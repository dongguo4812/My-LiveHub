package desktop

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/dongguo4812/My-LiveHub/app/internal/music"
)

func TestBuildLXPlayURIContainsMatchedSongMetadata(t *testing.T) {
	track := music.Track{
		ID: "tx_123", SourceID: "tx", Title: "稻香", Artist: "周杰伦",
		Album: "魔杰座", DurationMS: 223000, CoverURL: "https://example.test/cover.jpg",
		SourceData: map[string]string{"songmid":"native-123", "media_mid":"media-123", "album_mid":"album-123"},
		Qualities: []music.TrackQuality{{Type:"320k", Size:"8 MiB"}},
	}
	uri, err := buildLXPlayURI(track)
	if err != nil { t.Fatal(err) }
	parsed, err := url.Parse(uri)
	if err != nil { t.Fatal(err) }
	if parsed.Scheme != "lxmusic" || parsed.Host != "music" || parsed.Path != "/play" {
		t.Fatalf("unexpected scheme URI: %s", uri)
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(parsed.Query().Get("data")), &payload); err != nil { t.Fatal(err) }
	for key, want := range map[string]string{"name":"稻香","singer":"周杰伦","source":"tx","songmid":"native-123","strMediaMid":"media-123","albumMid":"album-123"} {
		if payload[key] != want { t.Errorf("%s = %#v, want %q", key, payload[key], want) }
	}
	types, ok := payload["types"].([]any)
	if !ok || len(types) != 1 { t.Fatalf("unexpected types payload: %#v", payload["types"]) }
}

func TestBuildLXPlayURIRejectsIncompleteTrack(t *testing.T) {
	_, err := buildLXPlayURI(music.Track{ID:"kw_1", SourceID:"kw", Title:"Song"})
	if err == nil { t.Fatal("expected incomplete song identity to be rejected") }
}

func TestParseLXPlaybackSnapshot(t *testing.T) {
	value := map[string]any{
		"status":"playing", "name":"稻香", "singer":"周杰伦",
		"progress":12.5, "duration":223, "picUrl":"https://example.test/cover.jpg",
	}
	got := parseLXPlaybackSnapshot(value)
	if got.State != music.PlaybackPlaying { t.Fatalf("state = %q", got.State) }
	if got.Track == nil || got.Track.Title != "稻香" || got.Track.Artist != "周杰伦" { t.Fatalf("track = %#v", got.Track) }
	if got.PositionMS != 12500 || got.DurationMS != 223000 { t.Fatalf("timing = %d/%d", got.PositionMS, got.DurationMS) }
	if got.Track.CoverURL == "" { t.Fatal("cover URL was not parsed") }
}

func TestLXPlaybackEngineFallsBackAcrossStatusPaths(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.Query().Get("filter"), "name") { t.Errorf("status filter missing: %s", r.URL.RawQuery) }
		if r.URL.Path == "/status" { http.NotFound(w, r); return }
		if r.URL.Path == "/api/status" {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"status":"playing","name":"晴天","singer":"周杰伦"}`))
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()
	engine := NewLXPlaybackEngine([]string{server.URL})
	got, err := engine.Snapshot(context.Background())
	if err != nil { t.Fatal(err) }
	if got.State != music.PlaybackPlaying || got.Track == nil || got.Track.Title != "晴天" { t.Fatalf("unexpected snapshot: %#v", got) }
}


func TestLXPlaybackEngineFallsBackToNextConfiguredAddress(t *testing.T) {
	first := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "unavailable", http.StatusServiceUnavailable)
	}))
	defer first.Close()
	second := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/status" && r.URL.Path != "/api/status" { http.NotFound(w, r); return }
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"playing","name":"稻香","singer":"周杰伦"}`))
	}))
	defer second.Close()
	engine := NewLXPlaybackEngine([]string{first.URL, second.URL})
	got, err := engine.Snapshot(context.Background())
	if err != nil { t.Fatal(err) }
	if got.State != music.PlaybackPlaying || got.Track == nil || got.Track.Title != "稻香" {
		t.Fatalf("fallback endpoint returned unexpected status: %#v", got)
	}
}

func TestLXPlaybackEngineUsesDocumentedNextTrackEndpoint(t *testing.T) {
	var called string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = r.URL.Path
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	engine := NewLXPlaybackEngine([]string{server.URL})
	if err := engine.NextTrack(context.Background()); err != nil { t.Fatal(err) }
	if called != "/skip-next" { t.Fatalf("next endpoint = %q, want /skip-next", called) }
}

func TestLXPlaybackEngineUsesDocumentedPauseAndResumeEndpoints(t *testing.T) {
	var calls []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls = append(calls, r.URL.Path)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	engine := NewLXPlaybackEngine([]string{server.URL})
	if err := engine.Pause(context.Background()); err != nil { t.Fatal(err) }
	if err := engine.Resume(context.Background()); err != nil { t.Fatal(err) }
	if len(calls) != 2 || calls[0] != "/pause" || calls[1] != "/play" {
		t.Fatalf("unexpected control API calls: %#v", calls)
	}
}

func TestLXPlaybackEngineReadsOriginalAndTranslatedLyrics(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/lyric-all" { http.NotFound(w, r); return }
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"lyric":"[00:01.00]原文","tlyric":"[00:01.00]翻译","rlyric":"","lxlyric":""}`))
	}))
	defer server.Close()
	engine := NewLXPlaybackEngine([]string{server.URL})
	got, err := engine.GetCurrentLyrics(context.Background())
	if err != nil { t.Fatal(err) }
	if !strings.Contains(got, "[00:01.00]原文") || !strings.Contains(got, "[00:01.00]翻译") {
		t.Fatalf("lyrics did not preserve original and translation: %q", got)
	}
}

func TestLXPlaybackEngineFallsBackToPlainLyrics(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/lyric-all" { http.NotFound(w, r); return }
		if r.URL.Path == "/lyric" { _, _ = w.Write([]byte("[00:02.00]普通歌词")); return }
		http.NotFound(w, r)
	}))
	defer server.Close()
	engine := NewLXPlaybackEngine([]string{server.URL})
	got, err := engine.GetCurrentLyrics(context.Background())
	if err != nil { t.Fatal(err) }
	if got != "[00:02.00]普通歌词" { t.Fatalf("plain lyric fallback = %q", got) }
}


func TestLXPlaybackEngineStartDoesNotProbeOrOpenLocalApp(t *testing.T) {
	engine := NewLXPlaybackEngine(nil)
	opened := false
	engine.openApp = func() error {
		opened = true
		return nil
	}
	if err := engine.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	if opened {
		t.Fatal("opening the music module must not launch 洛雪")
	}
}

func TestLXPlaybackEngineEnsuresAPIOnlyWhenPlaybackRequested(t *testing.T) {
	var opened atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !opened.Load() {
			http.Error(w, "not ready", http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"playing","name":"稻香","singer":"周杰伦"}`))
	}))
	defer server.Close()
	engine := NewLXPlaybackEngine([]string{server.URL})
	engine.openApp = func() error {
		opened.Store(true)
		return nil
	}
	if err := engine.ensureAPIReadyForPlayback(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !opened.Load() {
		t.Fatal("playback request did not launch 洛雪 after the status API was unavailable")
	}
}


func TestBuildLXPlayURIUsesKugouFileHashFromCompositeID(t *testing.T) {
	track := music.Track{
		ID: "12345_AABBCCDDEEFF00112233445566778899",
		SourceID: "kg",
		LXSourceKey: "kg",
		Title: "测试歌曲",
		Artist: "测试歌手",
	}
	uri, err := buildLXPlayURI(track)
	if err != nil { t.Fatal(err) }
	parsed, err := url.Parse(uri)
	if err != nil { t.Fatal(err) }
	var payload map[string]any
	if err := json.Unmarshal([]byte(parsed.Query().Get("data")), &payload); err != nil { t.Fatal(err) }
	if got := payload["songmid"]; got != "AABBCCDDEEFF00112233445566778899" {
		t.Fatalf("Kugou songmid = %#v, want FileHash", got)
	}
}

func TestBuildLXPlayURIUsesKugouHashWhenIDIsHashOnly(t *testing.T) {
	track := music.Track{
		ID: "AABBCCDDEEFF00112233445566778899",
		SourceID: "kg",
		Title: "测试歌曲",
		Artist: "测试歌手",
	}
	uri, err := buildLXPlayURI(track)
	if err != nil { t.Fatal(err) }
	parsed, err := url.Parse(uri)
	if err != nil { t.Fatal(err) }
	var payload map[string]any
	if err := json.Unmarshal([]byte(parsed.Query().Get("data")), &payload); err != nil { t.Fatal(err) }
	if got := payload["songmid"]; got != track.ID {
		t.Fatalf("Kugou songmid = %#v, want %q", got, track.ID)
	}
}


func TestBuildLXPlayURIMapsNativeIdentityAndQualitiesForThreeSources(t *testing.T) {
	tests := []struct {
		name string
		track music.Track
		wantSource string
		wantSongID string
	}{
		{
			name: "QQ",
			track: music.Track{
				ID: "tx_qq-native-1", SourceID: "tx", LXSourceKey: "tx",
				Title: "测试歌曲", Artist: "测试歌手",
				SourceData: map[string]string{"songmid": "qq-native-1", "media_mid": "qq-media-1", "album_mid": "qq-album-1"},
				Qualities: []music.TrackQuality{{Type: "128k"}, {Type: "320k", SourceData: map[string]string{"file": "qq-320"}}},
			},
			wantSource: "tx", wantSongID: "qq-native-1",
		},
		{
			name: "Kugou",
			track: music.Track{
				ID: "12345_KGFILEHASH", SourceID: "kg", LXSourceKey: "kg",
				Title: "测试歌曲", Artist: "测试歌手",
				SourceData: map[string]string{"hash": "KGFILEHASH"},
				Qualities: []music.TrackQuality{{Type: "320k", SourceData: map[string]string{"hash": "KGFILEHASH"}}},
			},
			wantSource: "kg", wantSongID: "KGFILEHASH",
		},
		{
			name: "Migu",
			track: music.Track{
				ID: "mg_migu-native-3", SourceID: "mg", LXSourceKey: "mg",
				Title: "测试歌曲", Artist: "测试歌手",
				SourceData: map[string]string{"songmid": "migu-native-3", "copyright_id": "migu-copy-3"},
				Qualities: []music.TrackQuality{{Type: "flac24bit", SourceData: map[string]string{"copyrightId": "migu-copy-3"}}},
			},
			wantSource: "mg", wantSongID: "migu-native-3",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			uri, err := buildLXPlayURI(tt.track)
			if err != nil { t.Fatal(err) }
			parsed, err := url.Parse(uri)
			if err != nil { t.Fatal(err) }
			if parsed.Scheme != "lxmusic" || parsed.Host != "music" || parsed.Path != "/play" {
				t.Fatalf("unexpected playback protocol URI: %s", uri)
			}
			var payload map[string]any
			if err := json.Unmarshal([]byte(parsed.Query().Get("data")), &payload); err != nil { t.Fatal(err) }
			if payload["source"] != tt.wantSource { t.Fatalf("source = %#v, want %q", payload["source"], tt.wantSource) }
			if payload["songmid"] != tt.wantSongID { t.Fatalf("songmid = %#v, want %q", payload["songmid"], tt.wantSongID) }
			qualities, ok := payload["types"].([]any)
			if !ok || len(qualities) != len(tt.track.Qualities) {
				t.Fatalf("quality payload = %#v, want %d entries", payload["types"], len(tt.track.Qualities))
			}
			for i, expected := range tt.track.Qualities {
				quality, ok := qualities[i].(map[string]any)
				if !ok || quality["type"] != expected.Type {
					t.Fatalf("quality[%d] = %#v, want type %q", i, qualities[i], expected.Type)
				}
				for key, value := range expected.SourceData {
					if quality[key] != value {
						t.Errorf("quality[%d].%s = %#v, want %q", i, key, quality[key], value)
					}
				}
			}
		})
	}
}
