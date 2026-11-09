package bilibili

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Akegarasu/blivedm-go/message"
	eventstream "github.com/dongguo4812/My-LiveHub/app/internal/events"
)

type fakeStreamClient struct {
	mu sync.Mutex
	cookie string
	handler func(*message.Danmaku)
	giftHandler func(*message.Gift)
	started bool
	stopCalls int
	done chan struct{}
	once sync.Once
	startErr error
	startGate <-chan struct{}
	startEntered chan struct{}
	startOnce sync.Once
}

func newFakeStreamClient(_ int) *fakeStreamClient { return &fakeStreamClient{done: make(chan struct{}), startEntered: make(chan struct{})} }
func (f *fakeStreamClient) SetCookie(cookie string) { f.mu.Lock(); f.cookie = cookie; f.mu.Unlock() }
func (f *fakeStreamClient) OnDanmaku(handler func(*message.Danmaku)) { f.mu.Lock(); f.handler = handler; f.mu.Unlock() }
func (f *fakeStreamClient) OnGift(handler func(*message.Gift)) { f.mu.Lock(); f.giftHandler = handler; f.mu.Unlock() }
func (f *fakeStreamClient) StartContext(ctx context.Context) error {
	f.startOnce.Do(func(){ close(f.startEntered) })
	if f.startGate != nil { select { case <-f.startGate: case <-ctx.Done(): return ctx.Err() } }
	f.mu.Lock(); f.started = true; err := f.startErr; f.mu.Unlock()
	return err
}
func (f *fakeStreamClient) Stop() { f.mu.Lock(); f.stopCalls++; f.mu.Unlock(); f.once.Do(func(){ close(f.done) }) }
func (f *fakeStreamClient) Wait() { <-f.done }
func (f *fakeStreamClient) Done() <-chan struct{} { return f.done }
func (f *fakeStreamClient) emit(d *message.Danmaku) { f.mu.Lock(); handler := f.handler; f.mu.Unlock(); if handler != nil { handler(d) } }
func (f *fakeStreamClient) emitGift(g *message.Gift) { f.mu.Lock(); handler := f.giftHandler; f.mu.Unlock(); if handler != nil { handler(g) } }

func newAdapterTestAuth(t *testing.T) *AuthManager {
	t.Helper()
	store := &memoryStore{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": map[string]any{"isLogin": true, "mid": json.Number("123"), "uname": "test-user"}})
	}))
	t.Cleanup(server.Close)
	client := server.Client()
	client.Transport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		clone := req.Clone(req.Context()); u := *req.URL
		u.Scheme = "http"; u.Host = strings.TrimPrefix(server.URL, "http://"); clone.URL = &u
		return http.DefaultTransport.RoundTrip(clone)
	})
	manager, err := NewAuthManager(store, client)
	if err != nil { t.Fatal(err) }
	if err := store.Save([]byte(`{"cookie":"SESSDATA=test-session","uid":"123"}`)); err != nil { t.Fatal(err) }
	return manager
}


func TestGiftCatalogRefreshCachesByRoomAndNormalizesImageURL(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.URL.Query().Get("platform") != "pc" || r.URL.Query().Get("room_id") == "" {
			t.Errorf("unexpected gift catalog query: %s", r.URL.RawQuery)
		}
		_, _ = w.Write([]byte(`{"code":0,"message":"0","data":{"gift_config":{"base_config":{"list":[{"id":30607,"img_basic":"http://i0.hdslb.com/bfs/live/gift.png"}]}}}}`))
	}))
	defer server.Close()
	catalog := newGiftCatalog(server.Client())
	catalog.endpoint = server.URL
	if err := catalog.Refresh(context.Background(), 12345); err != nil { t.Fatal(err) }
	if got := catalog.ImageURL(12345, 30607); got != "https://i0.hdslb.com/bfs/live/gift.png" {
		t.Fatalf("unexpected gift image URL: %q", got)
	}
	if err := catalog.Refresh(context.Background(), 12345); err != nil { t.Fatal(err) }
	if requests != 1 { t.Fatalf("same room refresh made %d requests, want 1", requests) }
	if err := catalog.Refresh(context.Background(), 54321); err != nil { t.Fatal(err) }
	if requests != 2 { t.Fatalf("different room refresh made %d requests, want 2", requests) }
	if got := catalog.ImageURL(54321, 30607); got == "" { t.Fatal("gift image should be cached independently for the second room") }
}

func TestAdapterReceivesGiftWithResolvedImage(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"code":0,"message":"0","data":{"gift_config":{"base_config":{"list":[{"id":30607,"img_basic":"https://i0.hdslb.com/bfs/live/gift.png"}]}}}}`))
	}))
	defer server.Close()

	a, err := NewAdapter(newAdapterTestAuth(t), 12345, eventstream.NewBuffer())
	if err != nil { t.Fatal(err) }
	a.giftCatalog = newGiftCatalog(server.Client())
	a.giftCatalog.endpoint = server.URL
	fake := newFakeStreamClient(12345)
	a.newStream = func(int) streamClient { return fake }
	if err := a.Connect(context.Background()); err != nil { t.Fatal(err) }
	fake.emitGift(&message.Gift{
		GiftId: 30607, GiftName: "小心心", Num: 2, Price: 50, TotalCoin: 100,
		Uid: 7, Uname: "viewer", Face: "https://i0.hdslb.com/bfs/face/viewer.png", Timestamp: 1_700_000_000,
	})
	got := a.events.Snapshot()
	if len(got) != 1 { t.Fatalf("got %d events, want 1: %+v", len(got), got) }
	event := got[0]
	if event.Type != "gift" || event.GiftID != 30607 || event.GiftName != "小心心" || event.GiftCount != 2 {
		t.Fatalf("unexpected gift event fields: %+v", event)
	}
	if event.GiftImageURL != "https://i0.hdslb.com/bfs/live/gift.png" {
		t.Fatalf("gift image URL was not resolved: %+v", event)
	}
	if event.UserID != 7 || event.UserName != "viewer" || event.Avatar == "" {
		t.Fatalf("gift sender metadata missing: %+v", event)
	}
	if err := a.Disconnect(context.Background()); err != nil { t.Fatal(err) }
}

func TestAdapterConnectStartsAuthenticatedStreamAndReceivesDanmaku(t *testing.T) {
	a, err := NewAdapter(newAdapterTestAuth(t), 12345, eventstream.NewBuffer())
	if err != nil { t.Fatal(err) }
	a.giftCatalog = nil
	fake := newFakeStreamClient(12345)
	a.newStream = func(int) streamClient { return fake }
	if err := a.Connect(context.Background()); err != nil { t.Fatal(err) }
	fake.mu.Lock(); started, cookie := fake.started, fake.cookie; fake.mu.Unlock()
	if !started || cookie != "SESSDATA=test-session" { t.Fatalf("stream started=%v cookie=%q", started, cookie) }
	fake.emit(&message.Danmaku{Sender: &message.User{Uid: 7, Uname: "viewer"}, Content: "hello", Timestamp: 1_700_000_000})
	got := a.events.Snapshot()
	if len(got) != 1 || got[0].RoomID != 12345 || got[0].Content != "hello" { t.Fatalf("unexpected received events: %+v", got) }
	if err := a.Disconnect(context.Background()); err != nil { t.Fatal(err) }
	if len(a.events.Snapshot()) != 1 { t.Fatal("disconnect should not clear received event history") }
}

func TestAdapterMapsDanmakuEmoticonsFromLegacyExtra(t *testing.T) {
	// Build a real heterogeneous DANMU_MSG.info array and JSON-encoded extra.
	emots := map[string]any{
		"[dog]": map[string]any{"url": "http://i0.hdslb.com/bfs/live/dog.png", "width": 24, "height": 24},
		"[wow]": map[string]any{"url": "//i0.hdslb.com/bfs/live/wow.png"},
	}
	extraJSON, err := json.Marshal(map[string]any{"emots": emots})
	if err != nil { t.Fatal(err) }
	meta := make([]any, 16)
	meta[1], meta[3], meta[4] = 1, 16777215, int64(1700000000000)
	meta[15] = map[string]any{"extra": string(extraJSON)}
	info := []any{meta, "[dog] and [wow]", []any{7, "viewer"}, []any{}, []any{}}
	packet, err := json.Marshal(map[string]any{"info": info})
	if err != nil { t.Fatal(err) }

	got := extractDanmakuEmoticons(&message.Danmaku{Raw: string(packet), Content: "[dog] and [wow]"})
	if len(got) != 2 {
		t.Fatalf("got %d emoticons, want 2: %+v", len(got), got)
	}
	byText := make(map[string]eventstream.EmoticonToken, len(got))
	for _, item := range got { byText[item.Text] = item }
	dog := byText["[dog]"]
	if dog.URL != "https://i0.hdslb.com/bfs/live/dog.png" || dog.Width != 24 || dog.Height != 24 {
		t.Fatalf("unexpected dog emoticon: %+v", dog)
	}
	if byText["[wow]"].URL != "https://i0.hdslb.com/bfs/live/wow.png" {
		t.Fatalf("unexpected wow emoticon: %+v", byText["[wow]"])
	}
}

func TestAdapterMapsStandaloneLegacyEmoticon(t *testing.T) {
	meta := make([]any, 16)
	meta[1], meta[3], meta[4] = 1, 16777215, int64(1700000000000)
	meta[12] = 1
	meta[13] = map[string]any{"url": "https://i0.hdslb.com/bfs/live/dog.png", "width": 60, "height": 60}
	info := []any{meta, "[dog]", []any{7, "viewer"}, []any{}, []any{}}
	packet, err := json.Marshal(map[string]any{"info": info})
	if err != nil { t.Fatal(err) }
	got := extractDanmakuEmoticons(&message.Danmaku{Raw: string(packet), Content: "[dog]"})
	if len(got) != 1 || got[0].URL != "https://i0.hdslb.com/bfs/live/dog.png" {
		t.Fatalf("unexpected standalone emoticon mapping: %+v", got)
	}
}

func TestAdapterMapsSingleProtobufEmoticon(t *testing.T) {
	got := extractDanmakuEmoticons(&message.Danmaku{Content: "[dog]", Emoticon: &message.Emoticon{Url: "https://i0.hdslb.com/bfs/live/dog.png", Width: 20, Height: 20}})
	if len(got) != 1 || got[0].Text != "[dog]" || got[0].URL == "" { t.Fatalf("unexpected emoticon mapping: %+v", got) }
}

func TestAdapterDisconnectStopsAndWaitsForStream(t *testing.T) {
	a, err := NewAdapter(newAdapterTestAuth(t), 12345, nil)
	if err != nil { t.Fatal(err) }
	fake := newFakeStreamClient(12345); a.newStream = func(int) streamClient { return fake }
	if err := a.Connect(context.Background()); err != nil { t.Fatal(err) }
	ctx, cancel := context.WithTimeout(context.Background(), time.Second); defer cancel()
	if err := a.Disconnect(ctx); err != nil { t.Fatal(err) }
	fake.mu.Lock(); stops := fake.stopCalls; fake.mu.Unlock()
	if stops != 1 { t.Fatalf("Stop called %d times, want 1", stops) }
	if err := a.SetRoomID(54321); err != nil { t.Fatalf("room ID should be editable after disconnect: %v", err) }
}

func TestAdapterRejectsRoomChangeWhileConnectedAndConnectIsIdempotent(t *testing.T) {
	a, err := NewAdapter(newAdapterTestAuth(t), 12345, nil)
	if err != nil { t.Fatal(err) }
	fake := newFakeStreamClient(12345); a.newStream = func(int) streamClient { return fake }
	if err := a.Connect(context.Background()); err != nil { t.Fatal(err) }
	if err := a.Connect(context.Background()); err != nil { t.Fatalf("second connect should be idempotent: %v", err) }
	if err := a.SetRoomID(54321); err == nil { t.Fatal("room ID change should be rejected while connected") }
	fake.mu.Lock(); stops := fake.stopCalls; fake.mu.Unlock()
	if stops != 0 { t.Fatalf("unexpected Stop calls after idempotent connect: %d", stops) }
	if err := a.Disconnect(context.Background()); err != nil { t.Fatal(err) }
}

func TestAdapterDisconnectCancelsPendingStartup(t *testing.T) {
	a, err := NewAdapter(newAdapterTestAuth(t), 12345, nil)
	if err != nil { t.Fatal(err) }
	gate := make(chan struct{})
	fake := newFakeStreamClient(12345); fake.startGate = gate
	a.newStream = func(int) streamClient { return fake }
	connectCtx, cancelConnect := context.WithCancel(context.Background())
	connectDone := make(chan error, 1)
	go func(){ connectDone <- a.Connect(connectCtx) }()
	select { case <-fake.startEntered: case <-time.After(time.Second): cancelConnect(); t.Fatal("stream startup did not begin") }
	cancelConnect()
	select {
	case err := <-connectDone: if err == nil { t.Fatal("expected startup cancellation") }
	case <-time.After(time.Second): t.Fatal("Connect did not return after context cancellation")
	}
	select { case <-fake.Done(): case <-time.After(time.Second): t.Fatal("cancelled stream was not stopped") }
}

func TestNormalizeEmoticonURL(t *testing.T) {
	cases := []struct { input, want string }{
		{"//i0.hdslb.com/bfs/live/dog.png", "https://i0.hdslb.com/bfs/live/dog.png"},
		{"http://i0.hdslb.com/bfs/live/dog.png", "https://i0.hdslb.com/bfs/live/dog.png"},
		{"https://i0.hdslb.com/bfs/live/dog.png", "https://i0.hdslb.com/bfs/live/dog.png"},
		{"javascript:alert(1)", ""},
		{"not a url", ""},
		{"", ""},
	}
	for _, tc := range cases {
		t.Run(tc.input, func(t *testing.T) {
			if got := normalizeEmoticonURL(tc.input); got != tc.want { t.Fatalf("normalizeEmoticonURL(%q) = %q, want %q", tc.input, got, tc.want) }
		})
	}
}
