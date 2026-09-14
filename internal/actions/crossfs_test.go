package actions

import (
	"context"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"testing"

	"github.com/kooler/MiddayCommander/internal/vfs"
	"github.com/kooler/MiddayCommander/internal/vfs/memfs"
)

// Operations whose two sides are different filesystems. memfs stands in for a
// remote backend, so no network is needed.

func memRef(f *memfs.FS, p string) vfs.FileRef {
	return vfs.FileRef{FS: f, Path: p, Kind: vfs.KindSSH}
}

func seedMem(t *testing.T, f *memfs.FS, files map[string]string) {
	t.Helper()
	for name, content := range files {
		if dir := path.Dir(name); dir != "." {
			if err := f.MkdirAll(dir, 0o755); err != nil {
				t.Fatal(err)
			}
		}
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
	}
}

func readMem(t *testing.T, f *memfs.FS, p string) string {
	t.Helper()
	file, err := f.Open(p)
	if err != nil {
		t.Fatalf("open %s: %v", p, err)
	}
	defer file.Close()
	b, err := io.ReadAll(file)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func memTree(t *testing.T, f *memfs.FS, root string) map[string]string {
	t.Helper()
	got := map[string]string{}
	err := vfs.WalkDir(memRef(f, root), func(r vfs.FileRef, d fs.DirEntry) error {
		if r.Path == root {
			return nil
		}
		if d.IsDir() {
			got[r.Path+"/"] = ""
		} else {
			got[r.Path] = readMem(t, f, r.Path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func TestCopyLocalToRemote(t *testing.T) {
	src := t.TempDir()
	tree(t, src, map[string]string{
		"payload/a.txt":     "alpha",
		"payload/sub/b.txt": "bravo",
	})

	remote := memfs.New()
	if err := remote.MkdirAll("dest", 0o755); err != nil {
		t.Fatal(err)
	}

	err := Copy(context.Background(),
		refs(filepath.Join(src, "payload")),
		memRef(remote, "dest"), nil)
	if err != nil {
		t.Fatalf("copy: %v", err)
	}

	want := map[string]string{
		"/dest/payload/":          "",
		"/dest/payload/a.txt":     "alpha",
		"/dest/payload/sub/":      "",
		"/dest/payload/sub/b.txt": "bravo",
	}
	got := memTree(t, remote, "/dest")
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%q: want %q, got %q", k, v, got[k])
		}
	}
	if len(got) != len(want) {
		t.Errorf("want %d entries, got %d: %v", len(want), len(got), got)
	}
}

func TestCopyRemoteToLocal(t *testing.T) {
	remote := memfs.New()
	seedMem(t, remote, map[string]string{
		"src/a.txt":     "alpha",
		"src/sub/b.txt": "bravo",
	})

	dst := t.TempDir()
	if err := Copy(context.Background(), []vfs.FileRef{memRef(remote, "src")}, ref(dst), nil); err != nil {
		t.Fatalf("copy: %v", err)
	}

	wantTree(t, dst, map[string]string{
		"src/":          "",
		"src/a.txt":     "alpha",
		"src/sub/":      "",
		"src/sub/b.txt": "bravo",
	})
}

func TestCopyRemoteSkipsPreCount(t *testing.T) {
	remote := memfs.New()
	seedMem(t, remote, map[string]string{"src/a.txt": "alpha"})

	dst := t.TempDir()
	var final Progress
	err := Copy(context.Background(), []vfs.FileRef{memRef(remote, "src")}, ref(dst),
		func(p Progress) { final = p })
	if err != nil {
		t.Fatalf("copy: %v", err)
	}

	// Counting a remote tree up front costs a full recursive stat, so the
	// totals stay zero and the dialog shows files done.
	if final.TotalFiles != 0 || final.TotalBytes != 0 {
		t.Errorf("want zero pre-count totals for a remote source, got %d files / %d bytes",
			final.TotalFiles, final.TotalBytes)
	}
	if final.DoneFiles != 1 {
		t.Errorf("want DoneFiles=1, got %d", final.DoneFiles)
	}
}

func TestMoveAcrossFilesystemsCopiesThenDeletes(t *testing.T) {
	src := t.TempDir()
	tree(t, src, map[string]string{"box/a.txt": "alpha"})

	remote := memfs.New()
	if err := remote.MkdirAll("dest", 0o755); err != nil {
		t.Fatal(err)
	}

	err := Move(context.Background(),
		refs(filepath.Join(src, "box")),
		memRef(remote, "dest"), nil)
	if err != nil {
		t.Fatalf("move: %v", err)
	}

	if got := readMem(t, remote, "dest/box/a.txt"); got != "alpha" {
		t.Errorf("want %q on the destination, got %q", "alpha", got)
	}
	if _, err := os.Stat(filepath.Join(src, "box")); !os.IsNotExist(err) {
		t.Errorf("want source removed after a cross-filesystem move, stat gave %v", err)
	}
}

func TestMoveWithinRemoteUsesRename(t *testing.T) {
	remote := memfs.New()
	seedMem(t, remote, map[string]string{"a/f.txt": "body"})
	if err := remote.MkdirAll("b", 0o755); err != nil {
		t.Fatal(err)
	}

	err := Move(context.Background(),
		[]vfs.FileRef{memRef(remote, "a")}, memRef(remote, "b"), nil)
	if err != nil {
		t.Fatalf("move: %v", err)
	}

	if got := readMem(t, remote, "b/a/f.txt"); got != "body" {
		t.Errorf("want %q after rename, got %q", "body", got)
	}
	if _, err := remote.Stat("a"); err == nil {
		t.Error("want the original path gone after a rename")
	}
}

func TestDeleteAndMkdirOnRemote(t *testing.T) {
	remote := memfs.New()
	seedMem(t, remote, map[string]string{"doomed/a.txt": "x", "keep.txt": "y"})

	if err := Mkdir(memRef(remote, "fresh")); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if info, err := remote.Stat("fresh"); err != nil || !info.IsDir() {
		t.Fatalf("want a new directory, got %v (%v)", info, err)
	}

	if err := Delete(context.Background(), []vfs.FileRef{memRef(remote, "doomed")}, nil); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := remote.Stat("doomed"); err == nil {
		t.Error("want the deleted tree gone")
	}
	if _, err := remote.Stat("keep.txt"); err != nil {
		t.Errorf("delete should not have touched keep.txt: %v", err)
	}
}

func TestRenameOnRemote(t *testing.T) {
	remote := memfs.New()
	seedMem(t, remote, map[string]string{"dir/before.txt": "body"})

	if err := Rename(memRef(remote, "dir/before.txt"), "after.txt"); err != nil {
		t.Fatalf("rename: %v", err)
	}

	if got := readMem(t, remote, "dir/after.txt"); got != "body" {
		t.Errorf("want %q at the new name, got %q", "body", got)
	}
}

func TestCopyToReadOnlyFilesystemFails(t *testing.T) {
	src := t.TempDir()
	if err := os.WriteFile(filepath.Join(src, "f"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	ro := vfs.FileRef{FS: readOnlyFS{memfs.New()}, Path: "/", Kind: vfs.KindArchive}
	err := Copy(context.Background(), refs(filepath.Join(src, "f")), ro, nil)
	if err == nil {
		t.Fatal("want an error when copying into a read-only filesystem")
	}
}

// readOnlyFS hides the write methods, as the archive backend does.
type readOnlyFS struct{ inner *memfs.FS }

func (r readOnlyFS) Open(name string) (fs.File, error)          { return r.inner.Open(name) }
func (r readOnlyFS) ReadDir(name string) ([]fs.DirEntry, error) { return r.inner.ReadDir(name) }
func (r readOnlyFS) Stat(name string) (fs.FileInfo, error)      { return r.inner.Stat(name) }
