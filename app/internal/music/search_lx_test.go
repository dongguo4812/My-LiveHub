package music

import (
	"errors"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestLXSearchClientCallsAllFiveProvidersAndKeepsPartialResults(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/kw", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("rn") != "30" || r.URL.Query().Get("pn") != "0" { t.Errorf("Kuwo pagination params: %v", r.URL.Query()) }
		_, _ = w.Write([]byte(`{"TOTAL":"1","abslist":[{"MUSICRID":"MUSIC_101","SONGNAME":"Kuwo Song","ARTIST":"Singer","ALBUM":"Album","ALBUMID":"A1","DURATION":"180","N_MINFO":"level:mp3,bitrate:320,format:mp3,size:5.0"}]}`))
	})
	mux.HandleFunc("/kg", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("pagesize") != "30" { t.Errorf("Kugou page size = %q", r.URL.Query().Get("pagesize")) }
		_, _ = w.Write([]byte(`{"error_code":0,"data":{"total":1,"lists":[{"Audioid":"202","FileHash":"HASH","OriSongName":"Kugou Song","Singers":[{"name":"Singer"}],"AlbumName":"Album","Duration":181,"FileSize":1024,"HQFileSize":2048,"Grp":[]}]}}`))
	})
	mux.HandleFunc("/tx", func(w http.ResponseWriter, r *http.Request) {
		var request map[string]any
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil { t.Errorf("decode QQ request: %v", err) }
		service := mapValue(request["music.search.SearchCgiService"]); param := mapValue(service["param"])
		if intValue(param["num_per_page"]) != 30 { t.Errorf("QQ page size = %v", param["num_per_page"]) }
		if !strings.HasPrefix(r.URL.Query().Get("sign"), "zzc") { t.Errorf("QQ request missing LX signature") }
		_, _ = w.Write([]byte(`{"code":0,"music.search.SearchCgiService":{"code":0,"data":{"body":{"song":{"list":[{"id":303,"mid":"TX303","title":"QQ Song","interval":182,"singer":[{"name":"Singer"}],"album":{"mid":"ALB","name":"Album"},"file":{"media_mid":"MEDIA","size_128mp3":1000,"size_320mp3":2000}}]}},"meta":{"sum":1}}}}`))
	})
	mux.HandleFunc("/wy", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"code":200,"data":{"totalCount":1,"resources":[{"baseInfo":{"simpleSongData":{"id":404,"name":"NetEase Song","dt":183000,"ar":[{"name":"Singer"}],"al":{"id":4,"name":"Album","picUrl":"https://img.test/4.jpg"},"privilege":{"maxbr":320000,"maxBrLevel":"higher"},"l":{"size":1000},"h":{"size":2000}}}}]}}`))
	})
	mux.HandleFunc("/mg", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("pageSize") != "30" { t.Errorf("Migu page size = %q", r.URL.Query().Get("pageSize")) }
		if r.Header.Get("sign") == "" { t.Errorf("Migu request missing signature") }
		_, _ = w.Write([]byte(`{"code":"000000","songResultData":{"totalCount":1,"resultList":[[{"songId":"505","copyrightId":"COPY505","name":"Migu Song","duration":184,"singerList":[{"name":"Singer"}],"album":"Album","albumId":"5","audioFormats":[{"formatType":"PQ","asize":1000},{"formatType":"ZQ24","asize":4000}],"img3":"https://img.test/5.jpg"}]]}}`))
	})
	server := httptest.NewServer(mux)
	defer server.Close()

	client := NewLXSearchClientWithEndpoints(server.Client(), LXSearchEndpoints{
		Kuwo: server.URL+"/kw", Kugou: server.URL+"/kg", QQ: server.URL+"/tx",
		Netease: server.URL+"/wy", Migu: server.URL+"/mg",
	})
	result, errs, err := client.Search(context.Background(), "test song", 1)
	if err != nil { t.Fatalf("Search() error = %v, provider errors = %#v", err, errs) }
	if len(errs) != 0 { t.Fatalf("unexpected provider errors: %#v", errs) }
	if len(result.Tracks) != 5 { t.Fatalf("got %d tracks, want 5: %#v", len(result.Tracks), result.Tracks) }
	bySource := make(map[string]Track)
	for _, track := range result.Tracks {
		bySource[track.SourceID] = track
		if len(track.Qualities) == 0 { t.Errorf("track %s has no quality metadata", track.SourceID) }
	}
	for _, source := range []string{"kw", "kg", "tx", "wy", "mg"} {
		if _, ok := bySource[source]; !ok { t.Errorf("search result missing source %s", source) }
	}
	if bySource["tx"].SourceData["media_mid"] != "MEDIA" { t.Fatalf("QQ native media ID was not retained: %#v", bySource["tx"].SourceData) }
	if bySource["mg"].ID != "mg_505" { t.Fatalf("Migu native song ID was not retained: %q", bySource["mg"].ID) }
	if bySource["mg"].SourceData["copyright_id"] != "COPY505" { t.Fatalf("Migu copyright ID was not retained: %#v", bySource["mg"].SourceData) }
	hasMiguHiRes := false
	for _, item := range bySource["mg"].Qualities { if item.Type == "flac24bit" { hasMiguHiRes = true } }
	if !hasMiguHiRes { t.Fatalf("Migu ZQ quality was not mapped to flac24bit: %#v", bySource["mg"].Qualities) }
	if result.PageSize != 30 || result.Total != 1 || result.MaxPage != 1 { t.Fatalf("LX merged pagination mismatch: %#v", result) }
}

func TestLXSearchClientReturnsSuccessfulProvidersWhenOneFails(t *testing.T) {
	client := &LXSearchClient{providers: []SearchProvider{
		stubSearchProvider{id:"kw", result:SearchPage{Tracks:[]Track{{ID:"kw:1",SourceID:"kw",Title:"one"}},Total:1,MaxPage:1}},
		stubSearchProvider{id:"kg", err:context.DeadlineExceeded},
	}}
	result, errs, err := client.Search(context.Background(), "song", 1)
	if err != nil { t.Fatal(err) }
	if len(result.Tracks) != 1 || errs["kg"] == "" { t.Fatalf("partial result mismatch: %#v %#v", result, errs) }
}

type stubSearchProvider struct { id string; result SearchPage; err error }
func (p stubSearchProvider) ID() string { return p.id }
func (p stubSearchProvider) Name() string { return p.id }
func (p stubSearchProvider) Search(context.Context,string,int,int)(SearchPage,error) { return p.result,p.err }


func TestLXSearchClientUsesMaxProviderTotalAndRelevanceSortWithIDDedup(t *testing.T) {
	client := &LXSearchClient{providers: []SearchProvider{
		stubSearchProvider{id:"kw", result:SearchPage{Tracks:[]Track{
			{ID:"kw:1",SourceID:"kw",Title:"completely unrelated"},
			{ID:"kw:2",SourceID:"kw",Title:"hello world",Artist:"artist"},
		},Total:40,Page:1,PageSize:30,MaxPage:2}},
		stubSearchProvider{id:"kg", result:SearchPage{Tracks:[]Track{
			{ID:"kw:2",SourceID:"kg",Title:"hello world",Artist:"artist"},
			{ID:"kg:3",SourceID:"kg",Title:"hello world",Artist:"artist"},
		},Total:90,Page:1,PageSize:30,MaxPage:3}},
	}}
	result, errs, err := client.Search(context.Background(), "hello world", 1)
	if err != nil { t.Fatal(err) }
	if len(errs) != 0 { t.Fatalf("unexpected errors: %#v", errs) }
	if result.Total != 90 || result.PageSize != 30 || result.MaxPage != 3 {
		t.Fatalf("pagination must use max provider total/pages and 30 item page size: %#v", result)
	}
	if len(result.Tracks) != 3 { t.Fatalf("duplicate IDs should be removed once: %#v", result.Tracks) }
	if result.Tracks[0].ID != "kw:2" { t.Fatalf("best keyword match should be first: %#v", result.Tracks) }
}

func TestLXSearchPageTwoUsesSameLimitAndOffsets(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/kw", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("pn") != "1" || r.URL.Query().Get("rn") != "30" { t.Errorf("Kuwo page 2 params = %v", r.URL.Query()) }
		_, _ = w.Write([]byte(`{"TOTAL":"60","abslist":[]}`))
	})
	server := httptest.NewServer(mux)
	defer server.Close()
	provider := kuwoSearchProvider{client:server.Client(), endpoint:server.URL+"/kw"}
	page, err := provider.Search(context.Background(), "song", 2, 30)
	if err != nil { t.Fatal(err) }
	if page.Page != 2 || page.PageSize != 30 || page.MaxPage != 2 { t.Fatalf("unexpected page metadata: %#v", page) }
}


func TestLXFormattingAndSimilarityMatchPinnedJavaScriptRules(t *testing.T) {
	if got := formatLXSize(0); got != "0 B" { t.Fatalf("zero size = %q, want 0 B", got) }
	if got := formatLXSize(1024); got != "1.00 KiB" { t.Fatalf("1 KiB = %q", got) }
	if got := formatLXSize(1536); got != "1.50 KiB" { t.Fatalf("1.5 KiB = %q", got) }
	if got := lxSimilarity("😀", "😁"); got != 0.5 { t.Fatalf("UTF-16 similarity = %v, want 0.5", got) }
}


func TestNewLXTrackIDsMatchPinnedLXMusicInfoConversion(t *testing.T) {
	tests := []struct{ source, nativeID, want string; data map[string]string }{
		{"kw","kw-id","kw_kw-id",map[string]string{"songmid":"kw-id"}},
		{"kg","kg-id","kg-id_file-hash",map[string]string{"songmid":"kg-id","hash":"file-hash"}},
		{"tx","tx-id","tx_tx-id",map[string]string{"songmid":"tx-id"}},
		{"wy","wy-id","wy_wy-id",map[string]string{"songmid":"wy-id"}},
		{"mg","mg-id","mg_mg-id",map[string]string{"songmid":"mg-id"}},
	}
	for _, test := range tests {
		track := newLXTrack(test.source,test.nativeID,"song","singer","album",0,"",test.data,nil)
		if track.ID != test.want { t.Errorf("%s ID = %q, want %q",test.source,track.ID,test.want) }
	}
}


func TestLXAllSourceSearchConvertsProviderFailuresToEmptyPages(t *testing.T) {
	client := &LXSearchClient{providers: []SearchProvider{
		stubSearchProvider{id:"kw", err:errors.New("kw unavailable")},
		stubSearchProvider{id:"kg", err:errors.New("kg unavailable")},
	}}
	first, errs, err := client.Search(context.Background(), "song", 1)
	if err != nil { t.Fatalf("all-source search should resolve with empty results: %v", err) }
	if len(errs) != 2 || len(first.Tracks) != 0 || first.Total != 0 || first.MaxPage != 1 || first.PageSize != 30 {
		t.Fatalf("unexpected empty first page: page=%#v errors=%#v", first, errs)
	}
	second, _, err := client.Search(context.Background(), "song", 2)
	if err != nil { t.Fatal(err) }
	if second.Total != 60 || second.MaxPage != 0 { t.Fatalf("unexpected empty later page: %#v", second) }
}

func TestKuwoRejectsResultsWithoutNMINFOMetadata(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/kw", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"TOTAL":"1","SHOW":"1","abslist":[{"MUSICRID":"MUSIC_1","SONGNAME":"song","ARTIST":"singer"}]}`))
	})
	server := httptest.NewServer(mux)
	defer server.Close()
	provider := kuwoSearchProvider{client:server.Client(), endpoint:server.URL+"/kw"}
	if _, err := provider.Search(context.Background(), "song", 1, 30); err == nil {
		t.Fatal("expected missing N_MINFO metadata to reject the invalid Kuwo result")
	}
}

func TestEAPIEncryptProducesHexCiphertext(t *testing.T) {
	a, err := eapiEncrypt("/api/search/song/list/page", struct {
		Keyword string `json:"keyword"`
		NeedCorrect string `json:"needCorrect"`
		Channel string `json:"channel"`
		Offset int `json:"offset"`
		Scene string `json:"scene"`
		Total bool `json:"total"`
		Limit int `json:"limit"`
	}{"song","1","typing",0,"normal",true,30})
	if err != nil { t.Fatal(err) }
	if len(a)==0 || len(a)%32!=0 { t.Fatalf("invalid AES ciphertext length %d",len(a)) }
	if _,err:=json.Marshal(a);err!=nil{t.Fatal(err)}
	if strings.ToUpper(a)!=a { t.Fatal("EAPI ciphertext must be uppercase hex") }
}
