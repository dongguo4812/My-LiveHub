package music

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestQQMusicSourceSearchPage(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("p") != "2" || r.URL.Query().Get("n") != "10" {
			t.Errorf("unexpected QQ pagination query: %s", r.URL.RawQuery)
		}
		_, _ = w.Write([]byte(`{"code":0,"data":{"song":{"totalnum":25,"list":[{"songid":1,"songmid":"qq-mid","songname":"找自己","interval":304,"singer":[{"name":"陶喆"}],"albumname":"I'm OK","albummid":"album-mid"}]}}}`))
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
	if result.Tracks[0].LXSourceKey != "tx" || result.Tracks[0].SourceData["songmid"] != "qq-mid" ||
		result.Tracks[0].Album != "I'm OK" || result.Tracks[0].CoverURL == "" {
		t.Fatalf("QQ LX metadata was not preserved: %#v", result.Tracks[0])
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
	if result.Tracks[0].LXSourceKey != "kg" || result.Tracks[0].SourceData["FileHash"] != "hash" {
		t.Fatalf("Kugou LX metadata was not preserved: %#v", result.Tracks[0])
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
	if result.Tracks[0].LXSourceKey != "kw" || result.Tracks[0].SourceData["MUSICRID"] != "MUSIC_456" {
		t.Fatalf("Kuwo LX metadata was not preserved: %#v", result.Tracks[0])
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

func TestMiguSourceSearchPageMatchesLXMetadata(t *testing.T){
 server:=httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter,r *http.Request){
  if r.URL.Query().Get("pageNo")!="2"||r.URL.Query().Get("pageSize")!="10"||r.URL.Query().Get("text")!="找自己 陶喆"||r.Header.Get("uiVersion")!="A_music_3.6.1"||r.Header.Get("sign")==""||r.Header.Get("deviceId")==""{t.Errorf("unexpected Migu request: %s",r.URL.RawQuery)}
  _,_=w.Write([]byte("{\"code\":\"000000\",\"info\":\"成功\",\"songResultData\":{\"totalCount\":\"21\",\"resultList\":[[{\"songId\":\"mg-song-id\",\"copyrightId\":\"mg-copy-id\",\"name\":\"找自己\",\"album\":\"I'm OK\",\"albumId\":\"album-9\",\"duration\":304,\"singerList\":[{\"name\":\"陶喆\"}],\"audioFormats\":[{\"formatType\":\"PQ\",\"asize\":1048576,\"isize\":0},{\"formatType\":\"HQ\",\"asize\":2097152,\"isize\":0},{\"formatType\":\"ZQ24\",\"asize\":4194304,\"isize\":0}],\"img3\":\"/cover.jpg\",\"lrcUrl\":\"https://lyrics.example/lrc\"}]]}}"))
 }));defer server.Close()
 src:=NewMiguSource(server.Client(),server.URL);src.now=func()time.Time{return time.UnixMilli(1700000000000)}
 got,err:=src.SearchPage(context.Background(),"找自己 陶喆",2,10);if err!=nil{t.Fatal(err)}
 if got.Total!=21||got.MaxPage!=3||len(got.Tracks)!=1{t.Fatalf("unexpected Migu result: %#v",got)}
 tr:=got.Tracks[0];if tr.SourceID!="migu"||tr.LXSourceKey!="mg"||tr.ID!="mg-song-id"||tr.Artist!="陶喆"||tr.DurationMS!=304000||tr.CoverURL!="http://d.musicapp.migu.cn/cover.jpg"{t.Fatalf("Migu metadata mismatch: %#v",tr)}
 if tr.SourceData["copyrightId"]!="mg-copy-id"||tr.SourceData["albumId"]!="album-9"||tr.SourceData["audio_formats_json"]==""{t.Fatalf("Migu metadata missing: %#v",tr.SourceData)}
}
