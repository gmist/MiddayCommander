package remote

import (
	"path/filepath"
	"testing"
)

func TestParseURL(t *testing.T) {
	tests := []struct {
		raw      string
		wantHost string
		wantUser string
		wantPort int
		wantPath string
		wantErr  bool
	}{
		{raw: "ssh://host", wantHost: "host", wantPath: "/"},
		{raw: "ssh://kk@host", wantHost: "host", wantUser: "kk", wantPath: "/"},
		{raw: "ssh://kk@host:2222", wantHost: "host", wantUser: "kk", wantPort: 2222, wantPath: "/"},
		{raw: "ssh://kk@host/var/log", wantHost: "host", wantUser: "kk", wantPath: "/var/log"},
		{raw: "ssh://host:2222/srv/app", wantHost: "host", wantPort: 2222, wantPath: "/srv/app"},
		{raw: "ssh://kk@host/", wantHost: "host", wantUser: "kk", wantPath: "/"},
		{raw: "/home/kk", wantErr: true},
		{raw: "ssh://", wantErr: true},
		{raw: "ssh://host:notaport", wantErr: true},
		{raw: "ssh://host:99999", wantErr: true},
	}

	for _, tc := range tests {
		t.Run(tc.raw, func(t *testing.T) {
			srv, remotePath, err := ParseURL(tc.raw)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("want an error for %q", tc.raw)
				}
				return
			}
			if err != nil {
				t.Fatalf("parse %q: %v", tc.raw, err)
			}
			if srv.Host != tc.wantHost {
				t.Errorf("host: want %q, got %q", tc.wantHost, srv.Host)
			}
			if srv.User != tc.wantUser {
				t.Errorf("user: want %q, got %q", tc.wantUser, srv.User)
			}
			if srv.Port != tc.wantPort {
				t.Errorf("port: want %d, got %d", tc.wantPort, srv.Port)
			}
			if remotePath != tc.wantPath {
				t.Errorf("path: want %q, got %q", tc.wantPath, remotePath)
			}
		})
	}
}

func TestURLRoundTrip(t *testing.T) {
	srv := Server{Host: "host", User: "kk", Port: 2222}
	raw := URL(srv, "/var/log")
	if raw != "ssh://kk@host:2222/var/log" {
		t.Fatalf("want ssh://kk@host:2222/var/log, got %q", raw)
	}

	back, remotePath, err := ParseURL(raw)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if back.Host != srv.Host || back.User != srv.User || back.Port != srv.Port {
		t.Errorf("round trip lost detail: %+v", back)
	}
	if remotePath != "/var/log" {
		t.Errorf("path: want /var/log, got %q", remotePath)
	}
}

func TestStorePersistsServers(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)

	s := LoadStore()
	if len(s.Servers) != 0 {
		t.Fatalf("want an empty store, got %d servers", len(s.Servers))
	}

	s.Add(Server{Name: "prod", Host: "prod.example.com", User: "deploy", Dir: "/srv"})
	s.Add(Server{Name: "staging", Host: "staging.example.com"})
	if err := s.Save(); err != nil {
		t.Fatalf("save: %v", err)
	}

	if _, err := filepath.Glob(filepath.Join(dir, "mdc", "servers.json")); err != nil {
		t.Fatal(err)
	}

	reloaded := LoadStore()
	if len(reloaded.Servers) != 2 {
		t.Fatalf("want 2 servers after reload, got %d", len(reloaded.Servers))
	}
	got, ok := reloaded.Find("prod")
	if !ok {
		t.Fatal("want to find the prod server")
	}
	if got.Host != "prod.example.com" || got.User != "deploy" || got.Dir != "/srv" {
		t.Errorf("server did not round trip: %+v", got)
	}
}

func TestStoreAddReplacesByName(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	s := LoadStore()
	s.Add(Server{Name: "box", Host: "old.example.com"})
	s.Add(Server{Name: "box", Host: "new.example.com"})

	if len(s.Servers) != 1 {
		t.Fatalf("want one server, got %d", len(s.Servers))
	}
	if s.Servers[0].Host != "new.example.com" {
		t.Errorf("want the host replaced, got %q", s.Servers[0].Host)
	}
}

func TestStoreRemoveAndSort(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	s := LoadStore()
	s.Add(Server{Name: "a", Host: "a.example.com"})
	s.Add(Server{Name: "b", Host: "b.example.com"})
	s.Touch("b")
	s.Touch("b")

	sorted := s.Sorted()
	if len(sorted) != 2 || sorted[0].Name != "b" {
		t.Errorf("want the most-used server first, got %v", sorted)
	}

	s.Remove("a")
	if _, ok := s.Find("a"); ok {
		t.Error("want the removed server gone")
	}
}
