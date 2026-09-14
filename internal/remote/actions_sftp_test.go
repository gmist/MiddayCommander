package remote

import (
	"context"
	"os"
	"path"
	"path/filepath"
	"testing"

	"github.com/kooler/MiddayCommander/internal/actions"
	"github.com/kooler/MiddayCommander/internal/remote/testserver"
	"github.com/kooler/MiddayCommander/internal/vfs"
	"github.com/kooler/MiddayCommander/internal/vfs/local"
)

// End to end: operations driven as the panels drive them, over real SFTP.

func localRef(p string) vfs.FileRef {
	return vfs.FileRef{
		FS:   local.New(string(filepath.Separator)),
		Path: p,
		Kind: vfs.KindLocal,
	}
}

func remoteRef(c *Conn, p string) vfs.FileRef {
	return vfs.FileRef{FS: c.FS(), Path: p, Kind: vfs.KindSSH}
}

func TestCopyLocalTreeToServer(t *testing.T) {
	isolate(t)
	srv := testserver.Start(t)
	_, conn := connect(t, srv)

	src := t.TempDir()
	for rel, content := range map[string]string{
		"project/main.go":      "package main",
		"project/lib/util.go":  "package lib",
		"project/lib/data.bin": "\x00\x01\x02",
	} {
		p := filepath.Join(src, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	err := actions.Copy(context.Background(),
		[]vfs.FileRef{localRef(filepath.Join(src, "project"))},
		remoteRef(conn, srv.Root), nil)
	if err != nil {
		t.Fatalf("copy to server: %v", err)
	}

	for rel, want := range map[string]string{
		"project/main.go":      "package main",
		"project/lib/util.go":  "package lib",
		"project/lib/data.bin": "\x00\x01\x02",
	} {
		got, err := os.ReadFile(srv.Path(filepath.FromSlash(rel)))
		if err != nil {
			t.Errorf("%s: %v", rel, err)
			continue
		}
		if string(got) != want {
			t.Errorf("%s: want %q, got %q", rel, want, string(got))
		}
	}
}

func TestCopyServerTreeToLocal(t *testing.T) {
	isolate(t)
	srv := testserver.Start(t)

	if err := os.MkdirAll(srv.Path("logs/old"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(srv.Path("logs/app.log"), []byte("line one\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(srv.Path("logs/old/app.log.1"), []byte("older\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, conn := connect(t, srv)
	dst := t.TempDir()

	err := actions.Copy(context.Background(),
		[]vfs.FileRef{remoteRef(conn, path.Join(srv.Root, "logs"))},
		localRef(dst), nil)
	if err != nil {
		t.Fatalf("copy from server: %v", err)
	}

	for rel, want := range map[string]string{
		"logs/app.log":       "line one\n",
		"logs/old/app.log.1": "older\n",
	} {
		got, err := os.ReadFile(filepath.Join(dst, filepath.FromSlash(rel)))
		if err != nil {
			t.Errorf("%s: %v", rel, err)
			continue
		}
		if string(got) != want {
			t.Errorf("%s: want %q, got %q", rel, want, string(got))
		}
	}
}

func TestCopyLargeFileToServerReportsProgress(t *testing.T) {
	isolate(t)
	srv := testserver.Start(t)
	_, conn := connect(t, srv)

	src := t.TempDir()
	payload := make([]byte, 3<<20) // larger than one copy chunk
	for i := range payload {
		payload[i] = byte(i % 251)
	}
	big := filepath.Join(src, "big.bin")
	if err := os.WriteFile(big, payload, 0o644); err != nil {
		t.Fatal(err)
	}

	var reports int
	var lastDone int64
	err := actions.Copy(context.Background(),
		[]vfs.FileRef{localRef(big)}, remoteRef(conn, srv.Root),
		func(p actions.Progress) {
			reports++
			lastDone = p.DoneBytes
		})
	if err != nil {
		t.Fatalf("copy: %v", err)
	}

	if reports == 0 {
		t.Error("want progress reports during a multi-chunk transfer")
	}
	if lastDone != int64(len(payload)) {
		t.Errorf("want %d bytes reported done, got %d", len(payload), lastDone)
	}

	got, err := os.ReadFile(srv.Path("big.bin"))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != len(payload) {
		t.Fatalf("want %d bytes on the server, got %d", len(payload), len(got))
	}
	for i := range got {
		if got[i] != payload[i] {
			t.Fatalf("content differs at byte %d", i)
		}
	}
}

func TestCancelledRemoteCopyStops(t *testing.T) {
	isolate(t)
	srv := testserver.Start(t)
	_, conn := connect(t, srv)

	src := t.TempDir()
	big := filepath.Join(src, "big.bin")
	if err := os.WriteFile(big, make([]byte, 8<<20), 0o644); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	err := actions.Copy(ctx, []vfs.FileRef{localRef(big)}, remoteRef(conn, srv.Root),
		func(p actions.Progress) { cancel() })
	if err == nil {
		t.Fatal("want the copy to stop when cancelled")
	}

	// The connection must survive a cancelled transfer.
	if _, err := conn.FS().ReadDir(srv.Root); err != nil {
		t.Errorf("connection unusable after a cancelled copy: %v", err)
	}
}

func TestMoveFromServerToLocalRemovesRemote(t *testing.T) {
	isolate(t)
	srv := testserver.Start(t)
	if err := os.WriteFile(srv.Path("report.txt"), []byte("contents"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, conn := connect(t, srv)
	dst := t.TempDir()

	err := actions.Move(context.Background(),
		[]vfs.FileRef{remoteRef(conn, path.Join(srv.Root, "report.txt"))},
		localRef(dst), nil)
	if err != nil {
		t.Fatalf("move: %v", err)
	}

	got, err := os.ReadFile(filepath.Join(dst, "report.txt"))
	if err != nil || string(got) != "contents" {
		t.Errorf("want the file moved locally, got %q (%v)", string(got), err)
	}
	if _, err := os.Stat(srv.Path("report.txt")); !os.IsNotExist(err) {
		t.Errorf("want the remote original removed, stat gave %v", err)
	}
}

func TestMoveWithinServerUsesRename(t *testing.T) {
	isolate(t)
	srv := testserver.Start(t)
	if err := os.MkdirAll(srv.Path("from"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(srv.Path("to"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(srv.Path("from/f.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, conn := connect(t, srv)

	err := actions.Move(context.Background(),
		[]vfs.FileRef{remoteRef(conn, path.Join(srv.Root, "from/f.txt"))},
		remoteRef(conn, path.Join(srv.Root, "to")), nil)
	if err != nil {
		t.Fatalf("move: %v", err)
	}

	if _, err := os.Stat(srv.Path("to/f.txt")); err != nil {
		t.Errorf("want the file at its new path: %v", err)
	}
	if _, err := os.Stat(srv.Path("from/f.txt")); !os.IsNotExist(err) {
		t.Errorf("want the old path gone, stat gave %v", err)
	}
}

func TestDeleteAndMkdirOnServer(t *testing.T) {
	isolate(t)
	srv := testserver.Start(t)
	if err := os.MkdirAll(srv.Path("doomed/inner"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(srv.Path("doomed/inner/f"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, conn := connect(t, srv)

	if err := actions.Mkdir(remoteRef(conn, path.Join(srv.Root, "created"))); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if info, err := os.Stat(srv.Path("created")); err != nil || !info.IsDir() {
		t.Errorf("want a directory created on the server: %v", err)
	}

	err := actions.Delete(context.Background(),
		[]vfs.FileRef{remoteRef(conn, path.Join(srv.Root, "doomed"))}, nil)
	if err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := os.Stat(srv.Path("doomed")); !os.IsNotExist(err) {
		t.Errorf("want the tree removed from the server, stat gave %v", err)
	}
}

func TestRenameOnServer(t *testing.T) {
	isolate(t)
	srv := testserver.Start(t)
	if err := os.WriteFile(srv.Path("before.txt"), []byte("body"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, conn := connect(t, srv)

	err := actions.Rename(remoteRef(conn, path.Join(srv.Root, "before.txt")), "after.txt")
	if err != nil {
		t.Fatalf("rename: %v", err)
	}

	if _, err := os.Stat(srv.Path("after.txt")); err != nil {
		t.Errorf("want the file renamed on the server: %v", err)
	}
}

func TestCancelledRemoteCopyLeavesNoPartialFile(t *testing.T) {
	isolate(t)
	srv := testserver.Start(t)
	_, conn := connect(t, srv)

	src := t.TempDir()
	big := filepath.Join(src, "big.bin")
	if err := os.WriteFile(big, make([]byte, 12<<20), 0o644); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	err := actions.Copy(ctx, []vfs.FileRef{localRef(big)}, remoteRef(conn, srv.Root),
		func(p actions.Progress) { cancel() })
	if err == nil {
		t.Fatal("want the copy to stop when cancelled")
	}

	entries, err := os.ReadDir(srv.Root)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		t.Errorf("a cancelled upload left %q on the server", e.Name())
	}
}

func TestDeletingRemoteSymlinkKeepsItsTarget(t *testing.T) {
	isolate(t)
	srv := testserver.Start(t)

	if err := os.MkdirAll(srv.Path("target"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(srv.Path("target/keep.txt"), []byte("keep"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(srv.Path("target"), srv.Path("link")); err != nil {
		t.Fatal(err)
	}

	_, conn := connect(t, srv)

	err := actions.Delete(context.Background(),
		[]vfs.FileRef{remoteRef(conn, path.Join(srv.Root, "link"))}, nil)
	if err != nil {
		t.Fatalf("delete the symlink: %v", err)
	}

	if _, err := os.Lstat(srv.Path("link")); !os.IsNotExist(err) {
		t.Errorf("want the symlink itself removed, stat gave %v", err)
	}
	got, err := os.ReadFile(srv.Path("target/keep.txt"))
	if err != nil {
		t.Fatalf("the link's target must survive: %v", err)
	}
	if string(got) != "keep" {
		t.Errorf("want %q, got %q", "keep", string(got))
	}
}
