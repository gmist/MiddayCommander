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
		{"control rune", tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a', '\n'}}, "", false},
		{"enter", tea.KeyMsg{Type: tea.KeyEnter}, "", false},
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

func TestGraphemeBoundaries(t *testing.T) {
	input := "a👨‍👩‍👧‍👦日本"
	familyStart := len("a")
	japanStart := familyStart + len("👨‍👩‍👧‍👦")
	lastCJKStart := japanStart + len("日")

	tests := []struct {
		name string
		got  int
		want int
	}{
		{"previous from end", PreviousGraphemeBoundary(input, len(input)), lastCJKStart},
		{"previous from family end", PreviousGraphemeBoundary(input, japanStart), familyStart},
		{"previous from start", PreviousGraphemeBoundary(input, 0), -1},
		{"next from family", NextGraphemeBoundary(input, familyStart), japanStart},
		{"next from Japan", NextGraphemeBoundary(input, japanStart), lastCJKStart},
		{"next from end", NextGraphemeBoundary(input, len(input)), len(input)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.got != tt.want {
				t.Errorf("got %d, want %d", tt.got, tt.want)
			}
		})
	}
}
