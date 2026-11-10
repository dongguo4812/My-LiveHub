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
