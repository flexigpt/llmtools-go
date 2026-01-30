package fileutil

import (
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"

	"github.com/flexigpt/llmtools-go/internal/toolutil"
)

func TestNormalizeAbsPath(t *testing.T) {
	absBase := filepath.Join(t.TempDir(), "x", "..", "y")
	tests := []struct {
		name      string
		in        string
		want      string
		wantErrIs error
	}{
		{
			name: "absolute path is cleaned",
			in:   "  " + absBase + "  ",
			want: filepath.Clean(absBase),
		},
		{
			name:      "relative path rejected",
			in:        "a/../b",
			wantErrIs: errPathMustBeAbsolute,
		},
		{
			name:      "empty path rejected",
			in:        "",
			wantErrIs: ErrInvalidPath,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := NormalizeAbsPath(tc.in)
			if tc.wantErrIs != nil {
				if err == nil {
					t.Fatalf("expected error, got nil (got=%q)", got)
				}
				if !errors.Is(err, tc.wantErrIs) {
					t.Fatalf("error=%v; want errors.Is(_, %v)=true", err, tc.wantErrIs)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.want {
				t.Fatalf("got=%q want=%q", got, tc.want)
			}
		})
	}
}

func TestNormalizePath(t *testing.T) {
	sep := string(os.PathSeparator)
	tests := []struct {
		in      string
		want    string
		wantErr bool
	}{
		{"", "", true},
		{"   ", "", true},
		{"a" + sep + ".." + sep + "b", filepath.Clean("a" + sep + ".." + sep + "b"), false},
		{"  .  ", ".", false},
		{"a\u0000b", "", true},
	}

	for _, tc := range tests {
		t.Run("in="+tc.in, func(t *testing.T) {
			got, err := NormalizePath(tc.in)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error, got nil (got=%q)", got)
				}
				if !errors.Is(err, ErrInvalidPath) {
					t.Fatalf("expected ErrInvalidPath, got: %v", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.want {
				t.Fatalf("NormalizePath(%q)=%q want=%q", tc.in, got, tc.want)
			}
		})
	}
}

func TestEnsureDirNoSymlink(t *testing.T) {
	root := t.TempDir()

	// Pre-create a dir to test "existing doesn't count".
	if err := os.Mkdir(filepath.Join(root, "already"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	tests := []struct {
		name        string
		dir         string
		maxNewDirs  int
		setup       func(t *testing.T)
		wantCreated int
		wantErr     bool
		errContains string
	}{
		{
			name:        "empty path invalid",
			dir:         "",
			maxNewDirs:  0,
			wantCreated: 0,
			wantErr:     true,
		},
		{
			name:        "dot does nothing",
			dir:         ".",
			maxNewDirs:  0,
			wantCreated: 0,
		},
		{
			name:        "creates nested directories",
			dir:         filepath.Join(root, "a", "b", "c"),
			maxNewDirs:  0,
			wantCreated: 3,
		},
		{
			name:        "existing parent does not count as created",
			dir:         filepath.Join(root, "already", "child"),
			maxNewDirs:  0,
			wantCreated: 1,
		},
		{
			name:        "maxNewDirs enforced",
			dir:         filepath.Join(root, "x", "y", "z"),
			maxNewDirs:  2,
			wantCreated: 2,
			wantErr:     true,
			errContains: "too many parent directories",
		},
		{
			name: "component exists as file => error",
			dir:  filepath.Join(root, "filecomp", "child"),
			setup: func(t *testing.T) {
				t.Helper()
				mustWriteBytes(t, filepath.Join(root, "filecomp"), []byte("x"))
			},
			wantCreated: 0,
			wantErr:     true,
			errContains: "not a directory",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if tc.setup != nil {
				tc.setup(t)
			}
			created, err := EnsureDirNoSymlink(tc.dir, tc.maxNewDirs)
			if created != tc.wantCreated {
				t.Fatalf("created=%d want=%d (err=%v)", created, tc.wantCreated, err)
			}
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error, got nil")
				}
				if tc.errContains != "" && !strings.Contains(err.Error(), tc.errContains) {
					t.Fatalf("error %q does not contain %q", err.Error(), tc.errContains)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if st, statErr := os.Stat(tc.dir); statErr != nil || !st.IsDir() {
				t.Fatalf("expected dir to exist: statErr=%v isDir=%v", statErr, st != nil && st.IsDir())
			}
		})
	}

	t.Run("refuses symlink component (if supported)", func(t *testing.T) {
		if runtime.GOOS == toolutil.GOOSWindows {
			t.Skip("symlink tests skipped on Windows")
		}
		real1 := filepath.Join(root, "real1")
		if err := os.Mkdir(real1, 0o755); err != nil {
			t.Fatalf("mkdir real: %v", err)
		}
		link := filepath.Join(root, "link")
		mustSymlinkOrSkip(t, real1, link)

		created, err := EnsureDirNoSymlink(filepath.Join(link, "child"), 0)
		if err == nil {
			t.Fatalf("expected error, got nil (created=%d)", created)
		}
		if !strings.Contains(err.Error(), "symlink path component") {
			t.Fatalf("unexpected error: %v", err)
		}
	})
}

func TestVerifyDirNoSymlink_AllowsDarwinSystemSymlinks(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("darwin-only")
	}

	if err := VerifyDirNoSymlink(t.TempDir()); err != nil {
		t.Fatalf("VerifyDirNoSymlink(os.TempDir()) unexpected error: %v", err)
	}
}

func TestVerifyDirNoSymlink(t *testing.T) {
	root := t.TempDir()

	okDir := filepath.Join(root, "ok", "nested")
	if err := os.MkdirAll(okDir, 0o755); err != nil {
		t.Fatalf("mkdirall: %v", err)
	}

	tests := []struct {
		name        string
		dir         string
		setup       func(t *testing.T)
		wantErr     bool
		errContains string
	}{
		{
			name:    "dot is ok",
			dir:     ".",
			wantErr: false,
		},
		{
			name:    "existing nested dir ok",
			dir:     okDir,
			wantErr: false,
		},
		{
			name:    "missing dir errors",
			dir:     filepath.Join(root, "nope"),
			wantErr: true,
		},
		{
			name: "file component errors",
			dir:  filepath.Join(root, "file", "child"),
			setup: func(t *testing.T) {
				t.Helper()
				mustWriteBytes(t, filepath.Join(root, "file"), []byte("x"))
			},
			wantErr:     true,
			errContains: "not a directory",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if tc.setup != nil {
				tc.setup(t)
			}
			err := VerifyDirNoSymlink(tc.dir)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error, got nil")
				}
				if tc.errContains != "" && !strings.Contains(err.Error(), tc.errContains) {
					t.Fatalf("error %q does not contain %q", err.Error(), tc.errContains)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}

	t.Run("symlink component errors (if supported)", func(t *testing.T) {
		if runtime.GOOS == toolutil.GOOSWindows {
			t.Skip("symlink tests skipped on Windows")
		}

		real2 := filepath.Join(root, "real2")
		if err := os.Mkdir(real2, 0o755); err != nil {
			t.Fatalf("mkdir real: %v", err)
		}
		link := filepath.Join(root, "link2")
		mustSymlinkOrSkip(t, real2, link)

		err := VerifyDirNoSymlink(filepath.Join(link, "child"))
		if err == nil {
			t.Fatalf("expected error, got nil")
		}
		if !strings.Contains(err.Error(), "symlink path component") {
			t.Fatalf("unexpected error: %v", err)
		}
	})
}

func TestUniquePathInDir(t *testing.T) {
	root := t.TempDir()

	t.Run("returns base name when available", func(t *testing.T) {
		p, err := UniquePathInDir(root, "a.txt")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if p != filepath.Join(root, "a.txt") {
			t.Fatalf("got=%q want=%q", p, filepath.Join(root, "a.txt"))
		}
	})

	t.Run("generates unique name when collision", func(t *testing.T) {
		// Create colliding name.
		mustWriteBytes(t, filepath.Join(root, "a.txt"), []byte("x"))

		p, err := UniquePathInDir(root, "a.txt")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if filepath.Dir(p) != root {
			t.Fatalf("expected same dir %q, got %q", root, filepath.Dir(p))
		}
		if p == filepath.Join(root, "a.txt") {
			t.Fatalf("expected unique path, got original: %q", p)
		}
		if _, err := os.Lstat(p); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("expected candidate not to exist yet, stat err=%v", err)
		}

		// Validate shape: a.<timestamp>.<hex>.txt.
		base := filepath.Base(p)
		re := regexp.MustCompile(`^a\.\d{8}T\d{6}\.\d{9}Z\.[0-9a-f]{12}\.txt$`)
		if !re.MatchString(base) {
			t.Fatalf("unexpected unique filename shape: %q", base)
		}
	})
}

func mustSymlinkOrSkip(t *testing.T, oldname, newname string) {
	t.Helper()
	if err := os.Symlink(oldname, newname); err != nil {
		// Often EPERM in CI environments.
		t.Skipf("symlink not supported/allowed: %v", err)
	}
}
