package bookmark

import (
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"sort"
	"time"
)

// Bookmark represents a saved directory bookmark.
type Bookmark struct {
	Path     string    `json:"path"`
	Name     string    `json:"name,omitempty"` // optional display name
	Count    int       `json:"count"`          // access count
	LastUsed time.Time `json:"last_used"`
}

// Store manages bookmarks with persistence and frecency scoring.
type Store struct {
	Bookmarks []Bookmark `json:"bookmarks"`
	path      string     // file path for persistence
}

// LoadStore loads bookmarks from ~/.config/mdc/bookmarks.json.
func LoadStore() *Store {
	s := &Store{path: storePath()}

	data, err := os.ReadFile(s.path)
	if err != nil {
		return s
	}

	_ = json.Unmarshal(data, s)
	return s
}

// Save writes bookmarks to disk.
func (s *Store) Save() error {
	dir := filepath.Dir(s.path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}

	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}

	return os.WriteFile(s.path, data, 0644)
}

// Add adds or updates a bookmark. If path already exists, increments count.
func (s *Store) Add(path, name string) {
	for i, b := range s.Bookmarks {
		if b.Path == path {
			s.Bookmarks[i].Count++
			s.Bookmarks[i].LastUsed = time.Now()
			if name != "" {
				s.Bookmarks[i].Name = name
			}
			return
		}
	}
	s.Bookmarks = append(s.Bookmarks, Bookmark{
		Path:     path,
		Name:     name,
		Count:    1,
		LastUsed: time.Now(),
	})
}

// Remove removes a bookmark by path.
func (s *Store) Remove(path string) {
	for i, b := range s.Bookmarks {
		if b.Path == path {
			s.Bookmarks = append(s.Bookmarks[:i], s.Bookmarks[i+1:]...)
			return
		}
	}
}

// Touch marks a bookmark as recently used (call when navigating to it).
func (s *Store) Touch(path string) {
	for i, b := range s.Bookmarks {
		if b.Path == path {
			s.Bookmarks[i].Count++
			s.Bookmarks[i].LastUsed = time.Now()
			return
		}
	}
}

// Sorted returns bookmarks sorted by frecency score (highest first).
func (s *Store) Sorted() []Bookmark {
	result := make([]Bookmark, len(s.Bookmarks))
	copy(result, s.Bookmarks)

	now := time.Now()
	sort.Slice(result, func(i, j int) bool {
		return frecency(result[i], now) > frecency(result[j], now)
	})

	return result
}

// frecency computes a score combining frequency and recency.
func frecency(b Bookmark, now time.Time) float64 {
	hoursSince := now.Sub(b.LastUsed).Hours()
	recency := math.Max(0, 100-hoursSince)
	return float64(b.Count)*10 + recency
}

// Has returns true if the path is bookmarked.
func (s *Store) Has(path string) bool {
	for _, b := range s.Bookmarks {
		if b.Path == path {
			return true
		}
	}
	return false
}

func storePath() string {
	if xdg := os.Getenv("XDG_CONFIG_HOME"); xdg != "" {
		return filepath.Join(xdg, "mdc", "bookmarks.json")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "bookmarks.json"
	}
	return filepath.Join(home, ".config", "mdc", "bookmarks.json")
}
