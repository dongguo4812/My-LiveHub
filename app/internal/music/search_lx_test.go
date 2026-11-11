package music

import (
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
		_, _ = w.Write([]byte(`{"TOTAL":"1","abslist":[{"MUSICRID":"MUSIC_101","SONGNAME":"Kuwo Song","ARTIST":"Singer","ALBUM":"Album","ALBUMID":"A1","DURATION":"180","N_MINFO":"level:mp3,bitrate:320,format:mp3,size:5.0"}]}`))
	})
	mux.HandleFunc("/kg", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"error_code":0,"data":{"total":1,"lists":[{"Audioid":"202","FileHash":"HASH","OriSongName":"Kugou Song","Singers":[{"name":"Singer"}],"AlbumName":"Album","Duration":181,"FileSize":1024,"HQFileSize":2048,"Grp":[]}]}}`))
	})
	mux.HandleFunc("/tx", func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Query().Get("sign"), "zzc") { t.Errorf("QQ request missing LX signature") }
		_, _ = w.Write([]byte(`{"code":0,"music.search.SearchCgiService":{"code":0,"data":{"body":{"song":{"list":[{"id":303,"mid":"TX303","title":"QQ Song","interval":182,"singer":[{"name":"Singer"}],"album":{"mid":"ALB","name":"Album"},"file":{"media_mid":"MEDIA","size_128mp3":1000,"size_320mp3":2000}}]}},"meta":{"sum":1}}}}`))
	})
	mux.HandleFunc("/wy", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"code":200,"data":{"totalCount":1,"resources":[{"baseInfo":{"simpleSongData":{"id":404,"name":"NetEase Song","dt":183000,"ar":[{"name":"Singer"}],"al":{"id":4,"name":"Album","picUrl":"https://img.test/4.jpg"},"privilege":{"maxbr":320000,"maxBrLevel":"higher"},"l":{"size":1000},"h":{"size":2000}}}}]}}`))
	})
	mux.HandleFunc("/mg", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("sign") == "" { t.Errorf("Migu request missing signature") }
		_, _ = w.Write([]byte(`{"code":"000000","songResultData":{"totalCount":1,"resultList":[[{"songId":"505","copyrightId":"COPY505","name":"Migu Song","duration":184,"singerList":[{"name":"Singer"}],"album":"Album","albumId":"5","audioFormats":[{"formatType":"PQ","asize":1000},{"formatType":"ZQ","asize":4000}],"img3":"https://img.test/5.jpg"}]]}}`))
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
	want := []string{"kw", "kg", "tx", "wy", "mg"}
	for i, source := range want {
		if result.Tracks[i].SourceID != source { t.Errorf("track[%d].SourceID = %q, want %q", i, result.Tracks[i].SourceID, source) }
		if len(result.Tracks[i].Qualities) == 0 { t.Errorf("track[%d] has no quality metadata", i) }
	}
	if result.Tracks[2].SourceData["media_mid"] != "MEDIA" { t.Fatalf("QQ native media ID was not retained: %#v", result.Tracks[2].SourceData) }
	if result.Tracks[4].ID != "mg:505" { t.Fatalf("Migu native song ID was not retained: %q", result.Tracks[4].ID) }
	if result.Tracks[4].SourceData["copyright_id"] != "COPY505" { t.Fatalf("Migu copyright ID was not retained: %#v", result.Tracks[4].SourceData) }
	hasMiguHiRes := false
	for _, item := range result.Tracks[4].Qualities { if item.Type == "flac24bit" { hasMiguHiRes = true } }
	if !hasMiguHiRes { t.Fatalf("Migu ZQ quality was not mapped to flac24bit: %#v", result.Tracks[4].Qualities) }
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
func (p stubSearchProvider) Search(context.Context,string,int)(SearchPage,error) { return p.result,p.err }

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
