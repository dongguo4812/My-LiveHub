package resource

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestPersistentCatalogRestoresPartitionedResourcesAndRefreshes(t *testing.T) {
	root := filepath.Join(t.TempDir(), "media-resources")
	catalog, err := NewPersistentCatalog(root)
	if err != nil {
		t.Fatal(err)
	}

	items := []Resource{
		{PlatformID: "bilibili", SourceID: "common", Type: TypeEmoticon, ID: "official_331", Name: "通用表情", ImageURL: "https://img.test/common.png"},
		{PlatformID: "bilibili", SourceID: "12345", Type: TypeEmoticon, ID: "room_12345_9", Name: "房间表情", ImageURL: "https://img.test/room.png"},
		{PlatformID: "bilibili", SourceID: "12345", Type: TypeGift, ID: "30607", Name: "小心心", ImageURL: "https://img.test/gift.png"},
		{PlatformID: "douyin", SourceID: "common", Type: TypeEmoticon, ID: "douyin_1", Name: "抖音表情", ImageURL: "https://img.test/douyin.png"},
	}
	for _, item := range items {
		if !catalog.Register(item) {
			t.Fatalf("register failed: %+v", item)
		}
	}
	refreshedAt := time.Now().Add(-time.Hour)
	for _, entry := range []struct {
		platform string
		source   string
		kind     Type
	}{
		{"bilibili", "common", TypeEmoticon},
		{"bilibili", "12345", TypeEmoticon},
		{"bilibili", "12345", TypeGift},
	} {
		if !catalog.MarkRefreshed(entry.platform, entry.source, entry.kind, refreshedAt) {
			t.Fatalf("mark refresh failed: %+v", entry)
		}
	}
	if err := catalog.Close(); err != nil {
		t.Fatal(err)
	}

	expectedFiles := []string{
		filepath.Join(root, "bilibili", "common", "emoticon.json"),
		filepath.Join(root, "bilibili", "rooms", "12345", "emoticon.json"),
		filepath.Join(root, "bilibili", "rooms", "12345", "gift.json"),
		filepath.Join(root, "douyin", "common", "emoticon.json"),
	}
	for _, path := range expectedFiles {
		if info, err := os.Stat(path); err != nil || info.IsDir() {
			t.Fatalf("expected partition file %q, stat error: %v", path, err)
		}
	}

	restored, err := NewPersistentCatalog(root)
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
	if !restored.IsFresh("bilibili", "common", TypeEmoticon, 24*time.Hour) {
		t.Fatal("common emoticon refresh timestamp was not restored")
	}
	if !restored.IsFresh("bilibili", "12345", TypeEmoticon, 6*time.Hour) {
		t.Fatal("room emoticon refresh timestamp was not restored")
	}
	if !restored.IsFresh("bilibili", "12345", TypeGift, 24*time.Hour) {
		t.Fatal("gift refresh timestamp was not restored")
	}
}

func TestPersistentCatalogUpdatesExistingResource(t *testing.T) {
	root := filepath.Join(t.TempDir(), "media-resources")
	catalog, err := NewPersistentCatalog(root)
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

	restored, err := NewPersistentCatalog(root)
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	got := restored.List("bilibili", "common", TypeEmoticon)
	if len(got) != 1 || got[0].ImageURL != item.ImageURL {
		t.Fatalf("resource was not updated in place: %+v", got)
	}
	if got[0].FirstSeenAt.UnixNano() != firstSeen.UnixNano() {
		t.Fatalf("first-seen timestamp changed: got %v want %v", got[0].FirstSeenAt, firstSeen)
	}
}

func TestPersistentCatalogMissingRootAndCorruptFileProtection(t *testing.T) {
	root := filepath.Join(t.TempDir(), "missing", "media-resources")
	catalog, err := NewPersistentCatalog(root)
	if err != nil {
		t.Fatalf("missing catalog directory should be treated as empty: %v", err)
	}
	if err := catalog.Close(); err != nil {
		t.Fatal(err)
	}

	broken := filepath.Join(root, "bilibili", "common", "emoticon.json")
	if err := os.MkdirAll(filepath.Dir(broken), 0o700); err != nil {
		t.Fatal(err)
	}
	original := []byte("{not-json")
	if err := os.WriteFile(broken, original, 0o600); err != nil {
		t.Fatal(err)
	}
	catalog, err = NewPersistentCatalog(root)
	if err == nil {
		t.Fatal("invalid JSON should be reported")
	}
	if len(catalog.List("", "", "")) != 0 {
		t.Fatal("invalid JSON should not produce partial catalog entries")
	}
	// Even if callers continue using the returned in-memory catalog after the
	// load error, closing it must not replace the unreadable file.
	if !catalog.Register(Resource{
		PlatformID: "bilibili", SourceID: "common", Type: TypeEmoticon,
		ID: "official_1", Name: "恢复测试", ImageURL: "https://img.test/recovery.png",
	}) {
		t.Fatal("in-memory registration should remain available after load failure")
	}
	if err := catalog.Close(); err != nil {
		t.Fatal(err)
	}
	preserved, err := os.ReadFile(broken)
	if err != nil {
		t.Fatal(err)
	}
	if string(preserved) != string(original) {
		t.Fatalf("unreadable catalog was overwritten: got %q want %q", preserved, original)
	}
}
