package music

import (
	"context"
	"errors"
	"testing"
)

type testSource struct {
	id string
	name string
	tracks []Track
	err error
}
func (s *testSource) ID() string { return s.id }
func (s *testSource) Name() string { return s.name }
func (s *testSource) Capabilities() SourceCapabilities { return SourceCapabilities{Search:true, Resolve:true} }
func (s *testSource) Search(context.Context, string, int) ([]Track, error) { return s.tracks, s.err }
func (s *testSource) Resolve(context.Context, Track) (PlaybackResource, error) { return PlaybackResource{URL:"https://media.invalid/audio"}, nil }

func TestSourceRegistryRegisterAndList(t *testing.T) {
	registry := NewSourceRegistry()
	source := &testSource{id:"netease", name:"网易云音乐"}
	if err := registry.Register(source); err != nil { t.Fatal(err) }
	if _, ok := registry.Get("netease"); !ok { t.Fatal("registered source not found") }
	items := registry.List(map[string]bool{"netease":true})
	if len(items) != 1 || items[0].ID != "netease" || !items[0].Enabled || !items[0].Available {
		t.Fatalf("unexpected source snapshots: %#v", items)
	}
	if err := registry.Register(source); err == nil { t.Fatal("expected duplicate registration error") }
}

func TestSourceRegistryRejectsInvalidSource(t *testing.T) {
	registry := NewSourceRegistry()
	if err := registry.Register(nil); err == nil { t.Fatal("expected nil source error") }
	source := &testSource{id:"", name:"No ID"}
	if err := registry.Register(source); err == nil { t.Fatal("expected missing ID error") }
}

func TestSearchQueriesAllEnabledSourcesAndKeepsPartialResults(t *testing.T) {
	registry := NewSourceRegistry()
	_ = registry.Register(&testSource{id:"netease", name:"网易云音乐", tracks:[]Track{{ID:"n1", Title:"稻香"}}})
	_ = registry.Register(&testSource{id:"qq_music", name:"QQ 音乐", err:errors.New("temporarily unavailable")})
	tracks, failures, err := registry.Search(context.Background(), " 稻香 ", map[string]bool{"netease":true,"qq_music":true}, 20)
	if err != nil { t.Fatal(err) }
	if len(tracks) != 1 || tracks[0].SourceID != "netease" { t.Fatalf("unexpected search results: %#v", tracks) }
	if failures["qq_music"] == "" { t.Fatalf("expected partial source error: %#v", failures) }
}

func TestSearchRejectsEmptyEnabledSourceSet(t *testing.T) {
	registry := NewSourceRegistry()
	if _, _, err := registry.Search(context.Background(), "song", map[string]bool{}, 20); err == nil {
		t.Fatal("expected no-enabled-source error")
	}
}

type pagedTestSource struct {
	*testSource
	pageData SourceSearchPage
}

func (s *pagedTestSource) SearchPage(context.Context, string, int, int) (SourceSearchPage, error) {
	return s.pageData, s.err
}

func TestSourceRegistryAggregatesSamePageAndDeduplicates(t *testing.T) {
	registry := NewSourceRegistry()
	first := &pagedTestSource{
		testSource: &testSource{id:"netease", name:"网易云音乐"},
		pageData: SourceSearchPage{
			Tracks: []Track{
				{ID:"n1", SourceID:"netease", Title:"找自己", Artist:"陶喆"},
				{ID:"n2", SourceID:"netease", Title:"其他歌曲", Artist:"其他歌手"},
			},
			Total: 35, MaxPage: 2, PageSize: 20,
		},
	}
	second := &pagedTestSource{
		testSource: &testSource{id:"qq_music", name:"QQ 音乐"},
		pageData: SourceSearchPage{
			Tracks: []Track{
				{ID:"q1", SourceID:"qq_music", Title:"找自己", Artist:"陶喆"},
				{ID:"q2", SourceID:"qq_music", Title:"找自己 (Live)", Artist:"陶喆"},
			},
			Total: 78, MaxPage: 4, PageSize: 20,
		},
	}
	if err := registry.Register(first); err != nil { t.Fatal(err) }
	if err := registry.Register(second); err != nil { t.Fatal(err) }

	tracks, failures, total, maxPage, err := registry.SearchPage(
		context.Background(), "找自己 陶喆",
		map[string]bool{"netease":true, "qq_music":true}, 20, 2,
	)
	if err != nil { t.Fatal(err) }
	if len(failures) != 0 { t.Fatalf("unexpected source errors: %#v", failures) }
	if total != 78 || maxPage != 4 { t.Fatalf("unexpected aggregate pagination: total=%d maxPage=%d", total, maxPage) }
	if len(tracks) != 3 { t.Fatalf("expected duplicate track to be removed, got %#v", tracks) }
	if tracks[0].Title != "找自己" { t.Fatalf("expected best keyword match first, got %#v", tracks) }
}

func TestSourceRegistryPagedSearchKeepsOtherSourcesWhenOneFails(t *testing.T) {
	registry := NewSourceRegistry()
	good := &pagedTestSource{
		testSource: &testSource{id:"netease", name:"网易云音乐"},
		pageData: SourceSearchPage{Tracks: []Track{{ID:"n1", SourceID:"netease", Title:"稻香"}}, Total: 1, MaxPage: 1, PageSize: 20},
	}
	bad := &pagedTestSource{testSource:&testSource{id:"qq_music", name:"QQ 音乐", err:errors.New("offline")}}
	if err := registry.Register(good); err != nil { t.Fatal(err) }
	if err := registry.Register(bad); err != nil { t.Fatal(err) }
	tracks, failures, _, _, err := registry.SearchPage(context.Background(), "稻香", map[string]bool{"netease":true, "qq_music":true}, 20, 1)
	if err != nil { t.Fatal(err) }
	if len(tracks) != 1 || failures["qq_music"] == "" { t.Fatalf("partial result was not preserved: tracks=%#v failures=%#v", tracks, failures) }
}
