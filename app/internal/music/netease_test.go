package music

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestNetEaseSourceSearch(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/eapi/batch" { t.Errorf("unexpected path %s", r.URL.Path) }
		if r.Method != http.MethodPost || r.Header.Get("Referer") == "" || r.Header.Get("Origin") == "" { t.Errorf("unexpected NetEase request headers/method: %s", r.Method) }
		if err := r.ParseForm(); err != nil || r.Form.Get("params") == "" { t.Fatal("expected encrypted EAPI params form field") }
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"code":200,"data":{"totalCount":25,"resources":[{"baseInfo":{"simpleSongData":{"id":123,"name":"稻香","dt":223000,"ar":[{"name":"周杰伦"}],"al":{"id":456,"name":"魔杰座","picUrl":"https://img.invalid/cover.jpg"},"privilege":{"maxbr":999000,"maxBrLevel":"hires"},"l":{"size":3000000},"h":{"size":7000000},"sq":{"size":12000000},"hr":{"size":24000000}}}}]}}`))
	}))
	defer server.Close()
	source := NewNetEaseSource(server.Client(), server.URL)
	tracks, err := source.Search(context.Background(), " 稻香 ", 5)
	if err != nil { t.Fatal(err) }
	if len(tracks) != 1 { t.Fatalf("expected one track, got %d", len(tracks)) }
	got := tracks[0]
	if got.ID != "123" || got.SourceID != "netease" || got.LXSourceKey != "wy" || got.Title != "稻香" || got.Artist != "周杰伦" || got.DurationMS != 223000 ||
		got.SourceData["albumId"] != "456" || got.SourceData["wy_privilege"] == "" {
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

func TestNetEaseSourceSearchPageUsesOffsetAndReturnsPageCount(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/eapi/batch" { t.Errorf("unexpected path %s", r.URL.Path) }
		if err := r.ParseForm(); err != nil || r.Form.Get("params") == "" { t.Fatal("expected EAPI params") }
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"code":200,"data":{"totalCount":21,"resources":[{"baseInfo":{"simpleSongData":{"id":321,"name":"第三页歌曲","dt":180000,"ar":[{"name":"歌手"}],"al":{"id":12,"name":"专辑"},"privilege":{"maxbr":320000,"maxBrLevel":"standard"},"l":{"size":1000},"h":{"size":3000}}}}]}}`))
	}))
	defer server.Close()

	source := NewNetEaseSource(server.Client(), server.URL)
	result, err := source.SearchPage(context.Background(), "第三页歌曲", 3, 5)
	if err != nil {
		t.Fatal(err)
	}
	if result.Total != 21 || result.MaxPage != 5 || result.PageSize != 5 {
		t.Fatalf("unexpected pagination metadata: %#v", result)
	}
	if len(result.Tracks) != 1 || result.Tracks[0].ID != "321" {
		t.Fatalf("unexpected page tracks: %#v", result.Tracks)
	}
}
