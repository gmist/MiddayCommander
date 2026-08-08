package theme

import "github.com/charmbracelet/lipgloss"

// Theme holds all styles used by the application.
type Theme struct {
	// Panel
	PanelBorder       lipgloss.Style
	PanelBorderActive lipgloss.Style
	PanelHeader       lipgloss.Style
	PanelHeaderActive lipgloss.Style

	// File list
	FileNormal            lipgloss.Style
	FileDir               lipgloss.Style
	FileExec              lipgloss.Style
	FileSymlink           lipgloss.Style
	FileCursor            lipgloss.Style
	FileCursorDir         lipgloss.Style
	FileSelected          lipgloss.Style
	FileCursorSelected    lipgloss.Style
	FileCursorDirSelected lipgloss.Style

	// Status bar and menu bar
	StatusBar lipgloss.Style
	MenuBar   lipgloss.Style
	FKeyHint  lipgloss.Style
	FKeyLabel lipgloss.Style

	// Command line
	CmdLine lipgloss.Style
}
