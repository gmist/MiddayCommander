// Package sftpfs adapts an SFTP client to the vfs interfaces.
//
// Paths here are interpreted by the server, not by the machine mdc runs on,
// so this package must not use path/filepath.
package sftpfs

import (
	"errors"
	"io/fs"
	"path"
	"strings"

	"github.com/pkg/sftp"

	"github.com/kooler/MiddayCommander/internal/vfs"
)

// FS is a vfs.WritableFS over an SFTP session.
type FS struct {
	client *sftp.Client
	label  string
}

// New wraps an established client. label is the panel header, by convention
// "ssh://user@host".
func New(client *sftp.Client, label string) *FS {
	return &FS{client: client, label: label}
}

func (f *FS) Label() string { return f.label }

// Client exposes protocol features the vfs interfaces do not cover.
func (f *FS) Client() *sftp.Client { return f.client }

// clean normalizes a path for the server. Empty and "." mean the root, which
// generic navigation code may hand over.
func clean(name string) string {
	if name == "" || name == "." {
		return "/"
	}
	if !strings.HasPrefix(name, "/") {
		return "/" + path.Clean(name)
	}
	return path.Clean(name)
}

// --- read side ---

func (f *FS) Open(name string) (fs.File, error) {
	file, err := f.client.Open(clean(name))
	if err != nil {
		return nil, err
	}
	return file, nil
}

func (f *FS) ReadDir(name string) ([]fs.DirEntry, error) {
	infos, err := f.client.ReadDir(clean(name))
	if err != nil {
		return nil, err
	}
	entries := make([]fs.DirEntry, 0, len(infos))
	for _, info := range infos {
		entries = append(entries, fs.FileInfoToDirEntry(info))
	}
	return entries, nil
}

func (f *FS) Stat(name string) (fs.FileInfo, error) {
	return f.client.Stat(clean(name))
}

func (f *FS) Lstat(name string) (fs.FileInfo, error) {
	return f.client.Lstat(clean(name))
}

func (f *FS) ReadLink(name string) (string, error) {
	return f.client.ReadLink(clean(name))
}

// --- write side ---

func (f *FS) Create(name string) (vfs.WriteFile, error) {
	file, err := f.client.Create(clean(name))
	if err != nil {
		return nil, err
	}
	return file, nil
}

// Mkdir creates a directory. SFTP's mkdir carries no mode, so the permission
// is a separate best-effort step.
func (f *FS) Mkdir(name string, perm fs.FileMode) error {
	p := clean(name)
	if err := f.client.Mkdir(p); err != nil {
		return err
	}
	_ = f.client.Chmod(p, perm.Perm())
	return nil
}

func (f *FS) MkdirAll(name string, perm fs.FileMode) error {
	p := clean(name)
	if err := f.client.MkdirAll(p); err != nil {
		return err
	}
	_ = f.client.Chmod(p, perm.Perm())
	return nil
}

func (f *FS) Remove(name string) error {
	return f.client.Remove(clean(name))
}

// RemoveAll removes name and everything beneath it. The client's own RemoveAll
// decides what to recurse into with a symlink-following stat, so deleting a
// link to a directory would take the target's contents with it. os.RemoveAll
// does not behave that way, and the two backends have to agree.
func (f *FS) RemoveAll(name string) error {
	return f.removeAll(clean(name), nil)
}

// info is the entry's own lstat when the caller already has it, saving a round
// trip per file; nil means look it up.
func (f *FS) removeAll(p string, info fs.FileInfo) error {
	if info == nil {
		fi, err := f.client.Lstat(p)
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				return nil // already gone, like os.RemoveAll
			}
			return err
		}
		info = fi
	}

	// A symlink is unlinked, never followed.
	if !info.IsDir() {
		return f.client.Remove(p)
	}

	entries, err := f.client.ReadDir(p)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if err := f.removeAll(path.Join(p, entry.Name()), entry); err != nil {
			return err
		}
	}
	return f.client.RemoveDirectory(p)
}

func (f *FS) Rename(oldname, newname string) error {
	return f.client.Rename(clean(oldname), clean(newname))
}

func (f *FS) Chmod(name string, mode fs.FileMode) error {
	return f.client.Chmod(clean(name), mode.Perm())
}

// Home is where a panel opens when no path is given.
func (f *FS) Home() string {
	wd, err := f.client.Getwd()
	if err != nil || wd == "" {
		return "/"
	}
	return clean(wd)
}

// Verify interface compliance at compile time.
var (
	_ vfs.WritableFS = (*FS)(nil)
	_ vfs.Chmoder    = (*FS)(nil)
	_ vfs.Lstater    = (*FS)(nil)
	_ vfs.LinkReader = (*FS)(nil)
)
