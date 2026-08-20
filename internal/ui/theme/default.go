package theme

import "github.com/charmbracelet/lipgloss"

// MC classic color palette.
var (
	colorBlue    = lipgloss.Color("4")
	colorCyan    = lipgloss.Color("6")
	colorWhite   = lipgloss.Color("15")
	colorBlack   = lipgloss.Color("0")
	colorGreen   = lipgloss.Color("2")
	colorYellow  = lipgloss.Color("3")
	colorMagenta = lipgloss.Color("5")
)

// Default returns the classic MC-like theme.
func Default() Theme {
	return Theme{
		PanelBorder: lipgloss.NewStyle().
			Foreground(colorCyan).
			Background(colorBlue),
		PanelBorderActive: lipgloss.NewStyle().
			Foreground(colorWhite).
			Background(colorBlue),
		PanelHeader: lipgloss.NewStyle().
			Foreground(colorCyan).
			Background(colorBlue).
			Bold(true),
		PanelHeaderActive: lipgloss.NewStyle().
			Foreground(colorWhite).
			Background(colorBlue).
			Bold(true),

		FileNormal: lipgloss.NewStyle().
			Foreground(colorWhite).
			Background(colorBlue),
		FileDir: lipgloss.NewStyle().
			Foreground(colorWhite).
			Background(colorBlue).
			Bold(true),
		FileExec: lipgloss.NewStyle().
			Foreground(colorGreen).
			Background(colorBlue),
		FileSymlink: lipgloss.NewStyle().
			Foreground(colorMagenta).
			Background(colorBlue),
		FileCursor: lipgloss.NewStyle().
			Foreground(colorBlack).
			Background(colorCyan),
		FileCursorDir: lipgloss.NewStyle().
			Foreground(colorBlack).
			Background(colorCyan).
			Bold(true),
		FileSelected: lipgloss.NewStyle().
			Foreground(colorYellow).
			Background(colorBlue).
			Bold(true),
		FileCursorSelected: lipgloss.NewStyle().
			Foreground(colorYellow).
			Background(colorCyan).
			Bold(true),
		FileCursorDirSelected: lipgloss.NewStyle().
			Foreground(colorYellow).
			Background(colorCyan).
			Bold(true),

		StatusBar: lipgloss.NewStyle().
			Foreground(colorBlack).
			Background(colorCyan),
		MenuBar: lipgloss.NewStyle().
			Foreground(colorBlack).
			Background(colorCyan),
		FKeyHint: lipgloss.NewStyle().
			Foreground(colorWhite).
			Background(colorBlack),
		FKeyLabel: lipgloss.NewStyle().
			Foreground(colorBlack).
			Background(colorCyan),

		CmdLine: lipgloss.NewStyle().
			Foreground(colorWhite).
			Background(colorBlack),
	}
}
