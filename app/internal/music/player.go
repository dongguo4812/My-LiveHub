package music

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

// Capabilities describes the operations a concrete music player actually supports.
type Capabilities struct {
	Search  bool `json:"search"`
	Play    bool `json:"play"`
	Pause   bool `json:"pause"`
	Resume  bool `json:"resume"`
	Stop    bool `json:"stop"`
	Next    bool `json:"next"`
	Seek    bool `json:"seek"`
	Volume  bool `json:"volume"`
}

// Track is player-neutral metadata. Player-specific identifiers remain opaque to the core.
type Track struct {
	ID         string `json:"id"`
	PlayerID   string `json:"player_id"`
	Title      string `json:"title"`
	Artist     string `json:"artist,omitempty"`
	Album      string `json:"album,omitempty"`
	DurationMS int64  `json:"duration_ms,omitempty"`
	CoverURL   string `json:"cover_url,omitempty"`
}

// PlayerStatus is a snapshot reported by a concrete player adapter.
type PlayerStatus struct {
	PlayerID string `json:"player_id"`
	State    string `json:"state"`
	Track    *Track `json:"track,omitempty"`
	PositionMS int64 `json:"position_ms,omitempty"`
	DurationMS int64 `json:"duration_ms,omitempty"`
	Volume   int `json:"volume,omitempty"`
	Error    string `json:"error,omitempty"`
}

// Player is an independently managed music player integration.
// Search and playback belong to the same adapter; adapters must not assume
// that another player's track identifier or playback session is compatible.
type Player interface {
	ID() string
	Name() string
	Capabilities() Capabilities
	Search(context.Context, string, int) ([]Track, error)
	Play(context.Context, Track) error
	Pause(context.Context) error
	Resume(context.Context) error
	Stop(context.Context) error
	Next(context.Context) error
	Status(context.Context) (PlayerStatus, error)
}

// PlayerSnapshot is safe to expose to the management frontend.
type PlayerSnapshot struct {
	ID           string       `json:"id"`
	Name         string       `json:"name"`
	Capabilities Capabilities `json:"capabilities"`
	Available    bool         `json:"available"`
	Error        string       `json:"error,omitempty"`
}

type Registry struct {
	mu      sync.RWMutex
	players map[string]Player
}

func NewRegistry() *Registry {
	return &Registry{players: make(map[string]Player)}
}

func (r *Registry) Register(player Player) error {
	if r == nil {
		return errors.New("music player registry is required")
	}
	if player == nil {
		return errors.New("music player is required")
	}
	id := strings.TrimSpace(player.ID())
	if id == "" || strings.TrimSpace(player.Name()) == "" {
		return errors.New("music player ID and name are required")
	}
	if !player.Capabilities().Play {
		return fmt.Errorf("music player %q must support playback", id)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.players[id]; exists {
		return fmt.Errorf("music player %q is already registered", id)
	}
	r.players[id] = player
	return nil
}

func (r *Registry) Get(id string) (Player, bool) {
	if r == nil {
		return nil, false
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	player, ok := r.players[strings.TrimSpace(id)]
	return player, ok
}

func (r *Registry) List() []PlayerSnapshot {
	if r == nil {
		return []PlayerSnapshot{}
	}
	r.mu.RLock()
	players := make([]Player, 0, len(r.players))
	for _, player := range r.players {
		players = append(players, player)
	}
	r.mu.RUnlock()

	result := make([]PlayerSnapshot, 0, len(players))
	for _, player := range players {
		result = append(result, PlayerSnapshot{
			ID: player.ID(), Name: player.Name(),
			Capabilities: player.Capabilities(), Available: true,
		})
	}
	sort.Slice(result, func(i, j int) bool { return result[i].ID < result[j].ID })
	return result
}

func validateTrack(track Track) error {
	if strings.TrimSpace(track.ID) == "" {
		return errors.New("track ID is required")
	}
	if strings.TrimSpace(track.PlayerID) == "" {
		return errors.New("track player ID is required")
	}
	if strings.TrimSpace(track.Title) == "" {
		return errors.New("track title is required")
	}
	if track.DurationMS < 0 {
		return errors.New("track duration cannot be negative")
	}
	return nil
}

func normalizeSearchLimit(limit int) int {
	if limit <= 0 {
		return 20
	}
	if limit > 50 {
		return 50
	}
	return limit
}

func normalizeSearchKeyword(keyword string) (string, error) {
	keyword = strings.TrimSpace(keyword)
	if keyword == "" {
		return "", errors.New("search keyword is required")
	}
	return keyword, nil
}

func nowUTC() time.Time { return time.Now().UTC() }
