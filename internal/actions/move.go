package actions

import (
	"context"
	"fmt"

	"github.com/kooler/MiddayCommander/internal/vfs"
)

// Move moves sources into destDir, renaming when both sides share a
// filesystem and falling back to copy-and-delete when they do not.
func Move(ctx context.Context, sources []vfs.FileRef, destDir vfs.FileRef, progressFn func(Progress)) error {
	// Precompute totals so the progress dialog has stable denominators even
	// when some sources are renamed and others fall back to copy.
	totalFiles, totalBytes := countFilesAndBytes(sources)
	agg := Progress{
		Op:         OpMove,
		TotalFiles: totalFiles,
		TotalBytes: totalBytes,
	}

	for _, src := range sources {
		if err := ctx.Err(); err != nil {
			return ErrCancelled
		}

		dst := destDir.Join(src.Base())

		// Without this the rename fails on servers that implement it as a
		// hard link, and the copy-and-delete fallback deletes the source.
		if vfs.SamePath(src, dst) {
			return fmt.Errorf("source and destination are the same: %s", src.Path)
		}

		if tryRename(src, dst) == nil {
			files, bytes := countFilesAndBytes([]vfs.FileRef{dst})
			agg.DoneFiles += files
			agg.DoneBytes += bytes
			agg.Current = src.Base()
			agg.FileTotalBytes = 0
			agg.FileDoneBytes = 0
			if progressFn != nil {
				progressFn(agg)
			}
			continue
		}

		// Forward the copy's progress into the aggregate totals.
		srcFiles, srcBytes := countFilesAndBytes([]vfs.FileRef{src})
		startDoneFiles := agg.DoneFiles
		startDoneBytes := agg.DoneBytes

		forward := func(p Progress) {
			agg.Current = p.Current
			agg.FileTotalBytes = p.FileTotalBytes
			agg.FileDoneBytes = p.FileDoneBytes
			agg.DoneFiles = startDoneFiles + p.DoneFiles
			agg.DoneBytes = startDoneBytes + p.DoneBytes
			if progressFn != nil {
				progressFn(agg)
			}
		}

		if err := Copy(ctx, []vfs.FileRef{src}, destDir, forward); err != nil {
			return fmt.Errorf("move (copy phase) %s: %w", src.Path, err)
		}
		if err := removeRef(src); err != nil {
			return fmt.Errorf("move (delete phase) %s: %w", src.Path, err)
		}
		// Ensure aggregate reflects completion of this source even if Copy
		// finished without a final progress tick at 100%.
		agg.DoneFiles = startDoneFiles + srcFiles
		agg.DoneBytes = startDoneBytes + srcBytes
	}

	return nil
}

// MoveAs moves a single source to destPath (a full path, not a directory).
// Used for single-item move where the user may have renamed the target.
func MoveAs(ctx context.Context, source, destPath vfs.FileRef, progressFn func(Progress)) error {
	if err := ctx.Err(); err != nil {
		return ErrCancelled
	}

	if vfs.SamePath(source, destPath) {
		return fmt.Errorf("source and destination are the same: %s", source.Path)
	}

	totalFiles, totalBytes := countFilesAndBytes([]vfs.FileRef{source})
	agg := Progress{
		Op:         OpMove,
		TotalFiles: totalFiles,
		TotalBytes: totalBytes,
	}

	if tryRename(source, destPath) == nil {
		agg.DoneFiles = totalFiles
		agg.DoneBytes = totalBytes
		agg.Current = destPath.Base()
		if progressFn != nil {
			progressFn(agg)
		}
		return nil
	}

	// Forward copy progress as a move so the dialog keeps showing "Moving".
	forward := func(p Progress) {
		agg.Current = p.Current
		agg.FileTotalBytes = p.FileTotalBytes
		agg.FileDoneBytes = p.FileDoneBytes
		agg.DoneFiles = p.DoneFiles
		agg.DoneBytes = p.DoneBytes
		if progressFn != nil {
			progressFn(agg)
		}
	}
	if err := CopyAs(ctx, source, destPath, forward); err != nil {
		return fmt.Errorf("move (copy phase) %s: %w", source.Path, err)
	}
	if err := removeRef(source); err != nil {
		return fmt.Errorf("move (delete phase) %s: %w", source.Path, err)
	}
	return nil
}

// tryRename fails when the two refs are not on one filesystem, which tells
// the caller to fall back to copy-and-delete.
func tryRename(src, dst vfs.FileRef) error {
	if !vfs.SameFS(src, dst) {
		return fmt.Errorf("different filesystems")
	}
	w, err := writableAt(dst)
	if err != nil {
		return err
	}
	return w.Rename(src.Path, dst.Path)
}

func removeRef(ref vfs.FileRef) error {
	w, err := writableAt(ref)
	if err != nil {
		return err
	}
	return w.RemoveAll(ref.Path)
}

// Rename renames an entry within its own directory.
func Rename(ref vfs.FileRef, newName string) error {
	w, err := writableAt(ref)
	if err != nil {
		return err
	}
	target := ref.Parent().Join(newName)
	return w.Rename(ref.Path, target.Path)
}
