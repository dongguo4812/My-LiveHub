package desktop

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
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
