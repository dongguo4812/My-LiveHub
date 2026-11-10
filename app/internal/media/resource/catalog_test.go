package resource

import "testing"

func TestCatalogImageURLIndexTracksUpdatesAndPromotion(t *testing.T) {
	catalog := NewCatalog()
	provisional := Resource{
		PlatformID: "bilibili", SourceID: "room-1", Type: TypeEmoticon,
		ID: "https://img.test/old.png", Name: "[old]", ImageURL: "https://img.test/old.png",
	}
	if !catalog.Register(provisional) {
		t.Fatal("failed to register provisional resource")
	}
	if _, ok := catalog.FindByImageURL("bilibili", TypeEmoticon, provisional.ImageURL); !ok {
		t.Fatal("provisional URL was not indexed")
	}

	updated := provisional
	updated.ID = "emoji_1"
	if !catalog.Register(updated) {
		t.Fatal("failed to promote provisional resource")
	}
	if _, ok := catalog.FindByImageURL("bilibili", TypeEmoticon, provisional.ImageURL); !ok {
		t.Fatal("promoted resource lost its current image URL")
	}

	// A later official directory refresh can update the image URL without
	// leaving a stale URL entry or a stale provisional identity.
	updated.ImageURL = "https://img.test/new.png"
	if !catalog.Register(updated) {
		t.Fatal("failed to update official resource URL")
	}
	if _, ok := catalog.FindByImageURL("bilibili", TypeEmoticon, provisional.ImageURL); ok {
		t.Fatal("stale provisional image URL remained in the index")
	}
	got, ok := catalog.FindByImageURL("bilibili", TypeEmoticon, updated.ImageURL)
	if !ok || got.ID != "emoji_1" {
		t.Fatalf("latest URL index returned %+v, found=%v", got, ok)
	}
	if resources := catalog.List("bilibili", "room-1", TypeEmoticon); len(resources) != 1 {
		t.Fatalf("promotion/update left duplicate resources: %+v", resources)
	}
}
