package vfs

import (
	"path"
	"path/filepath"
)

// Kind selects path semantics: local paths use the host separator, everything
// else is slash-separated whatever platform mdc runs on.
type Kind int

const (
	KindLocal Kind = iota
	KindArchive
	KindSSH
)

func (k Kind) IsLocal() bool { return k == KindLocal }

func (k Kind) slashed() bool { return k != KindLocal }

func (k Kind) RootPath() string {
	switch k {
	case KindArchive:
		return "."
	case KindSSH:
		return "/"
	default:
		return string(filepath.Separator)
	}
}

func JoinPath(k Kind, base, name string) string {
	if !k.slashed() {
		return filepath.Join(base, name)
	}
	if base == "" || base == "." {
		return name
	}
	return path.Join(base, name)
}

// ParentPath returns the directory containing p, or p when p is already root.
func ParentPath(k Kind, p string) string {
	if IsRootPath(k, p) {
		return p
	}
	if !k.slashed() {
		return filepath.Dir(p)
	}
	parent := path.Dir(p)
	if parent == "." || parent == "/" {
		return k.RootPath()
	}
	return parent
}

func BasePath(k Kind, p string) string {
	if !k.slashed() {
		return filepath.Base(p)
	}
	return path.Base(p)
}

// IsRootPath reports whether p is the top of its filesystem.
func IsRootPath(k Kind, p string) bool {
	switch k {
	case KindArchive:
		return p == "." || p == "" || p == "/"
	case KindSSH:
		return p == "/" || p == ""
	default:
		if p == string(filepath.Separator) {
			return true
		}
		// Windows drive root, e.g. "C:\".
		return len(p) == 3 && p[1] == ':'
	}
}

// FileRef pairs a path with the filesystem it lives on: a path alone cannot
// say which machine that is.
type FileRef struct {
	FS   FS
	Path string
	Kind Kind
}

func (r FileRef) IsLocal() bool { return r.Kind.IsLocal() }

func (r FileRef) Join(name string) FileRef {
	return FileRef{FS: r.FS, Path: JoinPath(r.Kind, r.Path, name), Kind: r.Kind}
}

func (r FileRef) Parent() FileRef {
	return FileRef{FS: r.FS, Path: ParentPath(r.Kind, r.Path), Kind: r.Kind}
}

func (r FileRef) WithPath(p string) FileRef {
	return FileRef{FS: r.FS, Path: p, Kind: r.Kind}
}

func (r FileRef) Base() string { return BasePath(r.Kind, r.Path) }

// Writable reports whether the backend accepts changes; archives do not.
func (r FileRef) Writable() (WritableFS, bool) {
	w, ok := r.FS.(WritableFS)
	return w, ok
}

// SameFS reports whether a rename between two refs is possible. Local refs
// always qualify: local paths are absolute, so any local FS resolves them.
func SameFS(a, b FileRef) bool {
	if a.Kind != b.Kind {
		return false
	}
	if a.Kind.IsLocal() {
		return true
	}
	return a.FS == b.FS
}

func SamePath(a, b FileRef) bool {
	if !SameFS(a, b) {
		return false
	}
	if a.Kind.IsLocal() {
		aa, ea := filepath.Abs(a.Path)
		bb, eb := filepath.Abs(b.Path)
		if ea != nil || eb != nil {
			return false
		}
		return filepath.Clean(aa) == filepath.Clean(bb)
	}
	return path.Clean(a.Path) == path.Clean(b.Path)
}

// Location is a directory a panel can browse.
type Location struct {
	FS    FS
	Path  string
	Kind  Kind
	Label string

	// Origin is the entry in the location above that was opened to get here,
	// so leaving can put the cursor back on it.
	Origin string
}

func NewLocation(fsys FS, p string, kind Kind, label string) Location {
	return Location{FS: fsys, Path: p, Kind: kind, Label: label}
}

func (l Location) Ref() FileRef {
	return FileRef{FS: l.FS, Path: l.Path, Kind: l.Kind}
}

func (l Location) Child(name string) FileRef { return l.Ref().Join(name) }

func (l Location) Join(name string) string { return JoinPath(l.Kind, l.Path, name) }

func (l Location) Parent() string { return ParentPath(l.Kind, l.Path) }

func (l Location) Base() string { return BasePath(l.Kind, l.Path) }

func (l Location) IsRoot() bool { return IsRootPath(l.Kind, l.Path) }

func (l Location) IsLocal() bool { return l.Kind.IsLocal() }

func (l Location) WithPath(p string) Location {
	l.Path = p
	return l
}

// URLFor returns the addressable form of a path: bare locally,
// "ssh://kk@host/var/log" on a server.
func (l Location) URLFor(p string) string {
	if l.IsLocal() || l.Label == "" {
		return p
	}
	return l.Label + p
}

// Display is the panel header. Labels carry their own separator, so an
// archive reads "src.zip://sub" and a server "ssh://kk@host/var/log".
func (l Location) Display() string {
	if l.IsLocal() || l.Label == "" {
		return l.Path
	}
	if l.Kind == KindArchive && IsRootPath(l.Kind, l.Path) {
		return l.Label
	}
	return l.Label + l.Path
}
