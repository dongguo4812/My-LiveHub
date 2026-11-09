package bilibili

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/url"
	"strings"
	"sort"
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
	return &Adapter{auth: auth, roomID: roomID, events: events, newStream: func(roomID int) streamClient { return client.NewClient(roomID) }}, nil
}

func medalName(m *message.Medal) string { if m == nil { return "" }; return m.Name }
func medalLevel(m *message.Medal) int { if m == nil { return 0 }; return m.Level }
func medalColor(m *message.Medal) uint32 { if m == nil { return 0 }; return uint32(m.Color) }

// normalizeEmoticonURL accepts only absolute HTTP(S) URLs and upgrades insecure URLs.
func normalizeEmoticonURL(raw string) string {
	raw = strings.TrimSpace(raw)
	if strings.HasPrefix(raw, "//") { raw = "https:" + raw }
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") { return "" }
	if parsed.Scheme == "http" { parsed.Scheme = "https" }
	return parsed.String()
}
// extractDanmakuEmoticons reads Bilibili's emots map from the legacy DANMU_MSG
// extra payload. The library separately exposes the single-emoticon protobuf form.
func extractDanmakuEmoticons(d *message.Danmaku) []eventstream.EmoticonToken {
	if d == nil {
		return nil
	}
	found := make(map[string]eventstream.EmoticonToken)
	if d.Raw != "" {
		// DANMU_MSG.info is a heterogeneous array:
		// [0] metadata array, [1] content string, [2] sender array, ...
		var packet struct {
			Info []json.RawMessage `json:"info"`
		}
		if json.Unmarshal([]byte(d.Raw), &packet) == nil && len(packet.Info) > 0 {
			var meta []json.RawMessage
			if json.Unmarshal(packet.Info[0], &meta) == nil {
				// Small inline emoticons are carried in info[0][15].extra.emots.
				if len(meta) > 15 {
					var extraHolder struct {
						Extra json.RawMessage `json:"extra"`
					}
					if json.Unmarshal(meta[15], &extraHolder) == nil && len(extraHolder.Extra) > 0 && string(extraHolder.Extra) != "null" {
						extraRaw := extraHolder.Extra
						var extraString string
						if json.Unmarshal(extraHolder.Extra, &extraString) == nil {
							extraRaw = []byte(extraString)
						}
						var extra struct {
							Emots map[string]struct {
								URL    string `json:"url"`
								Width  int    `json:"width"`
								Height int    `json:"height"`
							} `json:"emots"`
						}
						if json.Unmarshal(extraRaw, &extra) == nil {
							for token, emoticon := range extra.Emots {
								if token != "" && emoticon.URL != "" {
									found[token] = eventstream.EmoticonToken{Text: token, URL: emoticon.URL, Width: emoticon.Width, Height: emoticon.Height}
								}
							}
						}
					}
				}
				// A standalone room emoticon is carried in info[0][13].
				if len(meta) > 13 && d.Content != "" {
					var emoticon struct {
						URL    string `json:"url"`
						Width  int    `json:"width"`
						Height int    `json:"height"`
					}
					if json.Unmarshal(meta[13], &emoticon) == nil {
						if normalizedURL := normalizeEmoticonURL(emoticon.URL); normalizedURL != "" {
							found[d.Content] = eventstream.EmoticonToken{Text: d.Content, URL: normalizedURL, Width: emoticon.Width, Height: emoticon.Height}
						}
					}
				}
			}
		}
	}
	// The protobuf parser also exposes standalone emoticons directly.
	if d.Emoticon != nil && d.Content != "" {
		if normalizedURL := normalizeEmoticonURL(d.Emoticon.Url); normalizedURL != "" {
			found[d.Content] = eventstream.EmoticonToken{Text: d.Content, URL: normalizedURL, Width: d.Emoticon.Width, Height: d.Emoticon.Height}
		}
	}
	if len(found) == 0 {
		return nil
	}
	tokens := make([]eventstream.EmoticonToken, 0, len(found))
	for _, emoticon := range found {
		tokens = append(tokens, emoticon)
		log.Printf("[Bilibili emoticon] token=%q url=%s", emoticon.Text, emoticon.URL)
	}
	sort.Slice(tokens, func(i, j int) bool {
		if len(tokens[i].Text) == len(tokens[j].Text) {
			return tokens[i].Text < tokens[j].Text
		}
		return len(tokens[i].Text) > len(tokens[j].Text)
	})
	return tokens
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
		if d == nil || d.Sender == nil { return }
		timestamp := d.Timestamp
		if timestamp <= 0 { timestamp = time.Now().UnixMilli() } else if timestamp < 1_000_000_000_000 { timestamp *= 1000 }
		a.events.Add(eventstream.Event{
			PlatformID: "bilibili", Source: "live", Type: "danmaku", RoomID: roomID,
			UserID: d.Sender.Uid, UserName: d.Sender.Uname, Avatar: d.Sender.Face,
			Content: d.Content, Emoticons: extractDanmakuEmoticons(d), Timestamp: timestamp, Color: d.Color,
			IsModerator: d.Sender.Admin, UserLevel: int(d.Sender.UserLevel), GuardLevel: d.Sender.GuardLevel,
			FanMedalName: medalName(d.Sender.Medal), FanMedalLevel: medalLevel(d.Sender.Medal), FanMedalColor: medalColor(d.Sender.Medal),
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
