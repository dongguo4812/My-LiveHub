package music

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestNetEaseSourceSearch(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/search/get/web" { t.Errorf("unexpected path %s", r.URL.Path) }
		if r.URL.Query().Get("s") != "稻香" || r.URL.Query().Get("limit") != "5" { t.Errorf("unexpected query: %s", r.URL.RawQuery) }
		if r.Header.Get("Referer") == "" { t.Fatal("expected platform referer") }
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"code":200,"result":{"songs":[{"id":123,"name":"稻香","duration":223000,"artists":[{"name":"周杰伦"}],"album":{"name":"魔杰座","picUrl":"https://img.invalid/cover.jpg"}}]}}`))
	}))
	defer server.Close()
	source := NewNetEaseSource(server.Client(), server.URL)
	tracks, err := source.Search(context.Background(), " 稻香 ", 5)
	if err != nil { t.Fatal(err) }
	if len(tracks) != 1 { t.Fatalf("expected one track, got %d", len(tracks)) }
	got := tracks[0]
	if got.ID != "123" || got.SourceID != "netease" || got.Title != "稻香" || got.Artist != "周杰伦" || got.DurationMS != 223000 {
		t.Fatalf("unexpected track: %#v", got)
	}
	resource, err := source.Resolve(context.Background(), got)
	if err != nil { t.Fatal(err) }
	if resource.URL != server.URL+"/song/media/outer/url?id=123.mp3" { t.Fatalf("unexpected resource URL: %s", resource.URL) }
}

func TestNetEaseSourceRejectsForeignTrack(t *testing.T) {
	source := NewNetEaseSource(nil, "")
	if _, err := source.Resolve(context.Background(), Track{ID:"123", SourceID:"qq_music"}); err == nil {
		t.Fatal("expected source mismatch error")
	}
}
