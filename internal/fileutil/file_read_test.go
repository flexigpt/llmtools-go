package fileutil

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/flexigpt/llmtools-go/internal/toolutil"
)

func TestReadFile(t *testing.T) {
	dir := t.TempDir()

	textPath := filepath.Join(dir, "sample.txt")
	textContent := "Hello, world!\n"
	writeFile(t, textPath, textContent)

	nonExistentPath := filepath.Join(dir, "does_not_exist.txt")
	binaryContent := base64.StdEncoding.EncodeToString([]byte(textContent))

	tests := []struct {
		name            string
		path            string
		encoding        ReadEncoding
		want            string
		wantErr         bool
		wantErrContains string
		wantIsNotExist  bool
	}{
		{
			name:     "text encoding returns raw content",
			path:     textPath,
			encoding: ReadEncodingText,
			want:     textContent,
		},
		{
			name:     "binary encoding returns base64",
			path:     textPath,
			encoding: ReadEncodingBinary,
			want:     binaryContent,
		},
		{
			name:            "invalid encoding",
			path:            textPath,
			encoding:        ReadEncoding("invalid"),
			wantErr:         true,
			wantErrContains: `encoding must be "text" or "binary"`,
		},
		{
			name:            "empty encoding (zero value) is invalid",
			path:            textPath,
			encoding:        ReadEncoding(""),
			wantErr:         true,
			wantErrContains: `encoding must be "text" or "binary"`,
		},
		{
			name:            "empty path",
			path:            "",
			encoding:        ReadEncodingText,
			wantErr:         true,
			wantErrContains: ErrInvalidPath.Error(),
		},
		{
			name:           "non-existent path",
			path:           nonExistentPath,
			encoding:       ReadEncodingText,
			wantErr:        true,
			wantIsNotExist: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ReadFile(tc.path, tc.encoding, toolutil.MaxFileReadBytes)

			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error, got nil")
				}
				if tc.wantErrContains != "" && !strings.Contains(err.Error(), tc.wantErrContains) {
					t.Fatalf("error %q does not contain %q", err.Error(), tc.wantErrContains)
				}
				if tc.wantIsNotExist && !os.IsNotExist(err) {
					t.Fatalf("expected a not-exist error, got: %v", err)
				}
				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.want {
				t.Errorf("ReadFile(%q) = %q, want %q", tc.path, got, tc.want)
			}
		})
	}
}

func TestReadFile_MaxBytes(t *testing.T) {
	dir := t.TempDir()

	p := filepath.Join(dir, "data.bin")
	data := []byte("hello") // 5 bytes
	mustWriteBytes(t, p, data)

	tests := []struct {
		name        string
		maxBytes    int64
		encoding    ReadEncoding
		want        string
		wantErr     bool
		errContains string
	}{
		{
			name:     "maxBytes zero means unlimited",
			maxBytes: 0,
			encoding: ReadEncodingText,
			want:     "hello",
		},
		{
			name:     "maxBytes exact size ok",
			maxBytes: 5,
			encoding: ReadEncodingText,
			want:     "hello",
		},
		{
			name:        "maxBytes smaller than size errors",
			maxBytes:    4,
			encoding:    ReadEncodingText,
			wantErr:     true,
			errContains: "exceeds maximum allowed size",
		},
		{
			name:     "binary encoding base64",
			maxBytes: 5,
			encoding: ReadEncodingBinary,
			want:     base64.StdEncoding.EncodeToString(data),
		},
		{
			name:     "negative maxBytes treated as unlimited",
			maxBytes: -1,
			encoding: ReadEncodingText,
			want:     "hello",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ReadFile(p, tc.encoding, tc.maxBytes)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error, got nil (got=%q)", got)
				}
				if tc.errContains != "" && !strings.Contains(err.Error(), tc.errContains) {
					t.Fatalf("error %q does not contain %q", err.Error(), tc.errContains)
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
