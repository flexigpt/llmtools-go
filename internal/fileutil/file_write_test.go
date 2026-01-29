package fileutil

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/flexigpt/llmtools-go/internal/toolutil"
)

func TestWriteFileAtomicBytes_BasicAndSymlinkParent(t *testing.T) {
	dir := t.TempDir()

	dst := filepath.Join(dir, "out.txt")

	if err := WriteFileAtomicBytes(dst, []byte("hello\n"), 0o640, true); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	b, err := os.ReadFile(dst)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(b) != "hello\n" {
		t.Fatalf("content=%q want=%q", string(b), "hello\n")
	}

	// Overwrite.
	if err := WriteFileAtomicBytes(dst, []byte("changed"), 0o600, true); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	b, _ = os.ReadFile(dst)
	if string(b) != "changed" {
		t.Fatalf("content=%q want=%q", string(b), "changed")
	}

	if runtime.GOOS != toolutil.GOOSWindows {
		st, err := os.Stat(dst)
		if err != nil {
			t.Fatalf("stat: %v", err)
		}
		// Best effort: we try to set perms on tmp before rename; should usually stick on Unix.
		if st.Mode().Perm() != 0o600 {
			t.Fatalf("perm=%o want=%o", st.Mode().Perm(), 0o600)
		}
	}

	t.Run("symlink parent rejected (if supported)", func(t *testing.T) {
		if runtime.GOOS == toolutil.GOOSWindows {
			t.Skip("symlink tests skipped on Windows")
		}
		realParent := filepath.Join(dir, "realparent")
		if err := os.Mkdir(realParent, 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		linkParent := filepath.Join(dir, "linkparent")
		mustSymlinkOrSkip(t, realParent, linkParent)

		err := WriteFileAtomicBytes(filepath.Join(linkParent, "x.txt"), []byte("nope"), 0o600, true)
		if err == nil {
			t.Fatalf("expected error, got nil")
		}
		if !strings.Contains(err.Error(), "symlink path component") {
			t.Fatalf("unexpected error: %v", err)
		}
	})
}
