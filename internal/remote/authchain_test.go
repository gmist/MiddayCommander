package remote

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/crypto/ssh"

	"github.com/kooler/MiddayCommander/internal/remote/testserver"
)

// writeThrowawayKey writes an unencrypted key the server will not accept.
func writeThrowawayKey(t *testing.T, path string) {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	block, err := ssh.MarshalPrivateKey(priv, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, pem.EncodeToMemory(block), 0o600); err != nil {
		t.Fatal(err)
	}
}

// A rejected key must not block the one that works. The SSH protocol attempts
// each method name once, so separate ssh.PublicKeys methods would offer only
// the first. A decoy sits ahead of the real key in the default order.
func TestARejectedKeyDoesNotBlockAWorkingOne(t *testing.T) {
	isolate(t)
	srv := testserver.Start(t)

	home := os.Getenv("HOME")
	sshDir := filepath.Join(home, ".ssh")

	// A key the server has never heard of, tried first.
	writeThrowawayKey(t, filepath.Join(sshDir, "id_ed25519"))

	// The key the server does accept, tried second.
	accepted, err := os.ReadFile(srv.ClientKey)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sshDir, "id_rsa"), accepted, 0o600); err != nil {
		t.Fatal(err)
	}

	// KeyPath is empty, so the default search supplies both, in order.
	server := Server{Name: "test", Host: srv.Host, Port: srv.Port, User: "tester"}

	methods, closeAgent, err := authMethods(server, Credentials{})
	if err != nil {
		t.Fatalf("authMethods: %v", err)
	}
	closeAgent()
	if len(methods) != 1 {
		t.Errorf("want every key in a single publickey method, got %d methods", len(methods))
	}

	r := NewRegistry()
	_, err = r.Acquire(context.Background(), server, Credentials{})
	var unknown *UnknownHostError
	if !errors.As(err, &unknown) {
		t.Fatalf("want the host key prompt, got %v", err)
	}

	conn, err := r.Acquire(context.Background(), server,
		Credentials{AcceptFingerprint: unknown.Fingerprint})
	if err != nil {
		t.Fatalf("the working key should still authenticate behind a rejected one: %v", err)
	}
	defer r.Release(conn)

	if _, err := conn.FS().ReadDir(srv.Root); err != nil {
		t.Errorf("listing over the connection failed: %v", err)
	}
}

func TestNoCredentialsIsAClearError(t *testing.T) {
	isolate(t)
	srv := testserver.Start(t)

	// The isolated HOME has no keys and the agent is hidden.
	server := Server{Name: "test", Host: srv.Host, Port: srv.Port, User: "tester"}

	_, err := NewRegistry().Acquire(context.Background(), server, Credentials{})
	if err == nil {
		t.Fatal("want an error when there is nothing to authenticate with")
	}
	if !contains(err.Error(), "no usable credentials") {
		t.Errorf("want an error naming the problem, got %v", err)
	}
}

func contains(h, n string) bool {
	for i := 0; i+len(n) <= len(h); i++ {
		if h[i:i+len(n)] == n {
			return true
		}
	}
	return false
}
