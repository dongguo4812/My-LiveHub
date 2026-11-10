package music

import (
	"context"
	"testing"
)

type testPlayer struct {
	id string
	name string
	capabilities Capabilities
}

func (p *testPlayer) ID() string { return p.id }
func (p *testPlayer) Name() string { return p.name }
func (p *testPlayer) Capabilities() Capabilities { return p.capabilities }
func (p *testPlayer) Search(context.Context, string, int) ([]Track, error) { return nil, nil }
func (p *testPlayer) Play(context.Context, Track) error { return nil }
func (p *testPlayer) Pause(context.Context) error { return nil }
func (p *testPlayer) Resume(context.Context) error { return nil }
func (p *testPlayer) Stop(context.Context) error { return nil }
func (p *testPlayer) Next(context.Context) error { return nil }
func (p *testPlayer) Status(context.Context) (PlayerStatus, error) { return PlayerStatus{PlayerID:p.id}, nil }

func TestRegistryRegisterAndList(t *testing.T) {
	registry := NewRegistry()
	player := &testPlayer{id:"luoxue", name:"洛雪音乐", capabilities:Capabilities{Play:true, Search:true}}
	if err := registry.Register(player); err != nil { t.Fatal(err) }
	if _, ok := registry.Get("luoxue"); !ok { t.Fatal("registered player not found") }
	items := registry.List()
	if len(items) != 1 || items[0].ID != "luoxue" || !items[0].Available { t.Fatalf("unexpected snapshots: %#v", items) }
	if err := registry.Register(player); err == nil { t.Fatal("expected duplicate registration error") }
}

func TestRegistryRejectsInvalidPlayer(t *testing.T) {
	registry := NewRegistry()
	if err := registry.Register(nil); err == nil { t.Fatal("expected nil player error") }
	player := &testPlayer{id:"search-only", name:"Search only", capabilities:Capabilities{Search:true}}
	if err := registry.Register(player); err == nil { t.Fatal("expected playback capability error") }
}

func TestSearchNormalization(t *testing.T) {
	if got, err := normalizeSearchKeyword("  稻香 周杰伦  "); err != nil || got != "稻香 周杰伦" {
		t.Fatalf("unexpected keyword %q, error %v", got, err)
	}
	if _, err := normalizeSearchKeyword("  "); err == nil { t.Fatal("expected empty keyword error") }
	if got := normalizeSearchLimit(0); got != 20 { t.Fatalf("default limit = %d", got) }
	if got := normalizeSearchLimit(100); got != 50 { t.Fatalf("maximum limit = %d", got) }
}
