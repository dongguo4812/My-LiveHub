package desktop

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/dongguo4812/My-LiveHub/app/internal/platform"
	"github.com/dongguo4812/My-LiveHub/app/internal/media/resource"
	"github.com/dongguo4812/My-LiveHub/app/internal/media/imagecache"
	eventstream "github.com/dongguo4812/My-LiveHub/app/internal/events"
	"github.com/wailsapp/wails/v3/pkg/application"
)

type PlatformService struct {
	manager    *platform.Manager
	events     *eventstream.Buffer
	mediaCatalog *resource.Catalog
	imageCache *imagecache.Manager
	app        *application.App
	lightTheme bool
}

func NewPlatformService(manager *platform.Manager, events *eventstream.Buffer, catalogs ...*resource.Catalog) *PlatformService {
	service := &PlatformService{manager: manager, events: events}
	if len(catalogs) > 0 { service.mediaCatalog = catalogs[0] }
	return service
}

// GetMediaResources returns a bounded, platform-isolated list for the simulator.
func (s *PlatformService) GetMediaResources(platformID, resourceType, keyword string, limit int) []resource.Resource {
	if s == nil || s.mediaCatalog == nil || strings.TrimSpace(platformID) == "" { return []resource.Resource{} }
	kind := resource.Type(resourceType)
	if kind != resource.TypeGift && kind != resource.TypeEmoticon { return []resource.Resource{} }
	if limit <= 0 { limit = 1000 } else if limit > 1000 { limit = 1000 }
	keyword = strings.ToLower(strings.TrimSpace(keyword))
	items := s.mediaCatalog.List(strings.TrimSpace(platformID), "", kind)
	result := make([]resource.Resource, 0, min(limit, len(items)))
	for _, item := range items {
		if keyword != "" && !strings.Contains(strings.ToLower(item.Name), keyword) && !strings.Contains(strings.ToLower(item.ID), keyword) { continue }
		result = append(result, item)
		if len(result) >= limit { break }
	}
	return result
}

// SetApplication is called after the Wails application has been created.
func AttachApplication(s *PlatformService, app *application.App, lightTheme bool) {
	if s == nil {
		return
	}
	s.app = app
	s.lightTheme = lightTheme
}

func (s *PlatformService) GetPlatforms() []platform.ConnectionSnapshot {
	if s == nil || s.manager == nil {
		return nil
	}
	return s.manager.Snapshot()
}

func (s *PlatformService) GetEvents() []eventstream.Event {
	if s == nil || s.events == nil {
		return nil
	}
	return s.events.Snapshot()
}

// GetBilibiliEvents is retained for compatibility with the current simulator UI.
func (s *PlatformService) GetBilibiliEvents() []eventstream.Event {
	return s.GetEvents()
}

func (s *PlatformService) ConnectPlatform(platformID string) error {
	if s == nil || s.manager == nil {
		return errors.New("platform connection manager is unavailable")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	return s.manager.Connect(ctx, platformID)
}

func (s *PlatformService) DisconnectPlatform(platformID string) error {
	if s == nil || s.manager == nil {
		return errors.New("platform connection manager is unavailable")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	return s.manager.Disconnect(ctx, platformID)
}

// OpenSimulatorWindow opens one independent simulator window per platform.
func (s *PlatformService) OpenSimulatorWindow(platformID string) error {
	if s == nil || s.app == nil {
		return errors.New("simulator window service is unavailable")
	}
	platformID = strings.TrimSpace(platformID)
	if platformID == "" || s.manager == nil {
		return errors.New("请选择已注册的直播平台")
	}
	registered := false
	for _, item := range s.manager.Snapshot() {
		if item.PlatformID == platformID { registered = true; break }
	}
	if !registered { return fmt.Errorf("平台 %s 尚未注册", platformID) }
	name := "simulator-" + platformID
	if existing, ok := s.app.Window.GetByName(name); ok {
		existing.Show()
		existing.Focus()
		return nil
	}
	background := application.NewRGBA(18, 20, 27, 255)
	if s.lightTheme {
		background = application.NewRGBA(242, 243, 245, 255)
	}
	window := s.app.Window.NewWithOptions(application.WebviewWindowOptions{
		Name: name, Title: platformID + " 互动模拟器", Width: 1080, Height: 780,
		MinWidth: 900, MinHeight: 640, BackgroundColour: background,
		URL: "/#/simulator/" + url.PathEscape(platformID),
	})
	window.Center()
	window.Show()
	return nil
}

// SimulatePlatformEvent injects a local event through the shared platform event pipeline.
func (s *PlatformService) SimulatePlatformEvent(
	platformID string,
	eventType string,
	userID int,
	userName string,
	content string,
	giftName string,
	giftCount int,
	unitPrice float64,
	amount float64,
	duration int,
	guardLevel int,
	months int,
) (eventstream.Event, error) {
	if s == nil || s.events == nil {
		return eventstream.Event{}, errors.New("platform event service is unavailable")
	}
	return s.SimulatePlatformEventWithResource(platformID, eventType, userID, userName, content, giftName, giftCount, unitPrice, amount, duration, guardLevel, months, 0, "", "")
}

// SimulatePlatformEventWithResource carries the selected catalog identity and image URL into the shared event pipeline.
func (s *PlatformService) SimulatePlatformEventWithResource(platformID, eventType string, userID int, userName, content, giftName string, giftCount int, unitPrice, amount float64, duration, guardLevel, months, giftID int, giftImageURL string, giftResourceID string) (eventstream.Event, error) {
	return s.SimulatePlatformEventWithMediaResources(platformID, eventType, userID, userName, content, giftName, giftCount, unitPrice, amount, duration, guardLevel, months, giftID, giftImageURL, giftResourceID, nil)
}

// SimulatePlatformEventWithMediaResources resolves selected emoticon IDs from the platform catalog.
// The simulator never accepts arbitrary image URLs: event tokens are built from registered resources.
func (s *PlatformService) SimulatePlatformEventWithMediaResources(platformID, eventType string, userID int, userName, content, giftName string, giftCount int, unitPrice, amount float64, duration, guardLevel, months, giftID int, giftImageURL, giftResourceID string, emoticonResourceIDs []string) (eventstream.Event, error) {
	if s == nil || s.events == nil { return eventstream.Event{}, errors.New("platform event service is unavailable") }
	var emoticons []eventstream.EmoticonToken
	if eventType == "danmaku" && s.mediaCatalog != nil && len(emoticonResourceIDs) > 0 {
		// The simulator passes a composite sourceID/resourceID key so resources
		// with the same platform ID in different scopes remain distinguishable.
		catalogItems := make(map[string]resource.Resource)
		for _, item := range s.mediaCatalog.List(platformID, "", resource.TypeEmoticon) {
			key := item.SourceID + "\x00" + item.ID
			catalogItems[key] = item
		}
		seen := make(map[string]struct{}, len(emoticonResourceIDs))
		seenNames := make(map[string]struct{}, len(emoticonResourceIDs))
		for _, selectedKey := range emoticonResourceIDs {
			selectedKey = strings.TrimSpace(selectedKey)
			if selectedKey == "" { continue }
			if _, duplicate := seen[selectedKey]; duplicate { continue }
			seen[selectedKey] = struct{}{}
			item, ok := catalogItems[selectedKey]
			if !ok || item.Name == "" || item.ImageURL == "" || !strings.Contains(content, item.Name) { continue }
			// One text token must resolve to one image. Keep the first explicitly
			// selected resource when callers submit duplicate names from scopes.
			if _, duplicate := seenNames[item.Name]; duplicate { continue }
			seenNames[item.Name] = struct{}{}
			emoticons = append(emoticons, eventstream.EmoticonToken{Text: item.Name, ResourceID: item.SourceID + ":" + item.ID, URL: item.ImageURL, Width: item.Width, Height: item.Height})
		}
	}
	return s.events.AddSimulationEvent(platformID, eventstream.SimulationEventInput{
		Type: eventType, UserID: userID, UserName: userName, Content: content, GiftName: giftName,
		GiftCount: giftCount, UnitPrice: unitPrice, Amount: amount, Duration: duration,
		GuardLevel: guardLevel, Months: months, GiftID: giftID, GiftResourceID: giftResourceID, GiftImageURL: giftImageURL,
		Emoticons: emoticons,
	})
}

// SimulateBilibiliEvent is retained for compatibility with the current simulator UI.
func (s *PlatformService) SimulateBilibiliEvent(eventType string, userID int, userName, content, giftName string, giftCount int, unitPrice, amount float64, duration, guardLevel, months int) (eventstream.Event, error) {
	return s.SimulatePlatformEvent("bilibili", eventType, userID, userName, content, giftName, giftCount, unitPrice, amount, duration, guardLevel, months)
}


// SetMediaImageCache attaches the local image cache statistics provider.
func (s *PlatformService) SetMediaImageCache(cache *imagecache.Manager) {
	if s != nil { s.imageCache = cache }
}

// RefreshMediaCacheStats starts a non-blocking, read-only disk scan.
func (s *PlatformService) RefreshMediaCacheStats() {
	if s != nil && s.imageCache != nil { s.imageCache.RefreshStats() }
}

// GetMediaCacheStats returns the latest completed statistics snapshot.
func (s *PlatformService) GetMediaCacheStats() imagecache.CacheStats {
	if s == nil || s.imageCache == nil { return imagecache.CacheStats{Platforms: []imagecache.PlatformCacheStats{}} }
	return s.imageCache.GetStats()
}
