package text

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestPrintableInput(t *testing.T) {
	tests := []struct {
		name string
		msg  tea.KeyMsg
		want string
		ok   bool
	}{
		{"runes", tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("日本🎉")}, "日本🎉", true},
		{"space", tea.KeyMsg{Type: tea.KeySpace}, " ", true},
		{"multi-rune paste", tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("日本🎉")}, "日本🎉", true},
		{"control rune", tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a', '\n'}}, "", false},
		{"control only", tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'\t'}}, "", false},
		{"enter", tea.KeyMsg{Type: tea.KeyEnter}, "", false},
		{"esc", tea.KeyMsg{Type: tea.KeyEscape}, "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := PrintableInput(tt.msg)
			if got != tt.want || ok != tt.ok {
				t.Errorf("PrintableInput() = %q, %v; want %q, %v", got, ok, tt.want, tt.ok)
			}
		})
	}
}
