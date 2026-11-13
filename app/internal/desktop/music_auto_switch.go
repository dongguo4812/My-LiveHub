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

// resolveAndStartLXTrack tries the selected platform first, then the remaining
// enabled platforms in their configured priority order. Cross-platform
// candidates must match both title and artist; no album, duration, or native
// platform ID is required.
func (s *MusicService) resolveAndStartLXTrack(parent context.Context, original music.Track, skipCurrent bool) (music.Track, error) {
	if parent == nil {
		parent = context.Background()
	}
	ctx, cancel := context.WithTimeout(parent, 90*time.Second)
	defer cancel()

	activeID, err := s.GetActiveLXUserSourceID()
	if err != nil {
		return music.Track{}, fmt.Errorf("读取当前洛雪自定义源失败：%w", err)
	}
	if activeID == "" {
		return music.Track{}, errors.New("尚未启用洛雪自定义源，无法解析歌曲播放地址")
	}

	s.lxSessionMu.Lock()
	session, sessionID := s.lxSession, s.lxSessionID
	s.lxSessionMu.Unlock()
	if session == nil || sessionID != activeID || !session.IsRunning() {
		if err := s.SetActiveLXUserSource(activeID); err != nil {
			return music.Track{}, fmt.Errorf("启动洛雪自定义源失败：%w", err)
		}
		s.lxSessionMu.Lock()
		session, sessionID = s.lxSession, s.lxSessionID
		s.lxSessionMu.Unlock()
	}
	if session == nil || sessionID != activeID {
		return music.Track{}, errors.New("当前洛雪自定义源未能启动")
	}
	if err := session.WaitInitialized(ctx); err != nil {
		return music.Track{}, fmt.Errorf("等待洛雪音源初始化失败：%w", err)
	}

	priority := s.activeLXSearchSourceIDs()
	currentSource := original.LXSourceKey
	if currentSource == "" {
		currentSource = original.SourceID
	}
	ordered := make([]string, 0, len(priority))
	currentIndex := -1
	for i, source := range priority {
		if source == currentSource {
			currentIndex = i
			break
		}
	}
	if currentIndex >= 0 {
		if !skipCurrent {
			ordered = append(ordered, currentSource)
		}
		ordered = append(ordered, priority[currentIndex+1:]...)
	} else {
		if !skipCurrent && currentSource != "" {
			ordered = append(ordered, currentSource)
		}
		ordered = append(ordered, priority...)
	}

	var failures []string
	for _, source := range ordered {
		if err := ctx.Err(); err != nil {
			return music.Track{}, err
		}
		var candidate music.Track
		if source == currentSource {
			candidate = original
		} else {
			query := strings.TrimSpace(original.Title + " " + original.Artist)
			searchCtx, searchCancel := context.WithTimeout(ctx, 15*time.Second)
			page, searchErr := s.searcher.SearchProvider(searchCtx, source, query, 1)
			searchCancel()
			if searchErr != nil {
				failures = append(failures, source+": 搜索失败："+searchErr.Error())
				continue
			}
			found := false
			for _, item := range page.Tracks {
				if sameLXTrackIdentity(original, item) {
					candidate, found = item, true
					break
				}
			}
			if !found {
				failures = append(failures, source+": 未找到歌名和歌手均匹配的歌曲")
				continue
			}
		}

		var declared *music.LXDeclaredSource
		for _, item := range session.Snapshot().DeclaredSources {
			if item.Key == source {
				copy := item
				declared = &copy
				break
			}
		}
		if declared == nil || !containsLXAction(declared.Actions, "musicUrl") {
			failures = append(failures, source+": 当前音源脚本未声明 musicUrl 能力")
			continue
		}
		quality, qualityErr := chooseLXTrackQuality(candidate, *declared)
		if qualityErr != nil {
			failures = append(failures, source+": "+qualityErr.Error())
			continue
		}
		payload, payloadErr := buildLXMusicURLRequest(candidate, quality)
		if payloadErr != nil {
			failures = append(failures, source+": 构建解析请求失败："+payloadErr.Error())
			continue
		}
		rawURL, resolveErr := session.Request(ctx, payload)
		if resolveErr != nil {
			failures = append(failures, source+": 解析失败："+resolveErr.Error())
			continue
		}
		playbackURL, parseErr := parseLXMusicURLResponse(rawURL, source, quality.Type)
		if parseErr != nil {
			failures = append(failures, source+": 播放地址无效："+parseErr.Error())
			continue
		}
		if err := ctx.Err(); err != nil {
			return music.Track{}, err
		}
		if loadErr := loadAndStartPlayback(ctx, s.engine, music.PlaybackResource{URL: playbackURL}, candidate); loadErr != nil {
			failures = append(failures, source+": 播放器加载失败："+loadErr.Error())
			continue
		}
		return candidate, nil
	}
	if len(failures) == 0 {
		return music.Track{}, errors.New("没有可用于自动换源的已启用平台")
	}
	return music.Track{}, fmt.Errorf("所有可用音源均无法播放：%s", strings.Join(failures, "；"))
}

func sameLXTrackIdentity(a, b music.Track) bool {
	return normalizeLXTrackIdentity(a.Title) == normalizeLXTrackIdentity(b.Title) &&
		normalizeLXTrackIdentity(a.Artist) == normalizeLXTrackIdentity(b.Artist)
}

func normalizeLXTrackIdentity(value string) string {
	var result strings.Builder
	for _, r := range strings.ToLower(strings.TrimSpace(value)) {
		if unicode.IsSpace(r) || unicode.IsPunct(r) || unicode.IsSymbol(r) {
			continue
		}
		result.WriteRune(r)
	}
	return result.String()
}
