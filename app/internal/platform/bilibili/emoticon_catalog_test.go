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
	if len(items) != 2 {
		t.Fatalf("resource count = %d, want 2: %+v", len(items), items)
	}
	if items[0].SourceID != "common" && items[1].SourceID != "common" {
		t.Fatalf("common emoticon was not shared: %+v", items)
	}
	if items[0].SourceID != "12345" && items[1].SourceID != "12345" {
		t.Fatalf("room emoticon was not room-scoped: %+v", items)
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
