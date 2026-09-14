package actions

import (
	"context"

	"github.com/kooler/MiddayCommander/internal/vfs"
)

// Delete removes each ref and everything beneath it.
func Delete(ctx context.Context, refs []vfs.FileRef, progressFn func(Progress)) error {
	p := Progress{
		Op:         OpDelete,
		TotalFiles: len(refs),
	}

	for _, ref := range refs {
		if err := ctx.Err(); err != nil {
			return ErrCancelled
		}
		p.Current = ref.Base()
		if progressFn != nil {
			progressFn(p)
		}

		if err := removeRef(ref); err != nil {
			return err
		}

		p.DoneFiles++
		if progressFn != nil {
			progressFn(p)
		}
	}

	return nil
}
