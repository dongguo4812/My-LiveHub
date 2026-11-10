package imagecache

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type PlatformCacheStats struct {
	PlatformID string `json:"platformId"`
	FileCount  int64  `json:"fileCount"`
	Bytes      int64  `json:"bytes"`
}

type CacheStats struct {
	Platforms  []PlatformCacheStats `json:"platforms"`
	TotalFiles int64                `json:"totalFiles"`
	TotalBytes int64                `json:"totalBytes"`
	Ready      bool                 `json:"ready"`
}

var statsExtensions = map[string]struct{}{
	".png": {}, ".jpg": {}, ".jpeg": {}, ".gif": {}, ".webp": {}, ".avif": {}, ".bmp": {}, ".img": {},
}

// RefreshStats schedules a non-blocking, read-only disk scan. Requests received
// during a scan are coalesced into one additional scan so completed downloads
// cannot be omitted from the final snapshot.
func (m *Manager) RefreshStats() {
	if m == nil {
		return
	}
	m.statsMu.Lock()
	if m.statsRefreshing {
		m.statsRefreshPending = true
		m.statsMu.Unlock()
		return
	}
	m.statsRefreshing = true
	m.statsMu.Unlock()

	go func() {
		for {
			result := scanCacheStats(m.root)
			m.statsMu.Lock()
			m.stats = result
			if m.statsRefreshPending {
				m.statsRefreshPending = false
				m.statsMu.Unlock()
				continue
			}
			m.statsRefreshing = false
			m.statsMu.Unlock()
			return
		}
	}()
}

func scanCacheStats(root string) CacheStats {
	result := CacheStats{Platforms: []PlatformCacheStats{}, Ready: true}
	entries, err := os.ReadDir(root)
	if err != nil {
		result.Ready = false
		return result
	}
	for _, entry := range entries {
		if !entry.IsDir() || strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		item := PlatformCacheStats{PlatformID: entry.Name()}
		err := filepath.WalkDir(filepath.Join(root, entry.Name()), func(path string, d os.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if d.IsDir() || !d.Type().IsRegular() {
				return nil
			}
			if _, ok := statsExtensions[strings.ToLower(filepath.Ext(d.Name()))]; !ok {
				return nil
			}
			info, err := d.Info()
			if err != nil {
				return err
			}
			item.FileCount++
			item.Bytes += info.Size()
			return nil
		})
		if err != nil {
			result.Ready = false
			continue
		}
		result.TotalFiles += item.FileCount
		result.TotalBytes += item.Bytes
		result.Platforms = append(result.Platforms, item)
	}
	sort.Slice(result.Platforms, func(i, j int) bool {
		return result.Platforms[i].PlatformID < result.Platforms[j].PlatformID
	})
	return result
}

// GetStats returns the latest completed snapshot without disk I/O.
func (m *Manager) GetStats() CacheStats {
	if m == nil {
		return CacheStats{Platforms: []PlatformCacheStats{}}
	}
	m.statsMu.RLock()
	defer m.statsMu.RUnlock()
	result := m.stats
	result.Platforms = append([]PlatformCacheStats(nil), result.Platforms...)
	if result.Platforms == nil {
		result.Platforms = []PlatformCacheStats{}
	}
	return result
}

// WaitForStats is used by tests to await a background scan.
func (m *Manager) WaitForStats(timeout time.Duration) bool {
	if m == nil {
		return true
	}
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		m.statsMu.RLock()
		ready, running := m.stats.Ready, m.statsRefreshing
		m.statsMu.RUnlock()
		if ready || !running {
			return ready
		}
		time.Sleep(5 * time.Millisecond)
	}
	return false
}
