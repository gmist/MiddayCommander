package app

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/kooler/MiddayCommander/internal/actions"
	"github.com/kooler/MiddayCommander/internal/remote"
	"github.com/kooler/MiddayCommander/internal/remote/testserver"
	"github.com/kooler/MiddayCommander/internal/ui/dialog"
	"github.com/kooler/MiddayCommander/internal/ui/panel"
	"github.com/kooler/MiddayCommander/internal/ui/servers"
	"github.com/kooler/MiddayCommander/internal/vfs"
)

// Driving the root model with messages, as the connection flow really runs.
// No terminal involved.

func isolate(t *testing.T) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("SSH_AUTH_SOCK", "")
}

func newModel(t *testing.T) Model {
	t.Helper()
	m := New("test")
	m.width, m.height = 100, 30
	m.recalcLayout()
	return m
}

// run feeds one message in.
func run(t *testing.T, m Model, msg tea.Msg) (Model, tea.Cmd) {
	t.Helper()
	next, cmd := m.Update(msg)
	updated, ok := next.(Model)
	if !ok {
		t.Fatalf("Update returned %T, want app.Model", next)
	}
	return updated, cmd
}

// drain runs a command and returns its message.
func drain(t *testing.T, cmd tea.Cmd) tea.Msg {
	t.Helper()
	if cmd == nil {
		t.Fatal("want a command, got nil")
	}
	return cmd()
}

// serverConnect is what the server list sends when a server is picked.
func serverConnect(srv remote.Server) tea.Msg {
	return servers.ConnectMsg{Server: srv}
}

func testServerFor(t *testing.T, s *testserver.Server) remote.Server {
	t.Helper()
	return remote.Server{
		Name:    "test",
		Host:    s.Host,
		Port:    s.Port,
		User:    "tester",
		KeyPath: s.ClientKey,
	}
}

func TestConnectFlowAsksToTrustThenOpensPanel(t *testing.T) {
	isolate(t)
	srv := testserver.Start(t)
	server := testServerFor(t, srv)

	if err := os.WriteFile(srv.Path("remote-file.txt"), []byte("hi"), 0o644); err != nil {
		t.Fatal(err)
	}

	m := newModel(t)

	// Opening the server starts a connection and shows the waiting dialog.
	m, cmd := run(t, m, serverConnect(server))
	if m.dialog == nil || m.dialog.Kind() != dialog.KindProgress {
		t.Fatal("want a progress dialog while connecting")
	}

	// The first attempt comes back asking about the host key.
	m, _ = run(t, m, drain(t, cmd))
	if m.dialog == nil || m.dialog.Kind() != dialog.KindConfirm {
		t.Fatalf("want a confirmation dialog for the unknown host, got %v", m.dialog)
	}
	if m.pendingFingerprint != srv.Fingerprint {
		t.Errorf("want the server's fingerprint offered, got %q", m.pendingFingerprint)
	}

	// The user accepts, which retries with the fingerprint they confirmed.
	m, cmd = run(t, m, dialog.Result{Kind: dialog.KindConfirm, Confirmed: true, Tag: tagTrustHost})
	if m.pendingCreds.AcceptFingerprint != srv.Fingerprint {
		t.Errorf("want the accepted fingerprint carried into the retry, got %q",
			m.pendingCreds.AcceptFingerprint)
	}

	// The retry succeeds and the panel switches to the server.
	m, cmd = run(t, m, drain(t, cmd))
	if m.dialog != nil {
		t.Errorf("want the dialog closed after connecting, got %v", m.dialog.Kind())
	}

	loc := m.activePanel().Location()
	if loc.Kind != vfs.KindSSH {
		t.Fatalf("want the panel on an SSH location, got kind %v", loc.Kind)
	}
	if !strings.HasPrefix(loc.Label, "ssh://tester@") {
		t.Errorf("want an ssh:// label, got %q", loc.Label)
	}
	if m.panelConns[m.focus] == nil {
		t.Error("want the connection recorded against the panel")
	}

	// Loading the directory reaches the server.
	msg := drain(t, cmd)
	loaded, ok := msg.(panel.DirLoadedMsg)
	if !ok {
		t.Fatalf("want a DirLoadedMsg, got %T", msg)
	}
	if loaded.Err != nil {
		t.Fatalf("listing the server failed: %v", loaded.Err)
	}
}

func TestDeclinedHostKeyLeavesPanelLocal(t *testing.T) {
	isolate(t)
	srv := testserver.Start(t)

	m := newModel(t)
	before := m.activePanel().Path()

	m, cmd := run(t, m, serverConnect(testServerFor(t, srv)))
	m, _ = run(t, m, drain(t, cmd))

	// Saying no ends the attempt.
	m, _ = run(t, m, dialog.Result{Kind: dialog.KindConfirm, Confirmed: false, Tag: tagTrustHost})

	if !m.activePanel().IsLocal() {
		t.Error("want the panel still local after declining the host key")
	}
	if m.activePanel().Path() != before {
		t.Errorf("want the panel unmoved, got %q", m.activePanel().Path())
	}
	if m.panelConns[m.focus] != nil {
		t.Error("want no connection recorded after declining")
	}
}

func TestChangedHostKeyShowsAnErrorNotAPrompt(t *testing.T) {
	isolate(t)
	srv := testserver.Start(t)
	server := testServerFor(t, srv)

	// Trust the host once.
	m := newModel(t)
	m, cmd := run(t, m, serverConnect(server))
	m, _ = run(t, m, drain(t, cmd))
	m, cmd = run(t, m, dialog.Result{Kind: dialog.KindConfirm, Confirmed: true, Tag: tagTrustHost})
	m, _ = run(t, m, drain(t, cmd))

	// The host now answers with a different key.
	srv.RotateHostKey(t)

	m2 := newModel(t)
	m2, cmd = run(t, m2, serverConnect(server))
	m2, _ = run(t, m2, drain(t, cmd))

	if m2.dialog == nil {
		t.Fatal("want a dialog after a changed host key")
	}
	if m2.dialog.Kind() != dialog.KindError {
		t.Errorf("a changed host key must be an error, not a %v prompt", m2.dialog.Kind())
	}
}

func TestGoToAcceptsAnSSHAddress(t *testing.T) {
	isolate(t)
	srv := testserver.Start(t)

	m := newModel(t)
	// Save the server so the typed address picks up its key file.
	m.serverStore.Add(testServerFor(t, srv))

	addr := "ssh://tester@" + srv.Host + ":" + itoa(srv.Port) + "/tmp"
	m, _ = run(t, m, dialog.Result{Kind: dialog.KindInput, Confirmed: true, Text: addr, Tag: tagGoTo})

	if m.dialog == nil || m.dialog.Kind() != dialog.KindProgress {
		t.Fatal("want an ssh:// address in Go To to start a connection")
	}
	if m.pendingServer.Host != srv.Host {
		t.Errorf("want the host parsed out, got %q", m.pendingServer.Host)
	}
	if m.pendingServer.KeyPath != srv.ClientKey {
		t.Errorf("want the saved key file applied, got %q", m.pendingServer.KeyPath)
	}
	if m.pendingRemotePath != "/tmp" {
		t.Errorf("want the path parsed out, got %q", m.pendingRemotePath)
	}
}

func TestGoToStillHandlesLocalPaths(t *testing.T) {
	isolate(t)
	dir := t.TempDir()

	m := newModel(t)
	m, cmd := run(t, m, dialog.Result{Kind: dialog.KindInput, Confirmed: true, Text: dir, Tag: tagGoTo})

	if m.dialog != nil {
		t.Errorf("a local path should not open a dialog, got %v", m.dialog.Kind())
	}
	if m.activePanel().Path() != dir {
		t.Errorf("want the panel at %q, got %q", dir, m.activePanel().Path())
	}
	if cmd == nil {
		t.Error("want a directory load command")
	}
}

func TestLeavingAServerReleasesItsConnection(t *testing.T) {
	isolate(t)
	srv := testserver.Start(t)
	if err := os.MkdirAll(srv.Path("sub"), 0o755); err != nil {
		t.Fatal(err)
	}

	m := newModel(t)
	m, cmd := run(t, m, serverConnect(testServerFor(t, srv)))
	m, _ = run(t, m, drain(t, cmd))
	m, cmd = run(t, m, dialog.Result{Kind: dialog.KindConfirm, Confirmed: true, Tag: tagTrustHost})
	m, _ = run(t, m, drain(t, cmd))

	if m.panelConns[m.focus] == nil {
		t.Fatal("want a connection after opening the server")
	}

	// Going somewhere local drops it.
	m.activePanel().SetPath(t.TempDir())
	m.releaseUnusedConnections()

	if m.panelConns[m.focus] != nil {
		t.Error("want the connection released once the panel left the server")
	}
}

func TestRemotePanelDisablesLocalOnlyFeatures(t *testing.T) {
	isolate(t)
	srv := testserver.Start(t)

	m := newModel(t)
	m, cmd := run(t, m, serverConnect(testServerFor(t, srv)))
	m, _ = run(t, m, drain(t, cmd))
	m, cmd = run(t, m, dialog.Result{Kind: dialog.KindConfirm, Confirmed: true, Tag: tagTrustHost})
	m, _ = run(t, m, drain(t, cmd))

	if m.activePanel().IsLocal() {
		t.Fatal("expected a remote panel for this test")
	}

	// Fuzzy find and the command runner both need a local working directory.
	if next, _ := m.startFuzzyFind(); next.(Model).fuzzy != nil {
		t.Error("fuzzy find should not open on a remote panel")
	}
	if next, _ := m.startCmdExec(); next.(Model).cmdExec != nil {
		t.Error("the command runner should not open on a remote panel")
	}

	// The shell still opens, but in the panel's local directory.
	if got := m.activePanel().LocalPath(); got == "" || !filepath.IsAbs(got) {
		t.Errorf("want an absolute local path for the shell, got %q", got)
	}
}

func TestCopyPathUsesTheSSHURLForRemoteFiles(t *testing.T) {
	loc := vfs.Location{Kind: vfs.KindSSH, Label: "ssh://kk@host", Path: "/var/log"}
	if got := loc.URLFor("/var/log/app.log"); got != "ssh://kk@host/var/log/app.log" {
		t.Errorf("want a full ssh:// URL, got %q", got)
	}

	local := vfs.Location{Kind: vfs.KindLocal, Path: "/home/kk"}
	if got := local.URLFor("/home/kk/f"); got != "/home/kk/f" {
		t.Errorf("want a bare local path, got %q", got)
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}

// runAll unwraps tea.Batch so the work it holds runs, feeding every message
// back into the model.
func runAll(t *testing.T, m Model, cmd tea.Cmd) Model {
	t.Helper()
	if cmd == nil {
		return m
	}
	msg := cmd()
	switch v := msg.(type) {
	case tea.BatchMsg:
		for _, sub := range v {
			m = runAll(t, m, sub)
		}
	case nil:
	default:
		m, _ = run(t, m, msg)
	}
	return m
}

// loadPanel gives the active panel entries to select.
func loadPanel(t *testing.T, m Model) Model {
	t.Helper()
	cmd := m.activePanel().LoadDir()
	msg := cmd()
	m, _ = run(t, m, msg)
	return m
}

// selectEntry moves the cursor onto a named entry.
func selectEntry(t *testing.T, m Model, name string) Model {
	t.Helper()
	p := m.activePanel()
	for i := 0; i < 50; i++ {
		if e := p.CurrentEntry(); e != nil && e.Name() == name {
			return m
		}
		p.Update(tea.KeyMsg{Type: tea.KeyDown})
	}
	t.Fatalf("could not put the cursor on %q", name)
	return m
}

// connectPanel opens srv in the active panel, answering the host key prompt.
func connectPanel(t *testing.T, m Model, srv remote.Server) Model {
	t.Helper()
	m, cmd := run(t, m, serverConnect(srv))
	m, _ = run(t, m, drain(t, cmd))
	m, cmd = run(t, m, dialog.Result{Kind: dialog.KindConfirm, Confirmed: true, Tag: tagTrustHost})
	m, cmd = run(t, m, drain(t, cmd))
	if cmd != nil {
		m, _ = run(t, m, cmd()) // apply the directory listing
	}
	return m
}

func TestCopyFromRemotePanelToLocalPanel(t *testing.T) {
	isolate(t)
	srv := testserver.Start(t)
	if err := os.WriteFile(srv.Path("payload.txt"), []byte("from the server"), 0o644); err != nil {
		t.Fatal(err)
	}

	landing := t.TempDir()

	m := newModel(t)
	m.inactivePanelModel().SetPath(landing) // the copy destination
	m = connectPanel(t, m, testServerFor(t, srv))

	if m.activePanel().IsLocal() {
		t.Fatal("expected the active panel on the server")
	}
	m = selectEntry(t, m, "payload.txt")

	// F5 on a single selection opens "Copy to:" prefilled with the target.
	next, _ := m.startCopy()
	m = next.(Model)
	if m.dialog == nil || m.dialog.Kind() != dialog.KindInput {
		t.Fatal("want the copy input dialog")
	}
	if len(m.pendingSources) != 1 || m.pendingSources[0].Kind != vfs.KindSSH {
		t.Fatalf("want one remote source, got %+v", m.pendingSources)
	}
	if m.pendingDest.Kind != vfs.KindLocal || m.pendingDest.Path != landing {
		t.Fatalf("want the local panel as destination, got %+v", m.pendingDest)
	}

	target := filepath.Join(landing, "payload.txt")
	m, cmd := run(t, m, dialog.Result{
		Kind: dialog.KindInput, Confirmed: true, Text: target, Tag: tagCopyAs,
	})
	m = runAll(t, m, cmd)

	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("the file should have landed locally: %v", err)
	}
	if string(got) != "from the server" {
		t.Errorf("want %q, got %q", "from the server", string(got))
	}
}

func TestCopyFromLocalPanelToRemotePanel(t *testing.T) {
	isolate(t)
	srv := testserver.Start(t)

	src := t.TempDir()
	if err := os.WriteFile(filepath.Join(src, "upload.txt"), []byte("to the server"), 0o644); err != nil {
		t.Fatal(err)
	}

	m := newModel(t)
	// Connect the right panel, then drive from the left (local) one.
	m.focus = FocusRight
	m = connectPanel(t, m, testServerFor(t, srv))
	m.focus = FocusLeft
	m.activePanel().SetPath(src)
	m = loadPanel(t, m)

	if !m.activePanel().IsLocal() {
		t.Fatal("expected a local active panel")
	}
	if m.inactiveRef().Kind != vfs.KindSSH {
		t.Fatalf("expected the remote panel as destination, got %+v", m.inactiveRef())
	}
	m = selectEntry(t, m, "upload.txt")

	next, _ := m.startCopy()
	m = next.(Model)
	if m.dialog == nil {
		t.Fatal("want the copy dialog")
	}
	dest := m.pendingDest.Join("upload.txt")

	m, cmd := run(t, m, dialog.Result{
		Kind: dialog.KindInput, Confirmed: true, Text: dest.Path, Tag: tagCopyAs,
	})
	m = runAll(t, m, cmd)

	got, err := os.ReadFile(srv.Path("upload.txt"))
	if err != nil {
		t.Fatalf("the file should have landed on the server: %v", err)
	}
	if string(got) != "to the server" {
		t.Errorf("want %q, got %q", "to the server", string(got))
	}
}

func TestCancellingAConnectIsSilent(t *testing.T) {
	isolate(t)
	srv := testserver.Start(t)

	m := newModel(t)
	m, _ = run(t, m, serverConnect(testServerFor(t, srv)))

	// Esc on the connecting dialog cancels the attempt.
	m, _ = run(t, m, connectedMsg{
		side: m.focus,
		err:  context.Canceled,
	})

	if m.dialog != nil {
		t.Errorf("cancelling should not leave a dialog, got %v", m.dialog.Kind())
	}
	if !m.activePanel().IsLocal() {
		t.Error("want the panel unchanged after cancelling")
	}
}

func TestConnectTimeoutExplainsItself(t *testing.T) {
	isolate(t)
	srv := testserver.Start(t)

	m := newModel(t)
	m, _ = run(t, m, serverConnect(testServerFor(t, srv)))
	m, _ = run(t, m, connectedMsg{
		side: m.focus,
		err:  context.DeadlineExceeded,
	})

	if m.dialog == nil || m.dialog.Kind() != dialog.KindError {
		t.Fatal("want an error dialog after a timeout")
	}
}

func TestSwappingPanelsKeepsTheRemoteConnection(t *testing.T) {
	isolate(t)
	srv := testserver.Start(t)

	m := newModel(t)
	m = connectPanel(t, m, testServerFor(t, srv))

	conn := m.panelConns[FocusLeft]
	if conn == nil {
		t.Fatal("want the connection recorded against the left panel")
	}

	m, _ = run(t, m, tea.KeyMsg{Type: tea.KeyCtrlU})
	// The release sweep runs on the next panel keypress.
	m, _ = run(t, m, tea.KeyMsg{Type: tea.KeyDown})

	if !m.rightPanel.UsesFS(conn.FS()) {
		t.Fatal("the server should have moved to the right panel")
	}
	if m.panelConns[FocusRight] != conn {
		t.Error("want the connection tracked against the panel that now shows it")
	}
	if _, err := conn.FS().ReadDir(srv.Root); err != nil {
		t.Errorf("the connection must survive a panel swap: %v", err)
	}
}

func TestEnterOnARemoteFileStagesItRatherThanOpeningALocalPath(t *testing.T) {
	isolate(t)
	srv := testserver.Start(t)
	if err := os.WriteFile(srv.Path("notes.txt"), []byte("remote body"), 0o644); err != nil {
		t.Fatal(err)
	}

	m := newModel(t)
	m = connectPanel(t, m, testServerFor(t, srv))
	m = selectEntry(t, m, "notes.txt")

	m, cmd := run(t, m, panel.OpenFileMsg{Path: m.activePanel().CurrentPath()})
	if m.dialog == nil || m.dialog.Kind() != dialog.KindProgress {
		t.Fatal("want the download dialog: the remote path means nothing to a local editor")
	}

	ready, ok := drain(t, cmd).(stagedReadyMsg)
	if !ok {
		t.Fatalf("want a staged file, got %T", ready)
	}
	if ready.err != nil {
		t.Fatalf("staging failed: %v", ready.err)
	}
	got, err := os.ReadFile(ready.staged.tmpPath)
	if err != nil {
		t.Fatalf("reading the staged copy: %v", err)
	}
	if string(got) != "remote body" {
		t.Errorf("want the server's content staged, got %q", string(got))
	}
}

func TestEditedRemoteFileGoesBackWithItsMode(t *testing.T) {
	isolate(t)
	srv := testserver.Start(t)
	if err := os.WriteFile(srv.Path("run.sh"), []byte("old\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(srv.Path("run.sh"), 0o755); err != nil {
		t.Fatal(err)
	}

	m := newModel(t)
	m = connectPanel(t, m, testServerFor(t, srv))
	m = selectEntry(t, m, "run.sh")

	_, cmd := run(t, m, panel.OpenFileMsg{Path: m.activePanel().CurrentPath()})
	ready, ok := drain(t, cmd).(stagedReadyMsg)
	if !ok || ready.err != nil {
		t.Fatalf("staging failed: %v", ready.err)
	}

	// Stand in for the editor.
	if err := os.WriteFile(ready.staged.tmpPath, []byte("new content\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := uploadStaged(ready.staged); err != nil {
		t.Fatalf("upload: %v", err)
	}

	got, err := os.ReadFile(srv.Path("run.sh"))
	if err != nil {
		t.Fatalf("reading the file back: %v", err)
	}
	if string(got) != "new content\n" {
		t.Errorf("want the edit written back, got %q", string(got))
	}
	info, err := os.Stat(srv.Path("run.sh"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o755 {
		t.Errorf("want the executable bit kept, got %v", info.Mode().Perm())
	}
	if _, err := os.Stat(srv.Path("run.sh" + actions.PartSuffix)); !os.IsNotExist(err) {
		t.Errorf("the scratch file should be gone, stat gave %v", err)
	}
}
