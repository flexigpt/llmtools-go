package fstool

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestStatPath(t *testing.T) {
	tmpDir := t.TempDir()
	filePath := filepath.Join(tmpDir, "sample.txt")
	if err := os.WriteFile(filePath, []byte("hi"), 0o600); err != nil {
		t.Fatalf("write file: %v", err)
	}
	type tc struct {
		name string
		ctx  func(t *testing.T) context.Context
		args StatPathArgs

		wantErr      bool
		wantCanceled bool
		wantExists   bool
		wantIsDir    bool
		wantSize     int64
		wantName     string
		wantModTime  bool
	}

	tests := []tc{
		{
			name: "context_canceled",
			ctx: func(t *testing.T) context.Context {
				t.Helper()
				ctx, cancel := context.WithCancel(t.Context())
				cancel()
				return ctx
			},
			args:         StatPathArgs{Path: filePath},
			wantErr:      true,
			wantCanceled: true,
		},
		{
			name:        "existing_file",
			args:        StatPathArgs{Path: filePath},
			wantExists:  true,
			wantIsDir:   false,
			wantSize:    2,
			wantName:    "sample.txt",
			wantModTime: true,
		},
		{
			name:       "existing_dir",
			args:       StatPathArgs{Path: tmpDir},
			wantExists: true,
			wantIsDir:  true,
			wantName:   filepath.Base(tmpDir),
		},
		{
			name:       "missing_path_exists_false",
			args:       StatPathArgs{Path: filepath.Join(tmpDir, "missing.txt")},
			wantExists: false,
			wantIsDir:  false,
		},
		{
			name:    "empty_path_errors",
			args:    StatPathArgs{},
			wantErr: true,
		},
		{
			name:    "whitespace_path_errors",
			args:    StatPathArgs{Path: "   "},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := t.Context()
			if tt.ctx != nil {
				ctx = tt.ctx(t)
			}
			res, err := StatPath(ctx, tt.args)
			if (err != nil) != tt.wantErr {
				t.Fatalf("err=%v wantErr=%v", err, tt.wantErr)
			}
			if err != nil {
				if tt.wantCanceled && !errors.Is(err, context.Canceled) {
					t.Fatalf("expected context.Canceled, got %v", err)
				}
				return
			}
			if res.Exists != tt.wantExists {
				t.Fatalf("Exists=%v want %v (res=%+v)", res.Exists, tt.wantExists, res)
			}
			if res.IsDir != tt.wantIsDir {
				t.Fatalf("IsDir=%v want %v (res=%+v)", res.IsDir, tt.wantIsDir, res)
			}
			if tt.wantName != "" && res.Name != tt.wantName {
				t.Fatalf("Name=%q want %q", res.Name, tt.wantName)
			}
			if tt.wantExists && !tt.wantIsDir && res.SizeBytes != tt.wantSize {
				t.Fatalf("SizeBytes=%d want %d", res.SizeBytes, tt.wantSize)
			}
			if tt.wantModTime && res.ModTime == nil {
				t.Fatalf("expected ModTime to be set")
			}
		})
	}
}
