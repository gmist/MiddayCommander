package actions

import (
	"context"
	"fmt"
	"io"
	"io/fs"
	"time"

	"github.com/kooler/MiddayCommander/internal/vfs"
)

// copyChunk is the max bytes copied between progress reports / cancel checks.
const copyChunk = 256 * 1024

// progressInterval throttles per-file byte progress reports.
const progressInterval = 80 * time.Millisecond

// PartSuffix names the scratch file a transfer writes to before the rename.
const PartSuffix = ".mdc-part"

// Copy recursively copies sources into destDir, which may be on a different
// filesystem. Cancellable via ctx.
func Copy(ctx context.Context, sources []vfs.FileRef, destDir vfs.FileRef, progressFn func(Progress)) error {
	totalFiles, totalBytes := countFilesAndBytes(sources)
	p := Progress{
		Op:         OpCopy,
		TotalFiles: totalFiles,
		TotalBytes: totalBytes,
	}

	for _, src := range sources {
		if err := ctx.Err(); err != nil {
			return ErrCancelled
		}

		info, err := statRef(src)
		if err != nil {
			return fmt.Errorf("stat %s: %w", src.Path, err)
		}

		dst := destDir.Join(src.Base())

		if info.IsDir() {
			if err := copyDir(ctx, src, dst, &p, progressFn); err != nil {
				return err
			}
		} else {
			if err := copyFile(ctx, src, dst, info, &p, progressFn); err != nil {
				return err
			}
		}
	}

	return nil
}

// CopyAs copies a single source to destPath (a full path, not a directory).
// Used for single-item copy where the user may have renamed the target.
func CopyAs(ctx context.Context, source, destPath vfs.FileRef, progressFn func(Progress)) error {
	info, err := statRef(source)
	if err != nil {
		return fmt.Errorf("stat %s: %w", source.Path, err)
	}

	if vfs.SamePath(source, destPath) {
		return fmt.Errorf("source and destination are the same: %s", source.Path)
	}

	totalFiles, totalBytes := countFilesAndBytes([]vfs.FileRef{source})
	p := Progress{
		Op:         OpCopy,
		TotalFiles: totalFiles,
		TotalBytes: totalBytes,
	}

	if info.IsDir() {
		return copyDir(ctx, source, destPath, &p, progressFn)
	}
	return copyFile(ctx, source, destPath, info, &p, progressFn)
}

// statRef does not follow a final symlink, so a link is copied as the file it
// points at rather than recursed into as a directory.
func statRef(ref vfs.FileRef) (fs.FileInfo, error) {
	if l, ok := ref.FS.(vfs.Lstater); ok {
		return l.Lstat(ref.Path)
	}
	return ref.FS.Stat(ref.Path)
}

func writableAt(ref vfs.FileRef) (vfs.WritableFS, error) {
	w, ok := ref.Writable()
	if !ok {
		return nil, fmt.Errorf("%s is read-only", ref.Path)
	}
	return w, nil
}

// applyMode is best-effort: backends without Chmod keep their own default.
func applyMode(w vfs.WritableFS, ref vfs.FileRef, mode fs.FileMode) {
	c, ok := w.(vfs.Chmoder)
	if !ok {
		return
	}
	_ = c.Chmod(ref.Path, mode.Perm())
}

func copyFile(ctx context.Context, src, dst vfs.FileRef, info fs.FileInfo, p *Progress, progressFn func(Progress)) error {
	// Finalising removes the destination before the rename, so a source that
	// is also the destination would be deleted by its own copy.
	if vfs.SamePath(src, dst) {
		return fmt.Errorf("source and destination are the same: %s", src.Path)
	}

	p.Current = src.Base()
	p.FileTotalBytes = info.Size()
	p.FileDoneBytes = 0
	if progressFn != nil {
		progressFn(*p)
	}

	w, err := writableAt(dst)
	if err != nil {
		return err
	}

	srcFile, err := src.FS.Open(src.Path)
	if err != nil {
		return fmt.Errorf("open %s: %w", src.Path, err)
	}
	defer srcFile.Close()

	// Writing to a scratch name and renaming on success means a cancelled
	// transfer leaves nothing that could be mistaken for a complete file, and
	// does not destroy a file already at the destination.
	partPath := dst.Path + PartSuffix
	dstFile, err := w.Create(partPath)
	if err != nil {
		return fmt.Errorf("create %s: %w", dst.Path, err)
	}
	closed := false
	completed := false
	defer func() {
		if !closed {
			_ = dstFile.Close()
		}
		if !completed {
			_ = w.Remove(partPath)
		}
	}()

	buf := make([]byte, copyChunk)
	lastReport := time.Now()

	for {
		if err := ctx.Err(); err != nil {
			return ErrCancelled
		}

		n, rerr := srcFile.Read(buf)
		if n > 0 {
			if _, werr := dstFile.Write(buf[:n]); werr != nil {
				return fmt.Errorf("write %s: %w", dst.Path, werr)
			}
			p.FileDoneBytes += int64(n)
			p.DoneBytes += int64(n)
			if progressFn != nil && time.Since(lastReport) >= progressInterval {
				progressFn(*p)
				lastReport = time.Now()
			}
		}
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			return fmt.Errorf("read %s: %w", src.Path, rerr)
		}
	}

	// Over SFTP the write only completes when the handle closes.
	if cerr := dstFile.Close(); cerr != nil {
		return fmt.Errorf("close %s: %w", dst.Path, cerr)
	}
	closed = true

	// SFTP servers reject a rename over an existing path, unlike POSIX.
	_ = w.Remove(dst.Path)
	if err := w.Rename(partPath, dst.Path); err != nil {
		return fmt.Errorf("finalise %s: %w", dst.Path, err)
	}
	completed = true
	applyMode(w, dst, info.Mode())

	p.DoneFiles++
	if progressFn != nil {
		progressFn(*p)
	}

	return nil
}

func copyDir(ctx context.Context, src, dst vfs.FileRef, p *Progress, progressFn func(Progress)) error {
	if err := ctx.Err(); err != nil {
		return ErrCancelled
	}

	srcInfo, err := src.FS.Stat(src.Path)
	if err != nil {
		return err
	}

	w, err := writableAt(dst)
	if err != nil {
		return err
	}
	if err := w.MkdirAll(dst.Path, srcInfo.Mode().Perm()); err != nil {
		return fmt.Errorf("mkdir %s: %w", dst.Path, err)
	}
	applyMode(w, dst, srcInfo.Mode())

	entries, err := src.FS.ReadDir(src.Path)
	if err != nil {
		return fmt.Errorf("readdir %s: %w", src.Path, err)
	}

	for _, entry := range entries {
		srcChild := src.Join(entry.Name())
		dstChild := dst.Join(entry.Name())

		info, err := entry.Info()
		if err != nil {
			return err
		}

		if entry.IsDir() {
			if err := copyDir(ctx, srcChild, dstChild, p, progressFn); err != nil {
				return err
			}
		} else {
			if err := copyFile(ctx, srcChild, dstChild, info, p, progressFn); err != nil {
				return err
			}
		}
	}

	return nil
}

// countFilesAndBytes skips remote sources: a recursive stat over SFTP costs
// seconds before the first byte moves. A zero total makes the dialog show
// files completed instead of a percentage.
func countFilesAndBytes(refs []vfs.FileRef) (int, int64) {
	var files int
	var bytes int64

	for _, ref := range refs {
		if ref.Kind == vfs.KindSSH {
			return 0, 0
		}
	}

	for _, ref := range refs {
		f, b := vfs.CountFilesAndBytes(ref)
		files += f
		bytes += b
	}

	return files, bytes
}
