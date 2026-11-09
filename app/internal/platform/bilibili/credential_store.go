package bilibili

import (
	"errors"
	"os"
	"path/filepath"
)

type DPAPICredentialStore struct { path string }

func NewDPAPICredentialStore() (*DPAPICredentialStore, error) {
	root, err := os.UserConfigDir()
	if err != nil { return nil, err }
	return &DPAPICredentialStore{path:filepath.Join(root, "My-LiveHub", "secure", "bilibili-auth.dat")}, nil
}

func (s *DPAPICredentialStore) Save(data []byte) error {
	if s == nil || s.path == "" { return errors.New("credential store path is unavailable") }
	protected, err := protect(data)
	if err != nil { return err }
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil { return err }
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, protected, 0o600); err != nil { return err }
	if err := os.Rename(tmp, s.path); err != nil { _ = os.Remove(tmp); return err }
	return nil
}

func (s *DPAPICredentialStore) Load() ([]byte, error) {
	if s == nil || s.path == "" { return nil, errors.New("credential store path is unavailable") }
	protected, err := os.ReadFile(s.path)
	if errors.Is(err, os.ErrNotExist) { return nil, ErrCredentialNotFound }
	if err != nil { return nil, err }
	return unprotect(protected)
}

func (s *DPAPICredentialStore) Delete() error {
	if s == nil || s.path == "" { return errors.New("credential store path is unavailable") }
	err := os.Remove(s.path)
	if errors.Is(err, os.ErrNotExist) { return ErrCredentialNotFound }
	return err
}
