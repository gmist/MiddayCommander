package vfs

import "io/fs"

// stubFS is a comparable FS value, for testing filesystem identity.
type stubFS struct{ tag int }

func (stubFS) Open(string) (fs.File, error)          { return nil, fs.ErrNotExist }
func (stubFS) ReadDir(string) ([]fs.DirEntry, error) { return nil, fs.ErrNotExist }
func (stubFS) Stat(string) (fs.FileInfo, error)      { return nil, fs.ErrNotExist }
