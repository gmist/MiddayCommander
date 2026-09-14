package app

import (
	"context"
	"errors"
	"fmt"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/kooler/MiddayCommander/internal/remote"
	"github.com/kooler/MiddayCommander/internal/ui/dialog"
	"github.com/kooler/MiddayCommander/internal/ui/panel"
	"github.com/kooler/MiddayCommander/internal/vfs"
)

// connectTimeout bounds the attempt, so a host that accepts TCP then goes
// quiet cannot leave the panel waiting.
const connectTimeout = 30 * time.Second

type connectedMsg struct {
	conn *remote.Conn
	path string
	side FocusTarget
	err  error
}

// connectCmd dials off the event loop. Every SFTP call runs inside a tea.Cmd
// like this one: from Update, a slow network would freeze the interface.
func connectCmd(ctx context.Context, reg *remote.Registry, srv remote.Server, creds remote.Credentials, remotePath string, side FocusTarget) tea.Cmd {
	return func() tea.Msg {
		conn, err := reg.Acquire(ctx, srv, creds)
		return connectedMsg{
			conn: conn,
			path: remotePath,
			side: side,
			err:  err,
		}
	}
}

func (m Model) startConnect(srv remote.Server, remotePath string) (tea.Model, tea.Cmd) {
	m.pendingServer = srv
	m.pendingRemotePath = remotePath
	m.pendingSide = m.focus
	m.pendingCreds = remote.Credentials{}
	return m.dialConnection()
}

// retryConnect re-dials with whatever the user just supplied.
func (m Model) retryConnect() (tea.Model, tea.Cmd) {
	return m.dialConnection()
}

// dialConnection opens the waiting dialog and starts the attempt. The context
// lives here, not in the command, so Esc on the dialog can cancel it.
func (m Model) dialConnection() (tea.Model, tea.Cmd) {
	ctx, cancel := context.WithTimeout(context.Background(), connectTimeout)
	m.opCancel = cancel

	d := dialog.NewProgress("Connecting", tagConnect)
	d.SetConnecting(m.pendingServer.Label())
	m.dialog = &d

	return m, connectCmd(ctx, m.connRegistry, m.pendingServer, m.pendingCreds,
		m.pendingRemotePath, m.pendingSide)
}

// handleConnected opens the panel, or asks the user the next question.
func (m Model) handleConnected(msg connectedMsg) (tea.Model, tea.Cmd) {
	// Esc may have landed in the moment between the session coming up and this
	// message arriving.
	cancelled := m.dialog != nil &&
		m.dialog.Kind() == dialog.KindProgress &&
		m.dialog.CancelRequested()

	m.dialog = nil
	if m.opCancel != nil {
		m.opCancel()
		m.opCancel = nil
	}

	if msg.err != nil {
		return m.handleConnectError(msg.err)
	}
	if cancelled {
		m.connRegistry.Release(msg.conn)
		return m, nil
	}

	// Release whatever this panel was using before. Acquire took a reference
	// even when it handed back the connection this panel already had, so an
	// unchanged connection still needs one released.
	side := msg.side
	if old := m.panelConns[side]; old != nil {
		m.connRegistry.Release(old)
	}
	if m.panelConns == nil {
		m.panelConns = map[FocusTarget]*remote.Conn{}
	}
	m.panelConns[side] = msg.conn

	// The resolved server is the one that was actually dialled: ~/.ssh/config
	// may have supplied the user or the real hostname, and the header, the
	// copied URL and any bookmark have to match the connection.
	srv := msg.conn.Server()

	remotePath := msg.path
	if remotePath == "" || remotePath == "/" {
		if dir := srv.Dir; dir != "" {
			remotePath = dir
		} else {
			remotePath = msg.conn.FS().Home()
		}
	}

	p := m.panelFor(side)
	p.SetLocation(vfs.Location{
		FS:     msg.conn.FS(),
		Path:   remotePath,
		Kind:   vfs.KindSSH,
		Label:  srv.Label(),
		Origin: srv.DisplayName(),
	})

	return m, p.LoadDir()
}

// handleConnectError decides whether the failure is a question or an end.
func (m Model) handleConnectError(err error) (tea.Model, tea.Cmd) {
	// The user pressed Esc, so they know what happened.
	if errors.Is(err, context.Canceled) {
		return m, nil
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return m.showError("Connection failed",
			fmt.Errorf("%s did not respond within %s", m.pendingServer.Label(), connectTimeout))
	}

	var unknown *remote.UnknownHostError
	if errors.As(err, &unknown) {
		m.pendingFingerprint = unknown.Fingerprint
		body := fmt.Sprintf(
			"%s is not in your known_hosts file.\n\n%s key fingerprint:\n%s\n\n"+
				"Accept this key and continue?",
			unknown.Host, unknown.KeyType, unknown.Fingerprint)
		d := dialog.NewConfirm("Unknown host", body, tagTrustHost)
		m.dialog = &d
		return m, nil
	}

	var passphrase *remote.PassphraseRequiredError
	if errors.As(err, &passphrase) {
		d := dialog.NewPassword("Key passphrase",
			"Passphrase for "+passphrase.KeyPath+":", tagPassphrase)
		m.dialog = &d
		return m, nil
	}

	// A changed host key, a refused login, an unreachable host.
	return m.showError("Connection failed", err)
}

func (m *Model) panelFor(side FocusTarget) *panel.Model {
	if side == FocusLeft {
		return &m.leftPanel
	}
	return &m.rightPanel
}

// releaseUnusedConnections is how leaving a server with ".." closes it.
func (m *Model) releaseUnusedConnections() {
	for side, conn := range m.panelConns {
		if conn == nil {
			continue
		}
		if !m.panelFor(side).UsesFS(conn.FS()) {
			m.connRegistry.Release(conn)
			delete(m.panelConns, side)
		}
	}
}

// openServerTarget resolves Go To text: an ssh:// address or a saved name.
func (m Model) openServerTarget(text string) (remote.Server, string, bool) {
	if remote.IsURL(text) {
		srv, remotePath, err := remote.ParseURL(text)
		if err != nil {
			return remote.Server{}, "", false
		}
		// A typed address picks up the key file of a saved entry on the
		// same host.
		for _, saved := range m.serverStore.Servers {
			if saved.Host == srv.Host && (srv.User == "" || saved.User == srv.User) {
				if srv.User == "" {
					srv.User = saved.User
				}
				if srv.Port == 0 {
					srv.Port = saved.Port
				}
				srv.KeyPath = saved.KeyPath
				break
			}
		}
		return srv, remotePath, true
	}

	if srv, ok := m.serverStore.Find(text); ok {
		return srv, srv.Dir, true
	}
	return remote.Server{}, "", false
}
