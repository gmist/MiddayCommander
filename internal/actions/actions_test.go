package actions

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"github.com/kooler/MiddayCommander/internal/vfs"
	"github.com/kooler/MiddayCommander/internal/vfs/local"
)

// Local paths are absolute, so one instance serves every ref.
var lfs = local.New(string(filepath.Separator))

func ref(p string) vfs.FileRef {
	return vfs.FileRef{FS: lfs, Path: p, Kind: vfs.KindLocal}
}

func refs(paths ...string) []vfs.FileRef {
	out := make([]vfs.FileRef, 0, len(paths))
	for _, p := range paths {
		out = append(out, ref(p))
	}
	return out
}

// tree builds a layout under root. A key ending in "/" makes a directory,
// anything else a file holding the value.
func tree(t *testing.T, root string, files map[string]string) {
	t.Helper()
	for name, content := range files {
		p := filepath.Join(root, filepath.FromSlash(name))
		if content == "" && name[len(name)-1] == '/' {
			if err := os.MkdirAll(p, 0o755); err != nil {
				t.Fatal(err)
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// collect maps relative paths to contents; directories map to "".
func collect(t *testing.T, root string) map[string]string {
	t.Helper()
	got := make(map[string]string)
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		rel = filepath.ToSlash(rel)
		if d.IsDir() {
			got[rel+"/"] = ""
			return nil
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		got[rel] = string(b)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func wantTree(t *testing.T, root string, want map[string]string) {
	t.Helper()
	got := collect(t, root)
	for k, v := range want {
		g, ok := got[k]
		if !ok {
			t.Errorf("missing %q under %s", k, root)
			continue
		}
		if g != v {
			t.Errorf("%q: want content %q, got %q", k, v, g)
		}
	}
	for k := range got {
		if _, ok := want[k]; !ok {
			t.Errorf("unexpected %q under %s", k, root)
		}
	}
}

func TestCopyNestedTree(t *testing.T) {
	src, dst := t.TempDir(), t.TempDir()
	tree(t, src, map[string]string{
		"top.txt":           "top",
		"sub/one.txt":       "one",
		"sub/deep/two.txt":  "two",
		"sub/deep/empty.go": "",
		"blank/":            "",
	})

	if err := Copy(context.Background(), refs(filepath.Join(src, "sub")), ref(dst), nil); err != nil {
		t.Fatalf("copy: %v", err)
	}

	wantTree(t, dst, map[string]string{
		"sub/":              "",
		"sub/one.txt":       "one",
		"sub/deep/":         "",
		"sub/deep/two.txt":  "two",
		"sub/deep/empty.go": "",
	})
}

func TestCopyMultipleSources(t *testing.T) {
	src, dst := t.TempDir(), t.TempDir()
	tree(t, src, map[string]string{"a.txt": "a", "b.txt": "b", "c/d.txt": "d"})

	sources := refs(
		filepath.Join(src, "a.txt"),
		filepath.Join(src, "b.txt"),
		filepath.Join(src, "c"),
	)
	if err := Copy(context.Background(), sources, ref(dst), nil); err != nil {
		t.Fatalf("copy: %v", err)
	}

	wantTree(t, dst, map[string]string{
		"a.txt":   "a",
		"b.txt":   "b",
		"c/":      "",
		"c/d.txt": "d",
	})
}

func TestCopyPreservesMode(t *testing.T) {
	src, dst := t.TempDir(), t.TempDir()
	p := filepath.Join(src, "script.sh")
	if err := os.WriteFile(p, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	if err := Copy(context.Background(), refs(p), ref(dst), nil); err != nil {
		t.Fatalf("copy: %v", err)
	}

	info, err := os.Stat(filepath.Join(dst, "script.sh"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm()&0o111 == 0 {
		t.Errorf("want executable bits preserved, got mode %v", info.Mode().Perm())
	}
}

func TestCopyTruncatesExistingFile(t *testing.T) {
	src, dst := t.TempDir(), t.TempDir()
	if err := os.WriteFile(filepath.Join(src, "f"), []byte("short"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dst, "f"), []byte("a much longer previous file"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := Copy(context.Background(), refs(filepath.Join(src, "f")), ref(dst), nil); err != nil {
		t.Fatalf("copy: %v", err)
	}

	b, err := os.ReadFile(filepath.Join(dst, "f"))
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != "short" {
		t.Errorf("want %q after overwrite, got %q", "short", string(b))
	}
}

func TestCopyProgressTotals(t *testing.T) {
	src, dst := t.TempDir(), t.TempDir()
	tree(t, src, map[string]string{
		"a":     "12345",      // 5
		"d/b":   "1234567890", // 10
		"d/e/c": "123",        // 3
	})

	var final Progress
	var seen int
	err := Copy(context.Background(), refs(src), ref(dst), func(p Progress) {
		seen++
		final = p
		if p.Op != OpCopy {
			t.Errorf("want OpCopy, got %v", p.Op)
		}
	})
	if err != nil {
		t.Fatalf("copy: %v", err)
	}

	if seen == 0 {
		t.Fatal("progressFn was never called")
	}
	if final.TotalFiles != 3 {
		t.Errorf("want TotalFiles=3, got %d", final.TotalFiles)
	}
	if final.TotalBytes != 18 {
		t.Errorf("want TotalBytes=18, got %d", final.TotalBytes)
	}
	if final.DoneFiles != 3 {
		t.Errorf("want DoneFiles=3, got %d", final.DoneFiles)
	}
	if final.DoneBytes != 18 {
		t.Errorf("want DoneBytes=18, got %d", final.DoneBytes)
	}
}

func TestCopyCancelDuringTransfer(t *testing.T) {
	src, dst := t.TempDir(), t.TempDir()
	if err := os.WriteFile(filepath.Join(src, "big"), make([]byte, 16<<20), 0o644); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Cancelling on the first report exercises the in-loop check, not the
	// early return an already-dead ctx takes.
	err := Copy(ctx, refs(filepath.Join(src, "big")), ref(dst), func(p Progress) {
		cancel()
	})
	if !errors.Is(err, ErrCancelled) {
		t.Fatalf("want ErrCancelled, got %v", err)
	}
}

func TestCopyAsRenamesTarget(t *testing.T) {
	src, dst := t.TempDir(), t.TempDir()
	if err := os.WriteFile(filepath.Join(src, "orig"), []byte("body"), 0o644); err != nil {
		t.Fatal(err)
	}

	target := filepath.Join(dst, "renamed")
	if err := CopyAs(context.Background(), ref(filepath.Join(src, "orig")), ref(target), nil); err != nil {
		t.Fatalf("copyAs: %v", err)
	}

	wantTree(t, dst, map[string]string{"renamed": "body"})
}

func TestCopyAsRejectsSameSourceAndDest(t *testing.T) {
	src := t.TempDir()
	p := filepath.Join(src, "f")
	if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := CopyAs(context.Background(), ref(p), ref(p), nil); err == nil {
		t.Fatal("want error when source and destination are the same")
	}
}

func TestMoveSameFilesystem(t *testing.T) {
	base := t.TempDir()
	src := filepath.Join(base, "src")
	dst := filepath.Join(base, "dst")
	tree(t, src, map[string]string{"d/a.txt": "a"})
	if err := os.MkdirAll(dst, 0o755); err != nil {
		t.Fatal(err)
	}

	if err := Move(context.Background(), refs(filepath.Join(src, "d")), ref(dst), nil); err != nil {
		t.Fatalf("move: %v", err)
	}

	wantTree(t, dst, map[string]string{"d/": "", "d/a.txt": "a"})
	if _, err := os.Stat(filepath.Join(src, "d")); !os.IsNotExist(err) {
		t.Errorf("want source removed, stat gave %v", err)
	}
}

func TestMoveReportsCompletion(t *testing.T) {
	base := t.TempDir()
	src := filepath.Join(base, "src")
	dst := filepath.Join(base, "dst")
	tree(t, src, map[string]string{"a": "12345", "b": "123"})
	if err := os.MkdirAll(dst, 0o755); err != nil {
		t.Fatal(err)
	}

	var final Progress
	err := Move(context.Background(),
		refs(filepath.Join(src, "a"), filepath.Join(src, "b")), ref(dst),
		func(p Progress) { final = p })
	if err != nil {
		t.Fatalf("move: %v", err)
	}

	if final.Op != OpMove {
		t.Errorf("want OpMove, got %v", final.Op)
	}
	if final.TotalFiles != 2 {
		t.Errorf("want TotalFiles=2, got %d", final.TotalFiles)
	}
	if final.DoneFiles != 2 {
		t.Errorf("want DoneFiles=2, got %d", final.DoneFiles)
	}
}

func TestMoveAsRenamesTarget(t *testing.T) {
	base := t.TempDir()
	src := filepath.Join(base, "a")
	dst := filepath.Join(base, "b")
	if err := os.WriteFile(src, []byte("body"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := MoveAs(context.Background(), ref(src), ref(dst), nil); err != nil {
		t.Fatalf("moveAs: %v", err)
	}

	b, err := os.ReadFile(dst)
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != "body" {
		t.Errorf("want %q, got %q", "body", string(b))
	}
	if _, err := os.Stat(src); !os.IsNotExist(err) {
		t.Errorf("want source removed, stat gave %v", err)
	}
}

func TestMoveAsRejectsSameSourceAndDest(t *testing.T) {
	p := filepath.Join(t.TempDir(), "f")
	if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := MoveAs(context.Background(), ref(p), ref(p), nil); err == nil {
		t.Fatal("want error when source and destination are the same")
	}
}

func TestDeleteRemovesTree(t *testing.T) {
	root := t.TempDir()
	tree(t, root, map[string]string{"keep.txt": "k", "gone/a.txt": "a", "gone/b/c.txt": "c"})

	var final Progress
	err := Delete(context.Background(), refs(filepath.Join(root, "gone")), func(p Progress) {
		final = p
	})
	if err != nil {
		t.Fatalf("delete: %v", err)
	}

	wantTree(t, root, map[string]string{"keep.txt": "k"})
	if final.Op != OpDelete {
		t.Errorf("want OpDelete, got %v", final.Op)
	}
	if final.DoneFiles != 1 {
		t.Errorf("want DoneFiles=1, got %d", final.DoneFiles)
	}
}

func TestDeleteCancelled(t *testing.T) {
	root := t.TempDir()
	tree(t, root, map[string]string{"a.txt": "a"})

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if err := Delete(ctx, refs(filepath.Join(root, "a.txt")), nil); !errors.Is(err, ErrCancelled) {
		t.Fatalf("want ErrCancelled, got %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "a.txt")); err != nil {
		t.Errorf("cancelled delete should not have removed the file: %v", err)
	}
}

func TestMkdir(t *testing.T) {
	root := t.TempDir()
	p := filepath.Join(root, "created")

	if err := Mkdir(ref(p)); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	info, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	if !info.IsDir() {
		t.Error("want a directory")
	}
}

func TestRename(t *testing.T) {
	root := t.TempDir()
	old := filepath.Join(root, "before")
	if err := os.WriteFile(old, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := Rename(ref(old), "after"); err != nil {
		t.Fatalf("rename: %v", err)
	}

	wantTree(t, root, map[string]string{"after": "x"})
}

func TestCountFilesAndBytes(t *testing.T) {
	root := t.TempDir()
	tree(t, root, map[string]string{"a": "123", "d/b": "12", "d/e/c": "1"})

	files, bytes := countFilesAndBytes(refs(root))
	if files != 3 {
		t.Errorf("want 3 files, got %d", files)
	}
	if bytes != 6 {
		t.Errorf("want 6 bytes, got %d", bytes)
	}
}

func TestCancelledCopyLeavesNoPartialFile(t *testing.T) {
	src, dst := t.TempDir(), t.TempDir()
	if err := os.WriteFile(filepath.Join(src, "big"), make([]byte, 16<<20), 0o644); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	err := Copy(ctx, refs(filepath.Join(src, "big")), ref(dst), func(p Progress) { cancel() })
	if !errors.Is(err, ErrCancelled) {
		t.Fatalf("want ErrCancelled, got %v", err)
	}

	// No short file at the destination, and no leftover scratch file.
	if _, err := os.Stat(filepath.Join(dst, "big")); !os.IsNotExist(err) {
		t.Errorf("a cancelled copy must not leave a file at the destination, stat gave %v", err)
	}
	wantTree(t, dst, map[string]string{})
}

func TestCancelledCopyDoesNotDestroyExistingFile(t *testing.T) {
	src, dst := t.TempDir(), t.TempDir()
	if err := os.WriteFile(filepath.Join(src, "f"), make([]byte, 16<<20), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dst, "f"), []byte("the original"), 0o644); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	err := Copy(ctx, refs(filepath.Join(src, "f")), ref(dst), func(p Progress) { cancel() })
	if !errors.Is(err, ErrCancelled) {
		t.Fatalf("want ErrCancelled, got %v", err)
	}

	got, err := os.ReadFile(filepath.Join(dst, "f"))
	if err != nil {
		t.Fatalf("the existing file should survive a cancelled copy: %v", err)
	}
	if string(got) != "the original" {
		t.Errorf("want the existing file untouched, got %q", string(got))
	}
}

func TestMoveIntoOwnDirectoryIsRefused(t *testing.T) {
	root := t.TempDir()
	tree(t, root, map[string]string{"a.txt": "one", "b.txt": "two"})

	err := Move(context.Background(),
		refs(filepath.Join(root, "a.txt"), filepath.Join(root, "b.txt")),
		ref(root), nil)
	if err == nil {
		t.Fatal("moving files into the directory they already sit in should be refused")
	}

	// The copy-and-delete fallback would have removed them.
	wantTree(t, root, map[string]string{"a.txt": "one", "b.txt": "two"})
}

func TestCopyIntoOwnDirectoryIsRefused(t *testing.T) {
	root := t.TempDir()
	tree(t, root, map[string]string{"a.txt": "one"})

	err := Copy(context.Background(),
		refs(filepath.Join(root, "a.txt")), ref(root), nil)
	if err == nil {
		t.Fatal("copying a file onto itself should be refused")
	}
	wantTree(t, root, map[string]string{"a.txt": "one"})
}
