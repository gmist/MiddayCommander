package panel

import (
	"io/fs"
	"sort"
	"strings"
)

// SortMode determines how directory entries are sorted.
type SortMode int

const (
	SortByName SortMode = iota
	SortBySize
	SortByTime
	SortByExtension
)

// SortEntries sorts directory entries. Directories always come first.
// isDir classifies an entry as directory-like; callers should use it for both
// real directories and symlinks that resolve to directories so those sort into
// the same group.
func SortEntries(entries []fs.DirEntry, mode SortMode, isDir func(fs.DirEntry) bool) {
	sort.SliceStable(entries, func(i, j int) bool {
		a, b := entries[i], entries[j]

		// ".." always first
		if a.Name() == ".." {
			return true
		}
		if b.Name() == ".." {
			return false
		}

		// Directories before files
		aDir := isDir(a)
		bDir := isDir(b)
		if aDir != bDir {
			return aDir
		}

		switch mode {
		case SortBySize:
			ai, _ := a.Info()
			bi, _ := b.Info()
			if ai != nil && bi != nil {
				return ai.Size() < bi.Size()
			}
			return a.Name() < b.Name()

		case SortByTime:
			ai, _ := a.Info()
			bi, _ := b.Info()
			if ai != nil && bi != nil {
				return ai.ModTime().After(bi.ModTime())
			}
			return a.Name() < b.Name()

		case SortByExtension:
			aExt := extensionOf(a.Name())
			bExt := extensionOf(b.Name())
			if aExt != bExt {
				return aExt < bExt
			}
			return strings.ToLower(a.Name()) < strings.ToLower(b.Name())

		default: // SortByName
			return strings.ToLower(a.Name()) < strings.ToLower(b.Name())
		}
	})
}

func extensionOf(name string) string {
	for i := len(name) - 1; i >= 0; i-- {
		if name[i] == '.' {
			return strings.ToLower(name[i:])
		}
	}
	return ""
}
