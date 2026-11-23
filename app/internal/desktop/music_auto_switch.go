package desktop

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"

	"github.com/dongguo4812/My-LiveHub/app/internal/music"
)

// resolveAndStartLXTrack asks the 洛雪 desktop client to play the selected
// provider result. If playback cannot be confirmed, it searches the remaining
// enabled providers in configured priority order and verifies each candidate.
func (s *MusicService) resolvePlaybackResource(ctx context.Context, track music.Track) (music.PlaybackResource, error) {
	s.mu.RLock()
	searcher, resolver, cache := s.searcher, s.scriptRuntime, s.playbackResourceCache
	s.mu.RUnlock()
	if resolver == nil { return music.PlaybackResource{}, nil }
	return music.NewUnifiedSourceService(searcher, resolver).ResolvePlaybackResource(ctx, track, track.SelectedQuality, cache)
}

func (s *MusicService) resolveAndStartLXTrack(parent context.Context, original music.Track, skipCurrent bool) (music.Track, error) {
	if s == nil || s.engine == nil || s.searcher == nil {
		return music.Track{}, errors.New("洛雪播放服务尚未初始化")
	}
	if parent == nil { parent = context.Background() }
	resolutionCtx, resolutionGeneration, resolutionCancel := s.beginPlaybackResolution(parent)
	defer func() { s.finishPlaybackResolution(resolutionGeneration); resolutionCancel() }()
	ctx, cancel := context.WithTimeout(resolutionCtx, 90*time.Second)
	defer cancel()

	priority := s.enabledSearchSourceIDs()
	currentSource := firstNonEmpty(original.LXSourceKey, original.SourceID)
	ordered := make([]string, 0, len(priority))
	currentIndex := -1
	for i, source := range priority { if source == currentSource { currentIndex = i; break } }
	if currentIndex >= 0 {
		if !skipCurrent { ordered = append(ordered, currentSource) }
		ordered = append(ordered, priority[currentIndex+1:]...)
		ordered = append(ordered, priority[:currentIndex]...)
	} else {
		// A result from a platform that has since been disabled must not be
		// played; only the currently enabled priority list is eligible.
		ordered = append(ordered, priority...)
	}

	var failures []string
	seen := make(map[string]bool)
	for _, source := range ordered {
		if err := ctx.Err(); err != nil { return music.Track{}, err }
		if source == "" || seen[source] { continue }
		seen[source] = true
		candidates := []music.Track{}
		if source == currentSource {
			candidates = append(candidates, original)
		} else {
			query := strings.TrimSpace(original.Title + " " + original.Artist)
			searchCtx, searchCancel := context.WithTimeout(ctx, 15*time.Second)
			s.mu.RLock()
			searcher, resolver := s.searcher, s.scriptRuntime
			s.mu.RUnlock()
			page, searchErr := music.NewUnifiedSourceService(searcher, resolver).SearchSource(searchCtx, source, query, 1)
			searchCancel()
			if searchErr != nil {
				failures = append(failures, source+": 搜索失败："+searchErr.Error())
				continue
			}
			for _, item := range page.Tracks {
				if sameLXTrackIdentity(original, item) { candidates = append(candidates, item) }
			}
			if len(candidates) == 0 {
				failures = append(failures, source+": 未找到歌名和歌手均匹配的歌曲")
				continue
			}
		}

		for _, candidate := range candidates {
			if err := ctx.Err(); err != nil { return music.Track{}, err }
			resource, resolveErr := s.resolvePlaybackResource(ctx, candidate)
			if resolveErr != nil {
				failures = append(failures, source+": 播放资源解析失败："+resolveErr.Error())
				continue
			}
			// Keep the signed URL and media request context process-local. Track's
			// JSON representation deliberately excludes this field.
			if strings.TrimSpace(resource.URL) != "" { candidate.PlaybackResource = &resource }
			var loadErr error
			s.mu.RLock()
			if s.playResolveGeneration != resolutionGeneration {
				s.mu.RUnlock()
				return music.Track{}, context.Canceled
			}
			resourceEngine, resourceAware := s.engine.(interface {
				LoadResource(context.Context, music.Track, music.PlaybackResource) error
			})
			if resource.URL != "" && resourceAware {
				loadErr = resourceEngine.LoadResource(ctx, candidate, resource)
			} else {
				loadErr = s.engine.Load(ctx, candidate)
			}
			s.mu.RUnlock()
			if loadErr != nil && resource.URL != "" && resourceAware {
				// A resource-aware engine gets exactly one invalidation/re-resolution
				// retry. Media adapters still own the final HTTP failure signal.
				s.mu.RLock()
				if s.playResolveGeneration != resolutionGeneration {
					s.mu.RUnlock()
					return music.Track{}, context.Canceled
				}
				cache := s.playbackResourceCache
				searcher, resolver := s.searcher, s.scriptRuntime
				s.mu.RUnlock()
				fresh, refreshErr := music.NewUnifiedSourceService(searcher, resolver).RefreshPlaybackResource(ctx, candidate, resource, cache)
				if refreshErr == nil {
					candidate.PlaybackResource = &fresh
					resource = fresh
					s.mu.RLock()
					if s.playResolveGeneration != resolutionGeneration {
						s.mu.RUnlock()
						return music.Track{}, context.Canceled
					}
					loadErr = resourceEngine.LoadResource(ctx, candidate, fresh)
					s.mu.RUnlock()
				} else {
					loadErr = fmt.Errorf("%v; refresh failed: %w", loadErr, refreshErr)
				}
			}
			if loadErr != nil {
				s.mu.RLock()
				cache := s.playbackResourceCache
				s.mu.RUnlock()
				if cache != nil && resource.Quality != "" { cache.Invalidate(candidate, resource.Quality) }
				failures = append(failures, source+": 播放引擎未能确认播放："+loadErr.Error())
				continue
			}
			return candidate, nil
		}
	}
	if len(failures) == 0 { return music.Track{}, errors.New("没有可用于自动换源的已启用平台") }
	return music.Track{}, fmt.Errorf("所有可用音源均无法播放：%s", strings.Join(failures, "；"))
}

func sameLXTrackIdentity(a, b music.Track) bool {
	return normalizeLXTrackIdentity(a.Title) == normalizeLXTrackIdentity(b.Title) &&
		normalizeLXTrackIdentity(a.Artist) == normalizeLXTrackIdentity(b.Artist)
}

func normalizeLXTrackIdentity(value string) string {
	var result strings.Builder
	for _, r := range strings.ToLower(strings.TrimSpace(value)) {
		if unicode.IsSpace(r) || unicode.IsPunct(r) || unicode.IsSymbol(r) { continue }
		result.WriteRune(r)
	}
	return result.String()
}
