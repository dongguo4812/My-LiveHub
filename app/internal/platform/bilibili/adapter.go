package bilibili

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
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
	OnSuperChat(func(*message.SuperChat))
	OnGuardBuy(func(*message.GuardBuy))
	OnUserToast(func(*message.UserToast))
	OnInteractWord(func(*message.InteractWord))
	OnLiveStart(func(*message.LiveStart))
	OnLiveStop(func(*message.LiveStop))
	OnRawEvent(func(string, []byte))
	StartContext(context.Context) error
	Stop()
	Wait()
	Done() <-chan struct{}
}


const giftCatalogMetadataVersion = 2

// giftCatalog caches the public gift-panel metadata separately for each room.
type giftCatalog struct {
	mu sync.RWMutex
	client *http.Client
	endpoint string
	ttl time.Duration
	rooms map[int]giftCatalogEntry
}

type giftCatalogEntry struct { gifts map[int]giftCatalogResource; expiresAt time.Time }
type giftCatalogResource struct { name string; imageURL string; animationURL string; unitPrice float64 }

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
					Gif string `json:"gif"`
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
	return c.refresh(ctx, roomID, false)
}

// ForceRefresh bypasses the in-memory TTL. It is used when the persistent
// resource catalog says the last successful remote refresh has expired.
func (c *giftCatalog) ForceRefresh(ctx context.Context, roomID int) error {
	return c.refresh(ctx, roomID, true)
}

func (c *giftCatalog) refresh(ctx context.Context, roomID int, force bool) error {
	if c == nil { return nil }
	if roomID <= 0 { return errors.New("invalid Bilibili room ID for gift catalog") }
	c.mu.RLock(); cached, ok := c.rooms[roomID]; c.mu.RUnlock()
	if !force && ok && time.Now().Before(cached.expiresAt) { return nil }
	parentAreaID, areaID := 0, 0
	usePlatformEndpoints := strings.Contains(c.endpoint, "api.live.bilibili.com")
	if usePlatformEndpoints {
		var areaErr error
		parentAreaID, areaID, areaErr = fetchRoomAreaIDs(ctx, c.client, roomID)
		if areaErr != nil { log.Printf("[bilibili] room %d gift catalog could not resolve area IDs; continuing without them: %v", roomID, areaErr) }
	}
	gifts := make(map[int]giftCatalogResource)
	if err := c.fetchGiftEndpoint(ctx, c.endpoint, roomID, parentAreaID, areaID, gifts, 0); err != nil { return err }
	if usePlatformEndpoints {
		// giftConfig is a broader room gift metadata endpoint. The room panel
		// endpoint may omit campaign and customized gifts that are still
		// selectable in the live-room gift picker.
		giftConfigEndpoint := "https://api.live.bilibili.com/xlive/web-room/v1/giftPanel/giftConfig"
		if err := c.fetchGiftEndpoint(ctx, giftConfigEndpoint, roomID, parentAreaID, areaID, gifts, 0); err != nil {
			log.Printf("[bilibili] room %d supplementary gift config unavailable: %v", roomID, err)
		}
		for _, tabID := range []int{2, 3} {
			endpoint := "https://api.live.bilibili.com/xlive/web-room/v1/giftPanel/tabRoomGiftList"
			if err := c.fetchGiftEndpoint(ctx, endpoint, roomID, parentAreaID, areaID, gifts, tabID); err != nil {
				log.Printf("[bilibili] room %d optional gift tab %d unavailable: %v", roomID, tabID, err)
			}
		}
	}
	if len(gifts) == 0 { return errors.New("Bilibili gift catalog contains no usable gift images") }
	c.mu.Lock(); c.rooms[roomID] = giftCatalogEntry{gifts:gifts, expiresAt:time.Now().Add(c.ttl)}; c.mu.Unlock()
	return nil
}

func fetchRoomAreaIDs(ctx context.Context, httpClient *http.Client, roomID int) (int, int, error) {
	if httpClient == nil { httpClient = &http.Client{Timeout:3*time.Second} }
	u, err := url.Parse("https://api.live.bilibili.com/room/v1/Room/get_info"); if err != nil { return 0,0,err }
	q:=u.Query(); q.Set("room_id",strconv.Itoa(roomID)); u.RawQuery=q.Encode()
	req,err:=http.NewRequestWithContext(ctx,http.MethodGet,u.String(),nil); if err!=nil{return 0,0,err}
	resp,err:=httpClient.Do(req); if err!=nil{return 0,0,err}; defer resp.Body.Close()
	if resp.StatusCode<200||resp.StatusCode>=300{return 0,0,fmt.Errorf("Bilibili room info returned HTTP %d",resp.StatusCode)}
	var payload struct { Code int `json:"code"`; Data struct { RoomInfo struct { ParentAreaID int `json:"parent_area_id"`; AreaID int `json:"area_id"` } `json:"room_info"` } `json:"data"` }
	if err:=json.NewDecoder(io.LimitReader(resp.Body,1<<20)).Decode(&payload);err!=nil{return 0,0,err}
	if payload.Code!=0{return 0,0,fmt.Errorf("Bilibili room info API error %d",payload.Code)}
	return payload.Data.RoomInfo.ParentAreaID,payload.Data.RoomInfo.AreaID,nil
}

func (c *giftCatalog) fetchGiftEndpoint(ctx context.Context, endpoint string, roomID, parentAreaID, areaID int, gifts map[int]giftCatalogResource, tabID int) error {
	u,err:=url.Parse(endpoint);if err!=nil{return err};q:=u.Query()
	q.Set("platform","pc");q.Set("room_id",strconv.Itoa(roomID));q.Set("source","live")
	if parentAreaID>0{q.Set("area_parent_id",strconv.Itoa(parentAreaID))}
	if areaID>0{q.Set("area_id",strconv.Itoa(areaID))}
	if tabID>0{q.Set("tab_id",strconv.Itoa(tabID));q.Set("build","1")}
	u.RawQuery=q.Encode()
	req,err:=http.NewRequestWithContext(ctx,http.MethodGet,u.String(),nil);if err!=nil{return err};req.Header.Set("User-Agent","My-LiveHub/1.0")
	resp,err:=c.client.Do(req);if err!=nil{return err};defer resp.Body.Close()
	if resp.StatusCode<200||resp.StatusCode>=300{return fmt.Errorf("Bilibili gift endpoint returned HTTP %d",resp.StatusCode)}
	body,err:=io.ReadAll(io.LimitReader(resp.Body,4<<20));if err!=nil{return err}
	var envelope struct{Code int `json:"code"`;Message string `json:"message"`}
	if err:=json.Unmarshal(body,&envelope);err!=nil{return err};if envelope.Code!=0{return fmt.Errorf("Bilibili gift API error %d: %s",envelope.Code,envelope.Message)}
	var generic any;if err:=json.Unmarshal(body,&generic);err!=nil{return err}
	collectGiftCatalogResources(generic,gifts)
	return nil
}

func collectGiftCatalogResources(value any, gifts map[int]giftCatalogResource) {
	switch item:=value.(type){
	case []any: for _,child:=range item{collectGiftCatalogResources(child,gifts)}
	case map[string]any:
		id,_:=item["id"].(float64);if id==0{id,_=item["gift_id"].(float64)}
		name,_:=item["name"].(string);if name==""{name,_=item["gift_name"].(string)}
		image,_:=item["img_basic"].(string);if image==""{image,_=item["gift_img"].(string)}
		animation,_:=item["gif"].(string);if animation==""{animation,_=item["gif_url"].(string)}
		price,_:=item["price"].(float64)
		if id>0&&(image!=""||animation!=""){image=normalizeEmoticonURL(image);animation=normalizeEmoticonURL(animation);if image==""{image=animation};if image!=""{gifts[int(id)]=giftCatalogResource{name:strings.TrimSpace(name),imageURL:image,animationURL:animation,unitPrice:price/1000}}}
		for _,child:=range item{collectGiftCatalogResources(child,gifts)}
	}
}

func (c *giftCatalog) ImageURL(roomID, giftID int) string {
	if c == nil || roomID <= 0 || giftID <= 0 { return "" }
	c.mu.RLock(); defer c.mu.RUnlock()
	entry, ok := c.rooms[roomID]; if !ok { return "" }; return entry.gifts[giftID].imageURL
}
func (c *giftCatalog) AnimationURL(roomID, giftID int) string {
	if c == nil || roomID <= 0 || giftID <= 0 { return "" }
	c.mu.RLock(); defer c.mu.RUnlock()
	entry, ok := c.rooms[roomID]; if !ok { return "" }; return entry.gifts[giftID].animationURL
}

func(c *giftCatalog)Resources(roomID int)[]resource.Resource{if c==nil||roomID<=0{return nil};c.mu.RLock();entry,ok:=c.rooms[roomID];c.mu.RUnlock();if !ok{return nil};out:=make([]resource.Resource,0,len(entry.gifts));for id,g:=range entry.gifts{out=append(out,resource.Resource{PlatformID:"bilibili",SourceID:fmt.Sprint(roomID),Type:resource.TypeGift,ID:fmt.Sprint(id),Name:g.name,ImageURL:g.imageURL,AnimationURL:g.animationURL,MetadataVersion:giftCatalogMetadataVersion,UnitPrice:g.unitPrice})};return out}

type Adapter struct {
	auth *AuthManager
	events *eventstream.Buffer
	newStream func(int) streamClient
	giftCatalog *giftCatalog
	mediaCatalog *resource.Catalog
	guardMu sync.Mutex
	recentGuardEvents map[string]guardEventRecord
	mu sync.Mutex
	roomID int
	liveStatus string
	liveStatusRevision uint64
	client streamClient
	cancel context.CancelFunc
	connectionFailureHandler func(error)
}

func NewAdapter(auth *AuthManager, roomID int, events *eventstream.Buffer) (*Adapter, error) {
	if auth == nil { return nil, errors.New("Bilibili authentication manager is required") }
	if roomID < 0 { return nil, errors.New("Bilibili room ID cannot be negative") }
	if events == nil { events = eventstream.NewBuffer() }
	return &Adapter{auth: auth, roomID: roomID, liveStatus: "unknown", events: events, newStream: func(roomID int) streamClient { return client.NewClient(roomID) }, giftCatalog: newGiftCatalog(nil), recentGuardEvents: make(map[string]guardEventRecord)}, nil
}

func medalName(m *message.Medal) string { if m == nil { return "" }; return m.Name }
func medalLevel(m *message.Medal) int { if m == nil { return 0 }; return m.Level }
func medalColor(m *message.Medal) uint32 { if m == nil { return 0 }; return uint32(m.Color) }

func normalizeTimestamp(timestamp int64) int64 {
	if timestamp <= 0 {
		return time.Now().UnixMilli()
	}
	if timestamp < 1_000_000_000_000 {
		return timestamp * 1000
	}
	return timestamp
}

type guardEventRecord struct {
	eventID uint64
	seenAt  time.Time
}

func guardEventKey(uid, level, months, startTime int) string {
	if uid <= 0 || level < 1 || level > 3 || months <= 0 || startTime <= 0 {
		return ""
	}
	return fmt.Sprintf("%d:%d:%d:%d", uid, level, months, startTime)
}

// addLikeEvent maps Bilibili's per-user LIKE_INFO_V3_CLICK notification.
// LIKE_INFO_V3_UPDATE is deliberately ignored because it only reports room totals.
func (a *Adapter) addGiftEventImage(roomID int, raw []byte) {
	if a == nil { return }
	var envelope struct {
		Data struct {
			GiftID int `json:"giftId"`
			GiftIDAlt int `json:"gift_id"`
			GiftName string `json:"giftName"`
			GiftNameAlt string `json:"gift_name"`
			GiftInfo struct {
				ID int `json:"id"`
				Name string `json:"name"`
				ImgBasic string `json:"img_basic"`
				Gif string `json:"gif"`
				Price int `json:"price"`
			} `json:"gift_info"`
		} `json:"data"`
	}
	if json.Unmarshal(raw, &envelope) != nil { return }
	data := envelope.Data
	id := data.GiftInfo.ID
	if id <= 0 { id = data.GiftID }
	if id <= 0 { id = data.GiftIDAlt }
	name := firstNonEmpty(data.GiftInfo.Name, data.GiftName, data.GiftNameAlt)
	imageURL := normalizeEmoticonURL(data.GiftInfo.ImgBasic)
	animationURL := normalizeEmoticonURL(data.GiftInfo.Gif)
	if imageURL == "" { imageURL = animationURL }
	if id <= 0 || imageURL == "" { return }
	if a.mediaCatalog != nil {
		a.mediaCatalog.Register(resource.Resource{PlatformID:"bilibili", SourceID:strconv.Itoa(roomID), Type:resource.TypeGift, ID:strconv.Itoa(id), Name:name, ImageURL:imageURL, AnimationURL:animationURL, MetadataVersion:giftCatalogMetadataVersion, UnitPrice:float64(data.GiftInfo.Price)/1000})
	}
	a.events.UpdateGiftImage("bilibili", roomID, id, imageURL, animationURL)
}

func (a *Adapter) addLikeEvent(roomID int, raw []byte) {
	if a == nil || a.events == nil {
		return
	}
	var envelope struct {
		Data struct {
			UID       int64  `json:"uid"`
			UName     string `json:"uname"`
			LikeText  string `json:"like_text"`
			Timestamp int64  `json:"timestamp"`
			FansMedal struct {
				Name  string `json:"medal_name"`
				Level int    `json:"medal_level"`
				Color uint32 `json:"medal_color"`
			} `json:"fans_medal"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return
	}
	data := envelope.Data
	if data.UID <= 0 || strings.TrimSpace(data.UName) == "" {
		return
	}
	a.events.Add(eventstream.Event{
		PlatformID: "bilibili", Source: "live", Type: "like", RoomID: roomID,
		UserID: int(data.UID), UserName: data.UName,
		Content: firstNonEmpty(data.LikeText, "为直播间点赞"),
		Timestamp: normalizeTimestamp(data.Timestamp),
		FanMedalName: data.FansMedal.Name, FanMedalLevel: data.FansMedal.Level,
		FanMedalColor: data.FansMedal.Color, LikeCount: 1,
	})
}

func (a *Adapter) addGuardEvent(roomID, uid int, name, avatar string, level, months int, price int, timestamp int, key string, actualPayment bool) {
	if a == nil || a.events == nil || level < 1 || level > 3 || months < 1 {
		return
	}
	event := eventstream.Event{
		PlatformID: "bilibili", Source: "live", Type: "guard", RoomID: roomID,
		UserID: uid, UserName: strings.TrimSpace(name), Avatar: strings.TrimSpace(avatar),
		Content: fmt.Sprintf("开通了 %s × %d 个月", map[int]string{1: "总督", 2: "提督", 3: "舰长"}[level], months),
		Timestamp: normalizeTimestamp(int64(timestamp)), GuardLevel: level, Months: months,
		Amount: float64(price) / 1000,
	}
	if key == "" {
		a.events.Add(event)
		return
	}

	now := time.Now()
	a.guardMu.Lock()
	defer a.guardMu.Unlock()
	for existing, record := range a.recentGuardEvents {
		if now.Sub(record.seenAt) > 10*time.Second {
			delete(a.recentGuardEvents, existing)
		}
	}
	if record, exists := a.recentGuardEvents[key]; exists && now.Sub(record.seenAt) <= 10*time.Second {
		// GUARD_BUY and USER_TOAST_MSG describe the same order. Keep one
		// event, but fill fields that the first protocol did not provide.
		var actualAmount *float64
		if actualPayment {
			amount := event.Amount
			actualAmount = &amount
		}
		a.events.EnrichGuardEvent(record.eventID, event.UserName, event.Avatar, actualAmount)
		return
	}
	created := a.events.Add(event)
	a.recentGuardEvents[key] = guardEventRecord{eventID: created.ID, seenAt: now}
}

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

// formatClockDiagnostic makes the wall clock used for persisted timestamps
// observable in both local time and UTC when investigating cache timestamps.
func formatClockDiagnostic(at time.Time) string {
	return fmt.Sprintf("local=%s utc=%s unix=%d location=%s",
		at.Format(time.RFC3339Nano),
		at.UTC().Format(time.RFC3339Nano),
		at.Unix(),
		at.Location(),
	)
}

func giftCatalogMetadataCurrent(c *resource.Catalog, roomID int) bool {
	if c == nil || roomID <= 0 { return false }
	items := c.List("bilibili", strconv.Itoa(roomID), resource.TypeGift)
	if len(items) == 0 { return false }
	for _, item := range items {
		if item.MetadataVersion < giftCatalogMetadataVersion { return false }
	}
	return true
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

// LiveStatus returns the latest Bilibili LIVE/PREPARING or room-info status.
func (a *Adapter) LiveStatus() string {
	if a == nil { return "unknown" }
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.liveStatus == "" { return "unknown" }
	return a.liveStatus
}

func (a *Adapter) setLiveStatus(status string) {
	if a == nil { return }
	a.mu.Lock()
	a.liveStatus = status
	a.liveStatusRevision++
	a.mu.Unlock()
}

func fetchRoomLiveStatus(ctx context.Context, httpClient *http.Client, endpoint string, roomID int) (string, error) {
	if roomID <= 0 { return "unknown", errors.New("invalid Bilibili room ID") }
	requestURL, err := url.Parse(endpoint)
	if err != nil { return "unknown", err }
	query := requestURL.Query()
	query.Set("id", strconv.Itoa(roomID))
	requestURL.RawQuery = query.Encode()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, requestURL.String(), nil)
	if err != nil { return "unknown", err }
	if httpClient == nil { httpClient = &http.Client{Timeout: 3 * time.Second} }
	response, err := httpClient.Do(request)
	if err != nil { return "unknown", err }
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 { return "unknown", fmt.Errorf("Bilibili room status returned HTTP %d", response.StatusCode) }
	var payload struct {
		Code int `json:"code"`
		Data struct { LiveStatus int `json:"live_status"` } `json:"data"`
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&payload); err != nil { return "unknown", err }
	if payload.Code != 0 { return "unknown", fmt.Errorf("Bilibili room status API error %d", payload.Code) }
	switch payload.Data.LiveStatus {
	case 0: return "offline", nil
	case 1: return "live", nil
	case 2: return "loop", nil
	default: return "unknown", fmt.Errorf("unsupported Bilibili live status %d", payload.Data.LiveStatus)
	}
}

func fetchRoomOwnerUID(ctx context.Context, httpClient *http.Client, endpoint string, roomID int) (int, error) {
	if roomID <= 0 { return 0, errors.New("invalid Bilibili room ID") }
	requestURL, err := url.Parse(endpoint)
	if err != nil { return 0, err }
	query := requestURL.Query()
	query.Set("id", strconv.Itoa(roomID))
	requestURL.RawQuery = query.Encode()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, requestURL.String(), nil)
	if err != nil { return 0, err }
	if httpClient == nil { httpClient = &http.Client{Timeout: 3 * time.Second} }
	response, err := httpClient.Do(request)
	if err != nil { return 0, err }
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 { return 0, fmt.Errorf("Bilibili room owner API returned HTTP %d", response.StatusCode) }
	var payload struct {
		Code int `json:"code"`
		Data struct { UID int `json:"uid"` } `json:"data"`
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&payload); err != nil { return 0, err }
	if payload.Code != 0 { return 0, fmt.Errorf("Bilibili room owner API error %d", payload.Code) }
	if payload.Data.UID <= 0 { return 0, errors.New("Bilibili room owner UID is missing") }
	return payload.Data.UID, nil
}

func (a *Adapter) refreshInitialLiveStatus(ctx context.Context, roomID int, expectedRevision uint64) {
	status, err := fetchRoomLiveStatus(ctx, nil, "https://api.live.bilibili.com/room/v1/Room/room_init", roomID)
	if err != nil { return }
	a.mu.Lock()
	if a.liveStatusRevision == expectedRevision {
		a.liveStatus = status
		a.liveStatusRevision++
	}
	a.mu.Unlock()
}

// SetConnectionFailureHandler registers the manager callback for an unexpected
// stream termination. Intentional Disconnect calls never invoke this handler.
func (a *Adapter) SetConnectionFailureHandler(handler func(error)) {
	if a == nil { return }
	a.mu.Lock()
	a.connectionFailureHandler = handler
	a.mu.Unlock()
}

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
	a.liveStatus = "unknown"
	a.liveStatusRevision++
	initialLiveStatusRevision := a.liveStatusRevision
	a.mu.Unlock()
	if roomID <= 0 { return errors.New("请先设置有效的 B 站直播间房间号") }

	credentials, err := a.auth.LoadCredentials(ctx)
	if err != nil { return err }
	ownerCtx, ownerCancel := context.WithTimeout(ctx, 3*time.Second)
	roomOwnerUID, ownerErr := fetchRoomOwnerUID(ownerCtx, nil, "https://api.live.bilibili.com/room/v1/Room/room_init", roomID)
	ownerCancel()
	if ownerErr != nil { roomOwnerUID = 0 }
	stream := a.newStream(roomID)
	stream.SetCookie(credentials.Cookie)
	stream.OnRawEvent(func(cmd string, raw []byte) {
		baseCmd, _, _ := strings.Cut(cmd, ":")
		switch baseCmd {
		case "LIKE_INFO_V3_CLICK":
			a.addLikeEvent(roomID, raw)
		case "SEND_GIFT":
			a.addGiftEventImage(roomID, raw)
		}
	})
	stream.OnLiveStart(func(*message.LiveStart) { a.setLiveStatus("live") })
	stream.OnLiveStop(func(*message.LiveStop) { a.setLiveStatus("offline") })

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
		timestamp = normalizeTimestamp(timestamp)
		a.events.Add(eventstream.Event{
			PlatformID: "bilibili", Source: "live", Type: "danmaku", RoomID: roomID,
			UserID: d.Sender.Uid, UserName: d.Sender.Uname, Avatar: d.Sender.Face,
			Content: d.Content, Emoticons: emoticons, Timestamp: timestamp, Color: d.Color,
			IsModerator: d.Sender.Admin, IsRoomOwner: roomOwnerUID > 0 && d.Sender.Uid == roomOwnerUID, UserLevel: int(d.Sender.UserLevel), GuardLevel: d.Sender.GuardLevel,
			FanMedalName: medalName(d.Sender.Medal), FanMedalLevel: medalLevel(d.Sender.Medal), FanMedalColor: medalColor(d.Sender.Medal),
		})
	})

	stream.OnSuperChat(func(sc *message.SuperChat) {
		if sc == nil {
			return
		}
		userID, userName, avatar := sc.Uid, sc.UserInfo.Uname, sc.UserInfo.Face
		guardLevel := sc.UserInfo.GuardLevel
		var medal *message.Medal
		if sc.Sender != nil {
			if userID <= 0 { userID = sc.Sender.Uid }
			userName = firstNonEmpty(sc.Sender.Uname, userName)
			avatar = firstNonEmpty(sc.Sender.Face, avatar)
			guardLevel = sc.Sender.GuardLevel
			medal = sc.Sender.Medal
		}
		if sc.Medal != nil { medal = sc.Medal }
		if userID <= 0 || strings.TrimSpace(userName) == "" || sc.Price <= 0 || strings.TrimSpace(sc.Message) == "" {
			return
		}
		a.events.Add(eventstream.Event{
			PlatformID: "bilibili", Source: "live", Type: "super_chat", RoomID: roomID,
			UserID: userID, UserName: userName, Avatar: avatar,
			Content: sc.Message, Timestamp: normalizeTimestamp(int64(sc.StartTime)),
			Amount: float64(sc.Price), Duration: sc.Time, GuardLevel: guardLevel,
			FanMedalName: medalName(medal), FanMedalLevel: medalLevel(medal), FanMedalColor: medalColor(medal),
		})
	})
	stream.OnGuardBuy(func(guard *message.GuardBuy) {
		if guard == nil { return }
		key := guardEventKey(guard.Uid, guard.GuardLevel, guard.Num, guard.StartTime)
		a.addGuardEvent(roomID, guard.Uid, guard.Username, "", guard.GuardLevel, guard.Num, guard.Price, guard.StartTime, key, false)
	})
	stream.OnUserToast(func(toast *message.UserToast) {
		if toast == nil { return }
		key := guardEventKey(toast.Uid, toast.GuardLevel, toast.Num, toast.StartTime)
		a.addGuardEvent(roomID, toast.Uid, toast.Username, toast.Face, toast.GuardLevel, toast.Num, toast.Price, toast.StartTime, key, true)
	})
	stream.OnInteractWord(func(interaction *message.InteractWord) {
		if interaction == nil || interaction.User == nil { return }
		eventType, content := "", ""
		switch interaction.MsgType {
		case 1:
			eventType, content = "welcome", "进入了直播间"
		case 2:
			eventType, content = "follow", "关注了直播间"
		case 3:
			eventType, content = "share", "分享了直播间"
		default:
			return
		}
		user := interaction.User
		timestamp := normalizeTimestamp(int64(interaction.Timestamp))
		if interaction.TriggerTime > 0 {
			timestamp = normalizeTimestamp(interaction.TriggerTime / 1_000_000)
		}
		medal := user.Medal
		a.events.Add(eventstream.Event{
			PlatformID: "bilibili", Source: "live", Type: eventType, RoomID: roomID,
			UserID: user.Uid, UserName: user.Uname, Avatar: user.Face, Content: content,
			Timestamp: timestamp, GuardLevel: user.GuardLevel, UserLevel: int(user.UserLevel),
			FanMedalName: medalName(medal), FanMedalLevel: medalLevel(medal), FanMedalColor: medalColor(medal),
		})
	})

	stream.OnGift(func(g *message.Gift) {
		if g == nil { return }
		count := g.Num
		if count <= 0 { count = 1 }
		timestamp := int64(g.Timestamp)
		if timestamp <= 0 { timestamp = time.Now().UnixMilli() } else if timestamp < 1_000_000_000_000 { timestamp *= 1000 }
		var unitPrice, totalPrice float64
		if g.IsPaid() {
			unitPrice = float64(g.Price) / 1000
			totalPrice = float64(g.TotalCoin) / 1000
			if totalPrice <= 0 {
				totalPrice = unitPrice * float64(count)
			}
		}
		giftImageURL := a.giftCatalog.ImageURL(roomID, g.GiftId)
		giftAnimationURL := a.giftCatalog.AnimationURL(roomID, g.GiftId)
		if a.mediaCatalog != nil {
			if item, ok := a.mediaCatalog.Find("bilibili", strconv.Itoa(roomID), resource.TypeGift, strconv.Itoa(g.GiftId)); ok {
				giftImageURL = item.ImageURL
				giftAnimationURL = item.AnimationURL
			}
		}
		a.events.Add(eventstream.Event{
			PlatformID: "bilibili", Source: "live", Type: "gift", RoomID: roomID,
			UserID: g.Uid, UserName: g.Uname, Avatar: g.Face,
			Content: fmt.Sprintf("赠送了 %s × %d", g.GiftName, count),
			Timestamp: timestamp, GiftID: g.GiftId, GiftResourceID: strconv.Itoa(g.GiftId), GiftName: g.GiftName,
			GiftCount: count, UnitPrice: unitPrice, TotalPrice: totalPrice,
			GiftImageURL: giftImageURL, GiftAnimationURL: giftAnimationURL,
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
	go a.refreshInitialLiveStatus(runCtx, roomID, initialLiveStatusRevision)
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
	giftRefreshAt, hasGiftRefreshAt := time.Time{}, false
	if a.mediaCatalog != nil {
		giftRefreshAt, hasGiftRefreshAt = a.mediaCatalog.LastRefreshed("bilibili", strconv.Itoa(roomID), resource.TypeGift)
	}
	giftCatalogFresh := a.mediaCatalog != nil &&
		a.mediaCatalog.IsFresh("bilibili", strconv.Itoa(roomID), resource.TypeGift, 24*time.Hour) &&
		giftCatalogMetadataCurrent(a.mediaCatalog, roomID)
	if hasGiftRefreshAt {
		log.Printf("[bilibili] room %d gift catalog refresh decision: last_successful_refresh=%s; current_clock=%s; fresh=%t",
			roomID, formatClockDiagnostic(giftRefreshAt), formatClockDiagnostic(time.Now()), giftCatalogFresh)
	} else {
		log.Printf("[bilibili] room %d gift catalog refresh decision: no persisted refresh timestamp; current_clock=%s; fresh=%t",
			roomID, formatClockDiagnostic(time.Now()), giftCatalogFresh)
	}
	if a.giftCatalog != nil && !giftCatalogFresh {
		refreshStartedAt := time.Now()
		log.Printf("[bilibili] room %d gift catalog refresh started (persistent catalog fresh=%t; clock %s)", roomID, giftCatalogFresh, formatClockDiagnostic(refreshStartedAt))
		go func() {
			catalogCtx, catalogCancel := context.WithTimeout(runCtx, 8*time.Second)
			defer catalogCancel()
			if err := a.giftCatalog.ForceRefresh(catalogCtx, roomID); err != nil {
				failedAt := time.Now()
				log.Printf("[bilibili] room %d gift catalog refresh failed: %v (failed_at %s)", roomID, err, formatClockDiagnostic(failedAt))
				return
			}
			refreshedAt := time.Now()
			log.Printf("[bilibili] room %d gift catalog remote request succeeded (refresh_started_at %s; response_validated_at %s)", roomID, formatClockDiagnostic(refreshStartedAt), formatClockDiagnostic(refreshedAt))
			items := a.giftCatalog.Resources(roomID)
			if len(items) == 0 {
				log.Printf("[bilibili] room %d gift catalog refresh returned no resources (validated_at %s)", roomID, formatClockDiagnostic(refreshedAt))
				return
			}
			if a.mediaCatalog != nil {
				registered := 0
				for _, item := range items {
					if a.mediaCatalog.Register(item) {
						registered++
					}
					if giftID, parseErr := strconv.Atoi(item.ID); parseErr == nil {
						a.events.UpdateGiftImage("bilibili", roomID, giftID, item.ImageURL, item.AnimationURL)
					}
				}
				if !a.mediaCatalog.MarkRefreshed("bilibili", strconv.Itoa(roomID), resource.TypeGift, refreshedAt) {
					log.Printf("[bilibili] room %d gift catalog fetched %d items but failed to mark refresh time (attempted_timestamp %s)", roomID, len(items), formatClockDiagnostic(refreshedAt))
					return
				}
				log.Printf("[bilibili] room %d gift catalog refreshed: %d items registered; persisted_refresh_timestamp %s", roomID, registered, formatClockDiagnostic(refreshedAt))
			} else {
				log.Printf("[bilibili] room %d gift catalog fetched %d items; persistent media catalog unavailable (fetched_at %s)", roomID, len(items), formatClockDiagnostic(refreshedAt))
			}
		}()
	} else if giftCatalogFresh {
		log.Printf("[bilibili] room %d gift catalog refresh skipped: persistent catalog is fresh", roomID)
	}

	go func() {
		stream.Wait()
		a.mu.Lock()
		unexpected := a.client == stream
		var cancelStream context.CancelFunc
		var failureHandler func(error)
		if unexpected {
			cancelStream = a.cancel
			a.client, a.cancel = nil, nil
			a.liveStatus = "unknown"
			a.liveStatusRevision++
			failureHandler = a.connectionFailureHandler
		}
		a.mu.Unlock()
		if cancelStream != nil { cancelStream() }
		if unexpected && failureHandler != nil {
			failureHandler(errors.New("Bilibili live stream stopped unexpectedly"))
		}
	}()
	return nil
}

func (a *Adapter) Disconnect(ctx context.Context) error {
	if a == nil { return nil }
	if ctx == nil { return errors.New("Bilibili disconnection context is required") }
	a.mu.Lock()
	stream, cancel := a.client, a.cancel
	a.client, a.cancel = nil, nil
	a.liveStatus = "unknown"
	a.liveStatusRevision++
	a.mu.Unlock()
	if stream == nil { return nil }
	if cancel != nil { cancel() }
	stream.Stop()
	select {
	case <-stream.Done(): return nil
	case <-ctx.Done(): return ctx.Err()
	}
}
