package fileutil

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"sort"
	"strings"
	"testing"
)

func TestListDirectory(t *testing.T) {
	root := t.TempDir()
	mustWriteFile(t, root, "b.log", 1)
	mustWriteFile(t, root, "a.txt", 1)
	if err := os.Mkdir(filepath.Join(root, "subdir"), 0o755); err != nil {
		t.Fatalf("failed to mkdir subdir: %v", err)
	}

	tests := []struct {
		name           string
		setup          func(t *testing.T)
		dir            string
		pattern        string
		want           []string
		wantErrIs      error
		wantIsNotExist bool
	}{
		{
			name:    "no pattern returns all entries (already sorted by function)",
			dir:     root,
			pattern: "",
			want:    []string{"a.txt", "b.log", "subdir"},
		},
		{
			name:    "pattern filters entries",
			dir:     root,
			pattern: "*.txt",
			want:    []string{"a.txt"},
		},
		{
			name:      "invalid glob returns filepath.ErrBadPattern",
			dir:       root,
			pattern:   "[",
			wantErrIs: filepath.ErrBadPattern,
		},
		{
			name:           "non-existent directory returns not-exist",
			dir:            filepath.Join(root, "nope"),
			pattern:        "",
			wantIsNotExist: true,
		},
		{
			name:      "invalid path returns ErrInvalidPath",
			dir:       "a\x00b",
			pattern:   "",
			wantErrIs: ErrInvalidPath,
		},
		{
			name: "empty dir means dot (CWD)",
			setup: func(t *testing.T) {
				t.Helper()
				// Ensure deterministic: chdir into our temp dir for this subtest.
				t.Chdir(root)
			},
			dir:     "",
			pattern: "",
			want:    []string{"a.txt", "b.log", "subdir"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if tc.setup != nil {
				tc.setup(t)
			}
			got, err := ListDirectory(tc.dir, tc.pattern)

			if tc.wantErrIs != nil || tc.wantIsNotExist {
				if err == nil {
					t.Fatalf("expected error, got nil (got=%v)", got)
				}
				if tc.wantErrIs != nil && !errors.Is(err, tc.wantErrIs) {
					t.Fatalf("error=%v; want errors.Is(_, %v)=true", err, tc.wantErrIs)
				}
				if tc.wantIsNotExist && !os.IsNotExist(err) {
					t.Fatalf("error=%v; want os.IsNotExist=true", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("got=%#v want=%#v", got, tc.want)
			}
		})
	}
}

func TestListDirectory_DefaultPathDot(t *testing.T) {
	// Not parallel: this test changes the working directory globally.
	tmp := t.TempDir()
	t.Chdir(tmp)

	// Create some entries in the current directory.
	mustWriteFile(t, tmp, "x.txt", 1)
	if err := os.Mkdir(filepath.Join(tmp, "dir"), 0o755); err != nil {
		t.Fatalf("failed to mkdir: %v", err)
	}

	got, err := ListDirectory("", "")
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}

	// We only check that the created entries are present; there might be others.
	wantSet := map[string]bool{"x.txt": true, "dir": true}
	for name := range wantSet {
		found := slices.Contains(got, name)
		if !found {
			t.Errorf("expected to find %q in ListDirectory output, got %v", name, got)
		}
	}
}

func TestListDirectory_Additional(t *testing.T) {
	root := t.TempDir()
	mustWriteFile(t, root, "a.txt", 1)
	mustWriteFile(t, root, "b.log", 1)
	if err := os.Mkdir(filepath.Join(root, "subdir"), 0o755); err != nil {
		t.Fatalf("mkdir subdir: %v", err)
	}

	tests := []struct {
		name          string
		dir           string
		pattern       string
		want          []string
		wantErr       bool
		errContains   string
		errIsNotExist bool
	}{
		{
			name:          "nonexistent dir returns error",
			dir:           filepath.Join(root, "nope"),
			pattern:       "",
			wantErr:       true,
			errIsNotExist: true,
		},
		{
			name:        "invalid glob pattern returns error",
			dir:         root,
			pattern:     "[",
			wantErr:     true,
			errContains: "syntax error in pattern",
		},
		{
			name:    "pattern matches none returns empty slice",
			dir:     root,
			pattern: "*.nope",
			want:    []string{},
		},
		{
			name:    "pattern can match directories too",
			dir:     root,
			pattern: "sub*",
			want:    []string{"subdir"},
		},
		{
			name:    "basic no pattern includes files and dirs",
			dir:     root,
			pattern: "",
			want:    []string{"a.txt", "b.log", "subdir"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ListDirectory(tc.dir, tc.pattern)

			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error, got nil (got=%v)", got)
				}
				if tc.errContains != "" && !strings.Contains(err.Error(), tc.errContains) {
					t.Fatalf("error %q does not contain %q", err.Error(), tc.errContains)
				}
				if tc.errIsNotExist && !os.IsNotExist(err) {
					t.Fatalf("expected not-exist error, got: %v", err)
				}
				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			sort.Strings(got)
			sort.Strings(tc.want)
			if len(got) != len(tc.want) {
				t.Fatalf("len(got)=%d want=%d got=%v want=%v", len(got), len(tc.want), got, tc.want)
			}
			for i := range tc.want {
				if got[i] != tc.want[i] {
					t.Fatalf("got[%d]=%q want=%q got=%v", i, got[i], tc.want[i], got)
				}
			}
		})
	}
}

func mustWriteFile(t *testing.T, dir, name string, size int) string {
	t.Helper()
	full := filepath.Join(dir, name)
	data := bytes.Repeat([]byte("x"), size)
	if err := os.WriteFile(full, data, 0o600); err != nil {
		t.Fatalf("failed to write file %q: %v", full, err)
	}
	return full
}
