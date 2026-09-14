package dialog

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/kooler/MiddayCommander/internal/ui/theme"
)

func runes(s string) tea.KeyMsg {
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
}

// A paste arrives as one key message of many runes, which is how an ssh://
// address reaches Go To.
func TestInputAcceptsPastedText(t *testing.T) {
	m := NewInput("Go To", "Path:", "", "goto")
	m.Update(runes("ssh://kk@host/var/log"))

	if m.input != "ssh://kk@host/var/log" {
		t.Errorf("want the whole pasted address, got %q", m.input)
	}
	if m.inputPos != len(m.input) {
		t.Errorf("want the cursor at the end, got %d of %d", m.inputPos, len(m.input))
	}
}

func TestInputAcceptsNonASCII(t *testing.T) {
	m := NewInput("Rename", "New name:", "", "rename")
	m.Update(runes("café"))

	if m.input != "café" {
		t.Errorf("want multi-byte characters accepted, got %q", m.input)
	}
}

func TestInputInsertsAtTheCursor(t *testing.T) {
	m := NewInput("Go To", "Path:", "/varlog", "goto")
	for i := 0; i < 3; i++ {
		m.Update(tea.KeyMsg{Type: tea.KeyLeft})
	}
	m.Update(runes("/"))

	if m.input != "/var/log" {
		t.Errorf("want the text inserted at the cursor, got %q", m.input)
	}
}

func TestInputIgnoresNonTextKeys(t *testing.T) {
	m := NewInput("Go To", "Path:", "", "goto")
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("s"), Alt: true})

	if m.input != "" {
		t.Errorf("want alt-modified keys ignored, got %q", m.input)
	}
}

func TestPasswordMasksButKeepsTheValue(t *testing.T) {
	m := NewPassword("Key passphrase", "Passphrase:", "passphrase")
	m.Update(runes("hunter2"))

	if m.input != "hunter2" {
		t.Errorf("want the real value kept, got %q", m.input)
	}

	out := m.View(theme.Theme{}, 80, 24)
	if contains(out, "hunter2") {
		t.Error("the passphrase must not appear on screen")
	}
	if !contains(out, "*******") {
		t.Error("want the passphrase shown as stars")
	}

	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if r := m.GetResult(); !r.Confirmed || r.Text != "hunter2" {
		t.Errorf("want the value returned on Enter, got %+v", r)
	}
}

func contains(haystack, needle string) bool {
	return len(haystack) >= len(needle) && indexOf(haystack, needle) >= 0
}

func indexOf(h, n string) int {
	for i := 0; i+len(n) <= len(h); i++ {
		if h[i:i+len(n)] == n {
			return i
		}
	}
	return -1
}

func TestErrorDialogAdvertisesOneCloseKey(t *testing.T) {
	m := NewError("Connection failed", "ssh: handshake failed")
	out := m.View(theme.Theme{}, 60, 20)

	if !contains(out, "Esc") {
		t.Error("want Esc offered as the way to close")
	}
	if contains(out, "Enter:Close") {
		t.Error("Enter and Esc both closing does not need two hints")
	}
}

func TestErrorDialogStillClosesOnEnter(t *testing.T) {
	for _, k := range []tea.KeyMsg{
		{Type: tea.KeyEsc},
		{Type: tea.KeyEnter},
		{Type: tea.KeyRunes, Runes: []rune("q")},
	} {
		m := NewError("Connection failed", "boom")
		m.Update(k)
		if !m.Done() {
			t.Errorf("%v should close the error dialog", k)
		}
	}
}
