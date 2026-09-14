package remote

import (
	"os"
	"path/filepath"
	"testing"
)

func writeSSHConfig(t *testing.T, body string) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	if err := os.MkdirAll(filepath.Join(home, ".ssh"), 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(home, ".ssh", "config")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return home
}

func TestResolveFillsFromSSHConfig(t *testing.T) {
	home := writeSSHConfig(t, `
Host prod
    HostName prod.internal.example.com
    User deploy
    Port 2222
    IdentityFile ~/.ssh/deploy_ed25519
`)

	got := Server{Name: "prod", Host: "prod"}.Resolve()

	if got.Host != "prod.internal.example.com" {
		t.Errorf("HostName: want prod.internal.example.com, got %q", got.Host)
	}
	if got.Alias != "prod" {
		t.Errorf("want the alias remembered, got %q", got.Alias)
	}
	if got.User != "deploy" {
		t.Errorf("User: want deploy, got %q", got.User)
	}
	if got.Port != 2222 {
		t.Errorf("Port: want 2222, got %d", got.Port)
	}
	want := filepath.Join(home, ".ssh", "deploy_ed25519")
	if got.KeyPath != want {
		t.Errorf("IdentityFile: want %q, got %q", want, got.KeyPath)
	}

	// The panel header shows the alias, not the machine behind it.
	if label := got.Label(); label != "ssh://deploy@prod:2222" {
		t.Errorf("want the alias in the label, got %q", label)
	}
}

func TestResolveDoesNotOverrideExplicitSettings(t *testing.T) {
	writeSSHConfig(t, `
Host box
    HostName box.example.com
    User fromconfig
    Port 2222
    IdentityFile ~/.ssh/fromconfig
`)

	got := Server{
		Host:    "box",
		User:    "explicit",
		Port:    2022,
		KeyPath: "/keys/explicit",
	}.Resolve()

	if got.User != "explicit" {
		t.Errorf("want the configured user kept, got %q", got.User)
	}
	if got.Port != 2022 {
		t.Errorf("want the configured port kept, got %d", got.Port)
	}
	if got.KeyPath != "/keys/explicit" {
		t.Errorf("want the configured key kept, got %q", got.KeyPath)
	}
	// HostName still applies: it says where the alias points.
	if got.Host != "box.example.com" {
		t.Errorf("want the alias resolved, got %q", got.Host)
	}
}

func TestResolveAppliesNoDefaults(t *testing.T) {
	// A config that mentions the host but sets nothing relevant must not
	// introduce the library's built-in defaults (Port 22, ~/.ssh/identity).
	writeSSHConfig(t, `
Host somewhere
    Compression yes
`)

	got := Server{Host: "somewhere"}.Resolve()

	if got.Port != 0 {
		t.Errorf("want the port left unset, got %d", got.Port)
	}
	if got.KeyPath != "" {
		t.Errorf("want no identity file invented, got %q", got.KeyPath)
	}
	if got.Alias != "" {
		t.Errorf("want no alias when HostName is absent, got %q", got.Alias)
	}
	if got.Host != "somewhere" {
		t.Errorf("want the host unchanged, got %q", got.Host)
	}
}

func TestResolveWithoutAnSSHConfig(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	got := Server{Host: "plain.example.com", User: "kk"}.Resolve()

	if got.Host != "plain.example.com" || got.User != "kk" || got.Port != 0 {
		t.Errorf("a missing ssh config should change nothing, got %+v", got)
	}
}

func TestResolveWildcardHostBlock(t *testing.T) {
	writeSSHConfig(t, `
Host *.example.com
    User wildcard

Host specific.example.com
    Port 2200
`)

	got := Server{Host: "specific.example.com"}.Resolve()

	if got.User != "wildcard" {
		t.Errorf("want the wildcard block applied, got %q", got.User)
	}
	if got.Port != 2200 {
		t.Errorf("want the specific block applied, got %d", got.Port)
	}
}
