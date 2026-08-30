package dialog

import (
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/kooler/MiddayCommander/internal/ui/theme"
)

// TestWrapTextNoHang guards the regression where a wide first grapheme at
// width 1 made ansi.Truncate return "" and the loop never advanced.
func TestWrapTextNoHang(t *testing.T) {
	done := make(chan []string, 1)
	go func() { done <- wrapText("Delete 日本語.txt?", 1) }()
	select {
	case lines := <-done:
		if len(lines) == 0 {
			t.Fatal("expected lines, got none")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("wrapText(\"Delete 日本語.txt?\", 1) did not finish in 2s: infinite loop")
	}
}

// TestWrapText: every line is valid UTF-8 and fits the budget, except a
// single wide grapheme (emoji at width 1) which is kept whole on purpose.
func TestWrapText(t *testing.T) {
	tests := []struct {
		name  string
		text  string
		width int
	}{
		{"plain word wrap", "hello world foo", 5},
		{"cjk at width 2", "日本語のテキスト", 2},
		{"emoji first at width 1", "🎉🎉🎉 party", 1},
		{"zwj family", "👨‍👩‍👧‍👦семья", 1},
		{"empty text", "", 3},
		{"clamped width", "abc", 0},
		{"negative width", "abc", -5},
		{"mixed", "größe 日本語 datei", 8},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			budget := tt.width
			if budget < 1 {
				budget = 1
			}
			lines := wrapText(tt.text, tt.width)
			if len(lines) == 0 {
				t.Fatal("no lines produced")
			}
			for i, l := range lines {
				if !utf8.ValidString(l) {
					t.Errorf("line %d is invalid UTF-8: %q", i, l)
				}
				if w := ansi.StringWidth(l); w > budget && w > 2 {
					t.Errorf("line %d width %d exceeds budget %d and is not a single wide grapheme: %q", i, w, budget, l)
				}
			}
		})
	}
}

// TestWrapTextWordBoundaries checks that ASCII words stay intact when spaces
// allow a break within the budget.
func TestWrapTextWordBoundaries(t *testing.T) {
	lines := wrapText("hello world foo", 5)
	want := []string{"hello", "world", "foo"}
	if len(lines) != len(want) {
		t.Fatalf("got %v, want %v", lines, want)
	}
	for i := range want {
		if lines[i] != want[i] {
			t.Errorf("line %d = %q, want %q", i, lines[i], want[i])
		}
	}
}

// TestWrapTextKeepsGraphemes verifies that wide characters are never split:
// every CJK rune must reappear whole across the produced lines.
func TestWrapTextKeepsGraphemes(t *testing.T) {
	lines := wrapText("日本語のテキスト", 2)
	joined := ""
	for _, l := range lines {
		joined += l
	}
	if joined != "日本語のテキスト" {
		t.Errorf("graphemes lost or duplicated: %q", joined)
	}
}

func press(m *Model, t tea.KeyType, runes ...rune) {
	if runes != nil {
		m.updateInput(tea.KeyMsg{Type: tea.KeyRunes, Runes: runes})
		return
	}
	m.updateInput(tea.KeyMsg{Type: t})
}

// TestInputClusterEditing walks the rename dialog through the byte-splitting
// scenario from the review: select 日本語.txt, backspace 5 times.
func TestInputClusterEditing(t *testing.T) {
	m := NewInput("Rename", "New name:", "日本語.txt", "rename")
	for range 5 {
		press(&m, tea.KeyBackspace)
	}
	if !utf8.ValidString(m.input) {
		t.Fatalf("input became invalid UTF-8: %q", m.input)
	}
	if m.input != "日本" {
		t.Errorf("input = %q, want 日本", m.input)
	}
	press(&m, tea.KeyEnter)
	if m.result.Text != "日本" {
		t.Errorf("result = %q, want 日本", m.result.Text)
	}
}

// TestInputCursorPlacement checks insertion around wide characters.
func TestInputCursorPlacement(t *testing.T) {
	m := NewInput("Rename", "New name:", "日本語", "rename")
	press(&m, tea.KeyLeft)
	press(&m, tea.KeyLeft)
	press(&m, tea.KeyRunes, 'X')
	if m.input != "日X本語" {
		t.Errorf("middle insert: %q, want 日X本語", m.input)
	}
	press(&m, tea.KeyHome)
	press(&m, tea.KeyRunes, '=')
	if m.input != "=日X本語" {
		t.Errorf("home insert: %q, want =日X本語", m.input)
	}
	press(&m, tea.KeyEnd)
	press(&m, tea.KeyBackspace)
	if m.input != "=日X本" {
		t.Errorf("end+backspace: %q, want =日X本", m.input)
	}
}

func TestInputUnicodeInsertion(t *testing.T) {
	m := NewInput("Rename", "New name:", "ab", "rename")
	press(&m, tea.KeyHome)
	press(&m, tea.KeyRunes, '日', '本')
	if m.input != "日本ab" {
		t.Errorf("CJK insertion = %q, want 日本ab", m.input)
	}
	if m.inputPos != len("日本") {
		t.Errorf("CJK input position = %d, want %d", m.inputPos, len("日本"))
	}

	press(&m, tea.KeyEnd)
	press(&m, tea.KeyRunes, '🎉')
	if m.input != "日本ab🎉" {
		t.Errorf("emoji insertion = %q, want 日本ab🎉", m.input)
	}
	if m.inputPos != len(m.input) {
		t.Errorf("emoji input position = %d, want %d", m.inputPos, len(m.input))
	}
}

func TestInputSpaceInsertion(t *testing.T) {
	m := NewInput("Rename", "New name:", "ab", "rename")
	press(&m, tea.KeyHome)
	press(&m, tea.KeySpace)
	if m.input != " ab" {
		t.Errorf("space insertion = %q, want %q", m.input, " ab")
	}
}

// TestInputEmojiSequenceIntegrity ensures a ZWJ emoji sequence is removed
// whole by a single backspace, never split mid-cluster.
func TestInputEmojiSequenceIntegrity(t *testing.T) {
	family := "👨‍👩‍👧‍👦"
	m := NewInput("Rename", "New name:", family+"x.txt", "rename")
	for range 5 { // removes t, t, ., x - one per grapheme
		press(&m, tea.KeyBackspace)
	}
	if m.input != family {
		t.Fatalf("after 5 backspaces = %q, want the family only", m.input)
	}
	press(&m, tea.KeyBackspace) // removes the WHOLE family cluster at once
	if m.input != "" {
		t.Errorf("after 6th backspace = %q, want \"\" (family removed whole)", m.input)
	}
	if !utf8.ValidString(m.input) {
		t.Errorf("input became invalid UTF-8")
	}
}
func TestInputViewWidth(t *testing.T) {
	tests := []string{
		"📁 My Folder",
		"❤️fire.txt",
		"👨‍👩‍👧‍👦family.jpg",
		"🇷🇺🇩🇪 flags.png",
		strings.Repeat("🎉", 25) + ".pdf",
	}
	for _, name := range tests {
		m := NewInput("Copy", "Copy to:", name, "copyas")
		m.width = 50
		v := m.View(theme.Theme{}, 120, 40)
		for i, line := range strings.Split(v, "\n") {
			if w := ansi.StringWidth(line); w != 50 {
				t.Errorf("%q: line %d width %d, want 50", name, i, w)
			}
		}
	}
}
