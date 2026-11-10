package resource

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestPersistentCatalogRestoresPartitionedResourcesAndRefreshes(t *testing.T) {
	root := filepath.Join(t.TempDir(), "media-resources")
	catalog, err := NewPersistentCatalog(root)
	if err != nil {
		t.Fatal(err)
	}
	if !catalog.SetPlatformScopeDirectory("bilibili", "rooms") {
		t.Fatal("Bilibili scope directory configuration failed")
	}
	if !catalog.SetPlatformScopeDirectory("douyin", "houses") {
		t.Fatal("Douyin scope directory configuration failed")
	}

	items := []Resource{
		{PlatformID: "bilibili", SourceID: "common", Type: TypeEmoticon, ID: "official_331", Name: "通用表情", ImageURL: "https://img.test/common.png"},
		{PlatformID: "bilibili", SourceID: "12345", Type: TypeEmoticon, ID: "room_12345_9", Name: "房间表情", ImageURL: "https://img.test/room.png"},
		{PlatformID: "bilibili", SourceID: "12345", Type: TypeGift, ID: "30607", Name: "小心心", ImageURL: "https://img.test/gift.png"},
		{PlatformID: "douyin", SourceID: "common", Type: TypeEmoticon, ID: "douyin_1", Name: "抖音表情", ImageURL: "https://img.test/douyin.png"},
		{PlatformID: "douyin", SourceID: "live-67890", Type: TypeEmoticon, ID: "douyin_live_1", Name: "抖音直播表情", ImageURL: "https://img.test/douyin-live.png"},
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
		filepath.Join(root, "douyin", "houses", "live-67890", "emoticon.json"),
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
	if got := len(restored.List("douyin", "", TypeEmoticon)); got != 2 {
		t.Fatalf("platform isolation failed, count = %d, want 2", got)
	}
	if got := len(restored.List("douyin", "live-67890", TypeEmoticon)); got != 1 {
		t.Fatalf("platform-specific scope resource count = %d, want 1", got)
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



func TestPersistentCatalogDiscardsPartialLoadWhenLaterPartitionIsCorrupt(t *testing.T) {
	root := filepath.Join(t.TempDir(), "media-resources")
	validPath := filepath.Join(root, "bilibili", "common", "emoticon.json")
	corruptPath := filepath.Join(root, "z-platform", "common", "gift.json")
	if err := os.MkdirAll(filepath.Dir(validPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(corruptPath), 0o700); err != nil {
		t.Fatal(err)
	}

	valid := catalogFile{
		Version: catalogFileVersion,
		Resources: []Resource{{
			PlatformID: "bilibili", SourceID: "common", Type: TypeEmoticon,
			ID: "official_valid", Name: "有效资源", ImageURL: "https://img.test/valid.png",
		}},
		Refreshes: map[string]time.Time{
			refreshIdentity("bilibili", "common", TypeEmoticon): time.Now(),
		},
	}
	validBytes, err := json.Marshal(valid)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(validPath, validBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	corruptBytes := []byte("{broken-json")
	if err := os.WriteFile(corruptPath, corruptBytes, 0o600); err != nil {
		t.Fatal(err)
	}

	catalog, err := NewPersistentCatalog(root)
	if err == nil {
		t.Fatal("corrupt later partition should fail catalog loading")
	}
	if catalog == nil {
		t.Fatal("load failure should return a usable empty in-memory catalog")
	}
	if got := len(catalog.List("", "", "")); got != 0 {
		t.Fatalf("partial resources leaked from staging catalog: got %d entries", got)
	}
	if catalog.IsFresh("bilibili", "common", TypeEmoticon, time.Hour) {
		t.Fatal("refresh markers leaked from staging catalog")
	}

	// The returned empty catalog remains usable, but has no persistence writer.
	if !catalog.Register(Resource{
		PlatformID: "bilibili", SourceID: "common", Type: TypeEmoticon,
		ID: "recovery", Name: "恢复测试", ImageURL: "https://img.test/recovery.png",
	}) {
		t.Fatal("empty catalog should remain usable after load failure")
	}
	if err := catalog.Close(); err != nil {
		t.Fatal(err)
	}
	gotValid, err := os.ReadFile(validPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(gotValid) != string(validBytes) {
		t.Fatal("valid partition was modified after another partition failed to load")
	}
	gotCorrupt, err := os.ReadFile(corruptPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(gotCorrupt) != string(corruptBytes) {
		t.Fatal("corrupt partition was modified after load failure")
	}
}


func TestPersistentCatalogPreservesEmoticonDirectoryOrder(t *testing.T) {
	root := filepath.Join(t.TempDir(), "media-resources")
	catalog, err := NewPersistentCatalog(root)
	if err != nil {
		t.Fatal(err)
	}
	// Deliberately make alphabetical order differ from the platform order.
	items := []Resource{
		{PlatformID: "bilibili", SourceID: "common", Type: TypeEmoticon, ID: "emoji_2", Name: "[z-last]", ImageURL: "https://img.test/z.png", SortOrder: 2},
		{PlatformID: "bilibili", SourceID: "12345", Type: TypeEmoticon, ID: "room_1", Name: "[a-first]", ImageURL: "https://img.test/a.png", SortOrder: 1},
		{PlatformID: "bilibili", SourceID: "common", Type: TypeEmoticon, ID: "emoji_3", Name: "[m-third]", ImageURL: "https://img.test/m.png", SortOrder: 3},
	}
	for _, item := range items {
		if !catalog.Register(item) {
			t.Fatalf("register failed: %+v", item)
		}
	}
	got := catalog.List("bilibili", "", TypeEmoticon)
	if len(got) != len(items) {
		t.Fatalf("resource count = %d, want %d", len(got), len(items))
	}
	for i, want := range []string{"[a-first]", "[z-last]", "[m-third]"} {
		if got[i].Name != want {
			t.Fatalf("before persistence item %d = %q, want %q", i, got[i].Name, want)
		}
	}
	if err := catalog.Close(); err != nil {
		t.Fatal(err)
	}

	restored, err := NewPersistentCatalog(root)
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	got = restored.List("bilibili", "", TypeEmoticon)
	for i, want := range []string{"[a-first]", "[z-last]", "[m-third]"} {
		if got[i].Name != want {
			t.Fatalf("after restart item %d = %q, want %q", i, got[i].Name, want)
		}
		if got[i].SortOrder != i+1 {
			t.Fatalf("after restart item %d sort order = %d, want %d", i, got[i].SortOrder, i+1)
		}
	}
}


func TestPersistentCatalogConcurrentAccessAndCloseFlushesLatestSnapshot(t *testing.T) {
	root := filepath.Join(t.TempDir(), "media-resources")
	catalog, err := NewPersistentCatalog(root)
	if err != nil {
		t.Fatal(err)
	}

	const (
		writers       = 8
		resourcesEach = 40
	)
	var workers sync.WaitGroup
	for worker := 0; worker < writers; worker++ {
		worker := worker
		workers.Add(1)
		go func() {
			defer workers.Done()
			for index := 0; index < resourcesEach; index++ {
				id := fmt.Sprintf("worker-%d-resource-%d", worker, index)
				item := Resource{
					PlatformID: "bilibili",
					SourceID:   fmt.Sprintf("room-%d", worker),
					Type:       TypeEmoticon,
					ID:         id,
					Name:       id,
					ImageURL:   "https://img.test/" + id + ".png",
				}
				if !catalog.Register(item) {
					t.Errorf("concurrent register failed for %q", id)
					return
				}
				if index == 0 && !catalog.MarkRefreshed("bilibili", item.SourceID, TypeEmoticon, time.Now()) {
					t.Errorf("concurrent refresh mark failed for %q", item.SourceID)
					return
				}
			}
		}()
	}

	// Exercise read paths while registrations and refresh markers are changing.
	stopReaders := make(chan struct{})
	readDone := make(chan struct{})
	go func() {
		defer close(readDone)
		for {
			select {
			case <-stopReaders:
				return
			default:
				_ = catalog.List("bilibili", "", TypeEmoticon)
				_, _ = catalog.FindByImageURL("bilibili", TypeEmoticon, "https://img.test/worker-0-resource-0.png")
			}
		}
	}()

	workers.Wait()
	// Close must stop accepting mutations and flush the complete final snapshot,
	// even when the debounce timer has not fired yet.
	if err := catalog.Close(); err != nil {
		t.Fatal(err)
	}
	close(stopReaders)
	<-readDone

	if catalog.Register(Resource{
		PlatformID: "bilibili", SourceID: "late", Type: TypeEmoticon,
		ID: "late", Name: "late", ImageURL: "https://img.test/late.png",
	}) {
		t.Fatal("catalog accepted a resource after close")
	}
	if catalog.MarkRefreshed("bilibili", "late", TypeEmoticon, time.Now()) {
		t.Fatal("catalog accepted a refresh marker after close")
	}
	if err := catalog.Close(); err != nil {
		t.Fatalf("repeated close should be safe: %v", err)
	}

	restored, err := NewPersistentCatalog(root)
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	want := writers * resourcesEach
	if got := len(restored.List("bilibili", "", TypeEmoticon)); got != want {
		t.Fatalf("restored resource count = %d, want %d", got, want)
	}
	for worker := 0; worker < writers; worker++ {
		sourceID := fmt.Sprintf("room-%d", worker)
		if !restored.IsFresh("bilibili", sourceID, TypeEmoticon, time.Hour) {
			t.Errorf("refresh marker for %q was not persisted", sourceID)
		}
	}
}
