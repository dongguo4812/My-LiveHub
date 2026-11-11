package music

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestQQMusicSourceSearchPage(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("p") != "2" || r.URL.Query().Get("n") != "10" {
			t.Errorf("unexpected QQ pagination query: %s", r.URL.RawQuery)
		}
		_, _ = w.Write([]byte(`{"code":0,"data":{"song":{"totalnum":25,"list":[{"id":1,"mid":"qq-mid","title":"找自己","interval":304,"singer":[{"name":"陶喆"}],"album":{"name":"I'm OK","mid":"album-mid"}}]}}}`))
	}))
	defer server.Close()
	source := NewQQMusicSource(server.Client(), server.URL)
	result, err := source.SearchPage(context.Background(), "找自己 陶喆", 2, 10)
	if err != nil { t.Fatal(err) }
	if result.Total != 25 || result.MaxPage != 3 || len(result.Tracks) != 1 {
		t.Fatalf("unexpected QQ result: %#v", result)
	}
	if result.Tracks[0].ID != "qq-mid" || result.Tracks[0].Artist != "陶喆" || result.Tracks[0].DurationMS != 304000 {
		t.Fatalf("unexpected QQ track: %#v", result.Tracks[0])
	}
}

func TestKugouSourceSearchPage(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("page") != "2" || r.URL.Query().Get("pagesize") != "10" {
			t.Errorf("unexpected Kugou pagination query: %s", r.URL.RawQuery)
		}
		_, _ = w.Write([]byte(`{"error_code":0,"data":{"total":21,"lists":[{"Audioid":123,"FileHash":"hash","SongName":"找自己","AlbumName":"I'm OK","Duration":304,"Singers":[{"name":"陶喆"}]}]}}`))
	}))
	defer server.Close()
	source := NewKugouSource(server.Client(), server.URL)
	result, err := source.SearchPage(context.Background(), "找自己 陶喆", 2, 10)
	if err != nil { t.Fatal(err) }
	if result.Total != 21 || result.MaxPage != 3 || len(result.Tracks) != 1 {
		t.Fatalf("unexpected Kugou result: %#v", result)
	}
	if result.Tracks[0].ID != "123" || result.Tracks[0].Artist != "陶喆" || result.Tracks[0].DurationMS != 304000 {
		t.Fatalf("unexpected Kugou track: %#v", result.Tracks[0])
	}
}

func TestKuwoSourceSearchPage(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("pn") != "1" || r.URL.Query().Get("rn") != "10" {
			t.Errorf("unexpected Kuwo pagination query: %s", r.URL.RawQuery)
		}
		_, _ = w.Write([]byte(`{"TOTAL":"21","abslist":[{"MUSICRID":"MUSIC_456","SONGNAME":"找自己","ARTIST":"陶喆","DURATION":"304","ALBUMID":"album","ALBUM":"I'm OK"}]}`))
	}))
	defer server.Close()
	source := NewKuwoSource(server.Client(), server.URL)
	result, err := source.SearchPage(context.Background(), "找自己 陶喆", 2, 10)
	if err != nil { t.Fatal(err) }
	if result.Total != 21 || result.MaxPage != 3 || len(result.Tracks) != 1 {
		t.Fatalf("unexpected Kuwo result: %#v", result)
	}
	if result.Tracks[0].ID != "456" || result.Tracks[0].Artist != "陶喆" || result.Tracks[0].DurationMS != 304000 {
		t.Fatalf("unexpected Kuwo track: %#v", result.Tracks[0])
	}
}

func TestSearchOnlySourcesCannotResolvePlayback(t *testing.T) {
	sources := []Source{NewQQMusicSource(nil, ""), NewKugouSource(nil, ""), NewKuwoSource(nil, "")}
	for _, source := range sources {
		if !source.Capabilities().Search || source.Capabilities().Resolve {
			t.Errorf("unexpected capabilities for %s: %#v", source.ID(), source.Capabilities())
		}
		if _, err := source.Resolve(context.Background(), Track{ID:"1", SourceID:source.ID(), Title:"song"}); err == nil {
			t.Errorf("expected %s playback resolution to be explicitly unsupported", source.ID())
		}
	}
}
