package cmdexec

import (
	"testing"
	"unicode/utf8"

	tea "github.com/charmbracelet/bubbletea"
)

func press(m *Model, key tea.KeyType, runes ...rune) {
	var msg tea.KeyMsg
	if runes != nil {
		msg = tea.KeyMsg{Type: tea.KeyRunes, Runes: runes}
	} else {
		msg = tea.KeyMsg{Type: key}
	}
	updated, _ := m.handleKey(msg)
	*m = updated
}

func TestUnicodeInputEditing(t *testing.T) {
	m := New(".", 80, 24)
	press(&m, tea.KeyRunes, '日', '本')
	press(&m, tea.KeyRunes, '🎉')
	if m.input != "日本🎉" {
		t.Fatalf("input = %q, want 日本🎉", m.input)
	}

	press(&m, tea.KeyBackspace)
	if m.input != "日本" || m.inputPos != len(m.input) {
		t.Errorf("after backspace: input = %q, position = %d", m.input, m.inputPos)
	}
	press(&m, tea.KeyLeft)
	press(&m, tea.KeyDelete)
	if m.input != "日" || !utf8.ValidString(m.input) {
		t.Errorf("after delete: input = %q, want 日", m.input)
	}
}

func TestSpaceInput(t *testing.T) {
	m := New(".", 80, 24)
	press(&m, tea.KeySpace)
	if m.input != " " {
		t.Errorf("input = %q, want a space", m.input)
	}
}
