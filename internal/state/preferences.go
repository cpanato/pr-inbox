package state

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"time"
	"unicode/utf8"
)

var ErrInvalidPreferences = errors.New("invalid saved filters or ordering")
var ErrPreferencesConflict = errors.New("preferences changed in another tab or process; reload saved preferences before saving again")

type Preferences struct {
	Tab   string `json:"tab"`
	Query string `json:"query"`
	Org   string `json:"org"`
	Repo  string `json:"repo"`
	Order string `json:"order"`
}
type SavedPreferences struct {
	Preferences Preferences `json:"preferences"`
	Version     string      `json:"version"`
}
type preferencesFile struct {
	Owner string `json:"owner"`
	SavedPreferences
}

func DefaultPreferences() Preferences { return Preferences{Tab: "review", Order: "oldest"} }
func validPreferences(p Preferences) bool {
	return (p.Tab == "review" || p.Tab == "mine" || p.Tab == "history") && (p.Order == "oldest" || p.Order == "recent") && len(p.Query) <= 1024 && len(p.Org) <= 512 && len(p.Repo) <= 1024 && utf8.ValidString(p.Query) && utf8.ValidString(p.Org) && utf8.ValidString(p.Repo)
}
func (s *Store) preferencesDirectory(owner string) string {
	return filepath.Join(s.root, "preferences-v1", digest(owner))
}
func (s *Store) Preferences(owner string) (SavedPreferences, error) {
	empty := SavedPreferences{Preferences: DefaultPreferences()}
	file, err := os.Open(filepath.Join(s.preferencesDirectory(owner), "preferences.json"))
	if errors.Is(err, os.ErrNotExist) {
		return empty, nil
	}
	if err != nil {
		return empty, err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, (16<<10)+1))
	if err != nil {
		return empty, err
	}
	var saved preferencesFile
	if len(data) > 16<<10 || json.Unmarshal(data, &saved) != nil || saved.Owner != owner || saved.Version == "" || !validPreferences(saved.Preferences) {
		return empty, errors.New("invalid preferences storage")
	}
	return saved.SavedPreferences, nil
}
func (s *Store) SavePreferences(ctx context.Context, owner string, p Preferences, version string) (SavedPreferences, error) {
	if owner == "" || !validPreferences(p) {
		return SavedPreferences{}, ErrInvalidPreferences
	}
	dir := s.preferencesDirectory(owner)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return SavedPreferences{}, err
	}
	wait, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	unlock, err := lockDraft(wait, filepath.Join(dir, ".lock"))
	if err != nil {
		return SavedPreferences{}, err
	}
	defer unlock()
	old, err := s.Preferences(owner)
	if err != nil {
		return SavedPreferences{}, err
	}
	if old.Version != version {
		return SavedPreferences{}, ErrPreferencesConflict
	}
	saved := SavedPreferences{Preferences: p, Version: rand.Text()}
	data, err := json.Marshal(preferencesFile{Owner: owner, SavedPreferences: saved})
	if err != nil {
		return SavedPreferences{}, err
	}
	if len(data) > 16<<10 {
		return SavedPreferences{}, ErrInvalidPreferences
	}
	if err := atomicDraft(dir, filepath.Join(dir, "preferences.json"), data); err != nil {
		return SavedPreferences{}, err
	}
	return saved, nil
}
