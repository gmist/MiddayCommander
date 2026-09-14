package app

import (
	"context"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/kooler/MiddayCommander/internal/actions"
	"github.com/kooler/MiddayCommander/internal/vfs"
)

// $EDITOR and $PAGER need a real file on disk, so a remote file is staged to
// a temporary copy, uploaded back if it changed, then removed.

// stageChunk is the max bytes moved between cancellation checks.
const stageChunk = 256 * 1024

// stagedFile is a remote file downloaded for an external program.
type stagedFile struct {
	ref     vfs.FileRef
	tmpPath string
	modTime time.Time
	size    int64
	mode    fs.FileMode
}

type stagedReadyMsg struct {
	staged stagedFile
	edit   bool // upload the file back afterwards
	err    error
}

// stagedDoneMsg names the copy it refers to: a second file can be staged
// while the first is still uploading.
type stagedDoneMsg struct {
	tmpPath string
	err     error
}

func stageRemoteCmd(ctx context.Context, ref vfs.FileRef, edit bool) tea.Cmd {
	return func() tea.Msg {
		staged, err := stageRemote(ctx, ref)
		return stagedReadyMsg{staged: staged, edit: edit, err: err}
	}
}

func stageRemote(ctx context.Context, ref vfs.FileRef) (stagedFile, error) {
	// The upload writes to a fresh file, so the original's permissions have to
	// be carried across rather than inherited.
	var mode fs.FileMode
	if info, err := ref.FS.Stat(ref.Path); err == nil {
		mode = info.Mode().Perm()
	}

	src, err := ref.FS.Open(ref.Path)
	if err != nil {
		return stagedFile{}, fmt.Errorf("open %s: %w", ref.Path, err)
	}
	defer src.Close()

	dir, err := os.MkdirTemp("", "mdc-remote-*")
	if err != nil {
		return stagedFile{}, err
	}

	// Keep the name so the editor picks the right syntax mode.
	tmpPath := filepath.Join(dir, ref.Base())
	dst, err := os.Create(tmpPath)
	if err != nil {
		_ = os.RemoveAll(dir)
		return stagedFile{}, err
	}

	if err := copyCancellable(ctx, dst, src); err != nil {
		_ = dst.Close()
		_ = os.RemoveAll(dir)
		if ctx.Err() != nil {
			return stagedFile{}, ctx.Err()
		}
		return stagedFile{}, fmt.Errorf("download %s: %w", ref.Path, err)
	}
	if err := dst.Close(); err != nil {
		_ = os.RemoveAll(dir)
		return stagedFile{}, err
	}

	info, err := os.Stat(tmpPath)
	if err != nil {
		_ = os.RemoveAll(dir)
		return stagedFile{}, err
	}

	return stagedFile{
		ref:     ref,
		tmpPath: tmpPath,
		modTime: info.ModTime(),
		size:    info.Size(),
		mode:    mode,
	}, nil
}

// copyCancellable copies in chunks so Esc is answered mid-transfer.
func copyCancellable(ctx context.Context, dst io.Writer, src io.Reader) error {
	buf := make([]byte, stageChunk)
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		n, rerr := src.Read(buf)
		if n > 0 {
			if _, werr := dst.Write(buf[:n]); werr != nil {
				return werr
			}
		}
		if rerr == io.EOF {
			return nil
		}
		if rerr != nil {
			return rerr
		}
	}
}

// uploadStagedCmd writes the copy back only if it changed. A failed upload
// keeps the temporary file: it holds the only copy of the user's edits.
func uploadStagedCmd(staged stagedFile) tea.Cmd {
	return func() tea.Msg {
		done := stagedDoneMsg{tmpPath: staged.tmpPath}

		info, err := os.Stat(staged.tmpPath)
		if err != nil {
			_ = os.RemoveAll(filepath.Dir(staged.tmpPath))
			done.err = err
			return done
		}
		if info.ModTime().Equal(staged.modTime) && info.Size() == staged.size {
			_ = os.RemoveAll(filepath.Dir(staged.tmpPath))
			return done // untouched
		}

		if err := uploadStaged(staged); err != nil {
			done.err = fmt.Errorf("%w\n\nYour edits are kept at %s", err, staged.tmpPath)
			return done
		}

		_ = os.RemoveAll(filepath.Dir(staged.tmpPath))
		return done
	}
}

func uploadStaged(staged stagedFile) error {
	w, ok := staged.ref.Writable()
	if !ok {
		return fmt.Errorf("%s is read-only", staged.ref.Path)
	}

	src, err := os.Open(staged.tmpPath)
	if err != nil {
		return err
	}
	defer src.Close()

	// Same scratch-and-rename discipline as a copy: a connection that drops
	// mid-upload must not leave the file on the server truncated.
	partPath := staged.ref.Path + actions.PartSuffix
	dst, err := w.Create(partPath)
	if err != nil {
		return fmt.Errorf("upload %s: %w", staged.ref.Path, err)
	}
	if _, err := io.Copy(dst, src); err != nil {
		_ = dst.Close()
		_ = w.Remove(partPath)
		return fmt.Errorf("upload %s: %w", staged.ref.Path, err)
	}
	// Over SFTP the write only completes when the handle closes.
	if err := dst.Close(); err != nil {
		_ = w.Remove(partPath)
		return fmt.Errorf("upload %s: %w", staged.ref.Path, err)
	}

	// SFTP servers reject a rename over an existing path, unlike POSIX.
	_ = w.Remove(staged.ref.Path)
	if err := w.Rename(partPath, staged.ref.Path); err != nil {
		_ = w.Remove(partPath)
		return fmt.Errorf("finalise %s: %w", staged.ref.Path, err)
	}

	if staged.mode != 0 {
		if c, ok := w.(vfs.Chmoder); ok {
			_ = c.Chmod(staged.ref.Path, staged.mode)
		}
	}
	return nil
}

// discardStagedCmd drops a copy that will not be written back.
func discardStagedCmd(staged stagedFile) tea.Cmd {
	return func() tea.Msg {
		_ = os.RemoveAll(filepath.Dir(staged.tmpPath))
		return stagedDoneMsg{tmpPath: staged.tmpPath}
	}
}
