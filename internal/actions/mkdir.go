package actions

import (
	"io/fs"

	"github.com/kooler/MiddayCommander/internal/vfs"
)

const dirPerm fs.FileMode = 0o755

func Mkdir(ref vfs.FileRef) error {
	w, err := writableAt(ref)
	if err != nil {
		return err
	}
	if err := w.Mkdir(ref.Path, dirPerm); err != nil {
		return err
	}
	applyMode(w, ref, dirPerm)
	return nil
}
