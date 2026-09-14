package quickview

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kooler/MiddayCommander/internal/vfs"
	"github.com/kooler/MiddayCommander/internal/vfs/local"
	"github.com/kooler/MiddayCommander/internal/vfs/memfs"
)

func statOf(t *testing.T, path string) os.FileInfo {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat %s: %v", path, err)
	}
	return info
}

func TestSetFileClassification(t *testing.T) {
	dir := t.TempDir()

	textPath := filepath.Join(dir, "hello.txt")
	if err := os.WriteFile(textPath, []byte("line one\nline two\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	binPath := filepath.Join(dir, "data.bin")
	if err := os.WriteFile(binPath, []byte{0x00, 0x01, 0x02, 'a', 'b'}, 0o644); err != nil {
		t.Fatal(err)
	}
	emptyPath := filepath.Join(dir, "empty")
	if err := os.WriteFile(emptyPath, nil, 0o644); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name      string
		path      string
		isDir     bool
		available bool
		want      contentKind
	}{
		{"text", textPath, false, true, kindText},
		{"binary", binPath, false, true, kindBinary},
		{"empty", emptyPath, false, true, kindEmpty},
		{"dir", dir, true, true, kindDir},
		{"unavailable", "some/archive/path", false, false, kindUnavailable},
		{"missing", filepath.Join(dir, "nope"), false, true, kindError},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var m Model
			var info os.FileInfo
			if tc.available && !strings.Contains(tc.path, "nope") {
				if fi, err := os.Stat(tc.path); err == nil {
					info = fi
				}
			}
			m.SetFile(localRef(tc.path), info, tc.isDir, tc.available)
			if m.kind != tc.want {
				t.Errorf("kind = %d, want %d", m.kind, tc.want)
			}
		})
	}
}

func TestTextLinesLoaded(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "f.txt")
	if err := os.WriteFile(p, []byte("a\nb\nc"), 0o644); err != nil {
		t.Fatal(err)
	}
	var m Model
	m.SetFile(localRef(p), statOf(t, p), false, true)
	if got := len(m.lines); got != 3 {
		t.Fatalf("lines = %d, want 3", got)
	}
}

func TestTruncationFlag(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "big.txt")
	big := strings.Repeat("x", maxPreviewBytes+100)
	if err := os.WriteFile(p, []byte(big), 0o644); err != nil {
		t.Fatal(err)
	}
	var m Model
	m.SetFile(localRef(p), statOf(t, p), false, true)
	if !m.truncated {
		t.Error("expected truncated = true for oversized file")
	}
}

// localRef is what the panel hands the preview for a local selection.
func localRef(p string) vfs.FileRef {
	return vfs.FileRef{
		FS:   local.New(string(filepath.Separator)),
		Path: p,
		Kind: vfs.KindLocal,
	}
}

// remoteRef stands in for a file on a server: a non-local kind is what sends
// the read off the event loop.
func remoteRef(t *testing.T, name, content string) vfs.FileRef {
	t.Helper()
	f := memfs.New()
	w, err := f.Create(name)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.WriteString(w.(io.Writer), content); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return vfs.FileRef{FS: f, Path: name, Kind: vfs.KindSSH}
}

func TestRemotePreviewIsReadByACommand(t *testing.T) {
	ref := remoteRef(t, "/notes.txt", "line one\nline two\n")

	var m Model
	m.SetSize(40, 10)

	cmd := m.SetFile(ref, nil, false, true)
	if cmd == nil {
		t.Fatal("a remote read must not happen on the event loop")
	}
	if m.kind != kindLoading {
		t.Errorf("want the preview showing as loading, got kind %v", m.kind)
	}

	msg, ok := cmd().(FileLoadedMsg)
	if !ok {
		t.Fatalf("want a FileLoadedMsg, got %T", msg)
	}
	m.HandleFileLoaded(msg)

	if m.kind != kindText {
		t.Fatalf("want text content after the load, got kind %v", m.kind)
	}
	if len(m.lines) < 2 || m.lines[0] != "line one" || m.lines[1] != "line two" {
		t.Errorf("want the file's lines, got %q", m.lines)
	}
}

func TestRemotePreviewIgnoresAResultTheCursorLeft(t *testing.T) {
	first := remoteRef(t, "/first.txt", "first body\n")
	second := remoteRef(t, "/second.txt", "second body\n")

	var m Model
	m.SetSize(40, 10)

	slow := m.SetFile(first, nil, false, true)
	if slow == nil {
		t.Fatal("want a command for the first file")
	}
	stale, ok := slow().(FileLoadedMsg)
	if !ok {
		t.Fatalf("want a FileLoadedMsg, got %T", stale)
	}

	// The cursor moves on before the first read comes back.
	quick := m.SetFile(second, nil, false, true)
	m.HandleFileLoaded(quick().(FileLoadedMsg))
	m.HandleFileLoaded(stale)

	if m.path != "/second.txt" {
		t.Fatalf("want the preview on the second file, got %q", m.path)
	}
	if len(m.lines) == 0 || m.lines[0] != "second body" {
		t.Errorf("a late read for the previous file must be dropped, got %q", m.lines)
	}
}
