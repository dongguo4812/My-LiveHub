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
}

func newFakeStreamClient(_ int) *fakeStreamClient {
	return &fakeStreamClient{done: make(chan struct{})}
}
func (f *fakeStreamClient) SetCookie(cookie string) { f.mu.Lock(); f.cookie = cookie; f.mu.Unlock() }
func (f *fakeStreamClient) OnDanmaku(handler func(*message.Danmaku)) { f.mu.Lock(); f.handler = handler; f.mu.Unlock() }
func (f *fakeStreamClient) StartContext(ctx context.Context) error {
	if f.startGate != nil {
		select { case <-f.startGate: case <-ctx.Done(): return ctx.Err() }
	}
	f.mu.Lock()
	f.started = true
	err := f.startErr
	f.mu.Unlock()
	return err
}
func (f *fakeStreamClient) Stop() {
	f.mu.Lock()
	f.stopCalls++
	f.mu.Unlock()
	f.once.Do(func(){ close(f.done) })
}
func (f *fakeStreamClient) Wait() { <-f.done }
func (f *fakeStreamClient) Done() <-chan struct{} { return f.done }
func (f *fakeStreamClient) emit(d *message.Danmaku) {
	f.mu.Lock(); handler := f.handler; f.mu.Unlock()
	if handler != nil { handler(d) }
}

func newAdapterTestAuth(t *testing.T) *AuthManager {
	t.Helper()
	store := &memoryStore{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"code": 0,
			"data": map[string]any{"isLogin": true, "mid": json.Number("123"), "uname": "test-user"},
		})
	}))
	t.Cleanup(server.Close)
	client := server.Client()
	client.Transport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		clone := req.Clone(req.Context())
		u := *req.URL
		u.Scheme = "http"
		u.Host = strings.TrimPrefix(server.URL, "http://")
		clone.URL = &u
		return http.DefaultTransport.RoundTrip(clone)
	})
	manager, err := NewAuthManager(store, client)
	if err != nil { t.Fatal(err) }
	if err := store.Save([]byte(`{"cookie":"SESSDATA=test-session","uid":"123"}`)); err != nil { t.Fatal(err) }
	return manager
}

func TestAdapterConnectStartsAuthenticatedStreamAndReceivesDanmaku(t *testing.T) {
	a, err := NewAdapter(newAdapterTestAuth(t), 12345, NewEventBuffer())
	if err != nil { t.Fatal(err) }
	fake := newFakeStreamClient(12345)
	a.newStream = func(int) streamClient { return fake }

	if err := a.Connect(context.Background()); err != nil { t.Fatal(err) }
	fake.mu.Lock()
	started, cookie := fake.started, fake.cookie
	fake.mu.Unlock()
	if !started || cookie != "SESSDATA=test-session" { t.Fatalf("stream started=%v cookie=%q", started, cookie) }

	fake.emit(&message.Danmaku{Sender: &message.User{Uid: 7, Uname: "viewer"}, Content: "hello", Timestamp: 1_700_000_000})
	events := a.events.Snapshot()
	if len(events) != 1 || events[0].RoomID != 12345 || events[0].Content != "hello" {
		t.Fatalf("unexpected received events: %+v", events)
	}
	if err := a.Disconnect(context.Background()); err != nil { t.Fatal(err) }
	if len(a.events.Snapshot()) != 1 { t.Fatal("disconnect should not clear received event history") }
}

func TestAdapterDisconnectStopsAndWaitsForStream(t *testing.T) {
	a, err := NewAdapter(newAdapterTestAuth(t), 12345, nil)
	if err != nil { t.Fatal(err) }
	fake := newFakeStreamClient(12345)
	a.newStream = func(int) streamClient { return fake }
	if err := a.Connect(context.Background()); err != nil { t.Fatal(err) }

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := a.Disconnect(ctx); err != nil { t.Fatal(err) }
	fake.mu.Lock()
	stops := fake.stopCalls
	fake.mu.Unlock()
	if stops != 1 { t.Fatalf("Stop called %d times, want 1", stops) }
	if err := a.SetRoomID(54321); err != nil { t.Fatalf("room ID should be editable after disconnect: %v", err) }
}

func TestAdapterRejectsRoomChangeWhileConnectedAndConnectIsIdempotent(t *testing.T) {
	a, err := NewAdapter(newAdapterTestAuth(t), 12345, nil)
	if err != nil { t.Fatal(err) }
	fake := newFakeStreamClient(12345)
	a.newStream = func(int) streamClient { return fake }
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
	fake := newFakeStreamClient(12345)
	fake.startGate = gate
	a.newStream = func(int) streamClient { return fake }

	connectCtx, cancelConnect := context.WithCancel(context.Background())
	connectDone := make(chan error, 1)
	go func(){ connectDone <- a.Connect(connectCtx) }()
	deadline := time.Now().Add(time.Second)
	for {
		fake.mu.Lock(); started := fake.started; fake.mu.Unlock()
		if started { break }
		if time.Now().After(deadline) { cancelConnect(); t.Fatal("stream startup did not begin") }
		time.Sleep(time.Millisecond)
	}
	cancelConnect()
	select {
	case err := <-connectDone:
		if err == nil { t.Fatal("expected startup cancellation") }
	case <-time.After(time.Second):
		t.Fatal("Connect did not return after context cancellation")
	}
	select { case <-fake.Done(): case <-time.After(time.Second): t.Fatal("cancelled stream was not stopped") }
}
