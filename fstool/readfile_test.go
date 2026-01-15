package fstool

import (
	"context"
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/flexigpt/llmtools-go/spec"
)

// TestReadFile covers happy, error, and boundary cases for ReadFile.
func TestReadFile(t *testing.T) {
	t.Parallel()
	tmpDir := t.TempDir()

	textFile := filepath.Join(tmpDir, "file.txt")
	binaryFile := filepath.Join(tmpDir, "file.bin")
	imageFile := filepath.Join(tmpDir, "image.png")

	if err := os.WriteFile(textFile, []byte("hello world"), 0o600); err != nil {
		t.Fatalf("write textFile: %v", err)
	}
	binData := []byte{0x00, 0x01, 0x02, 0x03}
	if err := os.WriteFile(binaryFile, binData, 0o600); err != nil {
		t.Fatalf("write binaryFile: %v", err)
	}
	imgData := []byte{0x11, 0x22, 0x33}
	if err := os.WriteFile(imageFile, imgData, 0o600); err != nil {
		t.Fatalf("write imageFile: %v", err)
	}

	type testCase struct {
		name          string
		args          ReadFileArgs
		wantErr       bool
		wantKind      spec.ToolStoreOutputKind
		wantText      string
		wantFileName  string
		wantFileMIME  string
		wantImageName string
		wantMIMEPref  string
		wantBinary    []byte // expected raw bytes after base64 decoding (for file/image)
	}

	tests := []testCase{
		{
			name:    "Missing path returns error",
			args:    ReadFileArgs{},
			wantErr: true,
		},
		{
			name:    "Nonexistent file returns error",
			args:    ReadFileArgs{Path: filepath.Join(tmpDir, "nope.txt")},
			wantErr: true,
		},
		{
			name:     "Read text file as text",
			args:     ReadFileArgs{Path: textFile, Encoding: "text"},
			wantKind: spec.ToolStoreOutputKindText,
			wantText: "hello world",
		},
		{
			name:     "Read text file with default encoding",
			args:     ReadFileArgs{Path: textFile},
			wantKind: spec.ToolStoreOutputKindText,
			wantText: "hello world",
		},
		{
			name:         "Read binary file as binary -> file output",
			args:         ReadFileArgs{Path: binaryFile, Encoding: "binary"},
			wantKind:     spec.ToolStoreOutputKindFile,
			wantFileName: "file.bin",
			// Mime from ReadFile: ".bin" -> TypeByExtension("") => application/octet-stream.
			wantFileMIME: "application/octet-stream",
			wantBinary:   binData,
		},
		{
			name:          "Read image file as binary -> image output",
			args:          ReadFileArgs{Path: imageFile, Encoding: "binary"},
			wantKind:      spec.ToolStoreOutputKindImage,
			wantImageName: "image.png",
			wantMIMEPref:  "image/",
			wantBinary:    imgData,
		},
		{
			name:    "Invalid encoding returns error",
			args:    ReadFileArgs{Path: textFile, Encoding: "foo"},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			outs, err := ReadFile(context.Background(), tt.args)
			if (err != nil) != tt.wantErr {
				t.Fatalf("ReadFile error = %v, wantErr = %v", err, tt.wantErr)
			}
			if tt.wantErr {
				if len(outs) != 0 {
					t.Fatalf("expected no outputs on error, got %#v", outs)
				}
				return
			}

			if len(outs) != 1 {
				t.Fatalf("expected exactly 1 output, got %d: %#v", len(outs), outs)
			}
			out := outs[0]

			if out.Kind != tt.wantKind {
				t.Fatalf("Kind = %q, want %q", out.Kind, tt.wantKind)
			}

			switch tt.wantKind {
			case spec.ToolStoreOutputKindText:
				if out.TextItem == nil {
					t.Fatalf("TextItem is nil for text output")
				}
				if out.ImageItem != nil || out.FileItem != nil {
					t.Fatalf("unexpected non-nil image/file items in text output: %#v", out)
				}
				if out.TextItem.Text != tt.wantText {
					t.Fatalf("Text = %q, want %q", out.TextItem.Text, tt.wantText)
				}

			case spec.ToolStoreOutputKindFile:
				if out.FileItem == nil {
					t.Fatalf("FileItem is nil for file output")
				}
				if out.TextItem != nil || out.ImageItem != nil {
					t.Fatalf("unexpected non-nil text/image items in file output: %#v", out)
				}
				if tt.wantFileName != "" && out.FileItem.FileName != tt.wantFileName {
					t.Fatalf("FileName = %q, want %q", out.FileItem.FileName, tt.wantFileName)
				}
				if tt.wantFileMIME != "" && out.FileItem.FileMIME != tt.wantFileMIME {
					t.Fatalf("FileMIME = %q, want %q", out.FileItem.FileMIME, tt.wantFileMIME)
				}
				if tt.wantBinary != nil {
					raw, err := base64.StdEncoding.DecodeString(out.FileItem.FileData)
					if err != nil {
						t.Fatalf("FileData not valid base64: %v", err)
					}
					if len(raw) != len(tt.wantBinary) {
						t.Fatalf("decoded binary len=%d, want %d", len(raw), len(tt.wantBinary))
					}
					for i := range raw {
						if raw[i] != tt.wantBinary[i] {
							t.Fatalf("decoded[%d] = %d, want %d", i, raw[i], tt.wantBinary[i])
						}
					}
				}

			case spec.ToolStoreOutputKindImage:
				if out.ImageItem == nil {
					t.Fatalf("ImageItem is nil for image output")
				}
				if out.TextItem != nil || out.FileItem != nil {
					t.Fatalf("unexpected non-nil text/file items in image output: %#v", out)
				}
				if tt.wantImageName != "" && out.ImageItem.ImageName != tt.wantImageName {
					t.Fatalf("ImageName = %q, want %q", out.ImageItem.ImageName, tt.wantImageName)
				}
				if tt.wantMIMEPref != "" && !strings.HasPrefix(out.ImageItem.ImageMIME, tt.wantMIMEPref) {
					t.Fatalf("ImageMIME = %q, want prefix %q", out.ImageItem.ImageMIME, tt.wantMIMEPref)
				}
				if tt.wantBinary != nil {
					raw, err := base64.StdEncoding.DecodeString(out.ImageItem.ImageData)
					if err != nil {
						t.Fatalf("ImageData not valid base64: %v", err)
					}
					if len(raw) != len(tt.wantBinary) {
						t.Fatalf("decoded binary len=%d, want %d", len(raw), len(tt.wantBinary))
					}
					for i := range raw {
						if raw[i] != tt.wantBinary[i] {
							t.Fatalf("decoded[%d] = %d, want %d", i, raw[i], tt.wantBinary[i])
						}
					}
				}

			default:
				t.Fatalf("unexpected output kind: %q", out.Kind)
			}
		})
	}
}
