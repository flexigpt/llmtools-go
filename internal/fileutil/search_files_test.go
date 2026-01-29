package fileutil

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/flexigpt/llmtools-go/internal/toolutil"
)

// Structure describing the tree used in SearchFiles tests.
type searchTestTree struct {
	root        string
	helloPath   string
	matchPath   string
	contentPath string
	nestedPath  string
	bigPath     string
}

func TestSearchFilesBasic(t *testing.T) {
	tree := createSearchTestTree(t)

	tests := []struct {
		name            string
		root            string
		pattern         string
		maxResults      int
		wantExactPaths  []string // compare as set if non-nil
		allowedPaths    []string // each result must be one of these (for limit tests)
		wantLen         int      // -1 means "don't check"; overrides len(wantExactPaths) when set
		wantErr         bool
		wantErrContains string
	}{
		{
			name:           "match by filename only",
			root:           tree.root,
			pattern:        "matchname",
			maxResults:     0,
			wantLen:        -1,
			wantExactPaths: []string{tree.matchPath},
		},
		{
			name:           "match by content only",
			root:           tree.root,
			pattern:        "CONTENTPATTERN",
			maxResults:     0,
			wantLen:        -1,
			wantExactPaths: []string{tree.contentPath, tree.nestedPath},
		},
		{
			name:       "maxResults limits number of matches",
			root:       tree.root,
			pattern:    "CONTENTPATTERN",
			maxResults: 1,
			allowedPaths: []string{
				tree.contentPath,
				tree.nestedPath,
			},
			wantLen: 1,
		},
		{
			name:            "pattern is required",
			root:            tree.root,
			pattern:         "",
			maxResults:      0,
			wantErr:         true,
			wantErrContains: "pattern is required",
		},
		{
			name:       "invalid regexp pattern",
			root:       tree.root,
			pattern:    "(",
			maxResults: 0,
			wantErr:    true,
		},
		{
			name:       "non-existent root returns error",
			root:       filepath.Join(tree.root, "no_such_dir"),
			pattern:    "anything",
			maxResults: 0,
			wantErr:    true,
		},
		{
			name:           "maxResults zero treated as unlimited",
			root:           tree.root,
			pattern:        "txt",
			maxResults:     0,
			wantExactPaths: []string{tree.helloPath, tree.matchPath, tree.contentPath, tree.nestedPath},
			wantLen:        -1,
		},
		{
			name:           "negative maxResults treated as unlimited",
			root:           tree.root,
			pattern:        "txt",
			maxResults:     -10,
			wantExactPaths: []string{tree.helloPath, tree.matchPath, tree.contentPath, tree.nestedPath},
			wantLen:        -1,
		},
		{
			name:           "large files are not searched by content",
			root:           tree.root,
			pattern:        "BIGPATTERN",
			maxResults:     0,
			wantExactPaths: []string{},
			wantLen:        -1,
		},
		{
			name:           "root can be a single file",
			root:           tree.helloPath,
			pattern:        "hello",
			maxResults:     0,
			wantExactPaths: []string{tree.helloPath},
			wantLen:        -1,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, _, err := SearchFiles(t.Context(), tc.root, tc.pattern, tc.maxResults)

			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error, got nil")
				}
				if tc.wantErrContains != "" && !strings.Contains(err.Error(), tc.wantErrContains) {
					t.Fatalf("error %q does not contain %q", err.Error(), tc.wantErrContains)
				}
				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if tc.wantLen >= 0 && len(got) != tc.wantLen {
				t.Fatalf("len(results) = %d, want %d", len(got), tc.wantLen)
			}

			if tc.wantExactPaths != nil {
				if !equalStringSets(got, tc.wantExactPaths) {
					t.Fatalf("results = %#v, want %#v (order-independent)", got, tc.wantExactPaths)
				}
			}

			if tc.allowedPaths != nil {
				for _, p := range got {
					if !containsString(tc.allowedPaths, p) {
						t.Fatalf("result %q not in allowed set %#v", p, tc.allowedPaths)
					}
				}
			}
		})
	}
}

func TestSearchFilesRootDefaultUsesCWD(t *testing.T) {
	root := t.TempDir()
	filePath := filepath.Join(root, "cwdfile.txt")
	writeFile(t, filePath, "some pattern content")

	t.Chdir(root)

	tests := []struct {
		name           string
		pattern        string
		maxResults     int
		wantExactPaths []string
	}{
		{
			name:           "empty root searches current directory by path",
			pattern:        "cwdfile",
			maxResults:     0,
			wantExactPaths: []string{"cwdfile.txt"},
		},
		{
			name:           "empty root searches current directory by content",
			pattern:        "pattern content",
			maxResults:     0,
			wantExactPaths: []string{"cwdfile.txt"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, _, err := SearchFiles(t.Context(), "", tc.pattern, tc.maxResults)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !equalStringSets(got, tc.wantExactPaths) {
				t.Fatalf("results = %#v, want %#v (order-independent)", got, tc.wantExactPaths)
			}
		})
	}
}

func TestSearchFilesConcurrency(t *testing.T) {
	tree := createSearchTestTree(t)

	tests := []struct {
		name        string
		goroutines  int
		iterations  int
		searchRoot  string
		searchPat   string
		expectedSet []string
	}{
		{
			name:        "concurrent searches on same tree",
			goroutines:  8,
			iterations:  5,
			searchRoot:  tree.root,
			searchPat:   "CONTENTPATTERN",
			expectedSet: []string{tree.contentPath, tree.nestedPath},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var wg sync.WaitGroup
			errCh := make(chan error, tc.goroutines)

			for i := 0; i < tc.goroutines; i++ {
				wg.Add(1)
				go func(id int) {
					defer wg.Done()
					for j := 0; j < tc.iterations; j++ {
						got, _, err := SearchFiles(t.Context(), tc.searchRoot, tc.searchPat, 0)
						if err != nil {
							errCh <- fmt.Errorf("goroutine %d: unexpected error: %w", id, err)
							return
						}
						if !equalStringSets(got, tc.expectedSet) {
							errCh <- fmt.Errorf("goroutine %d: unexpected results: %#v, want %#v",
								id, got, tc.expectedSet)
							return
						}
					}
				}(i)
			}

			wg.Wait()
			close(errCh)

			for err := range errCh {
				if err != nil {
					t.Fatalf("concurrent SearchFiles error: %v", err)
				}
			}
		})
	}
}

func TestSearchFiles_ContextCanceled(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "a.txt"), "hello")

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	_, _, err := SearchFiles(ctx, root, "hello", 0)
	if err == nil {
		t.Fatalf("expected error, got nil")
	}
}

func TestSearchFiles_SkipsBinaryAndNonUTF8Content(t *testing.T) {
	root := t.TempDir()

	// Binary file containing pattern in bytes but should be skipped by content check.
	mustWriteBytes(t, filepath.Join(root, "bin.dat"), []byte{0x00, 'B', 'A', 'D', 'P', 'A', 'T', 'T', 'E', 'R', 'N'})

	// Invalid UTF-8 file containing the pattern as bytes.
	mustWriteBytes(
		t,
		filepath.Join(root, "badutf8.txt"),
		[]byte{0xff, 0xfe, 'B', 'A', 'D', 'P', 'A', 'T', 'T', 'E', 'R', 'N'},
	)

	got, _, err := SearchFiles(t.Context(), root, "BADPATTERN", 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("expected no matches, got %v", got)
	}
}

func TestSearchFiles_PathMatchStillWorksForBinary(t *testing.T) {
	root := t.TempDir()

	p := filepath.Join(root, "match-by-path.bin")
	mustWriteBytes(t, p, []byte{0x00, 0x01, 0x02})

	got, _, err := SearchFiles(t.Context(), root, "match-by-path", 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 1 || got[0] != p {
		t.Fatalf("got=%v want=[%v]", got, p)
	}
}

func TestSearchFiles_UnreadableFileIsIgnored(t *testing.T) {
	if runtime.GOOS == toolutil.GOOSWindows {
		t.Skip("chmod semantics differ on Windows")
	}

	root := t.TempDir()
	p := filepath.Join(root, "secret.txt")
	writeFile(t, p, "SOMEPATTERN")

	if err := os.Chmod(p, 0); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(p, 0o600) })

	// Give filesystem a moment (some FS/CI can be weird).
	time.Sleep(10 * time.Millisecond)

	got, _, err := SearchFiles(t.Context(), root, "SOMEPATTERN", 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("expected 0 matches (unreadable content ignored), got %v", got)
	}
}

// Build a deterministic directory tree for SearchFiles tests.
func createSearchTestTree(t *testing.T) searchTestTree {
	t.Helper()

	root := t.TempDir()

	helloPath := filepath.Join(root, "hello.txt")
	writeFile(t, helloPath, "hello world")

	matchPath := filepath.Join(root, "matchname.txt")
	writeFile(t, matchPath, "this file path will match; its content will not")

	contentPath := filepath.Join(root, "content.txt")
	writeFile(t, contentPath, "some CONTENTPATTERN in this file.")

	subdir := filepath.Join(root, "subdir")
	if err := os.MkdirAll(subdir, 0o755); err != nil {
		t.Fatalf("failed to create subdir %q: %v", subdir, err)
	}

	nestedPath := filepath.Join(subdir, "nested.txt")
	writeFile(t, nestedPath, "nested CONTENTPATTERN plus more text.")

	// Big file (>10MB) whose content contains BIGPATTERN but should not be read
	// by SearchFiles because of the size guard.
	bigPath := filepath.Join(subdir, "bigfile.bin")
	f, err := os.Create(bigPath)
	if err != nil {
		t.Fatalf("failed to create big file %q: %v", bigPath, err)
	}
	if _, err := f.WriteString("BIGPATTERN at the beginning of a big file."); err != nil {
		t.Fatalf("failed to write to big file %q: %v", bigPath, err)
	}
	if err := f.Truncate(10*1024*1024 + 1); err != nil {
		t.Fatalf("failed to truncate big file %q: %v", bigPath, err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("failed to close big file %q: %v", bigPath, err)
	}

	return searchTestTree{
		root:        root,
		helloPath:   helloPath,
		matchPath:   matchPath,
		contentPath: contentPath,
		nestedPath:  nestedPath,
		bigPath:     bigPath,
	}
}

// Helper to write text files in tests.
func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("failed to write test file %q: %v", path, err)
	}
}

// Helper to compare string slices as sets (order-independent).
func equalStringSets(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	m := make(map[string]int, len(a))
	for _, s := range a {
		m[s]++
	}
	for _, s := range b {
		m[s]--
		if m[s] < 0 {
			return false
		}
	}
	for _, v := range m {
		if v != 0 {
			return false
		}
	}
	return true
}

// Helper to check if a slice contains a string.
func containsString(slice []string, target string) bool {
	return slices.Contains(slice, target)
}
