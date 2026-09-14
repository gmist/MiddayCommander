package vfs

import (
	"path/filepath"
	"testing"
)

// Path dispatch fails quietly: filepath on a remote path yields backslashes
// on Windows and looks fine everywhere else. These pin the separators.

func TestJoinPath(t *testing.T) {
	sep := string(filepath.Separator)

	tests := []struct {
		name string
		kind Kind
		base string
		add  string
		want string
	}{
		{"local joins with the host separator", KindLocal, sep + "home", "kk", sep + "home" + sep + "kk"},
		{"ssh always joins with a slash", KindSSH, "/var", "log", "/var/log"},
		{"ssh from root", KindSSH, "/", "etc", "/etc"},
		{"archive root is a dot", KindArchive, ".", "src", "src"},
		{"archive nested", KindArchive, "src", "main.go", "src/main.go"},
		{"archive empty base", KindArchive, "", "top", "top"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := JoinPath(tc.kind, tc.base, tc.add); got != tc.want {
				t.Errorf("want %q, got %q", tc.want, got)
			}
		})
	}
}

func TestParentPath(t *testing.T) {
	sep := string(filepath.Separator)

	tests := []struct {
		name string
		kind Kind
		path string
		want string
	}{
		{"local one level up", KindLocal, sep + "home" + sep + "kk", sep + "home"},
		{"local root stays put", KindLocal, sep, sep},
		{"ssh one level up", KindSSH, "/var/log/nginx", "/var/log"},
		{"ssh stops at root", KindSSH, "/var", "/"},
		{"ssh root stays put", KindSSH, "/", "/"},
		{"archive one level up", KindArchive, "src/ui/panel", "src/ui"},
		{"archive stops at root", KindArchive, "src", "."},
		{"archive root stays put", KindArchive, ".", "."},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := ParentPath(tc.kind, tc.path); got != tc.want {
				t.Errorf("want %q, got %q", tc.want, got)
			}
		})
	}
}

func TestIsRootPath(t *testing.T) {
	sep := string(filepath.Separator)

	tests := []struct {
		kind Kind
		path string
		want bool
	}{
		{KindLocal, sep, true},
		{KindLocal, sep + "home", false},
		{KindLocal, `C:\`, true},
		{KindSSH, "/", true},
		{KindSSH, "", true},
		{KindSSH, "/var", false},
		{KindArchive, ".", true},
		{KindArchive, "", true},
		{KindArchive, "src", false},
	}

	for _, tc := range tests {
		if got := IsRootPath(tc.kind, tc.path); got != tc.want {
			t.Errorf("IsRootPath(%v, %q): want %v, got %v", tc.kind, tc.path, tc.want, got)
		}
	}
}

func TestSameFS(t *testing.T) {
	a, b := stubFS{tag: 1}, stubFS{tag: 2}

	// Local paths are absolute, so any local filesystem resolves them.
	l1 := FileRef{FS: a, Path: "/x", Kind: KindLocal}
	l2 := FileRef{FS: b, Path: "/y", Kind: KindLocal}
	if !SameFS(l1, l2) {
		t.Error("want two local refs to count as one filesystem")
	}

	// Remote refs need the same instance: a rename cannot cross hosts.
	r1 := FileRef{FS: a, Path: "/x", Kind: KindSSH}
	r2 := FileRef{FS: b, Path: "/y", Kind: KindSSH}
	if SameFS(r1, r2) {
		t.Error("want refs on different remote filesystems to differ")
	}
	if !SameFS(r1, FileRef{FS: a, Path: "/z", Kind: KindSSH}) {
		t.Error("want refs on one remote filesystem to match")
	}

	// Kinds never mix.
	if SameFS(l1, r1) {
		t.Error("want a local and a remote ref to differ")
	}
}

func TestSamePath(t *testing.T) {
	a := stubFS{tag: 1}

	r1 := FileRef{FS: a, Path: "/var/log", Kind: KindSSH}
	r2 := FileRef{FS: a, Path: "/var/log/", Kind: KindSSH}
	if !SamePath(r1, r2) {
		t.Error("want a trailing slash to compare equal")
	}

	if SamePath(r1, FileRef{FS: a, Path: "/var/lib", Kind: KindSSH}) {
		t.Error("want different paths to compare unequal")
	}
}

func TestLocationDisplay(t *testing.T) {
	tests := []struct {
		name string
		loc  Location
		want string
	}{
		{
			"local shows the bare path",
			Location{Path: "/home/kk", Kind: KindLocal},
			"/home/kk",
		},
		{
			"archive root shows just the label",
			Location{Path: ".", Kind: KindArchive, Label: "src.zip://"},
			"src.zip://",
		},
		{
			"archive nested appends the inner path",
			Location{Path: "cmd/mdc", Kind: KindArchive, Label: "src.zip://"},
			"src.zip://cmd/mdc",
		},
		{
			"server appends the remote path",
			Location{Path: "/var/log", Kind: KindSSH, Label: "ssh://kk@host"},
			"ssh://kk@host/var/log",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.loc.Display(); got != tc.want {
				t.Errorf("want %q, got %q", tc.want, got)
			}
		})
	}
}

func TestLocationNavigation(t *testing.T) {
	loc := Location{FS: stubFS{tag: 1}, Path: "/var", Kind: KindSSH, Label: "ssh://host"}

	if got := loc.Join("log"); got != "/var/log" {
		t.Errorf("Join: want /var/log, got %q", got)
	}
	if got := loc.Child("log").Path; got != "/var/log" {
		t.Errorf("Child: want /var/log, got %q", got)
	}
	if got := loc.Parent(); got != "/" {
		t.Errorf("Parent: want /, got %q", got)
	}
	if loc.IsRoot() {
		t.Error("/var should not be root")
	}
	if !loc.WithPath("/").IsRoot() {
		t.Error("/ should be root")
	}
	if loc.IsLocal() {
		t.Error("an SSH location is not local")
	}
}
