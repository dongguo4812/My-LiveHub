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

func TestAdapterConnectStartsAuthenticatedStreamAndReceivesDanmaku(t *testing.T) {
	a, err := NewAdapter(newAdapterTestAuth(t), 12345, eventstream.NewBuffer())
	if err != nil { t.Fatal(err) }
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
	raw := `{"info":[[0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,{"extra":"{\"emots\":{\"[dog]\":{\"url\":\"https://i0.hdslb.com/bfs/live/dog.png\",\"width\":24,\"height\":24},\"[wow]\":{\"url\":\"https://i0.hdslb.com/bfs/live/wow.png\"}}}"}]]}`
	got := extractDanmakuEmoticons(&message.Danmaku{Raw: raw})
	if len(got) != 2 { t.Fatalf("got %d emoticons, want 2: %+v", len(got), got) }
	if got[0].Text != "[dog]" || got[0].URL != "https://i0.hdslb.com/bfs/live/dog.png" || got[0].Width != 24 || got[0].Height != 24 {
		t.Fatalf("unexpected first emoticon: %+v", got[0])
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
