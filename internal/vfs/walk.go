package vfs

import (
	"io/fs"
	"sort"
)

// WalkDir walks the tree rooted at ref, calling fn for the root and every
// entry beneath it, in name order. It stops at the first error fn returns.
func WalkDir(ref FileRef, fn func(ref FileRef, d fs.DirEntry) error) error {
	info, err := ref.FS.Stat(ref.Path)
	if err != nil {
		return err
	}
	return walk(ref, fs.FileInfoToDirEntry(info), fn)
}

func walk(ref FileRef, d fs.DirEntry, fn func(FileRef, fs.DirEntry) error) error {
	if err := fn(ref, d); err != nil {
		return err
	}
	if !d.IsDir() {
		return nil
	}

	entries, err := ref.FS.ReadDir(ref.Path)
	if err != nil {
		return err
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })

	for _, e := range entries {
		if err := walk(ref.Join(e.Name()), e, fn); err != nil {
			return err
		}
	}
	return nil
}

// CountFilesAndBytes totals the files and bytes beneath ref. An error stops
// the count and returns what was reached so far.
func CountFilesAndBytes(ref FileRef) (int, int64) {
	var files int
	var bytes int64

	_ = WalkDir(ref, func(_ FileRef, d fs.DirEntry) error {
		if d.IsDir() {
			return nil
		}
		files++
		if info, err := d.Info(); err == nil {
			bytes += info.Size()
		}
		return nil
	})

	return files, bytes
}
