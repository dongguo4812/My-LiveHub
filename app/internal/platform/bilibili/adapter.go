package bilibili

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/Akegarasu/blivedm-go/client"
	"github.com/Akegarasu/blivedm-go/message"
	eventstream "github.com/dongguo4812/My-LiveHub/app/internal/events"
)

type streamClient interface {
	SetCookie(string)
	OnDanmaku(func(*message.Danmaku))
	StartContext(context.Context) error
	Stop()
	Wait()
	Done() <-chan struct{}
}

type Adapter struct {
	auth *AuthManager
	events *eventstream.Buffer
	newStream func(int) streamClient
	mu sync.Mutex
	roomID int
	client streamClient
	cancel context.CancelFunc
}

func NewAdapter(auth *AuthManager, roomID int, events *eventstream.Buffer) (*Adapter, error) {
	if auth == nil { return nil, errors.New("Bilibili authentication manager is required") }
	if roomID < 0 { return nil, errors.New("Bilibili room ID cannot be negative") }
	if events == nil { events = eventstream.NewBuffer() }
	return &Adapter{
		auth: auth,
		roomID: roomID,
		events: events,
		newStream: func(roomID int) streamClient { return client.NewClient(roomID) },
	}, nil
}

func (a *Adapter) ID() string { return "bilibili" }

func (a *Adapter) RoomID() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.roomID
}

func (a *Adapter) SetRoomID(roomID int) error {
	if roomID < 0 { return errors.New("Bilibili room ID cannot be negative") }
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.client != nil { return errors.New("请先断开 B 站直播间连接，再修改房间号") }
	a.roomID = roomID
	return nil
}

func (a *Adapter) Connect(ctx context.Context) error {
	if a == nil { return errors.New("Bilibili adapter is required") }
	if ctx == nil { return errors.New("Bilibili connection context is required") }
	a.mu.Lock()
	if a.client != nil { a.mu.Unlock(); return nil }
	roomID := a.roomID
	a.mu.Unlock()
	if roomID <= 0 { return errors.New("请先设置有效的 B 站直播间房间号") }

	credentials, err := a.auth.LoadCredentials(ctx)
	if err != nil { return err }
	stream := a.newStream(roomID)
	stream.SetCookie(credentials.Cookie)
	stream.OnDanmaku(func(d *message.Danmaku) {
		if d == nil || d.Sender == nil {
			return
		}
		timestamp := d.Timestamp
		if timestamp <= 0 {
			timestamp = time.Now().UnixMilli()
		} else if timestamp < 1_000_000_000_000 {
			timestamp *= 1000
		}
		a.events.Add(eventstream.Event{
			PlatformID: "bilibili", Source: "live", Type: "danmaku", RoomID: roomID,
			UserID: d.Sender.Uid, UserName: d.Sender.Uname, Avatar: d.Sender.Face,
			Content: d.Content, Timestamp: timestamp, Color: d.Color,
		})
	})

	runCtx, cancel := context.WithCancel(context.Background())
	started := make(chan error, 1)
	go func() { started <- stream.StartContext(runCtx) }()
	select {
	case err := <-started:
		if err != nil { cancel(); return fmt.Errorf("启动 B 站弹幕连接失败：%w", err) }
	case <-ctx.Done():
		cancel()
		stream.Stop()
		return ctx.Err()
	}

	a.mu.Lock()
	if a.client != nil {
		a.mu.Unlock()
		cancel()
		stream.Stop()
		return errors.New("Bilibili connection was started concurrently")
	}
	a.client, a.cancel = stream, cancel
	a.mu.Unlock()

	go func() {
		stream.Wait()
		a.mu.Lock()
		if a.client == stream { a.client, a.cancel = nil, nil }
		a.mu.Unlock()
	}()
	return nil
}

func (a *Adapter) Disconnect(ctx context.Context) error {
	if a == nil { return nil }
	if ctx == nil { return errors.New("Bilibili disconnection context is required") }
	a.mu.Lock()
	stream, cancel := a.client, a.cancel
	a.client, a.cancel = nil, nil
	a.mu.Unlock()
	if stream == nil { return nil }
	if cancel != nil { cancel() }
	stream.Stop()
	select {
	case <-stream.Done(): return nil
	case <-ctx.Done(): return ctx.Err()
	}
}
