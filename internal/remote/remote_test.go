package remote

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kooler/MiddayCommander/internal/remote/testserver"
)

// isolate keeps tests away from the real known_hosts, and hides any running
// agent so the key file under test is the only credential offered.
func isolate(t *testing.T) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("SSH_AUTH_SOCK", "")
}

func testSrv(t *testing.T, s *testserver.Server) Server {
	t.Helper()
	return Server{
		Name:    "test",
		Host:    s.Host,
		Port:    s.Port,
		User:    "tester",
		KeyPath: s.ClientKey,
	}
}

func TestUnknownHostIsRefusedWithFingerprint(t *testing.T) {
	isolate(t)
	srv := testserver.Start(t)

	r := NewRegistry()
	_, err := r.Acquire(context.Background(), testSrv(t, srv), Credentials{})

	var unknown *UnknownHostError
	if !errors.As(err, &unknown) {
		t.Fatalf("want *UnknownHostError on first contact, got %v", err)
	}
	if unknown.Fingerprint != srv.Fingerprint {
		t.Errorf("want fingerprint %s, got %s", srv.Fingerprint, unknown.Fingerprint)
	}
}

func TestAcceptingHostKeyRecordsItAndConnects(t *testing.T) {
	isolate(t)
	srv := testserver.Start(t)
	server := testSrv(t, srv)
	r := NewRegistry()

	// First contact is refused with the fingerprint to show the user.
	_, err := r.Acquire(context.Background(), server, Credentials{})
	var unknown *UnknownHostError
	if !errors.As(err, &unknown) {
		t.Fatalf("want *UnknownHostError, got %v", err)
	}

	// The user confirms, so the retry carries the fingerprint they accepted.
	conn, err := r.Acquire(context.Background(), server,
		Credentials{AcceptFingerprint: unknown.Fingerprint})
	if err != nil {
		t.Fatalf("connect after accepting: %v", err)
	}
	defer r.Release(conn)

	data, err := os.ReadFile(KnownHostsPath())
	if err != nil {
		t.Fatalf("read known_hosts: %v", err)
	}
	if !strings.Contains(string(data), "ssh-ed25519") {
		t.Errorf("want the host key recorded in known_hosts, got %q", string(data))
	}

	// A later connection needs no confirmation.
	r.Release(conn)
	again, err := r.Acquire(context.Background(), server, Credentials{})
	if err != nil {
		t.Fatalf("second connect should trust the recorded key: %v", err)
	}
	r.Release(again)
}

func TestRetryRejectsADifferentKeyThanWasConfirmed(t *testing.T) {
	isolate(t)
	srv := testserver.Start(t)

	// The host presents a different key than was confirmed, which is the
	// window interception would use.
	r := NewRegistry()
	_, err := r.Acquire(context.Background(), testSrv(t, srv), Credentials{
		AcceptFingerprint: "SHA256:definitelyNotTheKeyYouWereShown",
	})
	if err == nil {
		t.Fatal("want an error when the presented key differs from the confirmed one")
	}
	if _, statErr := os.Stat(KnownHostsPath()); statErr == nil {
		data, _ := os.ReadFile(KnownHostsPath())
		if strings.Contains(string(data), "ssh-ed25519") {
			t.Error("a rejected key must not be recorded in known_hosts")
		}
	}
}

func TestChangedHostKeyIsRefusedNotPrompted(t *testing.T) {
	isolate(t)
	srv := testserver.Start(t)
	server := testSrv(t, srv)
	r := NewRegistry()

	_, err := r.Acquire(context.Background(), server, Credentials{})
	var unknown *UnknownHostError
	if !errors.As(err, &unknown) {
		t.Fatalf("want *UnknownHostError, got %v", err)
	}
	conn, err := r.Acquire(context.Background(), server,
		Credentials{AcceptFingerprint: unknown.Fingerprint})
	if err != nil {
		t.Fatalf("initial connect: %v", err)
	}
	r.Release(conn)

	// The host now answers with a different key.
	srv.RotateHostKey(t)

	_, err = NewRegistry().Acquire(context.Background(), server, Credentials{})
	var changed *ChangedHostKeyError
	if !errors.As(err, &changed) {
		t.Fatalf("want *ChangedHostKeyError, got %v", err)
	}
	var stillUnknown *UnknownHostError
	if errors.As(err, &stillUnknown) {
		t.Error("a changed host key must never be offered as a confirmation prompt")
	}
}

func TestRegistrySharesOneConnectionPerServer(t *testing.T) {
	isolate(t)
	srv := testserver.Start(t)
	server := testSrv(t, srv)
	r := NewRegistry()

	_, err := r.Acquire(context.Background(), server, Credentials{})
	var unknown *UnknownHostError
	if !errors.As(err, &unknown) {
		t.Fatalf("want *UnknownHostError, got %v", err)
	}
	creds := Credentials{AcceptFingerprint: unknown.Fingerprint}

	first, err := r.Acquire(context.Background(), server, creds)
	if err != nil {
		t.Fatalf("first acquire: %v", err)
	}
	second, err := r.Acquire(context.Background(), server, Credentials{})
	if err != nil {
		t.Fatalf("second acquire: %v", err)
	}

	if first != second {
		t.Error("want both panels to share one connection to the same server")
	}

	// One release leaves it usable for the other holder.
	r.Release(second)
	if !first.alive() {
		t.Fatal("connection closed while still in use by another panel")
	}
	if _, err := first.FS().ReadDir(srv.Root); err != nil {
		t.Errorf("shared connection should still work: %v", err)
	}

	// The last release tears it down.
	r.Release(first)
	if first.alive() {
		t.Error("want the connection closed once nothing holds it")
	}
}

// connect is the two-step dance the UI performs, condensed for tests that are
// not about host key handling.
func connect(t *testing.T, srv *testserver.Server) (*Registry, *Conn) {
	t.Helper()

	server := testSrv(t, srv)
	r := NewRegistry()

	_, err := r.Acquire(context.Background(), server, Credentials{})
	var unknown *UnknownHostError
	if !errors.As(err, &unknown) {
		t.Fatalf("want *UnknownHostError, got %v", err)
	}

	conn, err := r.Acquire(context.Background(), server,
		Credentials{AcceptFingerprint: unknown.Fingerprint})
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(func() { r.Release(conn) })
	return r, conn
}

func TestBrowseAndWriteOverSFTP(t *testing.T) {
	isolate(t)
	srv := testserver.Start(t)

	if err := os.WriteFile(srv.Path("hello.txt"), []byte("world"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(srv.Path("nested/deep"), 0o755); err != nil {
		t.Fatal(err)
	}

	_, conn := connect(t, srv)
	fsys := conn.FS()

	// SFTP serves the whole filesystem by absolute path, so the test browses
	// the served directory by its real path, exactly as a panel would.
	base := srv.Root
	entries, err := fsys.ReadDir(base)
	if err != nil {
		t.Fatalf("readdir: %v", err)
	}
	names := map[string]bool{}
	for _, e := range entries {
		names[e.Name()] = true
	}
	if !names["hello.txt"] || !names["nested"] {
		t.Errorf("want hello.txt and nested in the listing, got %v", names)
	}

	// Create, rename, stat, remove.
	w, err := fsys.Create(base + "/made.txt")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := w.(interface{ Write([]byte) (int, error) }).Write([]byte("body")); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	if got, err := os.ReadFile(srv.Path("made.txt")); err != nil || string(got) != "body" {
		t.Errorf("want %q written through sftp, got %q (%v)", "body", string(got), err)
	}

	if err := fsys.Mkdir(base+"/fresh", 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if info, err := os.Stat(srv.Path("fresh")); err != nil || !info.IsDir() {
		t.Errorf("want a directory created on the server: %v", err)
	}

	if err := fsys.Rename(base+"/made.txt", base+"/renamed.txt"); err != nil {
		t.Fatalf("rename: %v", err)
	}
	if _, err := os.Stat(srv.Path("renamed.txt")); err != nil {
		t.Errorf("want the renamed file on the server: %v", err)
	}

	if err := fsys.RemoveAll(base + "/nested"); err != nil {
		t.Fatalf("removeall: %v", err)
	}
	if _, err := os.Stat(srv.Path("nested")); !os.IsNotExist(err) {
		t.Errorf("want the tree removed, stat gave %v", err)
	}
}

func TestRemotePathsStaySlashSeparated(t *testing.T) {
	isolate(t)
	srv := testserver.Start(t)
	if err := os.MkdirAll(srv.Path("a/b"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(srv.Path("a/b/c.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, conn := connect(t, srv)

	// A path built the way the panel builds one must reach the file. On
	// Windows a filepath.Join here would produce backslashes and fail.
	if _, err := conn.FS().Stat(srv.Root + "/a/b/c.txt"); err != nil {
		t.Errorf("stat through a nested remote path: %v", err)
	}

	entries, err := conn.FS().ReadDir(srv.Root + "/a/b")
	if err != nil {
		t.Fatalf("readdir: %v", err)
	}
	if len(entries) != 1 || entries[0].Name() != "c.txt" {
		t.Errorf("want one entry named c.txt, got %v", entries)
	}
}

func TestHomeReturnsWorkingDirectory(t *testing.T) {
	isolate(t)
	srv := testserver.Start(t)
	_, conn := connect(t, srv)

	if home := conn.FS().Home(); !strings.HasPrefix(home, "/") {
		t.Errorf("want an absolute remote home, got %q", home)
	}
}

func TestServerLabelAndAddr(t *testing.T) {
	s := Server{Host: "example.com", User: "kk"}
	if got := s.Addr(); got != "example.com:22" {
		t.Errorf("want the default port applied, got %q", got)
	}
	if got := s.Label(); got != "ssh://kk@example.com" {
		t.Errorf("want ssh://kk@example.com, got %q", got)
	}

	s.Port = 2222
	if got := s.Addr(); got != "example.com:2222" {
		t.Errorf("want example.com:2222, got %q", got)
	}
	if got := s.Label(); got != "ssh://kk@example.com:2222" {
		t.Errorf("want the non-default port in the label, got %q", got)
	}
}

func TestExpandHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	if got := expandHome("~/.ssh/id_ed25519"); got != filepath.Join(home, ".ssh", "id_ed25519") {
		t.Errorf("want the home directory substituted, got %q", got)
	}
	if got := expandHome("/etc/keys/id"); got != "/etc/keys/id" {
		t.Errorf("want an absolute path left alone, got %q", got)
	}
}
