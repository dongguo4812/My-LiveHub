package bilibili

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestFetchRoomEmoticonResourcesSeparatesCommonAndRoomEmoticons(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("platform") != "pc" || r.URL.Query().Get("room_id") != "12345" {
			t.Errorf("unexpected query: %s", r.URL.RawQuery)
		}
		if r.Header.Get("Cookie") != "SESSDATA=test" {
			t.Errorf("cookie was not forwarded")
		}
		_, _ = w.Write([]byte(`{
			"code": 0,
			"data": {"data": [
				{"pkg_id": 1, "pkg_name": "通用", "pkg_type": 1, "emoticons": [
					{"emoticon_id": 331, "emoticon_unique": "official_331", "emoji": "[dog]", "url": "http://i0.hdslb.com/bfs/live/common.png", "width": 60, "height": 60}
				]},
				{"pkg_id": 2, "pkg_name": "房间专属", "pkg_type": 2, "emoticons": [
					{"emoticon_id": 9, "emoticon_unique": "room_12345_9", "emoji": "[room]", "url": "https://i0.hdslb.com/bfs/live/room.png", "width": 100, "height": 100}
				]},
				{"pkg_id": 3, "pkg_name": "无唯一标识表情包 A", "pkg_type": 1, "emoticons": [
					{"emoticon_id": 331, "emoji": "[dog-a]", "url": "https://i0.hdslb.com/bfs/live/dog-a.png"}
				]},
				{"pkg_id": 4, "pkg_name": "无唯一标识表情包 B", "pkg_type": 1, "emoticons": [
					{"emoticon_id": 331, "emoji": "[dog-b]", "url": "https://i0.hdslb.com/bfs/live/dog-b.png"}
				]}
			]}
		}`))
	}))
	defer server.Close()

	// The production endpoint is fixed; rewrite the request in a test transport.
	client := server.Client()
	client.Transport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		clone := req.Clone(req.Context())
		u := *req.URL
		u.Scheme = "http"
		u.Host = server.Listener.Addr().String()
		clone.URL = &u
		return http.DefaultTransport.RoundTrip(clone)
	})
	items, err := fetchRoomEmoticonResources(context.Background(), client, 12345, "SESSDATA=test")
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 4 {
		t.Fatalf("resource count = %d, want 4: %+v", len(items), items)
	}
	wantOrder := []string{"[dog]", "[room]", "[dog-a]", "[dog-b]"}
	for i, want := range wantOrder {
		if items[i].Name != want || items[i].SortOrder != i+1 {
			t.Fatalf("item %d = (%q, order %d), want (%q, order %d)", i, items[i].Name, items[i].SortOrder, want, i+1)
		}
	}
	if items[0].SourceID != "common" && items[1].SourceID != "common" {
		t.Fatalf("common emoticon was not shared: %+v", items)
	}
	if items[0].SourceID != "12345" && items[1].SourceID != "12345" && items[2].SourceID != "12345" && items[3].SourceID != "12345" {
		t.Fatalf("room emoticon was not room-scoped: %+v", items)
	}
	fallbackIDs := map[string]bool{}
	for _, item := range items {
		if item.Name == "[dog-a]" || item.Name == "[dog-b]" {
			fallbackIDs[item.ID] = true
			if item.SourceID != "common" {
				t.Fatalf("fallback common emoticon has wrong scope: %+v", item)
			}
		}
	}
	if !fallbackIDs["pkg_3_emoticon_331"] || !fallbackIDs["pkg_4_emoticon_331"] {
		t.Fatalf("missing package-scoped fallback IDs: %+v", items)
	}
}

func TestEmoticonSourceIDFallsBackToRoomForUnknownIdentity(t *testing.T) {
	if got := emoticonSourceID(12345, ""); got != "12345" {
		t.Fatalf("source ID = %q, want room scope", got)
	}
	if got := emoticonSourceID(12345, "official_331"); got != "common" {
		t.Fatalf("source ID = %q, want common", got)
	}
	if got := emoticonSourceID(12345, "room_12345_9"); got != "12345" {
		t.Fatalf("source ID = %q, want room scope", got)
	}
}


func TestEmoticonResourceIDFallsBackToPackageAndEmoticonIDs(t *testing.T) {
	if got := emoticonResourceID("", 12, 331, "https://i0.hdslb.com/bfs/live/a.png"); got != "pkg_12_emoticon_331" {
		t.Fatalf("resource ID = %q, want package-scoped ID", got)
	}
	if got := emoticonResourceID("", 13, 331, "https://i0.hdslb.com/bfs/live/b.png"); got != "pkg_13_emoticon_331" {
		t.Fatalf("resource ID = %q, want distinct package-scoped ID", got)
	}
	if got := emoticonResourceID("emoji_331", 12, 331, "https://i0.hdslb.com/bfs/live/a.png"); got != "emoji_331" {
		t.Fatalf("unique resource ID = %q, want emoticon_unique", got)
	}
	if got := emoticonResourceID("", 0, 0, "https://i0.hdslb.com/bfs/live/a.png"); got != "https://i0.hdslb.com/bfs/live/a.png" {
		t.Fatalf("resource ID = %q, want URL fallback", got)
	}
}

func TestEmoticonSourceIDUsesUniqueAndPackageType(t *testing.T) {
	cases := []struct {
		name        string
		unique      string
		packageType int
		want        string
	}{
		{name: "common unique", unique: "emoji_331", packageType: 2, want: "common"},
		{name: "official common unique", unique: "official_331", packageType: 2, want: "common"},
		{name: "room unique", unique: "room_12345_9", packageType: 1, want: "12345"},
		{name: "common package fallback", packageType: 1, want: "common"},
		{name: "room package fallback", packageType: 2, want: "12345"},
		{name: "unknown package stays room scoped", packageType: 0, want: "12345"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := emoticonSourceIDForPackage(12345, tc.unique, tc.packageType); got != tc.want {
				t.Fatalf("source ID = %q, want %q", got, tc.want)
			}
		})
	}
}
