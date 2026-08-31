package text

import (
	tea "github.com/charmbracelet/bubbletea"
)

// PrintableInput returns the printable text represented by a key message.
// Bubble Tea reports a regular space as KeySpace, while other text uses
// KeyRunes and may contain more than one rune when pasted or entered via IME.
func PrintableInput(msg tea.KeyMsg) (string, bool) {
	if msg.Type != tea.KeyRunes && msg.Type != tea.KeySpace {
		return "", false
	}
	s := string(msg.Runes)
	if msg.Type == tea.KeySpace {
		s = " "
	}
	if s == "" {
		return "", false
	}
	for _, r := range s {
		if r < 32 {
			return "", false
		}
	}
	return s, true
}
