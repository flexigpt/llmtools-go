package fstool

import (
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

	res, err := StatPath(t.Context(), StatPathArgs{Path: filePath})
	if err != nil {
		t.Fatalf("StatPath returned error: %v", err)
	}
	if !res.Exists || res.IsDir {
		t.Fatalf("expected file to exist and not be dir: %+v", res)
	}
	if res.SizeBytes != 2 {
		t.Fatalf("expected size 2, got %d", res.SizeBytes)
	}
	if res.ModTime == nil {
		t.Fatalf("expected mod time to be set")
	}

	dirRes, err := StatPath(t.Context(), StatPathArgs{Path: tmpDir})
	if err != nil {
		t.Fatalf("StatPath dir error: %v", err)
	}
	if !dirRes.Exists || !dirRes.IsDir {
		t.Fatalf("expected dir to exist and be dir: %+v", dirRes)
	}

	nonExistent, err := StatPath(t.Context(), StatPathArgs{
		Path: filepath.Join(tmpDir, "missing.txt"),
	})
	if err != nil {
		t.Fatalf("StatPath missing error: %v", err)
	}
	if nonExistent.Exists {
		t.Fatalf("expected missing path to report Exists=false")
	}

	if _, err := StatPath(t.Context(), StatPathArgs{}); err == nil {
		t.Fatalf("expected error for empty path")
	}
}
