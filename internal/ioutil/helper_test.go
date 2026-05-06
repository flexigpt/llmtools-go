package ioutil

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func mustWriteFile(t *testing.T, dir, name string, size int) string {
	t.Helper()
	full := filepath.Join(dir, name)
	data := bytes.Repeat([]byte("x"), size)
	if err := os.WriteFile(full, data, 0o600); err != nil {
		t.Fatalf("failed to write file %q: %v", full, err)
	}
	return full
}

func mustSymlinkOrSkip(t *testing.T, oldname, newname string) {
	t.Helper()
	if err := os.Symlink(oldname, newname); err != nil {
		// Often EPERM in CI environments.
		t.Skipf("symlink not supported/allowed: %v", err)
	}
}

// Helper to write text files in tests.
func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("failed to write test file %q: %v", path, err)
	}
}

func equalIntSlices(a, b []int) bool {
	if a == nil && b == nil {
		return true
	}
	if (a == nil) != (b == nil) {
		return false
	}
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func equalStringSlices(a, b []string) bool {
	if a == nil && b == nil {
		return true
	}
	if (a == nil) != (b == nil) {
		return false
	}
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
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

func canceledContext(ctx context.Context) context.Context {
	ctx, cancel := context.WithCancel(ctx)
	cancel()
	return ctx
}
