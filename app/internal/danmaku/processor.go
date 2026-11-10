package danmaku

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/dongguo4812/My-LiveHub/app/internal/config"
	"github.com/dongguo4812/My-LiveHub/app/internal/events"
)

// DisplayEvent is a presentation-safe copy of an ingress event.
type DisplayEvent struct {
	Event         events.Event `json:"event"`
	HighlightUntil int64        `json:"highlight_until,omitempty"`
}

// Processor applies display preferences without mutating the shared event history.
type Processor struct{}

func NewProcessor() *Processor { return &Processor{} }

// Process filters, de-duplicates and merges a snapshot for display.
func (p *Processor) Process(input []events.Event, settings config.DanmakuDisplayConfig, now time.Time) []DisplayEvent {
	if len(input) == 0 {
		return []DisplayEvent{}
	}
	ordered := append([]events.Event(nil), input...)
	sort.SliceStable(ordered, func(i, j int) bool {
		if ordered[i].Timestamp == ordered[j].Timestamp {
			return ordered[i].ID < ordered[j].ID
		}
		return ordered[i].Timestamp < ordered[j].Timestamp
	})

	result := make([]DisplayEvent, 0, len(ordered))
	lastDanmaku := make(map[string]int64)
	lastGiftIndex := make(map[string]int)
	lastLikeIndex := make(map[string]int)
	keepAfter := now.UnixMilli() - int64(settings.DisplayDurationSeconds)*1000

	for _, event := range ordered {
		if event.Timestamp <= 0 || event.Timestamp < keepAfter || !enabled(event, settings) {
			continue
		}
		if event.Type == "danmaku" && settings.DuplicateDanmakuFilterSeconds > 0 {
			key := danmakuKey(event)
			window := int64(settings.DuplicateDanmakuFilterSeconds) * 1000
			if previous, ok := lastDanmaku[key]; ok && event.Timestamp-previous <= window && event.Timestamp >= previous {
				lastDanmaku[key] = event.Timestamp
				continue
			}
			lastDanmaku[key] = event.Timestamp
		}


		if event.Type == "like" && settings.DuplicateDanmakuFilterSeconds > 0 {
			key := likeKey(event)
			if index, ok := lastLikeIndex[key]; ok {
				previous := &result[index]
				delta := event.Timestamp - previous.Event.Timestamp
				if delta >= 0 && delta <= int64(settings.DuplicateDanmakuFilterSeconds)*1000 {
					mergeLike(previous, event)
					merged := *previous
					result = append(result[:index], result[index+1:]...)
					result = append(result, merged)
					for existingKey, likeIndex := range lastLikeIndex {
						if likeIndex > index {
							lastLikeIndex[existingKey] = likeIndex - 1
						}
					}
					for existingKey, giftIndex := range lastGiftIndex {
						if giftIndex > index {
							lastGiftIndex[existingKey] = giftIndex - 1
						}
					}
					lastLikeIndex[key] = len(result) - 1
					continue
				}
			}
			if event.LikeCount < 1 {
				event.LikeCount = 1
			}
			lastLikeIndex[key] = len(result)
		}

		if event.Type == "gift" && settings.GiftMergeSeconds > 0 {
			key := giftKey(event)
			if index, ok := lastGiftIndex[key]; ok {
				previous := &result[index]
				delta := event.Timestamp - previous.Event.Timestamp
				if delta >= 0 && delta <= int64(settings.GiftMergeSeconds)*1000 {
					mergeGift(previous, event, settings)
					// Copy before shifting the slice: previous points into result.
					merged := *previous
					// A merged gift represents the latest activity, so move it to
					// the end of the display list to preserve chronological order.
					result = append(result[:index], result[index+1:]...)
					result = append(result, merged)
					for giftKey, giftIndex := range lastGiftIndex {
						if giftIndex > index {
							lastGiftIndex[giftKey] = giftIndex - 1
						}
					}
					for likeKey, likeIndex := range lastLikeIndex {
						if likeIndex > index {
							lastLikeIndex[likeKey] = likeIndex - 1
						}
					}
					lastGiftIndex[key] = len(result) - 1
					continue
				}
			}
			lastGiftIndex[key] = len(result)
		}

		display := DisplayEvent{Event: event}
		if event.Type == "gift" && settings.HighlightGifts {
			display.HighlightUntil = event.Timestamp + int64(settings.GiftHighlightMinutes)*60*1000
		}
		result = append(result, display)
	}
	return result
}

func enabled(event events.Event, settings config.DanmakuDisplayConfig) bool {
	switch event.Type {
	case "danmaku":
		return settings.ShowDanmaku
	case "gift":
		if !settings.ShowGifts {
			return false
		}
		if !settings.ShowFreeGifts && event.UnitPrice <= 0 && event.TotalPrice <= 0 {
			return false
		}
		return true
	case "super_chat":
		return settings.ShowSuperChat
	case "guard":
		return settings.ShowGuard
	case "follow":
		return settings.ShowFollows
	case "like":
		return settings.ShowLikes
	case "welcome", "entry":
		return settings.ShowWelcome
	default:
		return false
	}
}

func danmakuKey(event events.Event) string {
	return fmt.Sprintf("%s\x00%s\x00%d\x00%s", event.PlatformID, event.Source, event.UserID, event.Content)
}


func likeKey(event events.Event) string {
	return fmt.Sprintf("%s\x00%s\x00%d\x00%d", event.PlatformID, event.Source, event.RoomID, event.UserID)
}

func mergeLike(target *DisplayEvent, incoming events.Event) {
	count := incoming.LikeCount
	if count < 1 {
		count = 1
	}
	baseContent := strings.TrimSpace(target.Event.Content)
	if target.Event.LikeCount > 1 {
		baseContent = strings.TrimSuffix(baseContent, fmt.Sprintf(" × %d", target.Event.LikeCount))
	}
	target.Event.LikeCount += count
	target.Event.Content = fmt.Sprintf("%s × %d", baseContent, target.Event.LikeCount)
	target.Event.Timestamp = incoming.Timestamp
}

func giftKey(event events.Event) string {
	return fmt.Sprintf("%s\x00%s\x00%d\x00%s", event.PlatformID, event.Source, event.UserID, strings.TrimSpace(event.GiftName))
}

func mergeGift(target *DisplayEvent, incoming events.Event, settings config.DanmakuDisplayConfig) {
	count := incoming.GiftCount
	if count < 1 {
		count = 1
	}
	target.Event.GiftCount += count
	total := incoming.TotalPrice
	if total == 0 && incoming.UnitPrice > 0 {
		total = incoming.UnitPrice * float64(count)
	}
	target.Event.TotalPrice += total
	if incoming.Timestamp > target.Event.Timestamp {
		target.Event.Timestamp = incoming.Timestamp
	}
	target.Event.Content = fmt.Sprintf("赠送了 %s × %d", target.Event.GiftName, target.Event.GiftCount)
	if settings.HighlightGifts {
		target.HighlightUntil = target.Event.Timestamp + int64(settings.GiftHighlightMinutes)*60*1000
	}
}
