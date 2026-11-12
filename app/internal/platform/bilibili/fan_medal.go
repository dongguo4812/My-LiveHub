package bilibili

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

// FanMedalChecker verifies medals owned by the broadcaster, regardless of
// whether the viewer is currently wearing that medal.
type FanMedalChecker struct {
	auth *AuthManager
	mu sync.Mutex
	roomID int
	ownerUID int64
	ownerExpires time.Time
	cache map[string]medalCacheEntry
}

type medalWallItem struct {
	MedalInfo struct {
		TargetID int64 `json:"target_id"`
		Level int `json:"level"`
	} `json:"medal_info"`
}

func findFanMedalLevel(items []medalWallItem, ownerUID int64) int {
	level := 0
	for _, item := range items {
		if item.MedalInfo.TargetID == ownerUID && item.MedalInfo.Level > level {
			level = item.MedalInfo.Level
		}
	}
	return level
}

type medalCacheEntry struct {
	level int
	expires time.Time
}

func NewFanMedalChecker(auth *AuthManager, roomID int) *FanMedalChecker {
	return &FanMedalChecker{auth: auth, roomID: roomID, cache: make(map[string]medalCacheEntry)}
}

func (c *FanMedalChecker) SetRoomID(roomID int) {
	if c == nil { return }
	c.mu.Lock()
	if c.roomID != roomID {
		c.roomID, c.ownerUID, c.ownerExpires = roomID, 0, time.Time{}
		c.cache = make(map[string]medalCacheEntry)
	}
	c.mu.Unlock()
}

// Check returns whether userUID owns the broadcaster's medal at minLevel.
// A hidden medal wall or API/authentication failure returns an error and never
// grants eligibility.
func (c *FanMedalChecker) Check(parent context.Context, userUID, minLevel int) (bool, int, error) {
	if c == nil || c.auth == nil { return false, 0, errors.New("粉丝勋章查询服务不可用") }
	if userUID <= 0 { return false, 0, errors.New("无法获取点播用户 UID") }
	if minLevel < 1 || minLevel > 100 { return false, 0, errors.New("粉丝勋章最低等级设置无效") }
	if parent == nil { parent = context.Background() }
	ctx, cancel := context.WithTimeout(parent, 6*time.Second)
	defer cancel()

	c.mu.Lock()
	defer c.mu.Unlock()
	if c.roomID <= 0 { return false, 0, errors.New("尚未设置有效的 B 站直播间房间号") }
	ownerUID, err := c.getOwnerUID(ctx)
	if err != nil { return false, 0, err }
	now := time.Now()
	for cachedKey, entry := range c.cache { if !now.Before(entry.expires) { delete(c.cache, cachedKey) } }
	if len(c.cache) > 5000 { c.cache = make(map[string]medalCacheEntry) }
	key := fmt.Sprintf("%d:%d", ownerUID, userUID)
	if cached, ok := c.cache[key]; ok && now.Before(cached.expires) {
		return cached.level >= minLevel && cached.level > 0, cached.level, nil
	}

	credentials, err := c.auth.store.Load()
	if err != nil { return false, 0, fmt.Errorf("读取 B 站登录凭据失败：%w", err) }
	var saved Credentials
	if err := json.Unmarshal(credentials, &saved); err != nil || strings.TrimSpace(saved.Cookie) == "" {
		return false, 0, errors.New("B 站登录凭据无效，请重新登录")
	}
	endpoint, _ := url.Parse("https://api.live.bilibili.com/xlive/web-ucenter/user/MedalWall")
	query := endpoint.Query()
	query.Set("target_id", strconv.Itoa(userUID))
	endpoint.RawQuery = query.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil { return false, 0, err }
	setWebHeaders(req)
	req.Header.Set("Cookie", saved.Cookie)
	resp, err := c.auth.client.Do(req)
	if err != nil { return false, 0, fmt.Errorf("查询用户粉丝勋章失败：%w", err) }
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return false, 0, fmt.Errorf("粉丝勋章接口返回 HTTP %d", resp.StatusCode)
	}
	var payload struct {
		Code int `json:"code"`
		Message string `json:"message"`
		Data struct {
			List []medalWallItem `json:"list"`
		} `json:"data"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 2<<20)).Decode(&payload); err != nil {
		return false, 0, fmt.Errorf("解析粉丝勋章响应失败：%w", err)
	}
	if payload.Code != 0 { return false, 0, fmt.Errorf("粉丝勋章接口错误 %d：%s", payload.Code, safeMessage(payload.Message)) }
	level := findFanMedalLevel(payload.Data.List, ownerUID)
	ttl := 2 * time.Minute
	if level > 0 { ttl = 5 * time.Minute }
	c.cache[key] = medalCacheEntry{level: level, expires: time.Now().Add(ttl)}
	return level >= minLevel && level > 0, level, nil
}

func (c *FanMedalChecker) getOwnerUID(ctx context.Context) (int64, error) {
	if c.ownerUID > 0 && time.Now().Before(c.ownerExpires) { return c.ownerUID, nil }
	endpoint, _ := url.Parse("https://api.live.bilibili.com/room/v1/Room/room_init")
	query := endpoint.Query()
	query.Set("id", strconv.Itoa(c.roomID))
	endpoint.RawQuery = query.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil { return 0, err }
	setWebHeaders(req)
	resp, err := c.auth.client.Do(req)
	if err != nil { return 0, fmt.Errorf("查询直播间主播 UID 失败：%w", err) }
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 { return 0, fmt.Errorf("直播间信息接口返回 HTTP %d", resp.StatusCode) }
	var payload struct {
		Code int `json:"code"`
		Message string `json:"message"`
		Data struct { UID int64 `json:"uid"` } `json:"data"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&payload); err != nil { return 0, fmt.Errorf("解析直播间信息失败：%w", err) }
	if payload.Code != 0 || payload.Data.UID <= 0 { return 0, fmt.Errorf("获取直播间主播 UID 失败：%s", safeMessage(payload.Message)) }
	c.ownerUID, c.ownerExpires = payload.Data.UID, time.Now().Add(10*time.Minute)
	return c.ownerUID, nil
}
