package music

import "testing"

func TestSourceCatalogContainsFiveBuiltInPlatformsInStableOrder(t *testing.T) {
	sources := SourceCatalog()
	want := []struct{ id, name string }{
		{LXSearchTX, "QQ音乐"},
		{LXSearchKW, "酷我音乐"},
		{LXSearchKG, "酷狗音乐"},
		{LXSearchWY, "网易云音乐"},
		{LXSearchMG, "咪咕音乐"},
	}
	if len(sources) != len(want) {
		t.Fatalf("SourceCatalog() returned %d sources, want %d", len(sources), len(want))
	}
	for i, expected := range want {
		if sources[i].ID != expected.id || sources[i].Name != expected.name {
			t.Errorf("source[%d] = (%q, %q), want (%q, %q)", i, sources[i].ID, sources[i].Name, expected.id, expected.name)
		}
		if !sources[i].Capabilities.Search || !sources[i].Enabled || !sources[i].Available {
			t.Errorf("source[%d] should advertise enabled, available search capability: %+v", i, sources[i])
		}
	}
}

func TestSourceCatalogDoesNotDependOnGoSearchClient(t *testing.T) {
	sources := SourceCatalog()
	if len(sources) != 5 {
		t.Fatalf("source catalog returned %d entries, want 5", len(sources))
	}
}
