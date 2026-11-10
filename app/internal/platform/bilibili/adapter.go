package bilibili

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Akegarasu/blivedm-go/client"
	"github.com/Akegarasu/blivedm-go/message"
	eventstream "github.com/dongguo4812/My-LiveHub/app/internal/events"
	"github.com/dongguo4812/My-LiveHub/app/internal/media/resource"
)

type streamClient interface {
	SetCookie(string)
	OnDanmaku(func(*message.Danmaku))
	OnGift(func(*message.Gift))
	StartContext(context.Context) error
	Stop()
	Wait()
	Done() <-chan struct{}
}


// giftCatalog caches the public gift-panel metadata separately for each room.
type giftCatalog struct {
	mu sync.RWMutex
	client *http.Client
	endpoint string
	ttl time.Duration
	rooms map[int]giftCatalogEntry
}

type giftCatalogEntry struct { gifts map[int]giftCatalogResource; expiresAt time.Time }
type giftCatalogResource struct { name string; imageURL string; unitPrice float64 }

type roomGiftListResponse struct {
	Code int `json:"code"`
	Message string `json:"message"`
	Data struct {
		GiftConfig struct {
			BaseConfig struct {
				List []struct {
					ID int `json:"id"`
					Name string `json:"name"`
					ImgBasic string `json:"img_basic"`
					Price int `json:"price"`
				} `json:"list"`
			} `json:"base_config"`
		} `json:"gift_config"`
	} `json:"data"`
}

func newGiftCatalog(client *http.Client) *giftCatalog {
	if client == nil {
		client = &http.Client{Timeout: 4 * time.Second}
	}
	return &giftCatalog{
		client: client,
		endpoint: "https://api.live.bilibili.com/xlive/web-room/v1/giftPanel/roomGiftList",
		ttl: 24 * time.Hour,
		rooms: make(map[int]giftCatalogEntry),
	}
}

func (c *giftCatalog) Refresh(ctx context.Context, roomID int) error {
	if c == nil { return nil }
	if roomID <= 0 { return errors.New("invalid Bilibili room ID for gift catalog") }
	c.mu.RLock()
	cached, ok := c.rooms[roomID]
	c.mu.RUnlock()
	if ok && time.Now().Before(cached.expiresAt) { return nil }

	requestURL, err := url.Parse(c.endpoint)
	if err != nil { return err }
	query := requestURL.Query()
	query.Set("platform", "pc")
	query.Set("room_id", fmt.Sprint(roomID))
	requestURL.RawQuery = query.Encode()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, requestURL.String(), nil)
	if err != nil { return err }
	request.Header.Set("User-Agent", "My-LiveHub/1.0")
	response, err := c.client.Do(request)
	if err != nil { return err }
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("Bilibili gift catalog returned HTTP %d", response.StatusCode)
	}
	var payload roomGiftListResponse
	if err := json.NewDecoder(io.LimitReader(response.Body, 4<<20)).Decode(&payload); err != nil { return err }
	if payload.Code != 0 {
		return fmt.Errorf("Bilibili gift catalog API error %d: %s", payload.Code, payload.Message)
	}
	gifts := make(map[int]giftCatalogResource, len(payload.Data.GiftConfig.BaseConfig.List))
	for _, gift := range payload.Data.GiftConfig.BaseConfig.List {
		if gift.ID <= 0 { continue }
		if imageURL := normalizeEmoticonURL(gift.ImgBasic); imageURL != "" {
			gifts[gift.ID] = giftCatalogResource{name:gift.Name,imageURL:imageURL,unitPrice:float64(gift.Price)/1000}
		}
	}
	if len(gifts) == 0 { return errors.New("Bilibili gift catalog contains no usable gift images") }
	c.mu.Lock()
	c.rooms[roomID] = giftCatalogEntry{gifts: gifts, expiresAt: time.Now().Add(c.ttl)}
	c.mu.Unlock()
	return nil
}

func (c *giftCatalog) ImageURL(roomID, giftID int) string {
	if c == nil || roomID <= 0 || giftID <= 0 { return "" }
	c.mu.RLock()
	defer c.mu.RUnlock()
	entry, ok := c.rooms[roomID]
	if !ok { return "" }
	return entry.gifts[giftID].imageURL
}

func(c *giftCatalog)Resources(roomID int)[]resource.Resource{if c==nil||roomID<=0{return nil};c.mu.RLock();entry,ok:=c.rooms[roomID];c.mu.RUnlock();if !ok{return nil};out:=make([]resource.Resource,0,len(entry.gifts));for id,g:=range entry.gifts{out=append(out,resource.Resource{PlatformID:"bilibili",SourceID:fmt.Sprint(roomID),Type:resource.TypeGift,ID:fmt.Sprint(id),Name:g.name,ImageURL:g.imageURL,UnitPrice:g.unitPrice})};return out}

type Adapter struct {
	auth *AuthManager
	events *eventstream.Buffer
	newStream func(int) streamClient
	giftCatalog *giftCatalog
	mediaCatalog *resource.Catalog
	mu sync.Mutex
	roomID int
	client streamClient
	cancel context.CancelFunc
}

func NewAdapter(auth *AuthManager, roomID int, events *eventstream.Buffer) (*Adapter, error) {
	if auth == nil { return nil, errors.New("Bilibili authentication manager is required") }
	if roomID < 0 { return nil, errors.New("Bilibili room ID cannot be negative") }
	if events == nil { events = eventstream.NewBuffer() }
	return &Adapter{auth: auth, roomID: roomID, events: events, newStream: func(roomID int) streamClient { return client.NewClient(roomID) }, giftCatalog: newGiftCatalog(nil)}, nil
}

func medalName(m *message.Medal) string { if m == nil { return "" }; return m.Name }
func medalLevel(m *message.Medal) int { if m == nil { return 0 }; return m.Level }
func medalColor(m *message.Medal) uint32 { if m == nil { return 0 }; return uint32(m.Color) }

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}
	return ""
}

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
								URL             string `json:"url"`
								Width           int    `json:"width"`
								Height          int    `json:"height"`
								EmoticonUnique  string `json:"emoticon_unique"`
								EmotionUnique   string `json:"emotion_unique"`
								EmoticonID      int    `json:"emoticon_id"`
								EmotionID       int    `json:"emotion_id"`
							} `json:"emots"`
						}
						if json.Unmarshal(extraRaw, &extra) == nil {
							for token, emoticon := range extra.Emots {
								if token == "" {
									continue
								}
								if normalizedURL := normalizeEmoticonURL(emoticon.URL); normalizedURL != "" {
									resourceID := firstNonEmpty(emoticon.EmoticonUnique, emoticon.EmotionUnique)
									if resourceID == "" && emoticon.EmoticonID > 0 {
										resourceID = strconv.Itoa(emoticon.EmoticonID)
									}
									if resourceID == "" && emoticon.EmotionID > 0 {
										resourceID = strconv.Itoa(emoticon.EmotionID)
									}
									found[token] = eventstream.EmoticonToken{Text: token, ResourceID: resourceID, URL: normalizedURL, Width: emoticon.Width, Height: emoticon.Height}
								}
							}
						}
					}
				}
				// A standalone room emoticon is carried in info[0][13].
				if len(meta) > 13 && d.Content != "" {
					var emoticon struct {
						URL            string `json:"url"`
						Width          int    `json:"width"`
						Height         int    `json:"height"`
						EmoticonUnique string `json:"emoticon_unique"`
						EmotionUnique  string `json:"emotion_unique"`
						EmoticonID     int    `json:"emoticon_id"`
						EmotionID      int    `json:"emotion_id"`
					}
					if json.Unmarshal(meta[13], &emoticon) == nil {
						if normalizedURL := normalizeEmoticonURL(emoticon.URL); normalizedURL != "" {
							resourceID := firstNonEmpty(emoticon.EmoticonUnique, emoticon.EmotionUnique)
							if resourceID == "" && emoticon.EmoticonID > 0 {
								resourceID = strconv.Itoa(emoticon.EmoticonID)
							}
							if resourceID == "" && emoticon.EmotionID > 0 {
								resourceID = strconv.Itoa(emoticon.EmotionID)
							}
							if resourceID == "" && d.Extra != nil {
								resourceID = d.Extra.EmoticonUnique
							}
							found[d.Content] = eventstream.EmoticonToken{Text: d.Content, ResourceID: resourceID, URL: normalizedURL, Width: emoticon.Width, Height: emoticon.Height}
						}
					}
				}
			}
		}
	}
	// The protobuf parser also exposes standalone emoticons directly.
	if d.Emoticon != nil && d.Content != "" {
		if normalizedURL := normalizeEmoticonURL(d.Emoticon.Url); normalizedURL != "" {
			resourceID := d.Emoticon.EmoticonUnique
			if resourceID == "" && d.Extra != nil {
				resourceID = d.Extra.EmoticonUnique
			}
			found[d.Content] = eventstream.EmoticonToken{Text: d.Content, ResourceID: resourceID, URL: normalizedURL, Width: d.Emoticon.Width, Height: d.Emoticon.Height}
		}
	}
	if len(found) == 0 {
		return nil
	}
	tokens := make([]eventstream.EmoticonToken, 0, len(found))
	for _, emoticon := range found {
		tokens = append(tokens, emoticon)
		
	}
	sort.Slice(tokens, func(i, j int) bool {
		if len(tokens[i].Text) == len(tokens[j].Text) {
			return tokens[i].Text < tokens[j].Text
		}
		return len(tokens[i].Text) > len(tokens[j].Text)
	})
	return tokens
}

func (a *Adapter) SetMediaCatalog(c *resource.Catalog) {
	if a == nil {
		return
	}
	a.mediaCatalog = c
	if c != nil {
		c.SetPlatformScopeDirectory(a.ID(), "rooms")
	}
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
		emoticons := extractDanmakuEmoticons(d)
		for i := range emoticons {
			rawResourceID := emoticons[i].ResourceID
			sourceID := emoticonSourceID(roomID, rawResourceID)
			// Live danmaku often carries a numeric emoticon ID without its
			// package type. In that case the ID alone cannot distinguish a
			// shared emoticon from a room-specific one, so resolve by URL.
			// Explicit Bilibili unique prefixes already identify the scope and
			// must not be overridden by URL lookup.
			if a.mediaCatalog != nil && !hasExplicitEmoticonScope(rawResourceID) {
				if known, ok := a.mediaCatalog.FindByImageURL("bilibili", resource.TypeEmoticon, emoticons[i].URL); ok {
					rawResourceID = known.ID
					sourceID = known.SourceID
				}
			}
			if a.mediaCatalog != nil && rawResourceID != "" {
				a.mediaCatalog.Register(resource.Resource{
					PlatformID: "bilibili", SourceID: sourceID,
					Type: resource.TypeEmoticon, ID: rawResourceID, Name: emoticons[i].Text,
					ImageURL: emoticons[i].URL, Width: emoticons[i].Width, Height: emoticons[i].Height,
				})
			}
			if rawResourceID == "" {
				// Keep an event-local fallback until a later catalog refresh can
				// resolve the URL; do not persist this temporary identity.
				emoticons[i].ResourceID = sourceID + ":" + emoticons[i].URL
			} else {
				emoticons[i].ResourceID = sourceID + ":" + rawResourceID
			}
		}
		if timestamp <= 0 { timestamp = time.Now().UnixMilli() } else if timestamp < 1_000_000_000_000 { timestamp *= 1000 }
		a.events.Add(eventstream.Event{
			PlatformID: "bilibili", Source: "live", Type: "danmaku", RoomID: roomID,
			UserID: d.Sender.Uid, UserName: d.Sender.Uname, Avatar: d.Sender.Face,
			Content: d.Content, Emoticons: emoticons, Timestamp: timestamp, Color: d.Color,
			IsModerator: d.Sender.Admin, UserLevel: int(d.Sender.UserLevel), GuardLevel: d.Sender.GuardLevel,
			FanMedalName: medalName(d.Sender.Medal), FanMedalLevel: medalLevel(d.Sender.Medal), FanMedalColor: medalColor(d.Sender.Medal),
		})
	})
	stream.OnGift(func(g *message.Gift) {
		if g == nil { return }
		count := g.Num
		if count <= 0 { count = 1 }
		timestamp := int64(g.Timestamp)
		if timestamp <= 0 { timestamp = time.Now().UnixMilli() } else if timestamp < 1_000_000_000_000 { timestamp *= 1000 }
		unitPrice := float64(g.Price) / 1000
		totalPrice := float64(g.TotalCoin) / 1000
		if totalPrice <= 0 { totalPrice = unitPrice * float64(count) }
		giftImageURL := a.giftCatalog.ImageURL(roomID, g.GiftId)
		if a.mediaCatalog != nil {
			if item, ok := a.mediaCatalog.Find("bilibili", strconv.Itoa(roomID), resource.TypeGift, strconv.Itoa(g.GiftId)); ok {
				giftImageURL = item.ImageURL
			}
		}
		a.events.Add(eventstream.Event{
			PlatformID: "bilibili", Source: "live", Type: "gift", RoomID: roomID,
			UserID: g.Uid, UserName: g.Uname, Avatar: g.Face,
			Content: fmt.Sprintf("赠送了 %s × %d", g.GiftName, count),
			Timestamp: timestamp, GiftID: g.GiftId, GiftResourceID: strconv.Itoa(g.GiftId), GiftName: g.GiftName,
			GiftCount: count, UnitPrice: unitPrice, TotalPrice: totalPrice,
			GiftImageURL: giftImageURL,
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
	refreshCommonEmoticons := a.mediaCatalog != nil && !a.mediaCatalog.IsFresh("bilibili", "common", resource.TypeEmoticon, 24*time.Hour)
	refreshRoomEmoticons := a.mediaCatalog != nil && !a.mediaCatalog.IsFresh("bilibili", strconv.Itoa(roomID), resource.TypeEmoticon, 6*time.Hour)
	if a.mediaCatalog != nil && (refreshCommonEmoticons || refreshRoomEmoticons) {
		go func() {
			catalogCtx, catalogCancel := context.WithTimeout(runCtx, 8*time.Second)
			defer catalogCancel()
			httpClient := &http.Client{Timeout: 6 * time.Second}
			items, err := fetchRoomEmoticonResources(catalogCtx, httpClient, roomID, credentials.Cookie)
			if err != nil {
				return
			}
			for _, item := range items {
				if item.SourceID == "common" && !refreshCommonEmoticons {
					continue
				}
				if item.SourceID == strconv.Itoa(roomID) && !refreshRoomEmoticons {
					continue
				}
				a.mediaCatalog.Register(item)
			}
			refreshedAt := time.Now()
			if refreshCommonEmoticons {
				a.mediaCatalog.MarkRefreshed("bilibili", "common", resource.TypeEmoticon, refreshedAt)
			}
			if refreshRoomEmoticons {
				a.mediaCatalog.MarkRefreshed("bilibili", strconv.Itoa(roomID), resource.TypeEmoticon, refreshedAt)
			}
		}()
	}
	if a.giftCatalog != nil && (a.mediaCatalog == nil ||
		!a.mediaCatalog.IsFresh("bilibili", strconv.Itoa(roomID), resource.TypeGift, 24*time.Hour)) {
		go func() {
			catalogCtx, catalogCancel := context.WithTimeout(runCtx, 8*time.Second)
			defer catalogCancel()
			if err := a.giftCatalog.Refresh(catalogCtx, roomID); err != nil {
				return
			}
			if a.mediaCatalog != nil {
				for _, item := range a.giftCatalog.Resources(roomID) {
					a.mediaCatalog.Register(item)
					if giftID, parseErr := strconv.Atoi(item.ID); parseErr == nil {
						a.events.UpdateGiftImage("bilibili", roomID, giftID, item.ImageURL)
					}
				}
				a.mediaCatalog.MarkRefreshed("bilibili", strconv.Itoa(roomID), resource.TypeGift, time.Now())
			}
		}()
	}

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
