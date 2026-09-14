package bookmarks

import (
	"testing"
	"unicode/utf8"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/kooler/MiddayCommander/internal/bookmark"
)

func bookmarkKey(m Model, key tea.KeyType, runes ...rune) Model {
	var msg tea.KeyMsg
	if runes != nil {
		msg = tea.KeyMsg{Type: tea.KeyRunes, Runes: runes}
	} else {
		msg = tea.KeyMsg{Type: key}
	}
	updated, _ := m.Update(msg)
	return updated
}

func TestFilterUnicodeEditing(t *testing.T) {
	m := New(&bookmark.Store{}, ".", 80, 24)
	m.filtering = true
	m = bookmarkKey(m, tea.KeyRunes, '日', '本', '🎉')
	m = bookmarkKey(m, tea.KeyBackspace)
	if m.filter != "日本" || !utf8.ValidString(m.filter) {
		t.Errorf("filter after backspace = %q, want 日本", m.filter)
	}
	m = bookmarkKey(m, tea.KeySpace)
	if m.filter != "日本 " {
		t.Errorf("filter after space = %q, want 日本 ", m.filter)
	}
}

func TestAddNameUnicodeEditing(t *testing.T) {
	m := New(&bookmark.Store{}, ".", 80, 24)
	m.adding = true
	m = bookmarkKey(m, tea.KeyRunes, '👨', '\u200d', '👩')
	m = bookmarkKey(m, tea.KeyBackspace)
	if m.addName != "" {
		t.Errorf("name after backspace = %q, want empty", m.addName)
	}
}
