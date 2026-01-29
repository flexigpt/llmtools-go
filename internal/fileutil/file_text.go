package fileutil

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"
)

var (
	// ErrNotUTF8Text indicates a file could be read but is not valid UTF‑8.
	ErrNotUTF8Text        = errors.New("file is not valid UTF-8 text")
	ErrFileExceedsMaxSize = errors.New("file exceeds maximum allowed size")
)

// TextFile is a normalized in-memory view of a UTF‑8 text file.
// Lines never include trailing newline characters.
type TextFile struct {
	Path            string
	Perm            fs.FileMode
	Newline         NewlineKind
	HasFinalNewline bool
	Lines           []string
	SizeBytes       int64
	ModTimeUTC      *time.Time
}

// Render converts Lines back into a file string preserving newline style and final newline presence.
func (t *TextFile) Render() string {
	sep := t.Newline.sep()
	if len(t.Lines) == 0 {
		if t.HasFinalNewline {
			return sep
		}
		return ""
	}
	s := strings.Join(t.Lines, sep)
	if t.HasFinalNewline {
		s += sep
	}
	return s
}

// ReadTextFileUTF8 reads a file as UTF‑8 text and returns a normalized TextFile view.
// It preserves newline kind (LF vs CRLF) and whether the file ended with a final newline.
//
// Safety behavior:
//   - Enforces maxBytes if > 0.
//   - Refuses symlink file and symlink parent directories (best effort).
//   - Validates UTF‑8.
func ReadTextFileUTF8(path string, maxBytes int64) (*TextFile, error) {
	p, err := NormalizePath(path)
	if err != nil {
		return nil, err
	}

	st, err := RequireExistingRegularFileNoSymlink(p)
	if err != nil {
		return nil, err
	}
	if maxBytes > 0 && st.Size() > maxBytes {
		return nil, fmt.Errorf("file %q, allowed size %d bytes, error: %w", p, maxBytes, ErrFileExceedsMaxSize)
	}

	// Use existing utility (bounded).
	s, err := ReadFile(p, ReadEncodingText, maxBytes)
	if err != nil {
		return nil, err
	}
	if !utf8.ValidString(s) {
		return nil, ErrNotUTF8Text
	}

	kind := detectNewlineKind(s)
	norm, hasFinal := normalizeNewlines(s, kind)

	var lines []string
	if norm == "" && !hasFinal {
		lines = nil
	} else {
		// Note: if norm == "" and hasFinal == true, we want one empty line: [""].
		lines = strings.Split(norm, "\n")
	}

	mt := st.ModTime().UTC()
	out := &TextFile{
		Path:            p,
		Perm:            st.Mode().Perm(),
		Newline:         kind,
		HasFinalNewline: hasFinal,
		Lines:           lines,
		SizeBytes:       st.Size(),
		ModTimeUTC:      &mt,
	}
	return out, nil
}

// RequireExistingRegularFileNoSymlink validates that path exists, is a regular file,
// and is NOT a symlink (Lstat-based). It also verifies the parent directory contains
// no symlink components (best-effort hardening).
func RequireExistingRegularFileNoSymlink(path string) (fs.FileInfo, error) {
	p, err := NormalizePath(path)
	if err != nil {
		return nil, err
	}

	// Ensure parent path components are not symlinks.
	parent := filepath.Dir(p)
	if parent != "" && parent != "." {
		if err := VerifyDirNoSymlink(parent); err != nil {
			return nil, err
		}
	}

	st, err := os.Lstat(p)
	if err != nil {
		return nil, err
	}
	if (st.Mode() & os.ModeSymlink) != 0 {
		return nil, fmt.Errorf("refusing to operate on symlink file: %s", p)
	}
	if st.IsDir() {
		return nil, fmt.Errorf("expected file but got directory: %s", p)
	}
	if !st.Mode().IsRegular() {
		return nil, fmt.Errorf("expected regular file: %s", p)
	}
	return st, nil
}

func detectNewlineKind(s string) NewlineKind {
	// If we see any CRLF, preserve CRLF; this matches most “Windows file” expectations.
	if strings.Contains(s, "\r\n") {
		return NewlineCRLF
	}
	return NewlineLF
}

func normalizeNewlines(s string, kind NewlineKind) (norm string, hasFinalNewline bool) {
	// Convert to internal '\n' representation for consistent line splitting.
	if kind == NewlineCRLF {
		s = strings.ReplaceAll(s, "\r\n", "\n")
	} else {
		// If file is mostly LF but contains stray CR (rare), normalize them too.
		s = strings.ReplaceAll(s, "\r", "\n")
	}

	hasFinalNewline = strings.HasSuffix(s, "\n")
	if hasFinalNewline {
		s = strings.TrimSuffix(s, "\n")
	}
	return s, hasFinalNewline
}
