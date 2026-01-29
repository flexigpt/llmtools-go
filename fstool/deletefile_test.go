package fstool

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/flexigpt/llmtools-go/internal/toolutil"
)

func TestDeleteFile(t *testing.T) {
	t.Run("context_canceled", func(t *testing.T) {
		tmp := t.TempDir()
		p := filepath.Join(tmp, "a.txt")
		_ = os.WriteFile(p, []byte("x"), 0o600)

		ctx, cancel := context.WithCancel(t.Context())
		cancel()

		_, err := DeleteFile(ctx, DeleteFileArgs{Path: p, TrashDir: filepath.Join(tmp, "trash")})
		if err == nil {
			t.Fatalf("expected error")
		}
	})

	t.Run("nonexistent_errors", func(t *testing.T) {
		tmp := t.TempDir()
		_, err := DeleteFile(t.Context(), DeleteFileArgs{
			Path:     filepath.Join(tmp, "missing.txt"),
			TrashDir: filepath.Join(tmp, "trash"),
		})
		if err == nil {
			t.Fatalf("expected error")
		}
		if !os.IsNotExist(err) {
			t.Fatalf("expected IsNotExist, got: %T %v", err, err)
		}
	})

	t.Run("directory_errors", func(t *testing.T) {
		tmp := t.TempDir()

		_, err := DeleteFile(t.Context(), DeleteFileArgs{
			Path:     tmp,
			TrashDir: filepath.Join(tmp, "trash"),
		})
		if err == nil {
			t.Fatalf("expected error")
		}
	})

	t.Run("moves_to_explicit_trashDir", func(t *testing.T) {
		tmp := t.TempDir()
		p := filepath.Join(tmp, "a.txt")
		if err := os.WriteFile(p, []byte("hello"), 0o600); err != nil {
			t.Fatalf("WriteFile: %v", err)
		}

		trash := filepath.Join(tmp, "trash")
		out, err := DeleteFile(t.Context(), DeleteFileArgs{Path: p, TrashDir: trash})
		if err != nil {
			t.Fatalf("DeleteFile: %v", err)
		}
		if filepath.Dir(out.TrashedPath) != trash {
			t.Fatalf("TrashDirUsed mismatch: %+v", out)
		}

		if _, err := os.Lstat(p); !os.IsNotExist(err) {
			t.Fatalf("expected original removed, stat err=%v", err)
		}

		b, err := os.ReadFile(out.TrashedPath)
		if err != nil {
			t.Fatalf("ReadFile trashed: %v", err)
		}
		if string(b) != "hello" {
			t.Fatalf("content mismatch: %q", string(b))
		}
	})

	t.Run("name_collision_gets_unique_name", func(t *testing.T) {
		tmp := t.TempDir()
		trash := filepath.Join(tmp, "trash")

		p := filepath.Join(tmp, "same.txt")
		if err := os.WriteFile(p, []byte("one"), 0o600); err != nil {
			t.Fatalf("WriteFile: %v", err)
		}
		out1, err := DeleteFile(t.Context(), DeleteFileArgs{Path: p, TrashDir: trash})
		if err != nil {
			t.Fatalf("DeleteFile #1: %v", err)
		}

		// Recreate with same name and delete again.
		if err := os.WriteFile(p, []byte("two"), 0o600); err != nil {
			t.Fatalf("WriteFile: %v", err)
		}
		out2, err := DeleteFile(t.Context(), DeleteFileArgs{Path: p, TrashDir: trash})
		if err != nil {
			t.Fatalf("DeleteFile #2: %v", err)
		}

		if out1.TrashedPath == out2.TrashedPath {
			t.Fatalf("expected unique trashed paths, got same: %q", out1.TrashedPath)
		}

		b1, _ := os.ReadFile(out1.TrashedPath)
		b2, _ := os.ReadFile(out2.TrashedPath)
		if string(b1) != "one" || string(b2) != "two" {
			t.Fatalf("content mismatch: b1=%q b2=%q", string(b1), string(b2))
		}
	})

	t.Run("trashDir_is_file_errors", func(t *testing.T) {
		tmp := t.TempDir()

		p := filepath.Join(tmp, "a.txt")
		_ = os.WriteFile(p, []byte("x"), 0o600)

		trashAsFile := filepath.Join(tmp, "trash")
		_ = os.WriteFile(trashAsFile, []byte("not a dir"), 0o600)

		_, err := DeleteFile(t.Context(), DeleteFileArgs{Path: p, TrashDir: trashAsFile})
		if err == nil {
			t.Fatalf("expected error")
		}
	})

	t.Run("auto_uses_xdg_trash_when_set (non-windows)", func(t *testing.T) {
		if runtime.GOOS == toolutil.GOOSWindows {
			t.Skip("HOME/XDG behavior differs on Windows")
		}

		tmpHome := t.TempDir()
		t.Setenv("HOME", tmpHome)

		xdg := filepath.Join(tmpHome, "xdgdata")
		t.Setenv("XDG_DATA_HOME", xdg)

		tmp := t.TempDir()
		p := filepath.Join(tmp, "auto.txt")
		_ = os.WriteFile(p, []byte("x"), 0o600)

		out, err := DeleteFile(t.Context(), DeleteFileArgs{Path: p, TrashDir: "auto"})
		if err != nil {
			t.Fatalf("DeleteFile(auto): %v", err)
		}

		var wantTrash string
		switch runtime.GOOS {
		case "darwin":
			// "macOS" uses ~/.Trash as the system trash.
			wantTrash = filepath.Join(tmpHome, ".Trash")
		case "linux", "freebsd", "openbsd", "netbsd", "dragonfly":
			// XDG trash spec location when XDG_DATA_HOME is set.
			wantTrash = filepath.Join(xdg, "Trash", "files")
		default:
			t.Skipf("system trash not defined for GOOS=%q in this test", runtime.GOOS)
		}
		if filepath.Dir(out.TrashedPath) != wantTrash {
			t.Fatalf("TrashDirUsed=%q want=%q", filepath.Dir(out.TrashedPath), wantTrash)
		}
	})

	t.Run("symlink_is_moved_as_symlink (best effort)", func(t *testing.T) {
		if runtime.GOOS == toolutil.GOOSWindows {
			t.Skip("symlink tests often require privileges on Windows")
		}

		tmp := t.TempDir()
		target := filepath.Join(tmp, "target.txt")
		_ = os.WriteFile(target, []byte("keep"), 0o600)

		link := filepath.Join(tmp, "link.txt")
		if err := os.Symlink(target, link); err != nil {
			t.Skipf("symlink not available: %v", err)
		}

		trash := filepath.Join(tmp, "trash")
		out, err := DeleteFile(t.Context(), DeleteFileArgs{Path: link, TrashDir: trash})
		if err != nil {
			t.Fatalf("DeleteFile: %v", err)
		}

		// Target should still exist.
		if _, err := os.Stat(target); err != nil {
			t.Fatalf("target missing: %v", err)
		}

		// Trashed should be a symlink.
		st, err := os.Lstat(out.TrashedPath)
		if err != nil {
			t.Fatalf("Lstat trashed: %v", err)
		}
		if (st.Mode() & os.ModeSymlink) == 0 {
			t.Fatalf("expected symlink in trash, got mode: %v", st.Mode())
		}
	})
}
