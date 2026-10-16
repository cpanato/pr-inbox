// Package state stores local review progress without credentials or source text.
package state

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

type Store struct {
	root string
	mu   sync.Mutex
}
type Progress struct {
	Paths            []string `json:"paths"`
	PreviousRevision bool     `json:"previousRevision"`
}

func New(root string) *Store { return &Store{root: root} }
func digest(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}
func (s *Store) directory(key, revision string) string {
	return filepath.Join(s.root, "viewed-v1", digest(key), digest(revision))
}
func (s *Store) Get(key, revision string) (Progress, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	result := Progress{Paths: []string{}}
	dir := s.directory(key, revision)
	revisions, err := os.ReadDir(filepath.Dir(dir))
	if errors.Is(err, os.ErrNotExist) {
		return result, nil
	}
	if err != nil {
		return result, err
	}
	for _, rev := range revisions {
		if !rev.IsDir() {
			continue
		}
		entries, err := os.ReadDir(filepath.Join(filepath.Dir(dir), rev.Name()))
		if err != nil {
			return result, err
		}
		for _, entry := range entries {
			if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
				continue
			}
			if rev.Name() != filepath.Base(dir) {
				result.PreviousRevision = true
				continue
			}
			data, err := os.ReadFile(filepath.Join(dir, entry.Name()))
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			if err != nil {
				return result, err
			}
			var path string
			if err := json.Unmarshal(data, &path); err != nil {
				return result, err
			}
			if digest(path)+".json" != entry.Name() {
				return result, errors.New("invalid viewed marker")
			}
			result.Paths = append(result.Paths, path)
		}
	}
	return result, nil
}

// Each file has its own atomic record, so simultaneous processes cannot lose
// updates to other files. Concurrent updates to the same marker are last-writer-wins.
func (s *Store) Set(key, revision, path string, viewed bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	dir := s.directory(key, revision)
	target := filepath.Join(dir, digest(path)+".json")
	if !viewed {
		err := os.Remove(target)
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	data, err := json.Marshal(path)
	if err != nil {
		return err
	}
	file, err := os.CreateTemp(dir, ".marker-*")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	if _, err = file.Write(data); err != nil {
		file.Close()
		return err
	}
	if err = file.Sync(); err != nil {
		file.Close()
		return err
	}
	if err = file.Close(); err != nil {
		return err
	}
	return os.Rename(file.Name(), target)
}
