package resource

import "testing"

func TestCatalogFindUsesCompleteResourceIdentity(t *testing.T) {
	catalog := NewCatalog()
	items := []Resource{
		{PlatformID: "bilibili", SourceID: "12345", Type: TypeGift, ID: "30607", Name: "room gift", ImageURL: "https://img.test/room.png"},
		{PlatformID: "bilibili", SourceID: "54321", Type: TypeGift, ID: "30607", Name: "other room gift", ImageURL: "https://img.test/other-room.png"},
		{PlatformID: "douyin", SourceID: "12345", Type: TypeGift, ID: "30607", Name: "other platform gift", ImageURL: "https://img.test/douyin.png"},
		{PlatformID: "bilibili", SourceID: "12345", Type: TypeEmoticon, ID: "30607", Name: "emoticon", ImageURL: "https://img.test/emoticon.png"},
	}
	for _, item := range items {
		if !catalog.Register(item) { t.Fatalf("failed to register %+v", item) }
	}
	got, ok := catalog.Find("bilibili", "12345", TypeGift, "30607")
	if !ok || got.ImageURL != items[0].ImageURL { t.Fatalf("Find returned %+v, found=%v", got, ok) }
	for _, query := range [][4]string{
		{"bilibili", "99999", string(TypeGift), "30607"},
		{"douyin", "12345", string(TypeGift), "30607"},
		{"bilibili", "12345", string(TypeEmoticon), "30607"},
		{"bilibili", "12345", string(TypeGift), "missing"},
	} {
		if _, ok := catalog.Find(query[0], query[1], Type(query[2]), query[3]); ok {
			t.Fatalf("unexpected match for identity %v", query)
		}
	}
}

func TestCatalogSeparatesPlatformAndSource(t *testing.T) {
	c := NewCatalog()
	for _, r := range []Resource{
		{PlatformID: "bilibili", SourceID: "room-1", Type: TypeGift, ID: "1", Name: "gift", ImageURL: "https://img.test/a.png"},
		{PlatformID: "douyin", SourceID: "room-1", Type: TypeGift, ID: "1", Name: "gift", ImageURL: "https://img.test/b.png"},
		{PlatformID: "bilibili", SourceID: "room-2", Type: TypeGift, ID: "1", Name: "gift", ImageURL: "https://img.test/c.png"},
	} {
		if !c.Register(r) { t.Fatalf("register failed: %+v", r) }
	}
	if n := len(c.List("bilibili", "room-1", TypeGift)); n != 1 { t.Fatalf("filtered count=%d", n) }
	if n := len(c.List("", "", TypeGift)); n != 3 { t.Fatalf("all count=%d", n) }
}

func TestCatalogUpdatePreservesFirstSeen(t *testing.T) {
	c := NewCatalog()
	r := Resource{PlatformID: "bilibili", SourceID: "1", Type: TypeGift, ID: "9", Name: "gift", ImageURL: "https://img.test/old.png"}
	c.Register(r)
	old := c.List("bilibili", "1", TypeGift)[0]
	r.ImageURL = "https://img.test/new.png"
	c.Register(r)
	got := c.List("bilibili", "1", TypeGift)[0]
	if got.ImageURL != r.ImageURL || !got.FirstSeenAt.Equal(old.FirstSeenAt) { t.Fatalf("unexpected update: %+v", got) }
}

func TestCatalogRejectsInvalid(t *testing.T) {
	if NewCatalog().Register(Resource{PlatformID: "bilibili", Type: TypeGift, ID: "1"}) { t.Fatal("expected invalid resource rejection") }
}

func TestCatalogFindByImageURLPrefersCommonScope(t *testing.T) {
	catalog := NewCatalog()
	for _, item := range []Resource{
		{PlatformID: "bilibili", SourceID: "12345", Type: TypeEmoticon, ID: "room_9", Name: "[dog]", ImageURL: "https://img.test/dog.png"},
		{PlatformID: "bilibili", SourceID: "common", Type: TypeEmoticon, ID: "emoji_9", Name: "[dog]", ImageURL: "https://img.test/dog.png"},
		{PlatformID: "douyin", SourceID: "common", Type: TypeEmoticon, ID: "other", Name: "[dog]", ImageURL: "https://img.test/dog.png"},
	} {
		if !catalog.Register(item) { t.Fatalf("failed to register resource: %+v", item) }
	}
	got, ok := catalog.FindByImageURL("bilibili", TypeEmoticon, "https://img.test/dog.png")
	if !ok { t.Fatal("expected URL to resolve to a catalog resource") }
	if got.SourceID != "common" || got.ID != "emoji_9" { t.Fatalf("resolved resource = %+v, want common emoji_9", got) }
	if _, ok := catalog.FindByImageURL("bilibili", TypeGift, "https://img.test/dog.png"); ok { t.Fatal("emoticon URL must not resolve as a gift") }
	if _, ok := catalog.FindByImageURL("twitch", TypeEmoticon, "https://img.test/dog.png"); ok { t.Fatal("URL must not resolve across platforms") }
}

func TestCatalogPromotesProvisionalURLIdentityToOfficialID(t *testing.T) {
	catalog := NewCatalog()
	provisional := Resource{PlatformID: "bilibili", SourceID: "common", Type: TypeEmoticon, ID: "https://img.test/dog.png", Name: "[dog]", ImageURL: "https://img.test/dog.png"}
	if !catalog.Register(provisional) { t.Fatal("failed to register provisional emoticon") }
	firstSeen := catalog.List("bilibili", "common", TypeEmoticon)[0].FirstSeenAt
	official := Resource{PlatformID: "bilibili", SourceID: "common", Type: TypeEmoticon, ID: "emoji_331", Name: "[dog]", ImageURL: "https://img.test/dog.png"}
	if !catalog.Register(official) { t.Fatal("failed to register official emoticon") }
	got := catalog.List("bilibili", "common", TypeEmoticon)
	if len(got) != 1 { t.Fatalf("catalog contains %d resources, want 1: %+v", len(got), got) }
	if got[0].ID != "emoji_331" { t.Fatalf("resource ID = %q, want official ID", got[0].ID) }
	if !got[0].FirstSeenAt.Equal(firstSeen) { t.Fatalf("first-seen timestamp changed: got %v want %v", got[0].FirstSeenAt, firstSeen) }
}

func TestCatalogKeepsResourcesMissingFromLaterDirectoryRefresh(t *testing.T) {
	catalog := NewCatalog()
	removed := Resource{PlatformID: "bilibili", SourceID: "12345", Type: TypeEmoticon, ID: "room_12345_9", Name: "[old]", ImageURL: "https://img.test/old.png"}
	current := Resource{PlatformID: "bilibili", SourceID: "12345", Type: TypeEmoticon, ID: "room_12345_10", Name: "[new]", ImageURL: "https://img.test/new.png"}
	if !catalog.Register(removed) || !catalog.Register(current) { t.Fatal("failed to register initial directory") }
	nextRefresh := Resource{PlatformID: "bilibili", SourceID: "12345", Type: TypeEmoticon, ID: "room_12345_10", Name: "[renamed]", ImageURL: "https://img.test/new-v2.png"}
	if !catalog.Register(nextRefresh) { t.Fatal("failed to register refreshed resource") }
	got := catalog.List("bilibili", "12345", TypeEmoticon)
	if len(got) != 2 { t.Fatalf("catalog contains %d resources, want historical + current: %+v", len(got), got) }
	var foundRemoved bool
	for _, item := range got { if item.ID == removed.ID && item.ImageURL == removed.ImageURL { foundRemoved = true } }
	if !foundRemoved { t.Fatal("resource missing from latest directory was deleted") }
}

func TestCatalogImageURLIndexTracksUpdatesAndPromotion(t *testing.T) {
	catalog := NewCatalog()
	provisional := Resource{PlatformID: "bilibili", SourceID: "room-1", Type: TypeEmoticon, ID: "https://img.test/old.png", Name: "[old]", ImageURL: "https://img.test/old.png"}
	if !catalog.Register(provisional) { t.Fatal("failed to register provisional resource") }
	if _, ok := catalog.FindByImageURL("bilibili", TypeEmoticon, provisional.ImageURL); !ok { t.Fatal("provisional URL was not indexed") }

	updated := provisional
	updated.ID = "emoji_1"
	if !catalog.Register(updated) { t.Fatal("failed to promote provisional resource") }
	if _, ok := catalog.FindByImageURL("bilibili", TypeEmoticon, provisional.ImageURL); !ok { t.Fatal("promoted resource lost its current image URL") }

	updated.ImageURL = "https://img.test/new.png"
	if !catalog.Register(updated) { t.Fatal("failed to update official resource URL") }
	if _, ok := catalog.FindByImageURL("bilibili", TypeEmoticon, provisional.ImageURL); ok { t.Fatal("stale provisional image URL remained in the index") }
	got, ok := catalog.FindByImageURL("bilibili", TypeEmoticon, updated.ImageURL)
	if !ok || got.ID != "emoji_1" { t.Fatalf("latest URL index returned %+v, found=%v", got, ok) }
	if resources := catalog.List("bilibili", "room-1", TypeEmoticon); len(resources) != 1 { t.Fatalf("promotion/update left duplicate resources: %+v", resources) }
}
