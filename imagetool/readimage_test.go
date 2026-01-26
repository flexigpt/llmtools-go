package imagetool

import (
	"encoding/base64"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"testing"
)

func TestReadImage(t *testing.T) {
	tmpDir := t.TempDir()
	imgPath := filepath.Join(tmpDir, "img.png")

	// Create a known 8x6 PNG.
	img := image.NewRGBA(image.Rect(0, 0, 8, 6))
	for y := range 6 {
		for x := range 8 {
			img.Set(x, y, color.RGBA{R: 255, A: 255})
		}
	}

	f, err := os.Create(imgPath)
	if err != nil {
		t.Fatalf("create image: %v", err)
	}
	if err := png.Encode(f, img); err != nil {
		_ = f.Close()
		t.Fatalf("encode png: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("close image file: %v", err)
	}

	t.Run("metadata only", func(t *testing.T) {
		out, err := ReadImage(t.Context(), ReadImageArgs{
			Path:              imgPath,
			IncludeBase64Data: false,
		})
		if err != nil {
			t.Fatalf("ReadImage error: %v", err)
		}
		if !out.Exists {
			t.Fatalf("expected image to exist")
		}

		if out.Width != 8 || out.Height != 6 {
			t.Fatalf("unexpected dimensions: %+v", out)
		}
		if out.Format != "png" {
			t.Fatalf("expected png format, got %q", out.Format)
		}
		if out.MIMEType == "" {
			t.Fatalf("expected MIMEType to be set")
		}
		if out.SizeBytes <= 0 {
			t.Fatalf("expected positive SizeBytes, got %d", out.SizeBytes)
		}
		if out.ModTime == nil {
			t.Fatalf("expected ModTime to be set")
		}
		if out.Base64Data != "" {
			t.Fatalf("expected Base64Data to be empty when IncludeBase64Data=false")
		}
	})

	t.Run("metadata + base64", func(t *testing.T) {
		out, err := ReadImage(t.Context(), ReadImageArgs{
			Path:              imgPath,
			IncludeBase64Data: true,
		})
		if err != nil {
			t.Fatalf("ReadImage error: %v", err)
		}
		if !out.Exists {
			t.Fatalf("expected image to exist")
		}
		if out.Base64Data == "" {
			t.Fatalf("expected Base64Data to be set when IncludeBase64Data=true")
		}

		raw, err := os.ReadFile(imgPath)
		if err != nil {
			t.Fatalf("read file: %v", err)
		}
		want := base64.StdEncoding.EncodeToString(raw)
		if out.Base64Data != want {
			t.Fatalf("base64 mismatch")
		}
	})

	t.Run("non-existent path is not an error", func(t *testing.T) {
		missing := filepath.Join(tmpDir, "missing.png")
		out, err := ReadImage(t.Context(), ReadImageArgs{Path: missing})
		if err != nil {
			t.Fatalf("expected nil error for missing file, got %v", err)
		}
		if out == nil {
			t.Fatalf("expected non-nil out")
		}
		if out.Exists {
			t.Fatalf("expected Exists=false for missing file")
		}
		// For missing files, metadata should be absent/zero values.
		if out.Base64Data != "" {
			t.Fatalf("expected no Base64Data for missing file")
		}
	})

	t.Run("non-image file errors", func(t *testing.T) {
		textPath := filepath.Join(tmpDir, "notimg.txt")
		if err := os.WriteFile(textPath, []byte("plain"), 0o600); err != nil {
			t.Fatalf("write text: %v", err)
		}
		if _, err := ReadImage(t.Context(), ReadImageArgs{Path: textPath}); err == nil {
			t.Fatalf("expected error for non-image file")
		}
	})

	t.Run("directory path errors", func(t *testing.T) {
		if _, err := ReadImage(t.Context(), ReadImageArgs{Path: tmpDir}); err == nil {
			t.Fatalf("expected error for directory path")
		}
	})

	t.Run("empty path errors", func(t *testing.T) {
		if _, err := ReadImage(t.Context(), ReadImageArgs{}); err == nil {
			t.Fatalf("expected error for empty path")
		}
	})
}
