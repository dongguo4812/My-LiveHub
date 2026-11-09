package resource

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

const catalogFileVersion = 1

type catalogFile struct {
	Version   int        `json:"version"`
	Resources []Resource `json:"resources"`
}

type catalogPersistence struct {
	catalog *Catalog
	path    string
	dirty   chan struct{}
	stop    chan struct{}
	done    chan struct{}
	once    sync.Once
	errMu   sync.Mutex
	err     error
}

// NewPersistentCatalog loads the last saved catalog before returning. Subsequent
// registrations are coalesced and written by a background worker.
func NewPersistentCatalog(path string) (*Catalog, error) {
	if path == "" {
		return nil, errors.New("media catalog persistence path is required")
	}
	catalog := NewCatalog()
	var loadErr error
	data, err := os.ReadFile(path)
	if err == nil {
		var stored catalogFile
		if decodeErr := json.Unmarshal(data, &stored); decodeErr != nil {
			loadErr = fmt.Errorf("decode media catalog %q: %w", path, decodeErr)
		} else if stored.Version != catalogFileVersion {
			loadErr = fmt.Errorf("unsupported media catalog version %d", stored.Version)
		} else {
			for _, item := range stored.Resources {
				catalog.Register(item)
			}
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		loadErr = fmt.Errorf("read media catalog %q: %w", path, err)
	}

	persistence := &catalogPersistence{
		catalog: catalog,
		path:    path,
		dirty:   make(chan struct{}, 1),
		stop:    make(chan struct{}),
		done:    make(chan struct{}),
	}
	catalog.persistence = persistence
	go persistence.run()
	return catalog, loadErr
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
	snapshot := p.catalog.List("", "", "")
	data, err := json.MarshalIndent(catalogFile{Version: catalogFileVersion, Resources: snapshot}, "", "  ")
	if err != nil {
		p.setError(fmt.Errorf("encode media catalog: %w", err))
		return
	}
	if err := os.MkdirAll(filepath.Dir(p.path), 0o700); err != nil {
		p.setError(fmt.Errorf("create media catalog directory: %w", err))
		return
	}

	tempPath := p.path + ".tmp"
	file, err := os.OpenFile(tempPath, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		p.setError(fmt.Errorf("create media catalog temporary file: %w", err))
		return
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
		p.setError(fmt.Errorf("write media catalog: %w", err))
		return
	}
	if err := os.Rename(tempPath, p.path); err != nil {
		_ = os.Remove(tempPath)
		p.setError(fmt.Errorf("replace media catalog: %w", err))
		return
	}
	p.setError(nil)
}
