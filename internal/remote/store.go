package remote

import (
	"encoding/json"
	"errors"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// SavedServer adds the usage counters that order the list.
type SavedServer struct {
	Server
	Count    int       `json:"count"`
	LastUsed time.Time `json:"last_used"`
}

// Store persists the saved servers to ~/.config/mdc/servers.json. It holds no
// passwords: authentication is by agent or key file.
type Store struct {
	Servers []SavedServer `json:"servers"`
	path    string
}

// LoadStore returns an empty store when the file does not exist yet.
func LoadStore() *Store {
	s := &Store{path: storePath()}

	data, err := os.ReadFile(s.path)
	if err != nil {
		return s
	}
	_ = json.Unmarshal(data, s)
	return s
}

func (s *Store) Save() error {
	dir := filepath.Dir(s.path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}

	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(s.path, data, 0o600)
}

// Add replaces any server already saved under the same name.
func (s *Store) Add(srv Server) {
	for i, existing := range s.Servers {
		if existing.Name == srv.Name {
			s.Servers[i].Server = srv
			return
		}
	}
	s.Servers = append(s.Servers, SavedServer{Server: srv, LastUsed: time.Now()})
}

func (s *Store) Remove(name string) {
	for i, existing := range s.Servers {
		if existing.Name == name {
			s.Servers = append(s.Servers[:i], s.Servers[i+1:]...)
			return
		}
	}
}

func (s *Store) Touch(name string) {
	for i, existing := range s.Servers {
		if existing.Name == name {
			s.Servers[i].Count++
			s.Servers[i].LastUsed = time.Now()
			return
		}
	}
}

func (s *Store) Find(name string) (Server, bool) {
	for _, existing := range s.Servers {
		if existing.Name == name {
			return existing.Server, true
		}
	}
	return Server{}, false
}

// Sorted returns the most-used and most-recent first, as bookmarks are.
func (s *Store) Sorted() []SavedServer {
	out := make([]SavedServer, len(s.Servers))
	copy(out, s.Servers)

	now := time.Now()
	sort.SliceStable(out, func(i, j int) bool {
		return frecency(out[i], now) > frecency(out[j], now)
	})
	return out
}

func frecency(s SavedServer, now time.Time) float64 {
	hoursSince := now.Sub(s.LastUsed).Hours()
	recency := math.Max(0, 100-hoursSince)
	return float64(s.Count)*10 + recency
}

func storePath() string {
	if xdg := os.Getenv("XDG_CONFIG_HOME"); xdg != "" {
		return filepath.Join(xdg, "mdc", "servers.json")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "servers.json"
	}
	return filepath.Join(home, ".config", "mdc", "servers.json")
}

// ParseURL reads an ssh://[user@]host[:port][/path] address.
func ParseURL(raw string) (Server, string, error) {
	rest, ok := strings.CutPrefix(raw, "ssh://")
	if !ok {
		return Server{}, "", errors.New("not an ssh:// address")
	}
	if rest == "" {
		return Server{}, "", errors.New("ssh:// address has no host")
	}

	// The host part never contains a slash.
	hostPart, pathPart := rest, ""
	if i := strings.IndexByte(rest, '/'); i >= 0 {
		hostPart, pathPart = rest[:i], rest[i:]
	}

	var srv Server
	if user, hostPort, found := strings.Cut(hostPart, "@"); found {
		srv.User = user
		hostPart = hostPort
	}

	if host, portStr, found := strings.Cut(hostPart, ":"); found {
		port, err := strconv.Atoi(portStr)
		if err != nil || port <= 0 || port > 65535 {
			return Server{}, "", errors.New("invalid port in ssh:// address")
		}
		srv.Host = host
		srv.Port = port
	} else {
		srv.Host = hostPart
	}

	if srv.Host == "" {
		return Server{}, "", errors.New("ssh:// address has no host")
	}

	if pathPart == "" {
		pathPart = "/"
	}
	return srv, pathPart, nil
}

func IsURL(raw string) bool {
	return strings.HasPrefix(raw, "ssh://")
}

// URL renders the ssh:// address for a path on a server.
func URL(srv Server, remotePath string) string {
	if remotePath == "" {
		remotePath = "/"
	}
	if !strings.HasPrefix(remotePath, "/") {
		remotePath = "/" + remotePath
	}
	return srv.Label() + remotePath
}
