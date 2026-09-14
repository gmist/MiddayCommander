// Package testserver runs an SSH server with an SFTP subsystem in the test
// process, so the remote code paths need no Docker and no real host.
package testserver

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
)

type Server struct {
	Addr        string // host:port
	Host        string
	Port        int
	Root        string // directory the SFTP subsystem serves
	Fingerprint string // SHA256 fingerprint of the host key
	ClientKey   string // path to the private key clients authenticate with

	listener net.Listener
	hostKey  ssh.Signer
	wg       sync.WaitGroup
	mu       sync.Mutex
	closed   bool
}

// Start serves a fresh temporary directory, and stops when the test ends.
func Start(t *testing.T) *Server {
	t.Helper()

	hostKey := mustSigner(t)
	clientKeyPath, clientSigner := writeClientKey(t)
	authorized := clientSigner.PublicKey()

	cfg := &ssh.ServerConfig{
		PublicKeyCallback: func(_ ssh.ConnMetadata, key ssh.PublicKey) (*ssh.Permissions, error) {
			if string(key.Marshal()) == string(authorized.Marshal()) {
				return &ssh.Permissions{}, nil
			}
			return nil, fmt.Errorf("unknown public key")
		},
	}
	cfg.AddHostKey(hostKey)

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}

	host, portStr, _ := net.SplitHostPort(ln.Addr().String())
	port := 0
	_, _ = fmt.Sscanf(portStr, "%d", &port)

	s := &Server{
		Addr:        ln.Addr().String(),
		Host:        host,
		Port:        port,
		Root:        t.TempDir(),
		Fingerprint: ssh.FingerprintSHA256(hostKey.PublicKey()),
		ClientKey:   clientKeyPath,
		listener:    ln,
		hostKey:     hostKey,
	}

	s.wg.Add(1)
	go s.serve(cfg)

	t.Cleanup(s.Close)
	return s
}

// RotateHostKey restarts on the same port with a different host key, which
// is what interception looks like.
func (s *Server) RotateHostKey(t *testing.T) {
	t.Helper()

	s.Close()
	s.wg.Wait()

	hostKey := mustSigner(t)
	_, clientSigner := loadClientKey(t, s.ClientKey)
	authorized := clientSigner.PublicKey()

	cfg := &ssh.ServerConfig{
		PublicKeyCallback: func(_ ssh.ConnMetadata, key ssh.PublicKey) (*ssh.Permissions, error) {
			if string(key.Marshal()) == string(authorized.Marshal()) {
				return &ssh.Permissions{}, nil
			}
			return nil, fmt.Errorf("unknown public key")
		},
	}
	cfg.AddHostKey(hostKey)

	ln, err := net.Listen("tcp", s.Addr)
	if err != nil {
		t.Fatalf("relisten: %v", err)
	}

	s.mu.Lock()
	s.listener = ln
	s.closed = false
	s.hostKey = hostKey
	s.Fingerprint = ssh.FingerprintSHA256(hostKey.PublicKey())
	s.mu.Unlock()

	s.wg.Add(1)
	go s.serve(cfg)
	t.Cleanup(s.Close)
}

// Close stops accepting connections.
func (s *Server) Close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return
	}
	s.closed = true
	_ = s.listener.Close()
}

func (s *Server) serve(cfg *ssh.ServerConfig) {
	defer s.wg.Done()
	for {
		conn, err := s.listener.Accept()
		if err != nil {
			return // listener closed
		}
		go s.handle(conn, cfg)
	}
}

func (s *Server) handle(nConn net.Conn, cfg *ssh.ServerConfig) {
	defer nConn.Close()

	sshConn, chans, reqs, err := ssh.NewServerConn(nConn, cfg)
	if err != nil {
		return
	}
	defer sshConn.Close()
	go ssh.DiscardRequests(reqs)

	for newChan := range chans {
		if newChan.ChannelType() != "session" {
			_ = newChan.Reject(ssh.UnknownChannelType, "only sessions")
			continue
		}
		ch, chReqs, err := newChan.Accept()
		if err != nil {
			return
		}
		go s.session(ch, chReqs)
	}
}

func (s *Server) session(ch ssh.Channel, reqs <-chan *ssh.Request) {
	for req := range reqs {
		if req.Type != "subsystem" || len(req.Payload) < 4 ||
			string(req.Payload[4:]) != "sftp" {
			_ = req.Reply(false, nil)
			continue
		}
		_ = req.Reply(true, nil)

		srv, err := sftp.NewServer(ch, sftp.WithServerWorkingDirectory(s.Root))
		if err != nil {
			_ = ch.Close()
			return
		}
		if err := srv.Serve(); err != nil && err != io.EOF {
			_ = srv.Close()
		}
		_ = ch.Close()
		return
	}
}

// Path joins a name onto the server's served directory, for assertions made
// from the test's side of the connection.
func (s *Server) Path(name string) string { return filepath.Join(s.Root, name) }

func mustSigner(t *testing.T) ssh.Signer {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate host key: %v", err)
	}
	signer, err := ssh.NewSignerFromKey(priv)
	if err != nil {
		t.Fatalf("host signer: %v", err)
	}
	return signer
}

// writeClientKey generates a key pair and writes the private half where a
// Server config can point at it.
func writeClientKey(t *testing.T) (string, ssh.Signer) {
	t.Helper()

	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate client key: %v", err)
	}

	block, err := ssh.MarshalPrivateKey(priv, "")
	if err != nil {
		t.Fatalf("marshal client key: %v", err)
	}

	path := filepath.Join(t.TempDir(), "id_ed25519")
	if err := os.WriteFile(path, pem.EncodeToMemory(block), 0o600); err != nil {
		t.Fatalf("write client key: %v", err)
	}

	signer, err := ssh.NewSignerFromKey(priv)
	if err != nil {
		t.Fatalf("client signer: %v", err)
	}
	return path, signer
}

func loadClientKey(t *testing.T, path string) (string, ssh.Signer) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read client key: %v", err)
	}
	signer, err := ssh.ParsePrivateKey(data)
	if err != nil {
		t.Fatalf("parse client key: %v", err)
	}
	return path, signer
}
