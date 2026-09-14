// Package memfs is an in-memory filesystem for exercising cross-filesystem
// operations without a network or a disk.
package memfs

import (
	"io"
	"io/fs"
	"path"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/kooler/MiddayCommander/internal/vfs"
)

type node struct {
	name    string
	data    []byte
	mode    fs.FileMode
	modTime time.Time
	isDir   bool
}

// FS is an in-memory vfs.WritableFS. Both "/" and "." name the root.
type FS struct {
	mu    sync.RWMutex
	nodes map[string]*node
}

// New returns a filesystem holding only its root.
func New() *FS {
	return &FS{nodes: map[string]*node{
		"": {name: "/", mode: fs.ModeDir | 0o755, isDir: true, modTime: time.Now()},
	}}
}

// norm is the internal key form: no leading slash, root is "".
func norm(p string) string {
	p = strings.TrimPrefix(p, "/")
	p = path.Clean(p)
	if p == "." || p == "/" {
		return ""
	}
	return strings.TrimPrefix(p, "/")
}

func (f *FS) get(p string) (*node, bool) {
	n, ok := f.nodes[norm(p)]
	return n, ok
}

func (f *FS) pathErr(op, name string, err error) error {
	return &fs.PathError{Op: op, Path: name, Err: err}
}

// --- read side ---

func (f *FS) Open(name string) (fs.File, error) {
	f.mu.RLock()
	defer f.mu.RUnlock()

	n, ok := f.get(name)
	if !ok {
		return nil, f.pathErr("open", name, fs.ErrNotExist)
	}
	if n.isDir {
		return &openFile{n: n}, nil
	}
	return &openFile{n: n, r: strings.NewReader(string(n.data))}, nil
}

func (f *FS) ReadDir(name string) ([]fs.DirEntry, error) {
	f.mu.RLock()
	defer f.mu.RUnlock()

	dir, ok := f.get(name)
	if !ok {
		return nil, f.pathErr("readdir", name, fs.ErrNotExist)
	}
	if !dir.isDir {
		return nil, f.pathErr("readdir", name, fs.ErrInvalid)
	}

	prefix := norm(name)
	if prefix != "" {
		prefix += "/"
	}

	var out []fs.DirEntry
	for key, n := range f.nodes {
		if key == "" || !strings.HasPrefix(key, prefix) {
			continue
		}
		rest := strings.TrimPrefix(key, prefix)
		if rest == "" || strings.Contains(rest, "/") {
			continue // not an immediate child
		}
		out = append(out, fs.FileInfoToDirEntry(&fileInfo{n: n}))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name() < out[j].Name() })
	return out, nil
}

func (f *FS) Stat(name string) (fs.FileInfo, error) {
	f.mu.RLock()
	defer f.mu.RUnlock()

	n, ok := f.get(name)
	if !ok {
		return nil, f.pathErr("stat", name, fs.ErrNotExist)
	}
	return &fileInfo{n: n}, nil
}

// Lstat matches Stat: there are no symlinks here.
func (f *FS) Lstat(name string) (fs.FileInfo, error) { return f.Stat(name) }

// --- write side ---

func (f *FS) Create(name string) (vfs.WriteFile, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	key := norm(name)
	if key == "" {
		return nil, f.pathErr("create", name, fs.ErrInvalid)
	}
	if parent := path.Dir(key); parent != "." {
		if p, ok := f.nodes[parent]; !ok || !p.isDir {
			return nil, f.pathErr("create", name, fs.ErrNotExist)
		}
	}

	n := &node{name: path.Base(key), mode: 0o644, modTime: time.Now()}
	f.nodes[key] = n
	return &writeFile{fs: f, n: n}, nil
}

func (f *FS) Mkdir(name string, perm fs.FileMode) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.mkdir(name, perm)
}

func (f *FS) mkdir(name string, perm fs.FileMode) error {
	key := norm(name)
	if key == "" {
		return nil
	}
	if _, exists := f.nodes[key]; exists {
		return f.pathErr("mkdir", name, fs.ErrExist)
	}
	if parent := path.Dir(key); parent != "." {
		if p, ok := f.nodes[parent]; !ok || !p.isDir {
			return f.pathErr("mkdir", name, fs.ErrNotExist)
		}
	}
	f.nodes[key] = &node{
		name:    path.Base(key),
		mode:    fs.ModeDir | perm.Perm(),
		isDir:   true,
		modTime: time.Now(),
	}
	return nil
}

func (f *FS) MkdirAll(name string, perm fs.FileMode) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	key := norm(name)
	if key == "" {
		return nil
	}
	var built string
	for _, part := range strings.Split(key, "/") {
		if built == "" {
			built = part
		} else {
			built += "/" + part
		}
		if n, ok := f.nodes[built]; ok {
			if !n.isDir {
				return f.pathErr("mkdir", name, fs.ErrExist)
			}
			continue
		}
		if err := f.mkdir(built, perm); err != nil {
			return err
		}
	}
	return nil
}

func (f *FS) Remove(name string) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	key := norm(name)
	if _, ok := f.nodes[key]; !ok {
		return f.pathErr("remove", name, fs.ErrNotExist)
	}
	delete(f.nodes, key)
	return nil
}

func (f *FS) RemoveAll(name string) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	key := norm(name)
	if key == "" {
		return f.pathErr("removeall", name, fs.ErrInvalid)
	}
	for k := range f.nodes {
		if k == key || strings.HasPrefix(k, key+"/") {
			delete(f.nodes, k)
		}
	}
	return nil
}

func (f *FS) Rename(oldname, newname string) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	from, to := norm(oldname), norm(newname)
	if _, ok := f.nodes[from]; !ok {
		return f.pathErr("rename", oldname, fs.ErrNotExist)
	}
	if from == "" || to == "" {
		return f.pathErr("rename", oldname, fs.ErrInvalid)
	}

	moved := map[string]*node{}
	for k, n := range f.nodes {
		if k == from {
			moved[to] = n
			n.name = path.Base(to)
		} else if strings.HasPrefix(k, from+"/") {
			moved[to+strings.TrimPrefix(k, from)] = n
		} else {
			continue
		}
		delete(f.nodes, k)
	}
	for k, n := range moved {
		f.nodes[k] = n
	}
	return nil
}

func (f *FS) Chmod(name string, mode fs.FileMode) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	n, ok := f.get(name)
	if !ok {
		return f.pathErr("chmod", name, fs.ErrNotExist)
	}
	if n.isDir {
		n.mode = fs.ModeDir | mode.Perm()
	} else {
		n.mode = mode.Perm()
	}
	return nil
}

// --- file handles ---

type fileInfo struct{ n *node }

func (i *fileInfo) Name() string       { return i.n.name }
func (i *fileInfo) Size() int64        { return int64(len(i.n.data)) }
func (i *fileInfo) Mode() fs.FileMode  { return i.n.mode }
func (i *fileInfo) ModTime() time.Time { return i.n.modTime }
func (i *fileInfo) IsDir() bool        { return i.n.isDir }
func (i *fileInfo) Sys() any           { return nil }

type openFile struct {
	n *node
	r *strings.Reader
}

func (o *openFile) Stat() (fs.FileInfo, error) { return &fileInfo{n: o.n}, nil }
func (o *openFile) Close() error               { return nil }

func (o *openFile) Read(p []byte) (int, error) {
	if o.r == nil {
		return 0, io.EOF
	}
	return o.r.Read(p)
}

type writeFile struct {
	fs *FS
	n  *node
}

func (w *writeFile) Stat() (fs.FileInfo, error) { return &fileInfo{n: w.n}, nil }
func (w *writeFile) Close() error               { return nil }
func (w *writeFile) Read([]byte) (int, error)   { return 0, io.EOF }

func (w *writeFile) Write(p []byte) (int, error) {
	w.fs.mu.Lock()
	defer w.fs.mu.Unlock()
	w.n.data = append(w.n.data, p...)
	w.n.modTime = time.Now()
	return len(p), nil
}

// Verify interface compliance at compile time.
var (
	_ vfs.WritableFS = (*FS)(nil)
	_ vfs.Chmoder    = (*FS)(nil)
	_ vfs.Lstater    = (*FS)(nil)
)
