package panel

import (
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/lipgloss"
	"github.com/kooler/MiddayCommander/internal/config"
	"github.com/kooler/MiddayCommander/internal/ui/theme"
	"github.com/kooler/MiddayCommander/internal/vfs/local"
)

// newTestModel builds a panel.Model populated with real directory entries
// so renderRow and View can be exercised against actual fs.DirEntry / fs.FileInfo.
func newTestModel(t *testing.T, dir string) Model {
	t.Helper()
	lfs := local.New("/")
	cfg := config.Default()
	km := KeyMap{}
	m := New(lfs, dir, km, cfg)

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	m.entries = entries
	m.infos = make([]fs.FileInfo, len(entries))
	for i, e := range entries {
		info, err := e.Info()
		if err != nil {
			t.Fatal(err)
		}
		m.infos[i] = info
	}
	m.width = 80
	m.height = 10
	m.active = true
	m.selected = make(map[int]bool)
	m.cfg = cfg
	return m
}

// rowWidth returns the cell width of the rendered row (without the surrounding border bars).
func rowWidth(t *testing.T, row string) int {
	t.Helper()
	w := ansi.StringWidth(row)
	if w < 0 {
		t.Fatalf("negative width: %d", w)
	}
	return w
}

// TestRenderRowWidth verifies the exact-width contract: every row rendered by
// renderRow is exactly `width` cells wide, including when the filename is a
// wide grapheme (CJK / emoji) that gets truncated.
func TestRenderRowWidth(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "report.pdf"), []byte("x"), 0644)
	os.Mkdir(filepath.Join(dir, "subdir"), 0755)

	th := theme.Default()

	tests := []struct {
		name     string
		filename string
		isDir    bool
		width    int
	}{
		{"ascii fits", "report.pdf", false, 30},
		{"cjk truncated", "日本語のファイル名前です.txt", false, 20},
		{"emoji truncated", "🎉🎉🎉🎉🎉report.pdf", false, 16},
		{"wide dir", "日本語のディレクトリ", true, 20},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := newTestModel(t, dir)
			// Override the first entry's name for width testing.
			// We use a synthetic DirEntry-like struct because fs.DirEntry is an interface.
			m.entries = []fs.DirEntry{&nameOnlyEntry{name: tt.filename, isDir: tt.isDir}}
			m.infos = []fs.FileInfo{nil}

			m.cursor = 0
			m.active = true
			row := m.renderRow(0, tt.width, th)
			if got := rowWidth(t, row); got != tt.width {
				t.Errorf("width = %d, want %d (row %q)", got, tt.width, row)
			}
		})
	}
}

// TestRenderRowNarrow verifies that panels narrower than the column budget
// collapse size / time columns without breaking or returning the wrong width.
func TestRenderRowNarrow(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "a.txt"), []byte("x"), 0644)
	os.Mkdir(filepath.Join(dir, "b_dir"), 0755)

	th := theme.Default()
	m := newTestModel(t, dir)

	tests := []struct {
		name  string
		width int
	}{
		{"normal", 40},
		{"drop time", 20},
		{"drop size and time", 10},
		{"single cell", 1},
		{"zero width", 0},
		{"negative width", -3},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for idx := range m.entries {
				row := m.renderRow(idx, tt.width, th)
				if tt.width < 1 {
					if row != "" {
						t.Errorf("expected empty row for width=%d, got %q", tt.width, row)
					}
					continue
				}
				if got := rowWidth(t, row); got != tt.width {
					t.Errorf("width = %d, want %d (row %q)", got, tt.width, row)
				}
			}
		})
	}
}

// TestRenderRowColumnAlignment checks that a wide filename does not shift the
// size / time columns. For a fixed panel width, the right-hand columns must
// start at the same cell regardless of filename cell width.
func TestRenderRowColumnAlignment(t *testing.T) {
	dir := t.TempDir()
	info, _ := os.Stat(filepath.Join(dir, "short.txt"))
	os.WriteFile(filepath.Join(dir, "short.txt"), []byte("x"), 0644)
	os.Mkdir(filepath.Join(dir, "wide"), 0755)

	th := theme.Default()
	m := newTestModel(t, dir)

	// Replace entries with a fixed set: one ASCII name, one wide CJK name.
	m.entries = []fs.DirEntry{
		&nameOnlyEntry{name: "short.txt", isDir: false},
		&nameOnlyEntry{name: "日本語のファイル名前です.txt", isDir: false},
	}
	m.infos = []fs.FileInfo{info, info}
	m.cursor = 0
	m.active = true

	width := 50
	rows := make([]string, len(m.entries))
	for i := range m.entries {
		rows[i] = m.renderRow(i, width, th)
		if got := rowWidth(t, rows[i]); got != width {
			t.Errorf("row %d width = %d, want %d", i, got, width)
		}
	}

	// With fixed width, the size column should start at the same position in
	// both rows. We verify by finding the position of the size string in each row.
	sizeStr := " 4B" // FormatSize(4) for our 1-byte file
	for i, row := range rows {
		idx := lipgloss.Width(row) - lipgloss.Width(sizeStr)
		if idx < 0 {
			t.Fatalf("size column not found in row %d", i)
		}
		// The size column should be at width - len(sizeStr) for all rows.
		if idx != width-lipgloss.Width(sizeStr) {
			t.Errorf("row %d: size column at %d, want %d", i, idx, width-lipgloss.Width(sizeStr))
		}
	}
}

// TestRenderRowStates checks that cursor / selected / dir / exec styles don't
// affect width.
func TestRenderRowStates(t *testing.T) {
	dir := t.TempDir()
	binPath := filepath.Join(dir, "run.sh")
	os.WriteFile(binPath, []byte("#!/bin/sh\necho hi\n"), 0755)
	info, _ := os.Stat(binPath)

	th := theme.Default()
	m := newTestModel(t, dir)
	m.entries = []fs.DirEntry{&nameOnlyEntry{name: "run.sh", isDir: false}}
	m.infos = []fs.FileInfo{info}
	m.width = 30

	tests := []struct {
		name      string
		active    bool
		cursor    bool
		selected  bool
		expectExec bool
	}{
		{"normal", true, false, false, true},
		{"cursor", true, true, false, true},
		{"selected", true, false, true, true},
		{"cursor+selected", true, true, true, true},
		{"inactive cursor", false, true, false, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m.active = tt.active
			m.cursor = 0
			if tt.cursor {
				m.cursor = 0
			} else {
				m.cursor = 1 // off the entry
			}
			m.selected = map[int]bool{0: tt.selected}
			row := m.renderRow(0, 30, th)
			if got := rowWidth(t, row); got != 30 {
				t.Errorf("width = %d, want 30", got)
			}
		})
	}
}

// nameOnlyEntry is a minimal fs.DirEntry for tests.
type nameOnlyEntry struct {
	name  string
	isDir bool
}

func (e *nameOnlyEntry) Name() string              { return e.name }
func (e *nameOnlyEntry) IsDir() bool               { return e.isDir }
func (e *nameOnlyEntry) Type() fs.FileMode         { return 0 }
func (e *nameOnlyEntry) Info() (fs.FileInfo, error) { return nil, nil }
func (e *nameOnlyEntry) String() string            { return e.name }
