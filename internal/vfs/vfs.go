package vfs

import (
	"io"
	"io/fs"
)

// FS is the base read-only filesystem interface.
type FS interface {
	fs.FS
	fs.ReadDirFS
	fs.StatFS
}

// WritableFS extends FS with mutation operations.
type WritableFS interface {
	FS
	Create(name string) (WriteFile, error)
	Mkdir(name string, perm fs.FileMode) error
	MkdirAll(name string, perm fs.FileMode) error
	Remove(name string) error
	RemoveAll(name string) error
	Rename(oldname, newname string) error
}

// WriteFile extends fs.File with write capability.
type WriteFile interface {
	fs.File
	io.Writer
}

// Backends differ: an archive is read-only, and SFTP sets a directory's mode
// only after creating it. Optional capabilities are therefore separate
// interfaces that callers type-assert and skip when absent.

type Chmoder interface {
	Chmod(name string, mode fs.FileMode) error
}

// Lstater stats a symlink without following it.
type Lstater interface {
	Lstat(name string) (fs.FileInfo, error)
}

// LinkReader reads a symlink's target.
type LinkReader interface {
	ReadLink(name string) (string, error)
}
