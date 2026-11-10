package resource

import (
	"sort"
	"strings"
	"sync"
	"time"
)

type Type string

const (
	TypeEmoticon Type = "emoticon"
	TypeGift     Type = "gift"
)

type Resource struct {
	PlatformID  string    `json:"platform_id"`
	SourceID    string    `json:"source_id,omitempty"`
	Type        Type      `json:"resource_type"`
	ID          string    `json:"resource_id,omitempty"`
	Name        string    `json:"name"`
	ImageURL    string    `json:"image_url"`
	UnitPrice   float64   `json:"unit_price,omitempty"`
	Width       int       `json:"width,omitempty"`
	Height      int       `json:"height,omitempty"`
	SortOrder   int       `json:"sort_order,omitempty"`
	FirstSeenAt time.Time `json:"first_seen_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

type Catalog struct {
	mu              sync.RWMutex
	items           map[string]Resource
	imageURLIndex   map[string]map[string]struct{}
	provisionalURLs map[string]string
	refreshes       map[string]time.Time
	scopeDirectories map[string]string
	persistence     *catalogPersistence
	closed          bool
}

func NewCatalog() *Catalog {
	return &Catalog{
		items: make(map[string]Resource),
		imageURLIndex: make(map[string]map[string]struct{}),
		provisionalURLs: make(map[string]string),
		refreshes: make(map[string]time.Time),
		scopeDirectories: make(map[string]string),
	}
}


// SetPlatformScopeDirectory configures the directory name used for this
// platform's non-common resource scope. Platform adapters own this terminology.
func (c *Catalog) SetPlatformScopeDirectory(platformID, directory string) bool {
	if c == nil {
		return false
	}
	platformID = strings.TrimSpace(platformID)
	directory = strings.TrimSpace(directory)
	if platformID == "" || directory == "" || directory == "common" || safeCatalogSegment(directory) != directory {
		return false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return false
	}
	c.scopeDirectories[platformID] = directory
	return true
}

func imageURLIdentity(platformID string, kind Type, imageURL string) string {
	return strings.Join([]string{platformID, string(kind), imageURL}, "\x00")
}

func provisionalIdentity(r Resource) string {
	return strings.Join([]string{r.PlatformID, r.SourceID, string(r.Type), r.ImageURL}, "\x00")
}

func (c *Catalog) addImageIndex(key string, r Resource) {
	indexKey := imageURLIdentity(r.PlatformID, r.Type, r.ImageURL)
	keys := c.imageURLIndex[indexKey]
	if keys == nil { keys = make(map[string]struct{}); c.imageURLIndex[indexKey] = keys }
	keys[key] = struct{}{}
	if r.ID == r.ImageURL { c.provisionalURLs[provisionalIdentity(r)] = key }
}

func (c *Catalog) removeImageIndex(key string, r Resource) {
	indexKey := imageURLIdentity(r.PlatformID, r.Type, r.ImageURL)
	if keys := c.imageURLIndex[indexKey]; keys != nil {
		delete(keys, key)
		if len(keys) == 0 { delete(c.imageURLIndex, indexKey) }
	}
	if r.ID == r.ImageURL {
		provisionalKey := provisionalIdentity(r)
		if c.provisionalURLs[provisionalKey] == key { delete(c.provisionalURLs, provisionalKey) }
	}
}

func identity(r Resource) string {
	return strings.Join([]string{r.PlatformID, r.SourceID, string(r.Type), r.ID}, "\x00")
}

func refreshIdentity(platformID, sourceID string, kind Type) string {
	return strings.Join([]string{strings.TrimSpace(platformID), strings.TrimSpace(sourceID), string(kind)}, "\x00")
}

// Register updates the in-memory catalog and only signals the persistence worker.
// It never performs filesystem I/O on the caller's goroutine.
func (c *Catalog) Register(r Resource) bool {
	if c == nil {
		return false
	}
	r.PlatformID = strings.TrimSpace(r.PlatformID)
	r.SourceID = strings.TrimSpace(r.SourceID)
	r.ID = strings.TrimSpace(r.ID)
	r.Name = strings.TrimSpace(r.Name)
	r.ImageURL = strings.TrimSpace(r.ImageURL)
	if r.PlatformID == "" || (r.Type != TypeEmoticon && r.Type != TypeGift) || r.ImageURL == "" {
		return false
	}
	if r.ID == "" {
		r.ID = r.ImageURL
	}

	now := time.Now()
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return false
	}

	key := identity(r)
	old, exists := c.items[key]
	promoted := false
	if !exists {
		provisionalKey := provisionalIdentity(r)
		if previousKey, ok := c.provisionalURLs[provisionalKey]; ok {
			if previous, found := c.items[previousKey]; found {
				old, exists, promoted = previous, true, true
				c.removeImageIndex(previousKey, previous)
				delete(c.items, previousKey)
			}
		}
	}
	if exists {
		if !promoted { c.removeImageIndex(key, old) }
		r.FirstSeenAt = old.FirstSeenAt
		if r.FirstSeenAt.IsZero() { r.FirstSeenAt = now }
		if r.Name == "" { r.Name = old.Name }
		// Live events may update an existing directory entry without carrying
		// its source ordering. Preserve the last known directory position.
		if r.SortOrder <= 0 && old.SortOrder > 0 { r.SortOrder = old.SortOrder }
		if !promoted && old.Name == r.Name && old.ImageURL == r.ImageURL && old.UnitPrice == r.UnitPrice && old.Width == r.Width && old.Height == r.Height && old.SortOrder == r.SortOrder {
			c.addImageIndex(key, old)
			return true
		}
		r.UpdatedAt = now
	} else {
		if r.FirstSeenAt.IsZero() { r.FirstSeenAt = now }
		if r.UpdatedAt.IsZero() { r.UpdatedAt = now }
	}
	c.items[key] = r
	c.addImageIndex(key, r)
	if c.persistence != nil {
		c.persistence.markDirty()
	}
	return true
}

// IsFresh reports whether a source's last successful directory refresh is within ttl.
func (c *Catalog) IsFresh(platformID, sourceID string, kind Type, ttl time.Duration) bool {
	if c == nil || ttl <= 0 {
		return false
	}
	c.mu.RLock()
	refreshedAt := c.refreshes[refreshIdentity(platformID, sourceID, kind)]
	c.mu.RUnlock()
	return !refreshedAt.IsZero() && time.Since(refreshedAt) < ttl
}

// MarkRefreshed records a successful directory refresh. Call it only after the
// remote request and response validation have completed successfully.
func (c *Catalog) MarkRefreshed(platformID, sourceID string, kind Type, at time.Time) bool {
	if c == nil || strings.TrimSpace(platformID) == "" || (kind != TypeEmoticon && kind != TypeGift) {
		return false
	}
	if at.IsZero() {
		at = time.Now()
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return false
	}
	if c.refreshes == nil {
		c.refreshes = make(map[string]time.Time)
	}
	c.refreshes[refreshIdentity(platformID, sourceID, kind)] = at
	if c.persistence != nil {
		c.persistence.markDirty()
	}
	return true
}

// FindByImageURL resolves an event's URL to an already-known resource identity.
// Shared resources are preferred when the same image URL appears in multiple scopes.
func (c *Catalog) FindByImageURL(platformID string, kind Type, imageURL string) (Resource, bool) {
	if c == nil || strings.TrimSpace(imageURL) == "" {
		return Resource{}, false
	}
	platformID = strings.TrimSpace(platformID)
	imageURL = strings.TrimSpace(imageURL)
	c.mu.RLock()
	defer c.mu.RUnlock()
	var match Resource
	found := false
	consider := func(key string) {
		item, ok := c.items[key]
		if !ok || (platformID != "" && item.PlatformID != platformID) || (kind != "" && item.Type != kind) { return }
		if !found || (item.SourceID == "common" && match.SourceID != "common") || (item.SourceID == match.SourceID && item.ID < match.ID) {
			match, found = item, true
		}
	}
	if platformID != "" && kind != "" {
		for key := range c.imageURLIndex[imageURLIdentity(platformID, kind, imageURL)] { consider(key) }
	} else {
		for indexKey, keys := range c.imageURLIndex {
			parts := strings.Split(indexKey, "\x00")
			if len(parts) != 3 || parts[2] != imageURL || (platformID != "" && parts[0] != platformID) || (kind != "" && parts[1] != string(kind)) { continue }
			for key := range keys { consider(key) }
		}
	}
	return match, found
}

func (c *Catalog) List(platformID, sourceID string, kind Type) []Resource {
	if c == nil {
		return nil
	}
	c.mu.RLock()
	out := make([]Resource, 0)
	for _, r := range c.items {
		if platformID != "" && r.PlatformID != platformID {
			continue
		}
		if sourceID != "" && r.SourceID != sourceID {
			continue
		}
		if kind != "" && r.Type != kind {
			continue
		}
		out = append(out, r)
	}
	c.mu.RUnlock()
	sort.Slice(out, func(i, j int) bool {
		// Positive positions come from the platform directory response and
		// take precedence over the alphabetical fallback used by older or
		// event-discovered resources.
		if out[i].SortOrder > 0 && out[j].SortOrder > 0 && out[i].SortOrder != out[j].SortOrder {
			return out[i].SortOrder < out[j].SortOrder
		}
		if (out[i].SortOrder > 0) != (out[j].SortOrder > 0) {
			return out[i].SortOrder > 0
		}
		if out[i].Name != out[j].Name {
			return out[i].Name < out[j].Name
		}
		if out[i].PlatformID != out[j].PlatformID {
			return out[i].PlatformID < out[j].PlatformID
		}
		if out[i].SourceID != out[j].SourceID {
			return out[i].SourceID < out[j].SourceID
		}
		return out[i].ID < out[j].ID
	})
	return out
}

// Close stops the persistence worker after it has flushed the latest snapshot.
func (c *Catalog) Close() error {
	if c == nil {
		return nil
	}
	c.mu.Lock()
	c.closed = true
	persistence := c.persistence
	c.mu.Unlock()
	if persistence == nil {
		return nil
	}
	return persistence.close()
}
