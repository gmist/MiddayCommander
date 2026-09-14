package remote

import (
	"os"
	"path/filepath"
	"strconv"

	"github.com/kevinburke/ssh_config"
)

// Host aliases from ~/.ssh/config. Only settings the user wrote are read: a
// library default for IdentityFile or Port would override the server entry.
//
// ProxyJump is not implemented, so an alias needing a bastion fails to dial
// rather than connecting somewhere unexpected.

func sshConfigPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".ssh", "config")
}

// lookupSSHConfig returns "" when the file is absent, unreadable, or silent
// on the alias.
func lookupSSHConfig(alias, key string) string {
	path := sshConfigPath()
	if path == "" {
		return ""
	}
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()

	cfg, err := ssh_config.Decode(f)
	if err != nil {
		return ""
	}
	value, err := cfg.Get(alias, key)
	if err != nil {
		return ""
	}
	return value
}

// Resolve fills unset fields from the matching Host block. Values already on
// the server win.
func (s Server) Resolve() Server {
	alias := s.Host
	if alias == "" {
		return s
	}

	if hostName := lookupSSHConfig(alias, "HostName"); hostName != "" && hostName != alias {
		s.Alias = alias
		s.Host = hostName
	}
	if s.User == "" {
		s.User = lookupSSHConfig(alias, "User")
	}
	if s.Port == 0 {
		if p := lookupSSHConfig(alias, "Port"); p != "" {
			if port, err := strconv.Atoi(p); err == nil && port > 0 && port <= 65535 {
				s.Port = port
			}
		}
	}
	if s.KeyPath == "" {
		if id := lookupSSHConfig(alias, "IdentityFile"); id != "" {
			s.KeyPath = expandHome(id)
		}
	}

	return s
}
