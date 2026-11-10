package resource

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

const catalogFileVersion = 1

type catalogFile struct {
	Version   int                  `json:"version"`
	Resources []Resource           `json:"resources"`
	Refreshes map[string]time.Time `json:"refreshes,omitempty"`
}

type catalogPersistence struct {
	catalog *Catalog
	root    string
	dirty   chan struct{}
	stop    chan struct{}
	done    chan struct{}
	once    sync.Once
	errMu   sync.Mutex
	err     error
}

// NewPersistentCatalog loads a platform/scope/type-partitioned JSON catalog.
// The supplied path is the media-resources root directory, not a JSON filename.
func NewPersistentCatalog(root string) (*Catalog, error) {
	if strings.TrimSpace(root) == "" {
		return nil, errors.New("media catalog persistence directory is required")
	}
	catalog := NewCatalog()
	if err := loadCatalogDirectory(root, catalog); err != nil {
		// Fail closed: never start a writer when any existing catalog file is
		// unreadable, otherwise a later flush could erase recoverable data.
		return catalog, err
	}

	persistence := &catalogPersistence{
		catalog: catalog,
		root:    root,
		dirty:   make(chan struct{}, 1),
		stop:    make(chan struct{}),
		done:    make(chan struct{}),
	}
	catalog.persistence = persistence
	go persistence.run()
	return catalog, nil
}

func loadCatalogDirectory(root string, catalog *Catalog) error {
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if errors.Is(walkErr, os.ErrNotExist) && path == root {
			return nil
		}
		if walkErr != nil {
			return fmt.Errorf("walk media catalog directory %q: %w", root, walkErr)
		}
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("read media catalog %q: %w", path, err)
		}
		var stored catalogFile
		if err := json.Unmarshal(data, &stored); err != nil {
			return fmt.Errorf("decode media catalog %q: %w", path, err)
		}
		if stored.Version != catalogFileVersion {
			return fmt.Errorf("unsupported media catalog version %d in %q", stored.Version, path)
		}
		for _, item := range stored.Resources {
			catalog.Register(item)
		}
		for key, refreshedAt := range stored.Refreshes {
			if !refreshedAt.IsZero() {
				catalog.refreshes[key] = refreshedAt
			}
		}
		return nil
	})
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

func safeCatalogSegment(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "_"
	}
	for _, r := range value {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') ||
			(r >= '0' && r <= '9') || r == '-' || r == '_' {
			continue
		}
		return fmt.Sprintf("id-%x", []byte(value))
	}
	if value == "." || value == ".." {
		return fmt.Sprintf("id-%x", []byte(value))
	}
	return value
}

// catalogScopeDirectory keeps platform-specific live-resource terminology
// out of the shared catalog layout. Add a mapping only when that platform's
// adapter is implemented and its resource scope is known.
func catalogScopeDirectory(platformID string) string {
	switch strings.ToLower(strings.TrimSpace(platformID)) {
	case "bilibili":
		return "rooms"
	default:
		// Unknown platforms must not inherit Bilibili's room terminology.
		return "scopes"
	}
}

func catalogPartitionPath(root string, platformID, sourceID string, kind Type) string {
	scope := catalogScopeDirectory(platformID)
	scopeID := sourceID
	if sourceID == "common" {
		scope = "common"
		scopeID = ""
	}
	dir := filepath.Join(root, safeCatalogSegment(platformID), scope)
	if scopeID != "" {
		dir = filepath.Join(dir, safeCatalogSegment(scopeID))
	}
	return filepath.Join(dir, string(kind)+".json")
}

func (p *catalogPersistence) markDirty() {
	select {
	case p.dirty <- struct{}{}:
	default:
	}
}

func (p *catalogPersistence) close() error {
	p.once.Do(func() { close(p.stop) })
	<-p.done
	p.errMu.Lock()
	defer p.errMu.Unlock()
	return p.err
}

func (p *catalogPersistence) setError(err error) {
	p.errMu.Lock()
	p.err = err
	p.errMu.Unlock()
}

func (p *catalogPersistence) run() {
	defer close(p.done)
	timer := time.NewTimer(time.Hour)
	if !timer.Stop() {
		<-timer.C
	}
	var timerC <-chan time.Time
	for {
		select {
		case <-p.dirty:
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			timer.Reset(250 * time.Millisecond)
			timerC = timer.C
		case <-timerC:
			p.flush()
			timerC = nil
		case <-p.stop:
			if timerC != nil && !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			p.flush()
			return
		}
	}
}

func (p *catalogPersistence) flush() {
	p.catalog.mu.RLock()
	partitions := make(map[string]*catalogFile)
	for _, item := range p.catalog.items {
		path := catalogPartitionPath(p.root, item.PlatformID, item.SourceID, item.Type)
		file := partitions[path]
		if file == nil {
			file = &catalogFile{Version: catalogFileVersion}
			partitions[path] = file
		}
		file.Resources = append(file.Resources, item)
	}
	for key, refreshedAt := range p.catalog.refreshes {
		parts := strings.Split(key, "\x00")
		if len(parts) != 3 {
			continue
		}
		path := catalogPartitionPath(p.root, parts[0], parts[1], Type(parts[2]))
		file := partitions[path]
		if file == nil {
			file = &catalogFile{Version: catalogFileVersion}
			partitions[path] = file
		}
		if file.Refreshes == nil {
			file.Refreshes = make(map[string]time.Time)
		}
		file.Refreshes[key] = refreshedAt
	}
	p.catalog.mu.RUnlock()

	paths := make([]string, 0, len(partitions))
	for path := range partitions {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	for _, path := range paths {
		if err := writeCatalogFile(path, partitions[path]); err != nil {
			p.setError(err)
			return
		}
	}
	p.setError(nil)
}

func writeCatalogFile(path string, snapshot *catalogFile) error {
	data, err := json.MarshalIndent(snapshot, "", "  ")
	if err != nil {
		return fmt.Errorf("encode media catalog: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create media catalog directory: %w", err)
	}
	tempPath := path + ".tmp"
	file, err := os.OpenFile(tempPath, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("create media catalog temporary file: %w", err)
	}
	if _, err = file.Write(data); err == nil {
		err = file.Sync()
	}
	closeErr := file.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		_ = os.Remove(tempPath)
		return fmt.Errorf("write media catalog: %w", err)
	}
	if err := os.Rename(tempPath, path); err != nil {
		_ = os.Remove(tempPath)
		return fmt.Errorf("replace media catalog: %w", err)
	}
	return nil
}
