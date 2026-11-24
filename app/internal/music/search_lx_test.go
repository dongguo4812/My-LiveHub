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
		q := r.URL.Query()
		if q.Get("pagesize") != "30" { t.Errorf("Kugou page size = %q", q.Get("pagesize")) }
		if q.Get("platform") != "WebFilter" { t.Errorf("Kugou platform = %q, want WebFilter", q.Get("platform")) }
		if q.Get("privilege_filter") != "0" { t.Errorf("Kugou privilege_filter = %q", q.Get("privilege_filter")) }
		if q.Get("filter") != "2" || q.Get("tag") != "em" || q.Get("userid") != "-1" {
			t.Errorf("Kugou WebFilter params = %v", q)
		}
		// Exercise the current WebFilter field names and HTML search highlights.
		_, _ = w.Write([]byte(`{"error_code":0,"data":{"total":1,"lists":[{"Audioid":202,"FileHash":"HASH","SongName":"<em>Kugou</em> Song","SingerName":"Singer","AlbumName":"Album","Duration":181,"FileSize":1024,"HQFileSize":2048,"Grp":[]}]}}`))
	})
	mux.HandleFunc("/tx", func(w http.ResponseWriter, r *http.Request) {
		var request map[string]any
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil { t.Errorf("decode QQ request: %v", err) }
		service := mapValue(request["music.search.SearchCgiService"]); param := mapValue(service["param"])
		if intValue(param["num_per_page"]) != 30 { t.Errorf("QQ page size = %v", param["num_per_page"]) }
		if r.URL.Query().Get("sign") != "" { t.Errorf("legacy QQ sign query should be omitted") }
		if r.Header.Get("Referer") != "https://y.qq.com/" { t.Errorf("QQ Referer = %q", r.Header.Get("Referer")) }
		if _, ok := request["comm"]; ok { t.Errorf("legacy QQ comm envelope should be omitted") }
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
	hasMiguFLAC := false
	for _, item := range bySource["mg"].Qualities { if item.Type == "flac" { hasMiguFLAC = true } }
	if !hasMiguFLAC { t.Fatalf("Migu ZQ24 quality was not normalized to the Xinghai-supported flac tier: %#v", bySource["mg"].Qualities) }
	if result.PageSize != 30 || result.Total != 1 || result.MaxPage != 1 { t.Fatalf("LX merged pagination mismatch: %#v", result) }
}

func TestKugouSearchCleansHighlightedSongAndArtistFields(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"error_code":0,"data":{"total":1,"lists":[{"Audioid":"440613","FileHash":"ABC","OriSongName":"<em>稻香</em>","SingerName":"周杰伦","AlbumName":"魔杰座","Duration":223,"FileSize":1000,"HQFileSize":2000,"SQFileSize":3000,"ResFileSize":4000}]}}`))
	}))
	defer server.Close()
	provider := kugouSearchProvider{client: server.Client(), endpoint: server.URL}
	page, err := provider.Search(context.Background(), "稻香", 1, 30)
	if err != nil { t.Fatalf("Kugou Search: %v", err) }
	if len(page.Tracks) != 1 { t.Fatalf("got %d tracks, want 1: %#v", len(page.Tracks), page.Tracks) }
	track := page.Tracks[0]
	if track.Title != "稻香" || track.Artist != "周杰伦" {
		t.Fatalf("Kugou highlight/name parsing mismatch: title=%q artist=%q", track.Title, track.Artist)
	}
	if track.SourceData["hash"] != "ABC" || track.SourceData["songmid"] != "440613" {
		t.Fatalf("Kugou native IDs were not retained: %#v", track.SourceData)
	}
	if len(track.Qualities) != 4 { t.Fatalf("Kugou qualities = %#v, want four tiers", track.Qualities) }
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


func TestLXSearchClientPreservesProviderPriorityAndIDDedup(t *testing.T) {
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
	if result.Tracks[0].ID != "kw:1" || result.Tracks[1].ID != "kw:2" || result.Tracks[2].ID != "kg:3" { t.Fatalf("results should retain provider priority and native order: %#v", result.Tracks) }
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

func TestLXSearchSingleProviderPreservesNativeOrder(t *testing.T) {
	client := &LXSearchClient{providers: []SearchProvider{
		stubSearchProvider{id:"tx", result:SearchPage{Tracks:[]Track{
			{ID:"tx:1",SourceID:"tx",Title:"稻香",Artist:"于潼"},
			{ID:"tx:2",SourceID:"tx",Title:"稻香",Artist:"周杰伦"},
		},Total:2,Page:1,PageSize:30,MaxPage:1}},
	}}
	result, errs, err := client.SearchSources(context.Background(), "稻香", 1, []string{"tx"})
	if err != nil { t.Fatal(err) }
	if len(errs) != 0 { t.Fatalf("unexpected provider errors: %#v", errs) }
	if len(result.Tracks) != 2 || result.Tracks[0].Artist != "于潼" || result.Tracks[1].Artist != "周杰伦" {
		t.Fatalf("single-provider search must preserve provider-native order, got %#v", result.Tracks)
	}
}


func TestQQSearchUsesCurrentUnsignedModuleEnvelope(t *testing.T) {
	attempts := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		var request map[string]any
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil { t.Errorf("decode QQ request: %v", err) }
		if r.URL.Query().Get("sign") != "" { t.Error("legacy QQ sign query should be omitted") }
		if _, ok := request["comm"]; ok { t.Error("legacy QQ comm envelope should be omitted") }
		service := mapValue(request["music.search.SearchCgiService"])
		if stringValue(service["method"]) != "DoSearchForQQMusicDesktop" { t.Errorf("unexpected QQ method: %#v", service) }
		_, _ = w.Write([]byte(`{"code":0,"music.search.SearchCgiService":{"code":0,"data":{"body":{"song":{"list":[]}},"meta":{"sum":0}}}}`))
	}))
	defer server.Close()
	provider := qqSearchProvider{client:server.Client(), endpoint:server.URL}
	if _, err := provider.Search(context.Background(), "test", 1, 30); err != nil { t.Fatalf("QQ Search() failed: %v", err) }
	if attempts != 1 { t.Fatalf("QQ request attempts = %d, want 1", attempts) }
}

func TestDefaultLXSearchQQEndpointMatchesLXDesktop(t *testing.T) {
	if got := DefaultLXSearchEndpoints().QQ; got != "https://u.y.qq.com/cgi-bin/musicu.fcg" {
		t.Fatalf("QQ search endpoint = %q, want current musicu.fcg endpoint", got)
	}
}


func TestQQSearchFallsBackToLegacyCGIImmediatelyOnDesktop2001(t *testing.T) {
	desktopCalls, legacyCalls := 0, 0
	mux := http.NewServeMux()
	mux.HandleFunc("/desktop", func(w http.ResponseWriter, r *http.Request) {
		desktopCalls++
		_, _ = w.Write([]byte(`{"code":0,"music.search.SearchCgiService":{"code":2001,"message":"anonymous search restricted"}}`))
	})
	mux.HandleFunc("/legacy", func(w http.ResponseWriter, r *http.Request) {
		legacyCalls++
		if r.Method != http.MethodGet { t.Errorf("legacy method = %s, want GET", r.Method) }
		q := r.URL.Query()
		for key, want := range map[string]string{
			"w":"晴天", "p":"2", "n":"30", "remoteplace":"txt.yqq.song",
			"loginUin":"0", "hostUin":"0", "format":"json",
		} {
			if got := q.Get(key); got != want { t.Errorf("legacy query %s = %q, want %q", key, got, want) }
		}
		_, _ = w.Write([]byte(`{"code":0,"data":{"song":{"totalnum":1,"list":[{"songmid":"0039MnYb0HxY2m","songid":12345,"songname":"晴天","albumname":"叶惠美","albummid":"0024N7xD","interval":269,"strMediaMid":"MEDIA123","singer":[{"name":"周杰伦","mid":"001"}],"size320":4096000}]}}}`))
	})
	server := httptest.NewServer(mux)
	defer server.Close()
	provider := qqSearchProvider{client:server.Client(), endpoint:server.URL+"/desktop", legacyEndpoint:server.URL+"/legacy"}
	result, err := provider.Search(context.Background(), "晴天", 2, 30)
	if err != nil { t.Fatalf("QQ fallback search failed: %v", err) }
	if desktopCalls != 1 { t.Fatalf("Desktop attempts = %d, want exactly 1 (no repeated 2001 retries)", desktopCalls) }
	if legacyCalls != 1 { t.Fatalf("legacy calls = %d, want 1", legacyCalls) }
	if result.Source != "tx" || result.Page != 2 || result.PageSize != 30 || result.Total != 1 || result.MaxPage != 1 {
		t.Fatalf("unexpected unified search page: %#v", result)
	}
	if len(result.Tracks) != 1 { t.Fatalf("tracks = %#v, want one converted track", result.Tracks) }
	track := result.Tracks[0]
	if track.ID != "tx_0039MnYb0HxY2m" || track.SourceID != "tx" || track.LXSourceKey != "tx" ||
		track.Title != "晴天" || track.Artist != "周杰伦" || track.Album != "叶惠美" ||
		track.DurationMS != 269000 || track.SourceData["songmid"] != "0039MnYb0HxY2m" ||
		track.SourceData["media_mid"] != "MEDIA123" {
		t.Fatalf("legacy track was not converted to the existing QQ Track model: %#v", track)
	}
	if len(track.Qualities) != 1 || track.Qualities[0].Type != "320k" {
		t.Fatalf("legacy quality metadata not preserved: %#v", track.Qualities)
	}
}

func TestQQSearchDoesNotFallbackForNon2001DesktopErrors(t *testing.T) {
	desktopCalls, legacyCalls := 0, 0
	mux := http.NewServeMux()
	mux.HandleFunc("/desktop", func(w http.ResponseWriter, r *http.Request) {
		desktopCalls++
		_, _ = w.Write([]byte(`{"code":0,"music.search.SearchCgiService":{"code":2002}}`))
	})
	mux.HandleFunc("/legacy", func(w http.ResponseWriter, r *http.Request) { legacyCalls++ })
	server := httptest.NewServer(mux)
	defer server.Close()
	provider := qqSearchProvider{client:server.Client(), endpoint:server.URL+"/desktop", legacyEndpoint:server.URL+"/legacy"}
	if _, err := provider.Search(context.Background(), "晴天", 1, 30); err == nil {
		t.Fatal("expected non-2001 Desktop error")
	}
	if desktopCalls != 6 || legacyCalls != 0 {
		t.Fatalf("non-2001 response should retain the existing retry policy without legacy fallback: Desktop=%d legacy=%d", desktopCalls, legacyCalls)
	}
}

func TestQQSearchFallbackHandlesDesktopIntermittent2001AcrossCalls(t *testing.T) {
	desktopCalls, legacyCalls := 0, 0
	mux := http.NewServeMux()
	mux.HandleFunc("/desktop", func(w http.ResponseWriter, r *http.Request) {
		desktopCalls++
		if desktopCalls == 1 {
			_, _ = w.Write([]byte(`{"code":0,"music.search.SearchCgiService":{"code":0,"data":{"body":{"song":{"list":[{"id":99,"mid":"DESKTOP99","title":"Desktop Song","interval":200,"singer":[{"name":"Singer"}],"album":{"mid":"ALBUM","name":"Album"},"file":{"media_mid":"MEDIA99","size_320mp3":1000}}]}},"meta":{"sum":1}}}}`))
			return
		}
		_, _ = w.Write([]byte(`{"code":0,"music.search.SearchCgiService":{"code":2001}}`))
	})
	mux.HandleFunc("/legacy", func(w http.ResponseWriter, r *http.Request) {
		legacyCalls++
		_, _ = w.Write([]byte(`{"code":0,"data":{"song":{"totalnum":1,"list":[{"songmid":"LEGACY100","songid":100,"songname":"Legacy Song","albumname":"Album","albummid":"ALBUM","interval":180,"strMediaMid":"MEDIA100","singer":[{"name":"Singer"}]}]}}}`))
	})
	server := httptest.NewServer(mux)
	defer server.Close()
	provider := qqSearchProvider{client:server.Client(), endpoint:server.URL+"/desktop", legacyEndpoint:server.URL+"/legacy"}
	first, err := provider.Search(context.Background(), "test", 1, 30)
	if err != nil { t.Fatal(err) }
	second, err := provider.Search(context.Background(), "test", 1, 30)
	if err != nil { t.Fatal(err) }
	if first.Tracks[0].SourceData["songmid"] != "DESKTOP99" {
		t.Fatalf("successful Desktop result changed: %#v", first.Tracks)
	}
	if second.Tracks[0].SourceData["songmid"] != "LEGACY100" {
		t.Fatalf("2001 did not switch to legacy result: %#v", second.Tracks)
	}
	if desktopCalls != 2 || legacyCalls != 1 {
		t.Fatalf("unexpected calls across intermittent responses: Desktop=%d legacy=%d", desktopCalls, legacyCalls)
	}
}


func TestMiguSearchFallsBackToPublicContentSearch(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/primary", func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "primary search temporarily unavailable", http.StatusServiceUnavailable)
	})
	mux.HandleFunc("/fallback", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("ua") != "Android_migu" || r.URL.Query().Get("version") != "5.0.1" {
			t.Errorf("Migu fallback client parameters = %v", r.URL.Query())
		}
		if r.URL.Query().Get("text") != "稻香" || r.URL.Query().Get("pageNo") != "1" || r.URL.Query().Get("pageSize") != "30" {
			t.Errorf("Migu fallback search parameters = %v", r.URL.Query())
		}
		_, _ = w.Write([]byte(`{"code":"000000","songResultData":{"totalCount":1,"result":[{"contentId":"MIGU-CONTENT-1","copyrightId":"MIGU-COPY-1","name":"稻香","duration":269,"singers":[{"name":"周杰伦"}],"albums":[{"name":"魔杰座"}],"audioFormats":[{"formatType":"HQ","asize":2048000}],"imgItems":[{"img":"/cover/daoxiang.jpg"}]}]}}`))
	})
	server := httptest.NewServer(mux)
	defer server.Close()

	client := NewLXSearchClientWithEndpoints(server.Client(), LXSearchEndpoints{
		Migu: server.URL + "/primary",
		MiguFallback: server.URL + "/fallback",
	})
	page, err := client.SearchProvider(context.Background(), LXSearchMG, "稻香", 1)
	if err != nil { t.Fatalf("Migu fallback search failed: %v", err) }
	if len(page.Tracks) != 1 { t.Fatalf("Migu fallback returned %d tracks, want 1: %#v", len(page.Tracks), page.Tracks) }
	track := page.Tracks[0]
	if track.Title != "稻香" || track.Artist != "周杰伦" || track.Album != "魔杰座" {
		t.Fatalf("Migu fallback metadata mismatch: %#v", track)
	}
	if track.SourceData["songmid"] != "MIGU-CONTENT-1" || track.SourceData["copyright_id"] != "MIGU-COPY-1" {
		t.Fatalf("Migu fallback native IDs were not retained: %#v", track.SourceData)
	}
	if track.DurationMS != 269000 { t.Fatalf("Migu fallback duration = %dms, want 269000ms", track.DurationMS) }
	if len(track.Qualities) != 1 || track.Qualities[0].Type != "320k" {
		t.Fatalf("Migu fallback quality metadata = %#v", track.Qualities)
	}
	if track.CoverURL != "https://d.musicapp.migu.cn/cover/daoxiang.jpg" {
		t.Fatalf("Migu fallback cover URL = %q", track.CoverURL)
	}
}

func TestKuwoQualityLabelsMatchXinghaiSupportedTiers(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"TOTAL":"1","SHOW":"1","abslist":[{"MUSICRID":"MUSIC_1","SONGNAME":"Song","ARTIST":"Singer","ALBUM":"Album","DURATION":"180","N_MINFO":"level:flac,bitrate:4000,format:flac,size:9.0;level:flac,bitrate:2000,format:flac,size:7.0;level:mp3,bitrate:320,format:mp3,size:5.0"}]}`))
	}))
	defer server.Close()
	provider := kuwoSearchProvider{client: server.Client(), endpoint: server.URL}
	page, err := provider.Search(context.Background(), "Song", 1, 30)
	if err != nil { t.Fatal(err) }
	if len(page.Tracks) != 1 { t.Fatalf("tracks=%#v", page.Tracks) }
	qualities := page.Tracks[0].Qualities
	countFLAC, hasUnsupported24Bit := 0, false
	for _, item := range qualities {
		if item.Type == "flac" { countFLAC++ }
		if item.Type == "flac24bit" { hasUnsupported24Bit = true }
	}
	if countFLAC != 1 || hasUnsupported24Bit { t.Fatalf("Kuwo qualities must expose one script-supported flac tier, got %#v", qualities) }
}

func TestKugouHiResQualityUsesXinghaiQualityName(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"error_code":0,"data":{"total":1,"lists":[{"Audioid":"1","FileHash":"A","SongName":"Song","SingerName":"Singer","Duration":180,"FileSize":1000,"HQFileSize":2000,"SQFileSize":3000,"ResFileSize":4000}]}}`))
	}))
	defer server.Close()
	provider := kugouSearchProvider{client: server.Client(), endpoint: server.URL}
	page, err := provider.Search(context.Background(), "Song", 1, 30)
	if err != nil { t.Fatal(err) }
	if len(page.Tracks) != 1 { t.Fatalf("tracks=%#v", page.Tracks) }
	hasHiRes, hasLegacyAlias := false, false
	for _, item := range page.Tracks[0].Qualities {
		if item.Type == "hires" { hasHiRes = true }
		if item.Type == "flac24bit" { hasLegacyAlias = true }
	}
	if !hasHiRes || hasLegacyAlias { t.Fatalf("Kugou Hi-Res must use Xinghai's hires label, got %#v", page.Tracks[0].Qualities) }
}
