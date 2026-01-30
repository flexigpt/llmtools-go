package fstool

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// TestListDirectory covers happy, error, and pattern cases for ListDirectory.
func TestListDirectory(t *testing.T) {
	tmpDir := t.TempDir()
	aPath := filepath.Join(tmpDir, "a.txt")
	bPath := filepath.Join(tmpDir, "b.md")
	subDir := filepath.Join(tmpDir, "subdir")

	if err := os.WriteFile(aPath, []byte("a"), 0o600); err != nil {
		t.Fatalf("write a.txt: %v", err)
	}
	if err := os.WriteFile(bPath, []byte("b"), 0o600); err != nil {
		t.Fatalf("write b.md: %v", err)
	}
	if err := os.Mkdir(subDir, 0o755); err != nil {
		t.Fatalf("mkdir subdir: %v", err)
	}

	tests := []struct {
		name string
		args ListDirectoryArgs
		ctx  func(t *testing.T) context.Context

		want       []string
		wantErr    bool
		strictWant bool // if true, require exact set equality; else just subset check
	}{
		{
			name: "context_canceled",
			ctx: func(t *testing.T) context.Context {
				t.Helper()
				ctx, cancel := context.WithCancel(t.Context())
				cancel()
				return ctx
			},
			args:    ListDirectoryArgs{Path: tmpDir},
			wantErr: true,
		},
		{
			name:       "List all entries",
			args:       ListDirectoryArgs{Path: tmpDir},
			want:       []string{"a.txt", "b.md", "subdir"},
			strictWant: true,
		},
		{
			name:       "List with pattern",
			args:       ListDirectoryArgs{Path: tmpDir, Pattern: "*.md"},
			want:       []string{"b.md"},
			strictWant: true,
		},
		{
			name:       "List with pattern no match",
			args:       ListDirectoryArgs{Path: tmpDir, Pattern: "*.go"},
			want:       []string{},
			strictWant: true,
		},
		{
			name:    "Nonexistent directory returns error",
			args:    ListDirectoryArgs{Path: filepath.Join(tmpDir, "nope")},
			wantErr: true,
		},
		{
			name:       "Default path lists current dir (deterministic)",
			args:       ListDirectoryArgs{},
			want:       []string{"a.txt", "b.md", "subdir"},
			strictWant: true,
		},
		{
			name:    "Path is file returns error",
			args:    ListDirectoryArgs{Path: aPath},
			wantErr: true,
		},
		{
			name:    "Invalid glob pattern returns error",
			args:    ListDirectoryArgs{Path: tmpDir, Pattern: "["},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := t.Context()
			if tt.ctx != nil {
				ctx = tt.ctx(t)
			}
			if tt.name == "Default path lists current dir (deterministic)" {
				t.Chdir(tmpDir)
			}
			out, err := ListDirectory(ctx, tt.args)
			if (err != nil) != tt.wantErr {
				t.Fatalf("ListDirectory error = %v, wantErr = %v", err, tt.wantErr)
			}
			if err != nil {
				if tt.name == "context_canceled" && !errors.Is(err, context.Canceled) {
					t.Fatalf("expected context.Canceled, got %v", err)
				}
				return
			}
			if tt.want == nil {
				// No specific expectations beyond success.
				return
			}
			got := make(map[string]bool)
			for _, e := range out.Entries {
				got[e] = true
			}
			for _, e := range tt.want {
				if !got[e] {
					t.Errorf("expected entry %q not found in %v", e, out.Entries)
				}
			}
			if tt.strictWant && len(out.Entries) != len(tt.want) {
				t.Errorf("expected %d entries, got %d (%v)", len(tt.want), len(out.Entries), out.Entries)
			}
		})
	}
}
