package resource

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPersistentCatalogRestoresResourcesAndSeparatesSources(t *testing.T) {
	path := filepath.Join(t.TempDir(), "media", "catalog.json")
	catalog, err := NewPersistentCatalog(path)
	if err != nil {
		t.Fatal(err)
	}
	items := []Resource{
		{PlatformID: "bilibili", SourceID: "common", Type: TypeEmoticon, ID: "official_331", Name: "通用表情", ImageURL: "https://img.test/common.png"},
		{PlatformID: "bilibili", SourceID: "12345", Type: TypeEmoticon, ID: "room_12345_9", Name: "房间表情", ImageURL: "https://img.test/room.png"},
		{PlatformID: "bilibili", SourceID: "12345", Type: TypeGift, ID: "30607", Name: "小心心", ImageURL: "https://img.test/gift.png"},
		{PlatformID: "douyin", SourceID: "common", Type: TypeEmoticon, ID: "official_331", Name: "抖音表情", ImageURL: "https://img.test/douyin.png"},
	}
	for _, item := range items {
		if !catalog.Register(item) {
			t.Fatalf("register failed: %+v", item)
		}
	}
	if err := catalog.Close(); err != nil {
		t.Fatal(err)
	}

	restored, err := NewPersistentCatalog(path)
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	if got := len(restored.List("bilibili", "", TypeEmoticon)); got != 2 {
		t.Fatalf("Bilibili emoticon count = %d, want 2", got)
	}
	if got := len(restored.List("bilibili", "common", TypeEmoticon)); got != 1 {
		t.Fatalf("common emoticon count = %d, want 1", got)
	}
	if got := len(restored.List("bilibili", "12345", TypeEmoticon)); got != 1 {
		t.Fatalf("room emoticon count = %d, want 1", got)
	}
	if got := len(restored.List("douyin", "", TypeEmoticon)); got != 1 {
		t.Fatalf("platform isolation failed, count = %d", got)
	}
	if got := len(restored.List("bilibili", "12345", TypeGift)); got != 1 {
		t.Fatalf("gift was not restored, count = %d", got)
	}
}

func TestPersistentCatalogUpdatesExistingResource(t *testing.T) {
	path := filepath.Join(t.TempDir(), "catalog.json")
	catalog, err := NewPersistentCatalog(path)
	if err != nil {
		t.Fatal(err)
	}
	item := Resource{PlatformID: "bilibili", SourceID: "common", Type: TypeEmoticon, ID: "official_1", Name: "表情", ImageURL: "https://img.test/old.png"}
	catalog.Register(item)
	firstSeen := catalog.List("", "", "")[0].FirstSeenAt
	item.ImageURL = "https://img.test/new.png"
	catalog.Register(item)
	if err := catalog.Close(); err != nil {
		t.Fatal(err)
	}

	restored, err := NewPersistentCatalog(path)
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	got := restored.List("bilibili", "common", TypeEmoticon)
	if len(got) != 1 || got[0].ImageURL != item.ImageURL {
		t.Fatalf("resource was not updated in place: %+v", got)
	}
	if !got[0].FirstSeenAt.Equal(firstSeen) {
		t.Fatalf("first-seen timestamp changed: got %v want %v", got[0].FirstSeenAt, firstSeen)
	}
}

func TestPersistentCatalogToleratesMissingAndInvalidFiles(t *testing.T) {
	dir := t.TempDir()
	missing := filepath.Join(dir, "missing.json")
	catalog, err := NewPersistentCatalog(missing)
	if err != nil {
		t.Fatalf("missing catalog should be treated as empty: %v", err)
	}
	if err := catalog.Close(); err != nil {
		t.Fatal(err)
	}

	broken := filepath.Join(dir, "broken.json")
	if err := os.WriteFile(broken, []byte("{not-json"), 0o600); err != nil {
		t.Fatal(err)
	}
	catalog, err = NewPersistentCatalog(broken)
	if err == nil {
		t.Fatal("invalid JSON should be reported")
	}
	if len(catalog.List("", "", "")) != 0 {
		t.Fatal("invalid JSON should not produce partial catalog entries")
	}
	if err := catalog.Close(); err != nil {
		t.Fatal(err)
	}
}
