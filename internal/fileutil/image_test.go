package fileutil

import (
	"bytes"
	"encoding/base64"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/flexigpt/llmtools-go/internal/toolutil"
)

func TestReadImage(t *testing.T) {
	dir := t.TempDir()

	// Build a small 2x3 PNG.
	img := image.NewRGBA(image.Rect(0, 0, 2, 3))
	img.Set(0, 0, color.RGBA{255, 0, 0, 255})

	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("png encode: %v", err)
	}
	pngBytes := buf.Bytes()

	imgPath := filepath.Join(dir, "img.png")
	mustWriteBytes(t, imgPath, pngBytes)

	tests := []struct {
		name        string
		path        string
		includeB64  bool
		wantErr     bool
		errContains string

		wantExists bool
		wantIsDir  bool
		wantW      int
		wantH      int
		wantFmt    string
		wantMIME   MIMEType
		wantB64    bool
	}{
		{
			name:        "empty path error",
			path:        "",
			wantErr:     true,
			errContains: ErrInvalidPath.Error(),
		},
		{
			name:       "nonexistent returns Exists=false and no error",
			path:       filepath.Join(dir, "missing.png"),
			wantExists: false,
			wantIsDir:  false,
		},
		{
			name:        "directory path error",
			path:        dir,
			wantErr:     true,
			errContains: "expected file",
		},
		{
			name:       "png without base64",
			path:       imgPath,
			includeB64: false,
			wantExists: true,
			wantIsDir:  false,
			wantW:      2,
			wantH:      3,
			wantFmt:    "png",
			wantMIME:   MIMEImagePNG,
			wantB64:    false,
		},
		{
			name:       "png with base64",
			path:       imgPath,
			includeB64: true,
			wantExists: true,
			wantIsDir:  false,
			wantW:      2,
			wantH:      3,
			wantFmt:    "png",
			wantMIME:   MIMEImagePNG,
			wantB64:    true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			out, err := ReadImage(tc.path, tc.includeB64, toolutil.MaxFileReadBytes)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error, got nil (out=%+v)", out)
				}
				if tc.errContains != "" && !strings.Contains(err.Error(), tc.errContains) {
					t.Fatalf("error %q does not contain %q", err.Error(), tc.errContains)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if out.Exists != tc.wantExists {
				t.Fatalf("Exists=%v want=%v", out.Exists, tc.wantExists)
			}
			if out.IsDir != tc.wantIsDir {
				t.Fatalf("IsDir=%v want=%v", out.IsDir, tc.wantIsDir)
			}
			if !tc.wantExists {
				return
			}

			if out.Width != tc.wantW || out.Height != tc.wantH {
				t.Fatalf("WxH=%dx%d want=%dx%d", out.Width, out.Height, tc.wantW, tc.wantH)
			}
			if out.Format != tc.wantFmt {
				t.Fatalf("Format=%q want=%q", out.Format, tc.wantFmt)
			}
			if out.MIMEType != tc.wantMIME {
				t.Fatalf("MIMEType=%q want=%q", out.MIMEType, tc.wantMIME)
			}
			if tc.wantB64 {
				want := base64.StdEncoding.EncodeToString(pngBytes)
				if out.Base64Data != want {
					t.Fatalf("Base64Data mismatch: gotLen=%d wantLen=%d", len(out.Base64Data), len(want))
				}
			} else if out.Base64Data != "" {
				t.Fatalf("expected empty Base64Data, got %q", out.Base64Data)
			}
		})
	}

	_ = os.Remove(imgPath)
}
