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
	"testing"

	"github.com/flexigpt/llmtools-go/internal/toolutil"
)

// TestReadFile covers happy, error, and boundary cases for ReadFile.
func TestReadFile(t *testing.T) {
	t.Parallel()
	writeFile := func(t *testing.T, p string, data []byte) {
		t.Helper()
		if err := os.WriteFile(p, data, 0o600); err != nil {
			t.Fatalf("WriteFile(%q): %v", p, err)
		}
	}

	type tc struct {
		name          string
		ctx           func(t *testing.T) context.Context
		args          func(t *testing.T) ReadFileArgs
		wantErr       bool
		wantCanceled  bool
		wantErrSubstr string
		wantKind      string // "text" | "file" | "image"
		wantText      string
		wantFileName  string
		wantFileMIME  string
		wantImageName string
		wantMIMEPref  string
		wantBinary    []byte
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
			args: func(t *testing.T) ReadFileArgs {
				t.Helper()
				return ReadFileArgs{Path: "whatever.txt"}
			},
			wantErr:      true,
			wantCanceled: true,
		},
		{
			name: "missing_path_errors",
			args: func(t *testing.T) ReadFileArgs {
				t.Helper()
				return ReadFileArgs{}
			},
			wantErr: true,
		},
		{
			name: "nonexistent_file_errors",
			args: func(t *testing.T) ReadFileArgs {
				t.Helper()
				tmp := t.TempDir()
				return ReadFileArgs{Path: filepath.Join(tmp, "nope.txt")}
			},
			wantErr:       true,
			wantErrSubstr: "does not exist",
		},
		{
			name: "read_text_default_encoding",
			args: func(t *testing.T) ReadFileArgs {
				t.Helper()
				tmp := t.TempDir()
				p := filepath.Join(tmp, "file.txt")
				writeFile(t, p, []byte("hello world"))
				return ReadFileArgs{Path: p}
			},
			wantKind: "text",
			wantText: "hello world",
		},
		{
			name: "read_text_encoding_is_trimmed_and_case_insensitive",
			args: func(t *testing.T) ReadFileArgs {
				t.Helper()
				tmp := t.TempDir()
				p := filepath.Join(tmp, "file.txt")
				writeFile(t, p, []byte("hello world"))
				return ReadFileArgs{Path: p, Encoding: "  TeXt "}
			},
			wantKind: "text",
			wantText: "hello world",
		},
		{
			name: "read_binary_file_as_binary_returns_file_union",
			args: func(t *testing.T) ReadFileArgs {
				t.Helper()
				tmp := t.TempDir()
				p := filepath.Join(tmp, "file.bin")
				writeFile(t, p, []byte{0x00, 0x01, 0x02, 0x03})
				return ReadFileArgs{Path: p, Encoding: "binary"}
			},
			wantKind:     "file",
			wantFileName: "file.bin",
			wantFileMIME: "application/octet-stream",
			wantBinary:   []byte{0x00, 0x01, 0x02, 0x03},
		},
		{
			name: "read_png_as_binary_returns_image_union",
			args: func(t *testing.T) ReadFileArgs {
				t.Helper()
				tmp := t.TempDir()
				p := filepath.Join(tmp, "image.png")
				writeFile(t, p, []byte{0x11, 0x22, 0x33})
				return ReadFileArgs{Path: p, Encoding: "binary"}
			},
			wantKind:      "image",
			wantImageName: "image.png",
			wantMIMEPref:  "image/",
			wantBinary:    []byte{0x11, 0x22, 0x33},
		},
		{
			name: "invalid_encoding_errors",
			args: func(t *testing.T) ReadFileArgs {
				t.Helper()
				tmp := t.TempDir()
				p := filepath.Join(tmp, "file.txt")
				writeFile(t, p, []byte("x"))
				return ReadFileArgs{Path: p, Encoding: "foo"}
			},
			wantErr:       true,
			wantErrSubstr: `encoding must be "text" or "binary"`,
		},
		{
			name: "read_non_text_as_text_errors",
			args: func(t *testing.T) ReadFileArgs {
				t.Helper()
				tmp := t.TempDir()
				p := filepath.Join(tmp, "file.bin")
				writeFile(t, p, []byte{0x00, 0x01, 0x02})
				return ReadFileArgs{Path: p, Encoding: "text"}
			},
			wantErr:       true,
			wantErrSubstr: "cannot read non-text file",
		},
		{
			name: "read_invalid_utf8_text_errors",
			args: func(t *testing.T) ReadFileArgs {
				t.Helper()
				tmp := t.TempDir()
				p := filepath.Join(tmp, "bad.txt")
				writeFile(t, p, []byte{0xff, 0xfe})
				return ReadFileArgs{Path: p, Encoding: "text"}
			},
			wantErr:       true,
			wantErrSubstr: "not valid UTF-8",
		},
		{
			name: "symlink_file_is_refused",
			args: func(t *testing.T) ReadFileArgs {
				t.Helper()
				if runtime.GOOS == toolutil.GOOSWindows {
					t.Skip("symlink often requires privileges on Windows")
				}
				tmp := t.TempDir()
				target := filepath.Join(tmp, "target.txt")
				writeFile(t, target, []byte("ok"))
				link := filepath.Join(tmp, "link.txt")
				if err := os.Symlink(target, link); err != nil {
					t.Skipf("symlink not available: %v", err)
				}
				return ReadFileArgs{Path: link, Encoding: "text"}
			},
			wantErr: true,
		},
		{
			name: "symlink_parent_component_is_refused",
			args: func(t *testing.T) ReadFileArgs {
				t.Helper()
				if runtime.GOOS == toolutil.GOOSWindows {
					t.Skip("symlink often requires privileges on Windows")
				}
				tmp := t.TempDir()
				realDir := filepath.Join(tmp, "real")
				if err := os.MkdirAll(realDir, 0o755); err != nil {
					t.Fatalf("MkdirAll: %v", err)
				}
				target := filepath.Join(realDir, "file.txt")
				writeFile(t, target, []byte("ok"))
				linkDir := filepath.Join(tmp, "linkdir")
				if err := os.Symlink(realDir, linkDir); err != nil {
					t.Skipf("symlink not available: %v", err)
				}
				return ReadFileArgs{Path: filepath.Join(linkDir, "file.txt"), Encoding: "text"}
			},
			wantErr: true,
		},
	}

	decode := func(t *testing.T, s string) []byte {
		t.Helper()
		b, err := base64.StdEncoding.DecodeString(s)
		if err != nil {
			t.Fatalf("invalid base64: %v", err)
		}
		return b
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ctx := t.Context()
			if tt.ctx != nil {
				ctx = tt.ctx(t)
			}
			outs, err := ReadFile(ctx, tt.args(t))

			if (err != nil) != tt.wantErr {
				t.Fatalf("err=%v wantErr=%v", err, tt.wantErr)
			}
			if err != nil {
				if tt.wantCanceled && !errors.Is(err, context.Canceled) {
					t.Fatalf("expected context.Canceled, got %v", err)
				}
				if tt.wantErrSubstr != "" && !strings.Contains(err.Error(), tt.wantErrSubstr) {
					t.Fatalf("err=%q does not contain %q", err.Error(), tt.wantErrSubstr)
				}
				if len(outs) != 0 {
					t.Fatalf("expected no outputs on error, got %#v", outs)
				}
				return
			}

			if len(outs) != 1 {
				t.Fatalf("expected exactly 1 output, got %d: %#v", len(outs), outs)
			}
			out := outs[0]
			if tt.wantKind != "" && string(out.Kind) != tt.wantKind {
				t.Fatalf("Kind=%q want %q", string(out.Kind), tt.wantKind)
			}

			switch tt.wantKind {
			case "text":
				if out.TextItem == nil || out.ImageItem != nil || out.FileItem != nil {
					t.Fatalf("unexpected union shape: %#v", out)
				}
				if out.TextItem.Text != tt.wantText {
					t.Fatalf("Text=%q want %q", out.TextItem.Text, tt.wantText)
				}
			case "file":
				if out.FileItem == nil || out.TextItem != nil || out.ImageItem != nil {
					t.Fatalf("unexpected union shape: %#v", out)
				}
				if tt.wantFileName != "" && out.FileItem.FileName != tt.wantFileName {
					t.Fatalf("FileName=%q want %q", out.FileItem.FileName, tt.wantFileName)
				}
				if tt.wantFileMIME != "" && out.FileItem.FileMIME != tt.wantFileMIME {
					t.Fatalf("FileMIME=%q want %q", out.FileItem.FileMIME, tt.wantFileMIME)
				}
				if tt.wantBinary != nil {
					raw := decode(t, out.FileItem.FileData)
					if !bytes.Equal(raw, tt.wantBinary) {
						t.Fatalf("decoded bytes=%v want %v", raw, tt.wantBinary)
					}
				}
			case "image":
				if out.ImageItem == nil || out.TextItem != nil || out.FileItem != nil {
					t.Fatalf("unexpected union shape: %#v", out)
				}
				if tt.wantImageName != "" && out.ImageItem.ImageName != tt.wantImageName {
					t.Fatalf("ImageName=%q want %q", out.ImageItem.ImageName, tt.wantImageName)
				}
				if tt.wantMIMEPref != "" && !strings.HasPrefix(out.ImageItem.ImageMIME, tt.wantMIMEPref) {
					t.Fatalf("ImageMIME=%q want prefix %q", out.ImageItem.ImageMIME, tt.wantMIMEPref)
				}
				if tt.wantBinary != nil {
					raw := decode(t, out.ImageItem.ImageData)
					if !bytes.Equal(raw, tt.wantBinary) {
						t.Fatalf("decoded bytes=%v want %v", raw, tt.wantBinary)
					}
				}
			}
		})
	}
}
