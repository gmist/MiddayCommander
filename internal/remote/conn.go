package remote

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"

	"github.com/kooler/MiddayCommander/internal/vfs/sftpfs"
)

const (
	DefaultPort = 22

	// dialTimeout bounds the connect so a dead host does not hang the panel.
	dialTimeout = 15 * time.Second

	// keepaliveInterval catches a connection that died without saying so.
	keepaliveInterval = 30 * time.Second
)

// PassphraseRequiredError asks the UI to prompt and retry.
type PassphraseRequiredError struct {
	KeyPath string
}

func (e *PassphraseRequiredError) Error() string {
	return fmt.Sprintf("passphrase required for %s", e.KeyPath)
}

// Server describes how to reach a host.
type Server struct {
	Name    string `json:"name"`
	Host    string `json:"host"`
	Port    int    `json:"port,omitempty"`
	User    string `json:"user,omitempty"`
	KeyPath string `json:"key_path,omitempty"`
	Dir     string `json:"dir,omitempty"`

	// Alias is the ~/.ssh/config name Host came from, shown in the panel
	// header because it is what the user typed. Resolved at connect, not saved.
	Alias string `json:"-"`
}

func (s Server) Addr() string {
	port := s.Port
	if port == 0 {
		port = DefaultPort
	}
	return net.JoinHostPort(s.Host, strconv.Itoa(port))
}

// effectiveUser falls back to the current login name.
func (s Server) effectiveUser() string {
	if s.User != "" {
		return s.User
	}
	if u := os.Getenv("USER"); u != "" {
		return u
	}
	return os.Getenv("USERNAME")
}

// Label is the panel header, e.g. "ssh://kk@host", with a non-default port
// appended. An aliased host shows under its alias.
func (s Server) Label() string {
	host := s.Host
	if s.Alias != "" {
		host = s.Alias
	}
	label := "ssh://" + s.effectiveUser() + "@" + host
	if s.Port != 0 && s.Port != DefaultPort {
		label += ":" + strconv.Itoa(s.Port)
	}
	return label
}

// key identifies a connection for sharing between panels.
func (s Server) key() string { return s.effectiveUser() + "@" + s.Addr() }

func (s Server) DisplayName() string {
	if s.Name != "" {
		return s.Name
	}
	return s.Label()
}

// Credentials carries what an earlier attempt asked the user for.
type Credentials struct {
	Passphrase string

	// AcceptFingerprint is the fingerprint the user confirmed after an
	// UnknownHostError. Any other key is then refused.
	AcceptFingerprint string
}

// Conn is a live SSH session and its filesystem. Panels share one, so it is
// reference counted.
type Conn struct {
	key    string
	client *ssh.Client
	sftp   *sftp.Client
	fsys   *sftpfs.FS
	server Server

	registry  *Registry
	refs      int
	closed    chan struct{}
	closeOnce sync.Once
}

func (c *Conn) FS() *sftpfs.FS { return c.fsys }

func (c *Conn) Server() Server { return c.server }

// Registry shares one connection per server, so opening the same host in the
// second panel is instant.
type Registry struct {
	mu    sync.Mutex
	conns map[string]*Conn
}

func NewRegistry() *Registry {
	return &Registry{conns: map[string]*Conn{}}
}

// Acquire dials only when no connection is open. The caller must Release it.
func (r *Registry) Acquire(ctx context.Context, srv Server, creds Credentials) (*Conn, error) {
	// Resolve before keying so two entries naming one host share a connection.
	srv = srv.Resolve()
	key := srv.key()

	r.mu.Lock()
	if existing, ok := r.conns[key]; ok && existing.alive() {
		existing.refs++
		r.mu.Unlock()
		return existing, nil
	}
	r.mu.Unlock()

	conn, err := dial(ctx, srv, creds)
	if err != nil {
		return nil, err
	}
	conn.registry = r
	conn.key = key
	conn.refs = 1

	r.mu.Lock()
	// Another panel may have won the race; prefer the one already recorded.
	if existing, ok := r.conns[key]; ok && existing.alive() {
		existing.refs++
		r.mu.Unlock()
		conn.shutdown()
		return existing, nil
	}
	r.conns[key] = conn
	r.mu.Unlock()

	go conn.keepalive()
	return conn, nil
}

// Release closes the connection once nothing holds it.
func (r *Registry) Release(c *Conn) {
	if c == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()

	c.refs--
	if c.refs > 0 {
		return
	}
	// A keepalive failure may already have replaced this entry with a fresh
	// connection to the same host; deleting by key alone would orphan it.
	if r.conns[c.key] == c {
		delete(r.conns, c.key)
	}
	c.shutdown()
}

// drop removes a dead connection, leaving any replacement in place.
func (r *Registry) drop(c *Conn) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.conns[c.key] == c {
		delete(r.conns, c.key)
	}
}

// CloseAll tears down every connection at shutdown.
func (r *Registry) CloseAll() {
	r.mu.Lock()
	defer r.mu.Unlock()
	for key, c := range r.conns {
		delete(r.conns, key)
		c.shutdown()
	}
}

func (c *Conn) alive() bool {
	select {
	case <-c.closed:
		return false
	default:
		return true
	}
}

// shutdown is safe to call from both Release and the keepalive goroutine.
func (c *Conn) shutdown() {
	c.closeOnce.Do(func() {
		close(c.closed)
		if c.sftp != nil {
			_ = c.sftp.Close()
		}
		if c.client != nil {
			_ = c.client.Close()
		}
	})
}

// keepalive drops the connection once the peer stops answering.
func (c *Conn) keepalive() {
	ticker := time.NewTicker(keepaliveInterval)
	defer ticker.Stop()

	for {
		select {
		case <-c.closed:
			return
		case <-ticker.C:
			_, _, err := c.client.SendRequest("keepalive@openssh.com", true, nil)
			if err != nil {
				if c.registry != nil {
					c.registry.drop(c)
				}
				c.shutdown()
				return
			}
		}
	}
}

// dial opens one SSH session and layers SFTP on it.
func dial(ctx context.Context, srv Server, creds Credentials) (*Conn, error) {
	auths, closeAgent, err := authMethods(srv, creds)
	if err != nil {
		return nil, err
	}
	defer closeAgent()
	if len(auths) == 0 {
		return nil, errors.New("no usable credentials: add a key file to this server, " +
			"or add a key to your ssh-agent")
	}

	var save func() error
	var hostKey ssh.HostKeyCallback
	if creds.AcceptFingerprint != "" {
		hostKey, save = pinnedHostKey(srv.Addr(), creds.AcceptFingerprint)
	} else {
		hostKey, err = hostKeyCallback()
		if err != nil {
			return nil, err
		}
	}

	cfg := &ssh.ClientConfig{
		User:            srv.effectiveUser(),
		Auth:            auths,
		HostKeyCallback: hostKey,
		Timeout:         dialTimeout,
	}

	d := net.Dialer{Timeout: dialTimeout}
	rawConn, err := d.DialContext(ctx, "tcp", srv.Addr())
	if err != nil {
		return nil, err
	}

	// Neither the handshake nor the sftp startup takes a context, and
	// cfg.Timeout covers only the TCP dial. Closing the socket is what makes
	// them return, so a host that accepts TCP and then goes quiet cannot hold
	// the panel past the caller's deadline or an Esc.
	stopWatch := make(chan struct{})
	defer close(stopWatch)
	go func() {
		select {
		case <-ctx.Done():
			_ = rawConn.Close()
		case <-stopWatch:
		}
	}()

	// cancelled reports the context's error in preference to the connection
	// reset that cancelling it caused.
	cancelled := func(err error) error {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		return err
	}

	sshConn, chans, reqs, err := ssh.NewClientConn(rawConn, srv.Addr(), cfg)
	if err != nil {
		_ = rawConn.Close()
		return nil, cancelled(err)
	}
	client := ssh.NewClient(sshConn, chans, reqs)

	// Record the key only once the handshake succeeded.
	if save != nil {
		if err := save(); err != nil {
			_ = client.Close()
			return nil, fmt.Errorf("recording host key: %w", err)
		}
	}

	sftpClient, err := sftp.NewClient(client)
	if err != nil {
		_ = client.Close()
		return nil, cancelled(fmt.Errorf("starting sftp: %w", err))
	}

	// Cancelling just as the session came up must not leave it open.
	if err := ctx.Err(); err != nil {
		_ = sftpClient.Close()
		_ = client.Close()
		return nil, err
	}

	return &Conn{
		client: client,
		sftp:   sftpClient,
		fsys:   sftpfs.New(sftpClient, srv.Label()),
		server: srv,
		closed: make(chan struct{}),
	}, nil
}

// authMethods offers agent keys first, then key files. The returned func
// closes the agent and must run only after the handshake.
//
// Every signer goes into one publickey method: the protocol attempts each
// method name once, so a second ssh.PublicKeys is never reached.
func authMethods(srv Server, creds Credentials) ([]ssh.AuthMethod, func(), error) {
	var signers []ssh.Signer
	closeAgent := func() {}

	if agentKeys, closer, err := agentSigners(); err == nil && len(agentKeys) > 0 {
		signers = append(signers, agentKeys...)
		closeAgent = func() { _ = closer.Close() }
	}

	for _, keyPath := range candidateKeys(srv) {
		signer, err := loadKey(keyPath, creds.Passphrase)
		if err != nil {
			var needPass *ssh.PassphraseMissingError
			if errors.As(err, &needPass) {
				// Only prompt for a key the user named, so an unrelated
				// encrypted id_rsa cannot interrupt an agent login.
				if srv.KeyPath != "" {
					closeAgent()
					return nil, nil, &PassphraseRequiredError{KeyPath: keyPath}
				}
				continue
			}
			if srv.KeyPath != "" {
				closeAgent()
				return nil, nil, fmt.Errorf("reading %s: %w", keyPath, err)
			}
			continue
		}
		signers = append(signers, signer)
	}

	if len(signers) == 0 {
		return nil, closeAgent, nil
	}
	return []ssh.AuthMethod{ssh.PublicKeys(signers...)}, closeAgent, nil
}

// candidateKeys returns the server's key file, or the usual defaults.
func candidateKeys(srv Server) []string {
	if srv.KeyPath != "" {
		return []string{expandHome(srv.KeyPath)}
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}
	var out []string
	for _, name := range []string{"id_ed25519", "id_ecdsa", "id_rsa"} {
		p := filepath.Join(home, ".ssh", name)
		if _, err := os.Stat(p); err == nil {
			out = append(out, p)
		}
	}
	return out
}

func loadKey(path, passphrase string) (ssh.Signer, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if passphrase != "" {
		return ssh.ParsePrivateKeyWithPassphrase(data, []byte(passphrase))
	}
	return ssh.ParsePrivateKey(data)
}

func expandHome(p string) string {
	if !strings.HasPrefix(p, "~") {
		return p
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return p
	}
	return filepath.Join(home, strings.TrimPrefix(p, "~"))
}
