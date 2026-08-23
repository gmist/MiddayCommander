package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/kooler/MiddayCommander/internal/actions"
	"github.com/kooler/MiddayCommander/internal/bookmark"
	"github.com/kooler/MiddayCommander/internal/config"
	"github.com/kooler/MiddayCommander/internal/remote"
	"github.com/kooler/MiddayCommander/internal/ui/bookmarks"
	"github.com/kooler/MiddayCommander/internal/ui/cmdexec"
	"github.com/kooler/MiddayCommander/internal/ui/copypath"
	"github.com/kooler/MiddayCommander/internal/ui/dialog"
	"github.com/kooler/MiddayCommander/internal/ui/fuzzy"
	"github.com/kooler/MiddayCommander/internal/ui/help"
	"github.com/kooler/MiddayCommander/internal/ui/menubar"
	"github.com/kooler/MiddayCommander/internal/ui/overlay"
	"github.com/kooler/MiddayCommander/internal/ui/panel"
	"github.com/kooler/MiddayCommander/internal/ui/quickview"
	"github.com/kooler/MiddayCommander/internal/ui/servers"
	"github.com/kooler/MiddayCommander/internal/ui/theme"
	"github.com/kooler/MiddayCommander/internal/ui/themepicker"
	"github.com/kooler/MiddayCommander/internal/vfs"
	"github.com/kooler/MiddayCommander/internal/vfs/local"
)

// FocusTarget tracks which panel has focus.
type FocusTarget int

const (
	FocusLeft FocusTarget = iota
	FocusRight
)

// Dialog tags identify which operation triggered the dialog.
const (
	tagCopy          = "copy"
	tagCopyAs        = "copyas"
	tagMove          = "move"
	tagMoveAs        = "moveas"
	tagDelete        = "delete"
	tagMkdir         = "mkdir"
	tagRename        = "rename"
	tagGoTo          = "goto"
	tagExecute       = "execute"
	tagSelectGroup   = "selectgroup"
	tagDeselectGroup = "deselectgroup"
	tagConnect       = "connect"
	tagTrustHost     = "trusthost"
	tagPassphrase    = "passphrase"
	tagStage         = "stage"
)

// Model is the root application model.
type Model struct {
	leftPanel  panel.Model
	rightPanel panel.Model
	focus      FocusTarget
	keyMap     KeyMap
	theme      theme.Theme
	cfg        config.Config
	version    string
	menuItems  []menubar.Item
	width      int
	height     int

	// Overlays
	dialog      *dialog.Model
	fuzzy       *fuzzy.Model
	bookmarks   *bookmarks.Model
	servers     *servers.Model
	help        *help.Model
	themePicker *themepicker.Model
	cmdExec     *cmdexec.Model
	copyPath    *copypath.Model

	// Quick view: non-nil when the inactive pane shows a file preview.
	// quickFocus toggles whether keys scroll the preview (true) or drive the
	// active listing (false). m.focus always remains the driver panel.
	quickview  *quickview.Model
	quickFocus bool

	// Saved theme for reverting on Esc in theme picker
	themeBeforePick theme.Theme

	// Bookmark store
	bookmarkStore *bookmark.Store

	// Saved servers, live connections, and the panel each one serves.
	serverStore  *remote.Store
	connRegistry *remote.Registry
	panelConns   map[FocusTarget]*remote.Conn

	// Kept while a host key or passphrase dialog is open, so the attempt can
	// be retried with the answer.
	pendingServer      remote.Server
	pendingRemotePath  string
	pendingSide        FocusTarget
	pendingCreds       remote.Credentials
	pendingFingerprint string

	// A remote file downloaded for $EDITOR or $PAGER, held until it is
	// written back.
	pendingStaged     stagedFile
	pendingStagedEdit bool

	// Pending operation state (saved while dialog is open)
	pendingSources     []vfs.FileRef
	pendingDest        vfs.FileRef
	pendingExecutePath string

	// In-flight file operation state
	opCancel context.CancelFunc

	// Double-Esc to quit
	lastEsc time.Time

	// Shift F-key menu bar
	shiftMenuItems []menubar.Item
	shiftHeld      bool
}

// New creates a new application model.
func New(version string) Model {
	cfg := config.Load()

	home, err := os.UserHomeDir()
	if err != nil {
		home = string(filepath.Separator)
	}

	cwd, err := os.Getwd()
	if err != nil {
		cwd = home
	}

	lfs := local.New(string(filepath.Separator))

	panelKM := panelKeyMapFromConfig(cfg.Keys)

	left := panel.New(lfs, cwd, panelKM, cfg)
	left.SetActive(true)

	right := panel.New(lfs, home, panelKM, cfg)

	th := theme.Resolve(cfg.Theme)

	return Model{
		leftPanel:      left,
		rightPanel:     right,
		focus:          FocusLeft,
		keyMap:         KeyMapFromConfig(cfg.Keys),
		theme:          th,
		cfg:            cfg,
		version:        version,
		menuItems:      menubar.DefaultItems(cfg),
		shiftMenuItems: menubar.ShiftItems(cfg),
		bookmarkStore:  bookmark.LoadStore(),
		serverStore:    remote.LoadStore(),
		connRegistry:   remote.NewRegistry(),
		panelConns:     map[FocusTarget]*remote.Conn{},
	}
}

func panelKeyMapFromConfig(keys config.KeyBindings) panel.KeyMap {
	return panel.KeyMap{
		Up:           binding(keys.Up, "up"),
		Down:         binding(keys.Down, "down"),
		PageUp:       binding(keys.PageUp, "page up"),
		PageDown:     binding(keys.PageDown, "page down"),
		Home:         binding(keys.Home, "home"),
		End:          binding(keys.End, "end"),
		GoBack:       binding(keys.GoBack, "go back"),
		ToggleSelect: binding(keys.ToggleSelect, "toggle select"),
		SelectUp:     binding(keys.SelectUp, "select up"),
		SelectDown:   binding(keys.SelectDown, "select down"),
		QuickSearch:  binding(keys.QuickSearch, "quick search"),
	}
}

func (m Model) Init() tea.Cmd {
	return tea.Batch(
		m.leftPanel.LoadDir(),
		m.rightPanel.LoadDir(),
	)
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.recalcLayout()
		return m, nil

	case panel.DirLoadedMsg:
		m.leftPanel.HandleDirLoaded(msg)
		m.rightPanel.HandleDirLoaded(msg)
		if m.quickview != nil && !m.quickFocus {
			return m, m.syncQuickView()
		}
		return m, nil

	case panel.RestoreCursorMsg:
		m.activePanel().RestoreCursor(msg.Name)
		if m.quickview != nil && !m.quickFocus {
			return m, m.syncQuickView()
		}
		return m, nil

	case quickview.FileLoadedMsg:
		if m.quickview != nil {
			m.quickview.HandleFileLoaded(msg)
		}
		return m, nil

	// Help messages
	case help.DismissMsg:
		m.help = nil
		return m, nil

	// Theme picker messages
	case themepicker.RemoteThemesMsg:
		if m.themePicker != nil {
			m.themePicker.HandleRemote(msg)
		}
		return m, nil

	case themepicker.SpinnerTickMsg:
		if m.themePicker != nil && m.themePicker.Loading() {
			m.themePicker.AdvanceSpinner()
			return m, themepicker.SpinnerTick()
		}
		return m, nil

	case themepicker.PreviewMsg:
		m.theme = msg.Theme
		return m, nil

	case themepicker.SelectMsg:
		m.theme = msg.Theme
		m.cfg.Theme = msg.Key
		m.themePicker = nil
		// Save remote theme file locally so it's available on next startup.
		if msg.Source == theme.SourceRemote && len(msg.RawTOML) > 0 {
			dir := theme.ThemesDir()
			_ = os.MkdirAll(dir, 0o755)
			_ = os.WriteFile(filepath.Join(dir, msg.Key+".toml"), msg.RawTOML, 0o644)
		}
		_ = config.SaveTheme(msg.Key)
		return m, nil

	case themepicker.DismissMsg:
		m.theme = m.themeBeforePick
		m.themePicker = nil
		return m, nil

	// Bookmark messages
	case bookmarks.SelectMsg:
		m.bookmarks = nil
		if srv, remotePath, ok := m.openServerTarget(msg.Path); ok {
			return m.startConnect(srv, remotePath)
		}
		m.activePanel().SetPath(msg.Path)
		m.releaseUnusedConnections()
		return m, m.activePanel().LoadDir()

	case bookmarks.DismissMsg:
		m.bookmarks = nil
		return m, nil

	case servers.ConnectMsg:
		m.servers = nil
		return m.startConnect(msg.Server, msg.Server.Dir)

	case servers.DismissMsg:
		m.servers = nil
		return m, nil

	case connectedMsg:
		return m.handleConnected(msg)

	case stagedReadyMsg:
		cancelled := m.dialog != nil &&
			m.dialog.Kind() == dialog.KindProgress &&
			m.dialog.CancelRequested()
		m.dialog = nil
		if m.opCancel != nil {
			m.opCancel()
			m.opCancel = nil
		}
		if msg.err != nil {
			if errors.Is(msg.err, context.Canceled) {
				return m, nil // the user pressed Esc
			}
			return m.showError("Download failed", msg.err)
		}
		if cancelled {
			return m, discardStagedCmd(msg.staged)
		}
		m.pendingStaged = msg.staged
		m.pendingStagedEdit = msg.edit
		if msg.edit {
			return m, editFileCmd(msg.staged.tmpPath)
		}
		return m, viewFileCmd(msg.staged.tmpPath)

	case stagedDoneMsg:
		if msg.err != nil {
			return m.showError("Upload failed", msg.err)
		}
		return m, m.refreshBothPanels()

	case copypath.DismissMsg:
		m.copyPath = nil
		return m, nil

	// Fuzzy finder internal messages — route to fuzzy model
	case fuzzy.FileWalkMsg:
		if m.fuzzy != nil {
			newFuzzy, cmd := m.fuzzy.Update(msg)
			m.fuzzy = &newFuzzy
			return m, cmd
		}
		return m, nil

	// Fuzzy finder result messages
	case fuzzy.ResultMsg:
		m.fuzzy = nil
		// Navigate to the selected path
		info, err := os.Stat(msg.Path)
		if err != nil {
			return m, nil
		}
		if info.IsDir() {
			m.activePanel().SetPath(msg.Path)
		} else {
			m.activePanel().SetPath(filepath.Dir(msg.Path))
		}
		return m, m.activePanel().LoadDir()

	case fuzzy.DismissMsg:
		m.fuzzy = nil
		return m, nil

	// Command execution messages
	case cmdexec.CommandDoneMsg:
		if m.cmdExec != nil {
			newCE, cmd := m.cmdExec.Update(msg)
			m.cmdExec = &newCE
			return m, cmd
		}
		return m, nil

	case cmdexec.DismissMsg:
		m.cmdExec = nil
		return m, m.refreshBothPanels()

	// File action messages from panel (configurable behavior)
	case panel.OpenFileMsg:
		// The path belongs to the server, so handing it to a local editor
		// would open this machine's file of the same name.
		if !m.activePanel().IsLocal() {
			return m.startStaged(m.cfg.Behavior.EnterAction != "preview")
		}
		return m, m.fileActionCmd(msg.Path, m.cfg.Behavior.EnterAction)

	case panel.ExecuteFileMsg:
		if !m.activePanel().IsLocal() {
			return m, nil // a remote file cannot be run on this machine
		}
		if m.cfg.Behavior.ConfirmExecute == nil || *m.cfg.Behavior.ConfirmExecute {
			m.pendingExecutePath = msg.Path
			d := dialog.NewConfirm("Execute file", fmt.Sprintf("Run %s?", filepath.Base(msg.Path)), tagExecute)
			m.dialog = &d
			return m, nil
		}
		return m, executeFileCmd(msg.Path, m.activePanel().Path(), m.cfg.Behavior.PauseAfterExecute)

	case panel.PreviewFileMsg:
		return m, m.fileActionCmd(msg.Path, m.cfg.Behavior.SpaceAction)

	// File operation results
	case copyDoneMsg:
		m.finishOp()
		if msg.err != nil && !errors.Is(msg.err, actions.ErrCancelled) {
			return m.showError("Copy Error", msg.err)
		}
		return m, m.refreshBothPanels()

	case moveDoneMsg:
		m.finishOp()
		if msg.err != nil && !errors.Is(msg.err, actions.ErrCancelled) {
			return m.showError("Move Error", msg.err)
		}
		return m, m.refreshBothPanels()

	case deleteDoneMsg:
		m.finishOp()
		if msg.err != nil && !errors.Is(msg.err, actions.ErrCancelled) {
			return m.showError("Delete Error", msg.err)
		}
		return m, m.refreshBothPanels()

	case progressMsg:
		if m.dialog != nil && m.dialog.Kind() == dialog.KindProgress {
			m.dialog.SetProgress(
				msg.p.TotalFiles, msg.p.DoneFiles,
				msg.p.TotalBytes, msg.p.DoneBytes,
				msg.p.FileTotalBytes, msg.p.FileDoneBytes,
				msg.p.Current,
			)
		}
		return m, waitForProgress(msg.ch)

	case progressChanClosedMsg:
		return m, nil

	case mkdirDoneMsg:
		if msg.err != nil {
			return m.showError("Mkdir Error", msg.err)
		}
		return m, m.activePanel().LoadDir()

	case renameDoneMsg:
		if msg.err != nil {
			return m.showError("Rename Error", msg.err)
		}
		return m, m.activePanel().LoadDir()

	case externalDoneMsg:
		// An edited remote file goes back; a viewed one is discarded. The slot
		// is cleared here, so a second file staged while this one uploads is
		// not mistaken for it.
		if m.pendingStaged.tmpPath != "" {
			staged := m.pendingStaged
			edit := m.pendingStagedEdit
			m.pendingStaged = stagedFile{}
			m.pendingStagedEdit = false
			if edit {
				return m, uploadStagedCmd(staged)
			}
			return m, discardStagedCmd(staged)
		}
		return m, m.refreshBothPanels()

	case dialog.Result:
		return m.handleDialogResult(msg)

	case ShiftPressMsg:
		m.shiftHeld = true
		return m, nil

	case ShiftReleaseMsg:
		m.shiftHeld = false
		return m, nil

	case tea.MouseMsg:
		// Track shift modifier from mouse events (primary shift detection).
		m.shiftHeld = msg.Shift

		if msg.Action == tea.MouseActionPress && msg.Button == tea.MouseButtonLeft {
			// Click on menu bar (last row)
			if msg.Y == m.height-1 {
				items := m.menuItems
				if m.shiftHeld {
					items = m.shiftMenuItems
				}
				raw := menubar.HandleClick(msg.X, m.width, items)
				if raw != "" {
					return m.dispatchKey(raw)
				}
			}
		}
		return m, nil

	case tea.KeyMsg:
		// Track shift state for menu bar display.
		// Detect shift from F13-F20 (shift+F1..F8) or any "shift+…" key name.
		m.shiftHeld = hasShiftModifier(msg)

		// Help overlay gets priority
		if m.help != nil {
			newHelp, cmd := m.help.Update(msg)
			m.help = &newHelp
			return m, cmd
		}

		// Bookmarks overlay gets priority
		if m.bookmarks != nil {
			newBM, cmd := m.bookmarks.Update(msg)
			m.bookmarks = &newBM
			return m, cmd
		}

		if m.servers != nil {
			newSV, cmd := m.servers.Update(msg)
			m.servers = &newSV
			return m, cmd
		}

		// Copy path overlay gets priority when active
		if m.copyPath != nil {
			newCP, cmd := m.copyPath.Update(msg)
			m.copyPath = &newCP
			return m, cmd
		}

		// Theme picker gets priority when active
		if m.themePicker != nil {
			newTP, cmd := m.themePicker.Update(msg)
			m.themePicker = &newTP
			return m, cmd
		}

		// Fuzzy finder gets priority when active
		if m.fuzzy != nil {
			newFuzzy, cmd := m.fuzzy.Update(msg)
			m.fuzzy = &newFuzzy
			return m, cmd
		}

		// Command execution gets priority when active
		if m.cmdExec != nil {
			newCE, cmd := m.cmdExec.Update(msg)
			m.cmdExec = &newCE
			return m, cmd
		}

		// Dialog gets priority
		if m.dialog != nil {
			// Progress dialog: Esc cancels the in-flight op; other keys no-op.
			// The dialog closes automatically when the op returns.
			if m.dialog.Kind() == dialog.KindProgress {
				if msg.String() == "esc" && !m.dialog.CancelRequested() {
					m.dialog.RequestCancel()
					if m.opCancel != nil {
						m.opCancel()
					}
				}
				return m, nil
			}
			cmd := m.dialog.Update(msg)
			if m.dialog.Done() {
				result := m.dialog.GetResult()
				m.dialog = nil
				return m.handleDialogResult(result)
			}
			return m, cmd
		}

		// Quick view active: Esc closes it; Tab toggles preview focus.
		if m.quickview != nil {
			if msg.String() == "esc" {
				m.closeQuickView()
				return m, nil
			}
			if key.Matches(msg, m.keyMap.TogglePanel) {
				m.quickFocus = !m.quickFocus
				m.quickview.SetFocused(m.quickFocus)
				return m, nil
			}
			if key.Matches(msg, m.keyMap.QuickView) {
				m.closeQuickView()
				return m, nil
			}
			// While the preview is focused it is modal: only scroll keys apply.
			if m.quickFocus {
				m.quickview.Update(msg)
				return m, nil
			}
		}

		// Esc cancels an active quick search before the double-Esc-to-quit
		// handler can swallow it, so the panel discards the typed query.
		if msg.String() == "esc" {
			if searching, _ := m.activePanel().Searching(); searching {
				return m, m.activePanel().Update(msg)
			}
		}

		// Double-Esc to quit
		if msg.String() == "esc" {
			now := time.Now()
			if now.Sub(m.lastEsc) < 400*time.Millisecond {
				return m, m.quit()
			}
			m.lastEsc = now
			return m, nil
		}

		// Global keybindings
		switch {
		case key.Matches(msg, m.keyMap.Quit):
			return m, m.quit()

		case key.Matches(msg, m.keyMap.QuickView):
			return m, m.openQuickView()

		case key.Matches(msg, m.keyMap.TogglePanel):
			m.toggleFocus()
			return m, nil

		case key.Matches(msg, m.keyMap.SwapPanels):
			if m.quickview != nil {
				return m, nil // swap disabled while previewing
			}
			m.leftPanel, m.rightPanel = m.rightPanel, m.leftPanel
			m.swapPanelConns()
			m.recalcLayout()
			return m, nil

		case key.Matches(msg, m.keyMap.SameDir):
			if !m.activePanel().IsLocal() {
				return m, nil // a remote path means nothing to the other panel
			}
			p := m.inactivePanelModel()
			p.SetPath(m.activePanel().Path())
			m.releaseUnusedConnections()
			return m, p.LoadDir()

		case key.Matches(msg, m.keyMap.Copy):
			return m.startCopy()

		case key.Matches(msg, m.keyMap.Move):
			return m.startMove()

		case key.Matches(msg, m.keyMap.Delete):
			return m.startDelete()

		case key.Matches(msg, m.keyMap.Mkdir):
			return m.startMkdir()

		case key.Matches(msg, m.keyMap.Rename):
			return m.startRename()

		case key.Matches(msg, m.keyMap.View):
			return m.startView()

		case key.Matches(msg, m.keyMap.Edit):
			return m.startEdit()

		case key.Matches(msg, m.keyMap.GoTo):
			return m.startGoTo()

		case key.Matches(msg, m.keyMap.FuzzyFind):
			return m.startFuzzyFind()

		case key.Matches(msg, m.keyMap.Servers):
			return m.startServers()
		case key.Matches(msg, m.keyMap.Bookmarks):
			return m.startBookmarks()

		case key.Matches(msg, m.keyMap.Help):
			return m.startHelp()

		case key.Matches(msg, m.keyMap.ThemePicker):
			return m.startThemePicker()

		case key.Matches(msg, m.keyMap.CmdExec):
			return m.startCmdExec()

		case key.Matches(msg, m.keyMap.Terminal):
			// The shell runs here, so it needs a local directory.
			return m, startTerminalCmd(m.activePanel().LocalPath())

		case key.Matches(msg, m.keyMap.ToggleHidden):
			m.leftPanel.ToggleHidden()
			m.rightPanel.ToggleHidden()
			_ = config.SaveShowHidden(m.leftPanel.ShowHidden())
			return m, m.refreshBothPanels()

		case key.Matches(msg, m.keyMap.SelectGroup):
			return m.startSelectGroup()

		case key.Matches(msg, m.keyMap.DeselectGroup):
			return m.startDeselectGroup()

		case key.Matches(msg, m.keyMap.InvertSelection):
			m.activePanel().InvertSelection()
			return m, nil

		case key.Matches(msg, m.keyMap.CopyPath):
			return m.startCopyPath()
		}

		// Delegate to active panel
		cmd := m.activePanel().Update(msg)
		// ".." out of a server leaves its connection unused.
		m.releaseUnusedConnections()
		// If quick view is following the cursor, re-load when the selection moved.
		if m.quickview != nil && !m.quickFocus {
			cmd = tea.Batch(cmd, m.syncQuickView())
		}
		return m, cmd
	}

	return m, nil
}

func (m Model) View() string {
	if m.width == 0 || m.height == 0 {
		return "Loading..."
	}

	leftView := m.leftPanel.View(m.theme)
	rightView := m.rightPanel.View(m.theme)
	if m.quickview != nil {
		qvView := m.quickview.View(m.theme, m.quickFocus)
		if m.focus == FocusLeft {
			rightView = qvView // driver is left, preview replaces right
		} else {
			leftView = qvView
		}
	}
	panels := lipgloss.JoinHorizontal(lipgloss.Top, leftView, rightView)

	items := m.menuItems
	if m.shiftHeld {
		items = m.shiftMenuItems
	}
	fkeyView := menubar.View(m.theme, m.width, items)

	screen := lipgloss.JoinVertical(lipgloss.Left, panels, fkeyView)

	if m.copyPath != nil {
		box := m.copyPath.View(m.theme, m.width, m.height)
		bw, bh := m.copyPath.BoxSize(m.width, m.height)
		screen = overlay.Place(screen, box, m.width, m.height, bw, bh)
	} else if m.help != nil {
		box := m.help.View(m.theme, m.width, m.height)
		bw, bh := m.help.BoxSize(m.width, m.height)
		screen = overlay.Place(screen, box, m.width, m.height, bw, bh)
	} else if m.bookmarks != nil {
		box := m.bookmarks.View(m.theme, m.width, m.height)
		bw, bh := m.bookmarks.BoxSize(m.width, m.height)
		screen = overlay.Place(screen, box, m.width, m.height, bw, bh)
	} else if m.servers != nil {
		box := m.servers.View(m.theme, m.width, m.height)
		bw, bh := m.servers.BoxSize(m.width, m.height)
		screen = overlay.Place(screen, box, m.width, m.height, bw, bh)
	} else if m.themePicker != nil {
		box := m.themePicker.View(m.theme, m.width, m.height)
		bw, bh := m.themePicker.BoxSize(m.width, m.height)
		screen = overlay.Place(screen, box, m.width, m.height, bw, bh)
	} else if m.fuzzy != nil {
		box := m.fuzzy.View(m.theme, m.width, m.height)
		bw, bh := m.fuzzy.BoxSize(m.width, m.height)
		screen = overlay.Place(screen, box, m.width, m.height, bw, bh)
	} else if m.cmdExec != nil {
		box := m.cmdExec.View(m.theme, m.width, m.height)
		bw, bh := m.cmdExec.BoxSize(m.width, m.height)
		screen = overlay.Place(screen, box, m.width, m.height, bw, bh)
	} else if m.dialog != nil {
		box := m.dialog.View(m.theme, m.width, m.height)
		bw, bh := m.dialog.BoxSize(m.width, m.height)
		screen = overlay.Place(screen, box, m.width, m.height, bw, bh)
	}

	return screen
}

// dispatchKey executes the action bound to a raw key string (used for menu bar clicks).
func (m Model) dispatchKey(raw string) (tea.Model, tea.Cmd) {
	cfg := m.cfg.Keys
	switch {
	case contains(cfg.Quit, raw):
		return m, m.quit()
	case contains(cfg.Copy, raw):
		return m.startCopy()
	case contains(cfg.Move, raw):
		return m.startMove()
	case contains(cfg.Delete, raw):
		return m.startDelete()
	case contains(cfg.Mkdir, raw):
		return m.startMkdir()
	case contains(cfg.Rename, raw):
		return m.startRename()
	case contains(cfg.CopyPath, raw):
		return m.startCopyPath()
	case contains(cfg.View, raw):
		return m.startView()
	case contains(cfg.Edit, raw):
		return m.startEdit()
	case contains(cfg.GoTo, raw):
		return m.startGoTo()
	case contains(cfg.Help, raw):
		return m.startHelp()
	case contains(cfg.Servers, raw):
		return m.startServers()
	case contains(cfg.Bookmarks, raw):
		return m.startBookmarks()
	case contains(cfg.FuzzyFind, raw):
		return m.startFuzzyFind()
	case contains(cfg.ThemePicker, raw):
		return m.startThemePicker()
	case contains(cfg.CmdExec, raw):
		return m.startCmdExec()
	case contains(cfg.Terminal, raw):
		return m, startTerminalCmd(m.activePanel().Path())
	}
	return m, nil
}

func isShiftFKey(msg tea.KeyMsg) bool {
	// KeyType uses negative iota: KeyF13 (-46) > KeyF20 (-53).
	return msg.Type <= tea.KeyF13 && msg.Type >= tea.KeyF20
}

func hasShiftModifier(msg tea.KeyMsg) bool {
	return isShiftFKey(msg) || strings.Contains(msg.String(), "shift+")
}

func contains(keys config.StringOrList, val string) bool {
	for _, k := range keys {
		if k == val {
			return true
		}
	}
	return false
}

// fileActionCmd maps a configurable action name to the appropriate command.
func (m *Model) fileActionCmd(path string, action string) tea.Cmd {
	switch action {
	case "edit":
		return editFileCmd(path)
	case "preview":
		if m.cfg.Behavior.ViewMode == "system" {
			return openSystemDefaultCmd(path)
		}
		return viewFileCmd(path)
	default:
		return editFileCmd(path)
	}
}

// --- File operation starters ---

func (m Model) startCopy() (tea.Model, tea.Cmd) {
	sources := m.selectedOrCurrent()
	if len(sources) == 0 {
		return m, nil
	}
	dest := m.inactiveRef()
	m.pendingSources = sources
	m.pendingDest = dest

	if len(sources) == 1 {
		defaultPath := dest.Join(sources[0].Base()).Path
		d := dialog.NewInput("Copy", "Copy to:", defaultPath, tagCopyAs)
		m.dialog = &d
		return m, nil
	}

	msg := fmt.Sprintf("Copy %d item(s) to %s?", len(sources), m.inactivePanel())
	d := dialog.NewConfirm("Copy", msg, tagCopy)
	m.dialog = &d
	return m, nil
}

func (m Model) startMove() (tea.Model, tea.Cmd) {
	sources := m.selectedOrCurrent()
	if len(sources) == 0 {
		return m, nil
	}
	dest := m.inactiveRef()
	m.pendingSources = sources
	m.pendingDest = dest

	if len(sources) == 1 {
		defaultPath := dest.Join(sources[0].Base()).Path
		d := dialog.NewInput("Move", "Move to:", defaultPath, tagMoveAs)
		m.dialog = &d
		return m, nil
	}

	msg := fmt.Sprintf("Move %d item(s) to %s?", len(sources), m.inactivePanel())
	d := dialog.NewConfirm("Move", msg, tagMove)
	m.dialog = &d
	return m, nil
}

func (m Model) startDelete() (tea.Model, tea.Cmd) {
	sources := m.selectedOrCurrent()
	if len(sources) == 0 {
		return m, nil
	}
	m.pendingSources = sources

	msg := fmt.Sprintf("Delete %d item(s)?", len(sources))
	d := dialog.NewConfirm("Delete", msg, tagDelete)
	m.dialog = &d
	return m, nil
}

func (m Model) startMkdir() (tea.Model, tea.Cmd) {
	d := dialog.NewInput("Create Directory", "Directory name:", "", tagMkdir)
	m.dialog = &d
	return m, nil
}

func (m Model) startSelectGroup() (tea.Model, tea.Cmd) {
	d := dialog.NewInput("Select group", "Pattern:", "*", tagSelectGroup)
	m.dialog = &d
	return m, nil
}

func (m Model) startDeselectGroup() (tea.Model, tea.Cmd) {
	d := dialog.NewInput("Deselect group", "Pattern:", "*", tagDeselectGroup)
	m.dialog = &d
	return m, nil
}

func (m Model) startRename() (tea.Model, tea.Cmd) {
	name := m.currentFileName()
	if name == "" || name == ".." {
		return m, nil
	}
	d := dialog.NewInput("Rename", "New name:", name, tagRename)
	m.dialog = &d
	return m, nil
}

func (m Model) startGoTo() (tea.Model, tea.Cmd) {
	d := dialog.NewInputWithBase("Go To", "Path:", m.activePanel().Path(), tagGoTo, m.activePanel().Path())
	m.dialog = &d
	return m, nil
}

// resolveTarget reads Copy/Move dialog text as a ref on the destination
// filesystem. A relative path is taken from the destination directory.
func (m Model) resolveTarget(text string) vfs.FileRef {
	dest := m.pendingDest
	if dest.IsLocal() {
		target := expandHome(text)
		if filepath.IsAbs(target) {
			return dest.WithPath(target)
		}
		return dest.Join(target)
	}
	if strings.HasPrefix(text, "/") {
		return dest.WithPath(path.Clean(text))
	}
	return dest.Join(text)
}

func (m Model) startHelp() (tea.Model, tea.Cmd) {
	h := help.New(m.cfg.Keys, m.version, m.width, m.height)
	m.help = &h
	return m, nil
}

func (m Model) startBookmarks() (tea.Model, tea.Cmd) {
	// A server directory is bookmarked in ssh:// form, so picking the
	// bookmark later reconnects.
	p := m.activePanel()
	bm := bookmarks.New(m.bookmarkStore, p.Location().URLFor(p.Path()), m.width, m.height)
	m.bookmarks = &bm
	return m, nil
}

func (m Model) startServers() (tea.Model, tea.Cmd) {
	sv := servers.New(m.serverStore, m.width, m.height)
	m.servers = &sv
	return m, nil
}

func (m Model) startThemePicker() (tea.Model, tea.Cmd) {
	m.themeBeforePick = m.theme
	available := theme.ListAvailable()
	activeKey := m.cfg.Theme
	if activeKey == "" {
		activeKey = theme.DefaultKey
	}
	tp := themepicker.New(available, activeKey, m.width, m.height)
	m.themePicker = &tp

	// Build set of local keys so remote fetch skips duplicates.
	localKeys := make(map[string]bool)
	for _, a := range available {
		if a.Key != "" {
			localKeys[a.Key] = true
		}
	}
	return m, tea.Batch(themepicker.FetchRemote(localKeys), themepicker.SpinnerTick())
}

func (m Model) startCopyPath() (tea.Model, tea.Cmd) {
	e := m.activePanel().CurrentEntry()
	if e == nil || e.Name() == ".." {
		return m, nil
	}
	p := m.activePanel()
	if p.Location().Kind == vfs.KindArchive {
		return m, nil // archive entries have no addressable path
	}
	cp := copypath.New(p.Location().URLFor(m.currentFilePath()), m.width, m.height)
	m.copyPath = &cp
	return m, nil
}

func (m Model) startCmdExec() (tea.Model, tea.Cmd) {
	if !m.activePanel().IsLocal() {
		return m, nil // the shell runs here, not on the server
	}
	ce := cmdexec.New(m.activePanel().Path(), m.width, m.height)
	m.cmdExec = &ce
	return m, nil
}

func (m Model) startFuzzyFind() (tea.Model, tea.Cmd) {
	if !m.activePanel().IsLocal() {
		// A remote tree needs a bounded, cancellable search of its own.
		return m, nil
	}
	f := fuzzy.New(m.activePanel().Path(), m.width, m.height)
	m.fuzzy = &f
	return m, f.Init()
}

func (m Model) startView() (tea.Model, tea.Cmd) {
	e := m.activePanel().CurrentEntry()
	if e == nil || e.IsDir() || m.activePanel().IsSymlinkToDir(e) {
		return m, nil
	}
	if !m.activePanel().IsLocal() {
		return m.startStaged(false)
	}
	path := m.currentFilePath()
	if m.cfg.Behavior.ViewMode == "system" {
		return m, openSystemDefaultCmd(path)
	}
	return m, viewFileCmd(path)
}

func (m Model) startEdit() (tea.Model, tea.Cmd) {
	e := m.activePanel().CurrentEntry()
	if e == nil || e.IsDir() || m.activePanel().IsSymlinkToDir(e) {
		return m, nil
	}
	if !m.activePanel().IsLocal() {
		return m.startStaged(true)
	}
	return m, editFileCmd(m.currentFilePath())
}

// startStaged downloads the file before an external viewer or editor opens
// it. Archives are excluded: there is no writable side to put an edit back.
func (m Model) startStaged(edit bool) (tea.Model, tea.Cmd) {
	p := m.activePanel()
	if p.Location().Kind != vfs.KindSSH {
		return m, nil
	}

	ctx, cancel := context.WithCancel(context.Background())
	m.opCancel = cancel

	d := dialog.NewProgress("Downloading", tagStage)
	d.SetConnecting(p.CurrentRef().Base())
	m.dialog = &d

	return m, stageRemoteCmd(ctx, p.CurrentRef(), edit)
}

// startProgressOp opens a progress dialog, creates a cancellable context
// and the channel used to stream progress updates back into Update.
func (m *Model) startProgressOp(title string) (context.Context, chan actions.Progress) {
	ctx, cancel := context.WithCancel(context.Background())
	m.opCancel = cancel
	d := dialog.NewProgress(title, "")
	m.dialog = &d
	ch := make(chan actions.Progress, 16)
	return ctx, ch
}

// finishOp closes the progress dialog and clears cancellation state.
func (m *Model) finishOp() {
	if m.opCancel != nil {
		m.opCancel()
		m.opCancel = nil
	}
	m.dialog = nil
}

func (m Model) showError(title string, err error) (tea.Model, tea.Cmd) {
	d := dialog.NewError(title, err.Error())
	m.dialog = &d
	return m, nil
}

func (m Model) handleDialogResult(result dialog.Result) (tea.Model, tea.Cmd) {
	switch result.Tag {
	case tagCopy:
		if result.Confirmed {
			ctx, ch := m.startProgressOp("Copying")
			return m, tea.Batch(
				copyCmd(ctx, ch, m.pendingSources, m.pendingDest),
				waitForProgress(ch),
			)
		}
	case tagCopyAs:
		if result.Confirmed && strings.TrimSpace(result.Text) != "" && len(m.pendingSources) == 1 {
			target := m.resolveTarget(result.Text)
			ctx, ch := m.startProgressOp("Copying")
			return m, tea.Batch(
				copyAsCmd(ctx, ch, m.pendingSources[0], target),
				waitForProgress(ch),
			)
		}
	case tagMove:
		if result.Confirmed {
			ctx, ch := m.startProgressOp("Moving")
			return m, tea.Batch(
				moveCmd(ctx, ch, m.pendingSources, m.pendingDest),
				waitForProgress(ch),
			)
		}
	case tagMoveAs:
		if result.Confirmed && strings.TrimSpace(result.Text) != "" && len(m.pendingSources) == 1 {
			target := m.resolveTarget(result.Text)
			ctx, ch := m.startProgressOp("Moving")
			return m, tea.Batch(
				moveAsCmd(ctx, ch, m.pendingSources[0], target),
				waitForProgress(ch),
			)
		}
	case tagDelete:
		if result.Confirmed {
			ctx, ch := m.startProgressOp("Deleting")
			return m, tea.Batch(
				deleteCmd(ctx, ch, m.pendingSources),
				waitForProgress(ch),
			)
		}
	case tagMkdir:
		if result.Confirmed && result.Text != "" {
			return m, mkdirCmd(m.activePanelMkdir(result.Text))
		}
	case tagRename:
		if result.Confirmed && result.Text != "" {
			return m, renameCmd(m.currentFileRef(), result.Text)
		}
	case tagGoTo:
		if result.Confirmed && result.Text != "" {
			if srv, remotePath, ok := m.openServerTarget(result.Text); ok {
				return m.startConnect(srv, remotePath)
			}
			path := expandHome(result.Text)
			m.activePanel().SetPath(path)
			m.releaseUnusedConnections()
			return m, m.activePanel().LoadDir()
		}
	case tagExecute:
		if result.Confirmed {
			return m, executeFileCmd(m.pendingExecutePath, m.activePanel().Path(), m.cfg.Behavior.PauseAfterExecute)
		}
	case tagTrustHost:
		if result.Confirmed {
			m.pendingCreds.AcceptFingerprint = m.pendingFingerprint
			return m.retryConnect()
		}
	case tagPassphrase:
		if result.Confirmed && result.Text != "" {
			m.pendingCreds.Passphrase = result.Text
			return m.retryConnect()
		}
	case tagSelectGroup:
		if result.Confirmed && result.Text != "" {
			if err := m.activePanel().SelectByPattern(result.Text); err != nil {
				return m.showError("Invalid pattern", err)
			}
		}
	case tagDeselectGroup:
		if result.Confirmed && result.Text != "" {
			if err := m.activePanel().DeselectByPattern(result.Text); err != nil {
				return m.showError("Invalid pattern", err)
			}
		}
	}
	return m, nil
}

// quit disconnects cleanly instead of leaving servers with dropped sockets.
func (m Model) quit() tea.Cmd {
	m.connRegistry.CloseAll()
	return tea.Quit
}

// --- Layout helpers ---

func (m *Model) activePanel() *panel.Model {
	if m.focus == FocusLeft {
		return &m.leftPanel
	}
	return &m.rightPanel
}

// ActivePanelPath is what the shell wrapper cd's into on exit. An archive or
// server has no path this machine can enter, so the local one is reported.
func (m Model) ActivePanelPath() string {
	p := m.leftPanel
	if m.focus == FocusRight {
		p = m.rightPanel
	}
	return p.LocalPath()
}

func (m *Model) inactivePanelModel() *panel.Model {
	if m.focus == FocusLeft {
		return &m.rightPanel
	}
	return &m.leftPanel
}

func (m *Model) toggleFocus() {
	if m.focus == FocusLeft {
		m.focus = FocusRight
		m.leftPanel.SetActive(false)
		m.rightPanel.SetActive(true)
	} else {
		m.focus = FocusLeft
		m.leftPanel.SetActive(true)
		m.rightPanel.SetActive(false)
	}
}

func (m *Model) recalcLayout() {
	panelHeight := m.height - 3 // 2 for panel borders (top+bottom), 1 for fkey bar
	if panelHeight < 1 {
		panelHeight = 1
	}
	panelWidth := m.width / 2
	rightWidth := m.width - panelWidth

	m.leftPanel.SetSize(panelWidth, panelHeight)
	m.rightPanel.SetSize(rightWidth, panelHeight)

	if m.quickview != nil {
		// The preview occupies the inactive pane's slot.
		w := rightWidth
		if m.focus == FocusRight {
			w = panelWidth
		}
		m.quickview.SetSize(w, panelHeight)
	}
}

// openQuickView turns the inactive pane into a live preview of the active
// panel's current selection. Focus stays on the driver (listing) panel.
func (m *Model) openQuickView() tea.Cmd {
	qv := quickview.New()
	m.quickview = &qv
	m.quickFocus = false
	m.recalcLayout()
	return m.syncQuickView()
}

// swapPanelConns realigns the connection map after the panels trade places:
// the map is keyed by side, but the panels moved, and the release sweep would
// otherwise close a connection the other panel is still showing.
func (m *Model) swapPanelConns() {
	left, right := m.panelConns[FocusLeft], m.panelConns[FocusRight]
	delete(m.panelConns, FocusLeft)
	delete(m.panelConns, FocusRight)
	if right != nil {
		m.panelConns[FocusLeft] = right
	}
	if left != nil {
		m.panelConns[FocusRight] = left
	}
}

// closeQuickView restores the inactive pane to its listing.
func (m *Model) closeQuickView() {
	m.quickview = nil
	m.quickFocus = false
}

// syncQuickView reloads the preview to match the driver's current selection,
// but only when the selection actually changed.
func (m *Model) syncQuickView() tea.Cmd {
	p := m.activePanel()
	path := p.CurrentPath()
	if path == m.quickview.Path() {
		return nil
	}
	entry := p.CurrentEntry()
	isDir := entry != nil && (entry.IsDir() || p.IsSymlinkToDir(entry))
	// Archive entries have no readable stream; local and remote both do.
	available := p.Location().Kind != vfs.KindArchive
	// A remote file comes back as a quickview.FileLoadedMsg.
	return m.quickview.SetFile(p.CurrentRef(), p.CurrentInfo(), isDir, available)
}
