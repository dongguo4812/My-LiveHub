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
	"github.com/dongguo4812/My-LiveHub/app/internal/media/resource"
	eventstream "github.com/dongguo4812/My-LiveHub/app/internal/events"
)

type fakeStreamClient struct {
	mu sync.Mutex
	cookie string
	handler func(*message.Danmaku)
	giftHandler func(*message.Gift)
	superChatHandler func(*message.SuperChat)
	guardBuyHandler func(*message.GuardBuy)
	userToastHandler func(*message.UserToast)
	interactWordHandler func(*message.InteractWord)
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
func (f *fakeStreamClient) OnSuperChat(handler func(*message.SuperChat)) { f.mu.Lock(); f.superChatHandler = handler; f.mu.Unlock() }
func (f *fakeStreamClient) OnGuardBuy(handler func(*message.GuardBuy)) { f.mu.Lock(); f.guardBuyHandler = handler; f.mu.Unlock() }
func (f *fakeStreamClient) OnUserToast(handler func(*message.UserToast)) { f.mu.Lock(); f.userToastHandler = handler; f.mu.Unlock() }
func (f *fakeStreamClient) OnInteractWord(handler func(*message.InteractWord)) { f.mu.Lock(); f.interactWordHandler = handler; f.mu.Unlock() }
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

func (f *fakeStreamClient) emitSuperChat(sc *message.SuperChat) { f.mu.Lock(); handler := f.superChatHandler; f.mu.Unlock(); if handler != nil { handler(sc) } }
func (f *fakeStreamClient) emitGuardBuy(g *message.GuardBuy) { f.mu.Lock(); handler := f.guardBuyHandler; f.mu.Unlock(); if handler != nil { handler(g) } }
func (f *fakeStreamClient) emitUserToast(u *message.UserToast) { f.mu.Lock(); handler := f.userToastHandler; f.mu.Unlock(); if handler != nil { handler(u) } }
func (f *fakeStreamClient) emitInteractWord(i *message.InteractWord) { f.mu.Lock(); handler := f.interactWordHandler; f.mu.Unlock(); if handler != nil { handler(i) } }

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
		_, _ = w.Write([]byte(`{"code":0,"message":"0","data":{"gift_config":{"base_config":{"list":[{"id":30607,"price":1000,"img_basic":"http://i0.hdslb.com/bfs/live/gift.png"}]}}}}`))
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


func TestAdapterConnectContinuesWhenGiftCatalogUnavailable(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "temporarily unavailable", http.StatusServiceUnavailable)
	}))
	defer server.Close()

	a, err := NewAdapter(newAdapterTestAuth(t), 12345, eventstream.NewBuffer())
	if err != nil { t.Fatal(err) }
	a.giftCatalog = newGiftCatalog(server.Client())
	a.giftCatalog.endpoint = server.URL
	fake := newFakeStreamClient(12345)
	a.newStream = func(int) streamClient { return fake }
	if err := a.Connect(context.Background()); err != nil {
		t.Fatalf("gift catalog failure must not prevent live connection: %v", err)
	}
	fake.mu.Lock()
	started := fake.started
	fake.mu.Unlock()
	if !started { t.Fatal("live stream did not start after optional gift catalog failure") }
	fake.emitGift(&message.Gift{GiftId: 30607, GiftName: "小心心", Num: 1, Uid: 7, Uname: "viewer"})
	got := a.events.Snapshot()
	if len(got) != 1 || got[0].Type != "gift" || got[0].GiftImageURL != "" {
		t.Fatalf("gift event should still be delivered without an image: %+v", got)
	}
	if err := a.Disconnect(context.Background()); err != nil { t.Fatal(err) }
}

func TestAdapterReceivesGiftWithResolvedImage(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"code":0,"message":"0","data":{"gift_config":{"base_config":{"list":[{"id":30607,"name":"小心心","price":1000,"img_basic":"https://i0.hdslb.com/bfs/live/gift.png"}]}}}}`))
	}))
	defer server.Close()

	a, err := NewAdapter(newAdapterTestAuth(t), 12345, eventstream.NewBuffer())
	if err != nil { t.Fatal(err) }
	a.giftCatalog = newGiftCatalog(server.Client())
	a.giftCatalog.endpoint = server.URL
	catalog := resource.NewCatalog()
	a.SetMediaCatalog(catalog)
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
	if event.Type != "gift" || event.GiftID != 30607 || event.GiftResourceID != "30607" || event.GiftName != "小心心" || event.GiftCount != 2 {
		t.Fatalf("unexpected gift event fields: %+v", event)
	}
	deadline:=time.Now().Add(time.Second)
	for len(catalog.List("bilibili","12345",resource.TypeGift))==0&&time.Now().Before(deadline){time.Sleep(5*time.Millisecond)}
	items:=catalog.List("bilibili","12345",resource.TypeGift)
	if len(items)!=1||items[0].ImageURL!="https://i0.hdslb.com/bfs/live/gift.png" || items[0].UnitPrice!=1{t.Fatalf("catalog missing gift metadata or price: %+v",items)}
	if event.UserID != 7 || event.UserName != "viewer" || event.Avatar == "" {
		t.Fatalf("gift sender metadata missing: %+v", event)
	}
	if err := a.Disconnect(context.Background()); err != nil { t.Fatal(err) }
}


func TestAdapterConnectDoesNotWaitForGiftCatalog(t *testing.T){
	entered:=make(chan struct{},1);release:=make(chan struct{})
	server:=httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter,r *http.Request){select{case entered<-struct{}{}:default:};select{case <-release:case <-r.Context().Done():return};_,_=w.Write([]byte(`{"code":0,"data":{"gift_config":{"base_config":{"list":[{"id":1,"name":"gift","img_basic":"https://i0.hdslb.com/gift.png"}]}}}}`))}))
	defer server.Close();defer close(release)
	a,err:=NewAdapter(newAdapterTestAuth(t),12345,eventstream.NewBuffer());if err!=nil{t.Fatal(err)}
	a.giftCatalog=newGiftCatalog(server.Client());a.giftCatalog.endpoint=server.URL
	fake:=newFakeStreamClient(12345);a.newStream=func(int)streamClient{return fake}
	start:=time.Now();if err:=a.Connect(context.Background());err!=nil{t.Fatal(err)}
	if time.Since(start)>500*time.Millisecond{t.Fatal("Connect waited for optional gift catalog")}
	select{case <-entered:case <-time.After(time.Second):t.Fatal("background catalog refresh did not start")}
	if err:=a.Disconnect(context.Background());err!=nil{t.Fatal(err)}
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


func TestAdapterReceivesSCGuardFollowAndWelcome(t *testing.T) {
	a, err := NewAdapter(newAdapterTestAuth(t), 12345, eventstream.NewBuffer())
	if err != nil { t.Fatal(err) }
	a.giftCatalog = nil
	fake := newFakeStreamClient(12345)
	a.newStream = func(int) streamClient { return fake }
	if err := a.Connect(context.Background()); err != nil { t.Fatal(err) }

	sc := &message.SuperChat{Uid: 7, Price: 30, Message: "支持主播", StartTime: 1_700_000_000, Time: 60}
	sc.UserInfo.Uname, sc.UserInfo.Face = "viewer", "https://img.test/avatar.png"
	fake.emitSuperChat(sc)
	fake.emitGuardBuy(&message.GuardBuy{Uid: 8, Username: "captain", GuardLevel: 3, Num: 2, Price: 138000, StartTime: 1_700_000_001})
	// The matching toast is the richer duplicate representation of the same order.
	fake.emitUserToast(&message.UserToast{Uid: 8, Username: "captain", Face: "https://img.test/captain.png", GuardLevel: 3, Num: 2, Price: 138000, StartTime: 1_700_000_001})
	fake.emitInteractWord(&message.InteractWord{MsgType: 2, Timestamp: 1_700_000_002, User: &message.User{Uid: 9, Uname: "follower"}})
	fake.emitInteractWord(&message.InteractWord{MsgType: 1, Timestamp: 1_700_000_003, User: &message.User{Uid: 10, Uname: "visitor"}})
	// Share and other interaction types are intentionally outside this four-event scope.
	fake.emitInteractWord(&message.InteractWord{MsgType: 3, Timestamp: 1_700_000_004, User: &message.User{Uid: 11, Uname: "sharer"}})

	got := a.events.Snapshot()
	if len(got) != 4 { t.Fatalf("got %d events, want SC + guard + follow + welcome: %+v", len(got), got) }
	if got[0].Type != "super_chat" || got[0].Amount != 30 || got[0].Duration != 60 || got[0].Content != "支持主播" || got[0].UserName != "viewer" {
		t.Fatalf("unexpected SC event: %+v", got[0])
	}
	if got[1].Type != "guard" || got[1].GuardLevel != 3 || got[1].Months != 2 || got[1].Amount != 138 || !strings.Contains(got[1].Content, "舰长") {
		t.Fatalf("unexpected guard event: %+v", got[1])
	}
	if got[2].Type != "follow" || got[2].UserID != 9 { t.Fatalf("unexpected follow event: %+v", got[2]) }
	if got[3].Type != "welcome" || got[3].UserID != 10 { t.Fatalf("unexpected welcome event: %+v", got[3]) }
	if err := a.Disconnect(context.Background()); err != nil { t.Fatal(err) }
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


func TestGiftCatalogUses24HourTTL(t *testing.T) {
	catalog := newGiftCatalog(nil)
	if catalog.ttl != 24*time.Hour {
		t.Fatalf("gift catalog TTL = %v, want 24h", catalog.ttl)
	}
}

func TestAdapterSkipsFreshPersistedCatalogRefreshes(t *testing.T) {
	requests := make(chan struct{}, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case requests <- struct{}{}:
		default:
		}
		_, _ = w.Write([]byte(`{"code":0,"data":{"gift_config":{"base_config":{"list":[{"id":30607,"name":"小心心","price":1000,"img_basic":"https://i0.hdslb.com/bfs/live/gift.png"}]}}}}`))
	}))
	defer server.Close()

	a, err := NewAdapter(newAdapterTestAuth(t), 12345, eventstream.NewBuffer())
	if err != nil {
		t.Fatal(err)
	}
	a.giftCatalog = newGiftCatalog(server.Client())
	a.giftCatalog.endpoint = server.URL
	catalog := resource.NewCatalog()
	catalog.Register(resource.Resource{
		PlatformID: "bilibili", SourceID: "12345", Type: resource.TypeGift,
		ID: "30607", Name: "小心心", ImageURL: "https://i0.hdslb.com/bfs/live/gift.png", UnitPrice: 1,
	})
	now := time.Now()
	catalog.MarkRefreshed("bilibili", "12345", resource.TypeGift, now)
	catalog.MarkRefreshed("bilibili", "common", resource.TypeEmoticon, now)
	catalog.MarkRefreshed("bilibili", "12345", resource.TypeEmoticon, now)
	a.SetMediaCatalog(catalog)

	fake := newFakeStreamClient(12345)
	a.newStream = func(int) streamClient { return fake }
	if err := a.Connect(context.Background()); err != nil {
		t.Fatal(err)
	}
	time.Sleep(50 * time.Millisecond)
	select {
	case <-requests:
		t.Fatal("fresh persisted gift catalog triggered a network request")
	default:
	}

	fake.emitGift(&message.Gift{GiftId: 30607, GiftName: "小心心", Num: 1, Uid: 7, Uname: "viewer"})
	events := a.events.Snapshot()
	if len(events) != 1 || events[0].GiftImageURL != "https://i0.hdslb.com/bfs/live/gift.png" {
		t.Fatalf("persisted gift image was not used immediately: %+v", events)
	}
	if err := a.Disconnect(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := catalog.Close(); err != nil {
		t.Fatal(err)
	}
}


func TestAdapterResolvesLiveEmoticonWithoutIDFromCatalogURL(t *testing.T) {
	catalog := resource.NewCatalog()
	catalog.Register(resource.Resource{
		PlatformID: "bilibili", SourceID: "common", Type: resource.TypeEmoticon,
		ID: "emoji_331", Name: "[dog]", ImageURL: "https://i0.hdslb.com/bfs/live/dog.png",
	})
	now := time.Now()
	catalog.MarkRefreshed("bilibili", "common", resource.TypeEmoticon, now)
	catalog.MarkRefreshed("bilibili", "12345", resource.TypeEmoticon, now)

	a, err := NewAdapter(newAdapterTestAuth(t), 12345, eventstream.NewBuffer())
	if err != nil {
		t.Fatal(err)
	}
	a.giftCatalog = nil
	a.SetMediaCatalog(catalog)
	fake := newFakeStreamClient(12345)
	a.newStream = func(int) streamClient { return fake }
	if err := a.Connect(context.Background()); err != nil {
		t.Fatal(err)
	}

	extraJSON, err := json.Marshal(map[string]any{
		"emots": map[string]any{
			"[dog]": map[string]any{"url": "https://i0.hdslb.com/bfs/live/dog.png", "width": 24, "height": 24},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	meta := make([]any, 16)
	meta[15] = map[string]any{"extra": string(extraJSON)}
	packet, err := json.Marshal(map[string]any{"info": []any{meta, "[dog]", []any{7, "viewer"}, []any{}, []any{}}})
	if err != nil {
		t.Fatal(err)
	}
	fake.emit(&message.Danmaku{
		Raw: string(packet), Content: "[dog]",
		Sender: &message.User{Uid: 7, Uname: "viewer"},
	})

	events := a.events.Snapshot()
	if len(events) != 1 || len(events[0].Emoticons) != 1 {
		t.Fatalf("unexpected live emoticon event: %+v", events)
	}
	if got := events[0].Emoticons[0].ResourceID; got != "common:emoji_331" {
		t.Fatalf("resolved resource ID = %q, want common:emoji_331", got)
	}
	if err := a.Disconnect(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := catalog.Close(); err != nil {
		t.Fatal(err)
	}
}


// TestAdapterResolvesNumericLiveEmoticonIDByCatalogURL verifies that a
// numeric live ID without package metadata does not force a room scope.
func TestAdapterResolvesNumericLiveEmoticonIDByCatalogURL(t *testing.T) {
	catalog := resource.NewCatalog()
	catalog.Register(resource.Resource{
		PlatformID: "bilibili", SourceID: "common", Type: resource.TypeEmoticon,
		ID: "emoji_331", Name: "[dog]", ImageURL: "https://i0.hdslb.com/bfs/live/dog.png",
	})
	catalog.MarkRefreshed("bilibili", "common", resource.TypeEmoticon, time.Now())
	catalog.MarkRefreshed("bilibili", "12345", resource.TypeEmoticon, time.Now())

	a, err := NewAdapter(newAdapterTestAuth(t), 12345, eventstream.NewBuffer())
	if err != nil { t.Fatal(err) }
	a.giftCatalog = nil
	a.SetMediaCatalog(catalog)
	fake := newFakeStreamClient(12345)
	a.newStream = func(int) streamClient { return fake }
	if err := a.Connect(context.Background()); err != nil { t.Fatal(err) }

	extraJSON, err := json.Marshal(map[string]any{
		"emots": map[string]any{
			"[dog]": map[string]any{
				"url": "https://i0.hdslb.com/bfs/live/dog.png",
				"emoticon_id": 331,
			},
		},
	})
	if err != nil { t.Fatal(err) }
	meta := make([]any, 16)
	meta[15] = map[string]any{"extra": string(extraJSON)}
	packet, err := json.Marshal(map[string]any{"info": []any{meta, "[dog]", []any{7, "viewer"}, []any{}, []any{}}})
	if err != nil { t.Fatal(err) }
	fake.emit(&message.Danmaku{Raw: string(packet), Content: "[dog]", Sender: &message.User{Uid: 7, Uname: "viewer"}})

	events := a.events.Snapshot()
	if len(events) != 1 || len(events[0].Emoticons) != 1 {
		t.Fatalf("unexpected live emoticon event: %+v", events)
	}
	if got := events[0].Emoticons[0].ResourceID; got != "common:emoji_331" {
		t.Fatalf("resolved resource ID = %q, want common:emoji_331", got)
	}
	if err := a.Disconnect(context.Background()); err != nil { t.Fatal(err) }
	if err := catalog.Close(); err != nil { t.Fatal(err) }
}

// TestAdapterResolvesLiveEmoticonToRoomResourceByURL verifies room resources
// are selected when a live event has no explicit scope identifier.
func TestAdapterResolvesLiveEmoticonToRoomResourceByURL(t *testing.T) {
	catalog := resource.NewCatalog()
	catalog.Register(resource.Resource{
		PlatformID: "bilibili", SourceID: "12345", Type: resource.TypeEmoticon,
		ID: "room_12345_9", Name: "[dog]", ImageURL: "https://i0.hdslb.com/bfs/live/room-dog.png",
	})
	catalog.MarkRefreshed("bilibili", "common", resource.TypeEmoticon, time.Now())
	catalog.MarkRefreshed("bilibili", "12345", resource.TypeEmoticon, time.Now())

	a, err := NewAdapter(newAdapterTestAuth(t), 12345, eventstream.NewBuffer())
	if err != nil { t.Fatal(err) }
	a.giftCatalog = nil
	a.SetMediaCatalog(catalog)
	fake := newFakeStreamClient(12345)
	a.newStream = func(int) streamClient { return fake }
	if err := a.Connect(context.Background()); err != nil { t.Fatal(err) }

	extraJSON, err := json.Marshal(map[string]any{
		"emots": map[string]any{
			"[dog]": map[string]any{"url": "https://i0.hdslb.com/bfs/live/room-dog.png"},
		},
	})
	if err != nil { t.Fatal(err) }
	meta := make([]any, 16)
	meta[15] = map[string]any{"extra": string(extraJSON)}
	packet, err := json.Marshal(map[string]any{"info": []any{meta, "[dog]", []any{7, "viewer"}, []any{}, []any{}}})
	if err != nil { t.Fatal(err) }
	fake.emit(&message.Danmaku{Raw: string(packet), Content: "[dog]", Sender: &message.User{Uid: 7, Uname: "viewer"}})

	events := a.events.Snapshot()
	if len(events) != 1 || len(events[0].Emoticons) != 1 {
		t.Fatalf("unexpected live emoticon event: %+v", events)
	}
	if got := events[0].Emoticons[0].ResourceID; got != "12345:room_12345_9" {
		t.Fatalf("resolved resource ID = %q, want 12345:room_12345_9", got)
	}
	if err := a.Disconnect(context.Background()); err != nil { t.Fatal(err) }
	if err := catalog.Close(); err != nil { t.Fatal(err) }
}
