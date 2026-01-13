package fstool

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestSearchFiles covers happy, error, and boundary cases for SearchFiles.
func TestSearchFiles(t *testing.T) {
	tmpDir := t.TempDir()

	if err := os.WriteFile(filepath.Join(tmpDir, "foo.txt"), []byte("hello world"), 0o600); err != nil {
		t.Fatalf("write foo.txt: %v", err)
	}
	if err := os.WriteFile(filepath.Join(tmpDir, "bar.md"), []byte("goodbye world"), 0o600); err != nil {
		t.Fatalf("write bar.md: %v", err)
	}
	if err := os.Mkdir(filepath.Join(tmpDir, "sub"), 0o755); err != nil {
		t.Fatalf("mkdir sub: %v", err)
	}
	if err := os.WriteFile(filepath.Join(tmpDir, "sub", "baz.txt"), []byte("baz content"), 0o600); err != nil {
		t.Fatalf("write baz.txt: %v", err)
	}

	// Large file to exercise content-size guard (if implemented by fileutil.SearchFiles).
	largeFile := filepath.Join(tmpDir, "large.txt")
	largeContent := strings.Repeat("x", 11*1024*1024) // >10MB
	if err := os.WriteFile(largeFile, []byte(largeContent), 0o600); err != nil {
		t.Fatalf("write large.txt: %v", err)
	}

	tests := []struct {
		name       string
		args       SearchFilesArgs
		want       []string
		wantErr    bool
		shouldFind func([]string) bool
	}{
		{
			name:    "Missing pattern returns error",
			args:    SearchFilesArgs{Root: tmpDir},
			wantErr: true,
		},
		{
			name:    "Invalid regexp returns error",
			args:    SearchFilesArgs{Root: tmpDir, Pattern: "["},
			wantErr: true,
		},
		{
			name: "Match file path",
			args: SearchFilesArgs{Root: tmpDir, Pattern: "foo\\.txt"},
			want: []string{filepath.Join(tmpDir, "foo.txt")},
		},
		{
			name: "Match file content",
			args: SearchFilesArgs{Root: tmpDir, Pattern: "goodbye"},
			want: []string{filepath.Join(tmpDir, "bar.md")},
		},
		{
			name: "Match in subdirectory",
			args: SearchFilesArgs{Root: tmpDir, Pattern: "baz"},
			want: []string{filepath.Join(tmpDir, "sub", "baz.txt")},
		},
		{
			name: "MaxResults limits output",
			args: SearchFilesArgs{Root: tmpDir, Pattern: "txt", MaxResults: 1},
			shouldFind: func(matches []string) bool {
				return len(matches) == 1 && strings.HasSuffix(matches[0], ".txt")
			},
		},
		{
			name: "Large file does not match content (size guard)",
			args: SearchFilesArgs{Root: tmpDir, Pattern: "x{100,}"},
			want: []string{}, // Should not match large.txt content if size guard is active.
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out, err := SearchFiles(context.Background(), tt.args)
			if (err != nil) != tt.wantErr {
				t.Fatalf("SearchFiles error = %v, wantErr = %v", err, tt.wantErr)
			}
			if err != nil {
				return
			}
			if tt.shouldFind != nil {
				if !tt.shouldFind(out.Matches) {
					t.Errorf("custom predicate failed for matches: %v", out.Matches)
				}
				return
			}
			if tt.want == nil {
				return
			}
			wantMap := make(map[string]bool)
			for _, w := range tt.want {
				wantMap[w] = true
			}
			gotMap := make(map[string]bool)
			for _, g := range out.Matches {
				gotMap[g] = true
			}
			for w := range wantMap {
				if !gotMap[w] {
					t.Errorf("expected match %q not found in %v", w, out.Matches)
				}
			}
			if len(out.Matches) != len(tt.want) {
				t.Errorf("expected %d matches, got %d", len(tt.want), len(out.Matches))
			}
		})
	}
}
