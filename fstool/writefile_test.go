package fstool

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"

	"github.com/flexigpt/llmtools-go/internal/toolutil"
)

func TestWriteFile(t *testing.T) {
	t.Parallel()
	type tc struct {
		name string
		run  func(t *testing.T)
	}

	tests := []tc{
		{
			name: "context_canceled",
			run: func(t *testing.T) {
				t.Helper()
				tmp := t.TempDir()
				ctx, cancel := context.WithCancel(t.Context())
				cancel()
				_, err := WriteFile(ctx, WriteFileArgs{Path: filepath.Join(tmp, "a.txt"), Content: "x"})
				if err == nil || !errors.Is(err, context.Canceled) {
					t.Fatalf("expected context.Canceled, got %v", err)
				}
			},
		},
		{
			name: "rejects_relative_path",
			run: func(t *testing.T) {
				t.Helper()
				_, err := WriteFile(t.Context(), WriteFileArgs{Path: "relative.txt", Content: "hello"})
				if err == nil || !strings.Contains(strings.ToLower(err.Error()), "absolute") {
					t.Fatalf("expected absolute path error, got: %v", err)
				}
			},
		},
		{
			name: "writes_text_default_encoding",
			run: func(t *testing.T) {
				t.Helper()
				tmp := t.TempDir()
				p := filepath.Join(tmp, "text.txt")
				out, err := WriteFile(t.Context(), WriteFileArgs{Path: "  " + p + "  ", Content: "hello"})
				if err != nil {
					t.Fatalf("WriteFile: %v", err)
				}
				if out.Path != p || out.BytesWritten != 5 {
					t.Fatalf("unexpected out: %+v", out)
				}
				b, err := os.ReadFile(p)
				if err != nil {
					t.Fatalf("ReadFile: %v", err)
				}
				if string(b) != "hello" {
					t.Fatalf("content mismatch: %q", string(b))
				}
				if runtime.GOOS != toolutil.GOOSWindows {
					st, err := os.Stat(p)
					if err != nil {
						t.Fatalf("Stat: %v", err)
					}
					// Umask can only remove bits; ensure no group/other perms are granted.
					if st.Mode().Perm()&0o077 != 0 {
						t.Fatalf("expected no group/other perms, got %o", st.Mode().Perm())
					}
				}
			},
		},
		{
			name: "overwrite_false_errors_and_preserves_original",
			run: func(t *testing.T) {
				t.Helper()
				tmp := t.TempDir()
				p := filepath.Join(tmp, "exists.txt")
				if _, err := WriteFile(t.Context(), WriteFileArgs{Path: p, Content: "a"}); err != nil {
					t.Fatalf("seed write: %v", err)
				}
				_, err := WriteFile(t.Context(), WriteFileArgs{Path: p, Content: "b", Overwrite: false})
				if err == nil {
					t.Fatalf("expected error")
				}
				b, rerr := os.ReadFile(p)
				if rerr != nil {
					t.Fatalf("ReadFile: %v", rerr)
				}
				if string(b) != "a" {
					t.Fatalf("expected original content preserved, got %q", string(b))
				}
			},
		},
		{
			name: "overwrite_true_replaces_content",
			run: func(t *testing.T) {
				t.Helper()
				tmp := t.TempDir()
				p := filepath.Join(tmp, "ow.txt")
				if _, err := WriteFile(t.Context(), WriteFileArgs{Path: p, Content: "a"}); err != nil {
					t.Fatalf("seed write: %v", err)
				}
				_, err := WriteFile(t.Context(), WriteFileArgs{Path: p, Content: "bb", Overwrite: true})
				if err != nil {
					t.Fatalf("overwrite: %v", err)
				}
				b, _ := os.ReadFile(p)
				if string(b) != "bb" {
					t.Fatalf("content mismatch: %q", string(b))
				}
			},
		},
		{
			name: "writes_binary_base64_trimmed_and_case_insensitive_encoding",
			run: func(t *testing.T) {
				t.Helper()
				tmp := t.TempDir()
				p := filepath.Join(tmp, "bin.dat")
				raw := []byte{0x00, 0x01, 0x02, 0xff}
				b64 := base64.StdEncoding.EncodeToString(raw)
				out, err := WriteFile(t.Context(), WriteFileArgs{
					Path:     p,
					Encoding: "  BiNaRy ",
					Content:  "  " + b64 + "  ",
				})
				if err != nil {
					t.Fatalf("WriteFile: %v", err)
				}
				if out.BytesWritten != int64(len(raw)) {
					t.Fatalf("unexpected out: %+v", out)
				}
				got, _ := os.ReadFile(p)
				if !bytes.Equal(got, raw) {
					t.Fatalf("binary mismatch: got=%v want=%v", got, raw)
				}
			},
		},
		{
			name: "invalid_base64_errors",
			run: func(t *testing.T) {
				t.Helper()
				tmp := t.TempDir()
				p := filepath.Join(tmp, "badb64.dat")
				_, err := WriteFile(t.Context(), WriteFileArgs{Path: p, Encoding: "binary", Content: "!!!"})
				if err == nil {
					t.Fatalf("expected error")
				}
			},
		},
		{
			name: "createParents_false_missing_parent_errors",
			run: func(t *testing.T) {
				t.Helper()
				tmp := t.TempDir()
				p := filepath.Join(tmp, "nope", "a.txt")
				_, err := WriteFile(t.Context(), WriteFileArgs{Path: p, Content: "x", CreateParents: false})
				if err == nil {
					t.Fatalf("expected error")
				}
			},
		},
		{
			name: "createParents_true_creates_up_to_8",
			run: func(t *testing.T) {
				t.Helper()
				tmp := t.TempDir()
				p := filepath.Join(tmp, "a", "b", "c", "d.txt")
				_, err := WriteFile(t.Context(), WriteFileArgs{Path: p, Content: "ok", CreateParents: true})
				if err != nil {
					t.Fatalf("WriteFile: %v", err)
				}
				if _, err := os.Stat(p); err != nil {
					t.Fatalf("expected file to exist, stat err=%v", err)
				}
			},
		},
		{
			name: "createParents_true_depth_limit_exceeded",
			run: func(t *testing.T) {
				t.Helper()
				tmp := t.TempDir()
				p := filepath.Join(tmp, "1", "2", "3", "4", "5", "6", "7", "8", "9", "f.txt")
				_, err := WriteFile(t.Context(), WriteFileArgs{Path: p, Content: "x", CreateParents: true})
				if err == nil {
					t.Fatalf("expected error")
				}
				if _, statErr := os.Stat(p); statErr == nil {
					t.Fatalf("did not expect file to be created on error")
				}
			},
		},
		{
			name: "refuses_directory_target",
			run: func(t *testing.T) {
				t.Helper()
				tmp := t.TempDir()
				_, err := WriteFile(t.Context(), WriteFileArgs{Path: tmp, Content: "x"})
				if err == nil {
					t.Fatalf("expected error")
				}
			},
		},
		{
			name: "refuses_invalid_utf8_text",
			run: func(t *testing.T) {
				t.Helper()
				tmp := t.TempDir()
				p := filepath.Join(tmp, "badutf8.txt")
				s := string([]byte{0xff, 0xfe})
				_, err := WriteFile(t.Context(), WriteFileArgs{Path: p, Encoding: "text", Content: s})
				if err == nil {
					t.Fatalf("expected error")
				}
			},
		},
		{
			name: "symlink_component_rejected",
			run: func(t *testing.T) {
				t.Helper()
				tmp := t.TempDir()
				realDir := filepath.Join(tmp, "real")
				if err := os.MkdirAll(realDir, 0o755); err != nil {
					t.Fatalf("mkdir: %v", err)
				}
				linkDir := filepath.Join(tmp, "link")
				if err := os.Symlink(realDir, linkDir); err != nil {
					t.Skipf("symlink not supported: %v", err)
				}
				p := filepath.Join(linkDir, "child.txt")
				_, err := WriteFile(t.Context(), WriteFileArgs{Path: p, Content: "x", CreateParents: false})
				if err == nil {
					t.Fatalf("expected error")
				}
			},
		},
		{
			name: "refuses_non_regular_destination_unix_fifo",
			run: func(t *testing.T) {
				t.Helper()
				if runtime.GOOS == toolutil.GOOSWindows {
					t.Skip("no mkfifo on Windows")
				}
				tmp := t.TempDir()
				p := filepath.Join(tmp, "fifo")
				if err := syscall.Mkfifo(p, 0o600); err != nil {
					t.Skipf("mkfifo not supported: %v", err)
				}
				_, err := WriteFile(t.Context(), WriteFileArgs{Path: p, Content: "x", Overwrite: true})
				if err == nil {
					t.Fatalf("expected error")
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			tt.run(t)
		})
	}
}
