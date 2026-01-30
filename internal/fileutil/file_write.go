package fileutil

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"github.com/flexigpt/llmtools-go/internal/toolutil"
)

// WriteFileAtomicBytes writes data to path using an atomic commit strategy:
// temp file in same directory -> fsync -> commit (rename/link) -> best-effort dir sync.
//
// "overwrite=false" guarantees the destination won't be replaced; if it exists, returns an error wrapping os.ErrExist.
// Notes:
//   - On Windows, directory fsync is skipped (it often errors).
//   - If another process holds the destination open on Windows, rename may fail.
func WriteFileAtomicBytes(path string, data []byte, perm fs.FileMode, overwrite bool) error {
	p, err := NormalizePath(path)
	if err != nil {
		return err
	}

	parent := filepath.Dir(p)
	if parent != "" && parent != "." {
		if err := VerifyDirNoSymlink(parent); err != nil {
			return err
		}
	}

	// Validate destination type if it already exists (race-hardened).
	if st, err := os.Lstat(p); err == nil {
		if st.IsDir() {
			return fmt.Errorf("path is a directory, not a file: %s", p)
		}
		if !st.Mode().IsRegular() && (st.Mode()&os.ModeSymlink) == 0 {
			return fmt.Errorf("refusing to write to non-regular file: %s", p)
		}
		if !overwrite {
			return fmt.Errorf("file already exists: %w", os.ErrExist)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}

	tmp, err := os.CreateTemp(parent, ".tmp-llmtools-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()

	cleanup := func(retErr error) error {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
		return retErr
	}

	_ = tmp.Chmod(perm)

	n, err := tmp.Write(data)
	if err != nil {
		return cleanup(err)
	}
	if n != len(data) {
		return cleanup(fmt.Errorf("short write: wrote %d bytes, expected %d", n, len(data)))
	}
	if err := tmp.Sync(); err != nil {
		return cleanup(err)
	}
	if err := tmp.Close(); err != nil {
		return cleanup(err)
	}

	// Commit.
	if !overwrite {
		// Windows: rename won't overwrite, so it's sufficient.
		if runtime.GOOS == "windows" {
			if err := os.Rename(tmpName, p); err != nil {
				// If destination exists (race), return ErrExist-ish.
				if _, stErr := os.Lstat(p); stErr == nil {
					return cleanup(fmt.Errorf("file already exists: %w", os.ErrExist))
				}
				return cleanup(err)
			}
			_ = os.Chmod(p, perm)
			_ = syncDirBestEffort(parent)
			return nil
		}

		// Unix: hardlink is atomic and won't overwrite.
		if err := os.Link(tmpName, p); err == nil {
			_ = os.Remove(tmpName)
			_ = os.Chmod(p, perm)
			_ = syncDirBestEffort(parent)
			return nil
		} else if errors.Is(err, os.ErrExist) {
			return cleanup(fmt.Errorf("file already exists: %w", os.ErrExist))
		} else {
			// Filesystem may not support hardlinks. Preserve overwrite=false semantics:
			// create destination with O_EXCL and COPY contents from temp into it.
			out, perr := os.OpenFile(p, os.O_WRONLY|os.O_CREATE|os.O_EXCL, perm)
			if perr != nil {
				if errors.Is(perr, os.ErrExist) {
					return cleanup(fmt.Errorf("file already exists: %w", os.ErrExist))
				}
				return cleanup(perr)
			}
			defer out.Close()

			in, ierr := os.Open(tmpName)
			if ierr != nil {
				_ = os.Remove(p)
				return cleanup(ierr)
			}
			defer in.Close()

			if _, cerr := io.Copy(out, in); cerr != nil {
				_ = os.Remove(p)
				return cleanup(cerr)
			}
			if serr := out.Sync(); serr != nil {
				_ = os.Remove(p)
				return cleanup(serr)
			}
			if cerr := out.Close(); cerr != nil {
				_ = os.Remove(p)
				return cleanup(cerr)
			}

			_ = os.Remove(tmpName)
			_ = syncDirBestEffort(parent)
			return nil
		}
	}

	// "overwrite=true".
	if runtime.GOOS == "windows" {
		var renameErr error
		for attempt := range 6 {
			renameErr = os.Rename(tmpName, p)
			if renameErr == nil {
				break
			}
			// If dest exists, try remove then retry (AV/indexers may race).
			if _, stErr := os.Lstat(p); stErr == nil {
				_ = os.Remove(p)
			}
			time.Sleep(time.Duration(15*(attempt+1)) * time.Millisecond)
		}
		if renameErr != nil {
			return cleanup(renameErr)
		}
	} else {
		if err := os.Rename(tmpName, p); err != nil {
			return cleanup(err)
		}
	}

	_ = os.Chmod(p, perm)
	_ = syncDirBestEffort(parent)
	return nil
}

func syncDirBestEffort(dir string) error {
	if dir == "" || dir == "." {
		return nil
	}
	if runtime.GOOS == toolutil.GOOSWindows {
		// Directory Sync is not consistently supported on Windows.
		return nil
	}
	f, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer f.Close()
	return f.Sync()
}
