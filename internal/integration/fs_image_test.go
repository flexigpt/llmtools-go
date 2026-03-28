package integration

import (
	"bytes"
	"encoding/base64"
	"path/filepath"
	"testing"

	"github.com/flexigpt/llmtools-go/fstool"
	"github.com/flexigpt/llmtools-go/imagetool"
	"github.com/flexigpt/llmtools-go/spec"
)

// Write/read binary, MIME, list/search, safe delete-to-trash, image flows.

func TestE2E_FS_MIME_Delete_And_ImageFlows(t *testing.T) {
	base := t.TempDir()
	h := newHarness(t, base)

	// Binary write/read + MIME detection.
	payload := []byte("hello-binary")
	b64 := base64.StdEncoding.EncodeToString(payload)

	binRel := filepath.Join("bin", "data.bin")
	_ = callJSON[fstool.WriteFileOut](t, h.r, "writefile", fstool.WriteFileArgs{
		Path:          binRel,
		Encoding:      "binary",
		Content:       b64,
		Overwrite:     false,
		CreateParents: true,
	})

	st := callJSON[fstool.StatPathOut](t, h.r, "statpath", fstool.StatPathArgs{Path: binRel})
	if !st.Exists || st.IsDir || st.SizeBytes != int64(len(payload)) {
		t.Fatalf("unexpected stat: %s", debugJSON(t, st))
	}

	m := callJSON[fstool.MIMEForPathOut](t, h.r, "mimeforpath", fstool.MIMEForPathArgs{Path: binRel})
	if m.MIMEType == "" {
		t.Fatalf("expected MIME type, got: %s", debugJSON(t, m))
	}

	readBin := callRaw(t, h.r, "readfile", fstool.ReadFileArgs{Path: binRel, Encoding: "binary"})
	fileItem := requireKind(t, readBin, spec.ToolOutputKindFile)
	if fileItem.FileItem == nil || fileItem.FileItem.FileData == "" {
		t.Fatalf("expected file output with base64 data, got: %+v", fileItem)
	}
	gotBytes, err := base64.StdEncoding.DecodeString(fileItem.FileItem.FileData)
	if err != nil {
		t.Fatalf("decode returned base64: %v", err)
	}
	if !bytes.Equal(gotBytes, payload) {
		t.Fatalf("binary payload mismatch: got=%q want=%q", string(gotBytes), string(payload))
	}

	// "listdirectory + searchfiles".
	_ = callJSON[fstool.WriteFileOut](t, h.r, "writefile", fstool.WriteFileArgs{
		Path:     "notes.txt",
		Encoding: "text",
		Content:  "TODO: one\n",
	})
	_ = callJSON[fstool.WriteFileOut](t, h.r, "writefile", fstool.WriteFileArgs{
		Path:     "more.txt",
		Encoding: "text",
		Content:  "TODO: two\n",
	})

	ls := callJSON[fstool.ListDirectoryOut](t, h.r, "listdirectory", fstool.ListDirectoryArgs{
		Path:     ".",
		NameGlob: "*.txt",
	})
	if len(ls.Items) < 2 {
		t.Fatalf("expected txt entries, got: %s", debugJSON(t, ls))
	}

	sf := callJSON[fstool.SearchFilesOut](t, h.r, "searchfiles", fstool.SearchFilesArgs{
		Root:       ".",
		Query:      "TODO: (one|two)",
		Regexp:     true,
		MaxResults: 100,
	})
	if sf.MatchCount < 2 {
		t.Fatalf("expected >=2 matches, got: %s", debugJSON(t, sf))
	}

	// "deletefile" (explicit trash dir to avoid touching system trash).
	trashRel := "trash"
	del := callJSON[fstool.DeleteFileOut](t, h.r, "deletefile", fstool.DeleteFileArgs{
		Path:     "notes.txt",
		TrashDir: trashRel,
	})

	orig := callJSON[fstool.StatPathOut](t, h.r, "statpath", fstool.StatPathArgs{Path: "notes.txt"})
	if orig.Exists {
		t.Fatalf("expected original to be gone after deletefile, got: %s", debugJSON(t, orig))
	}

	trashed := callJSON[fstool.StatPathOut](t, h.r, "statpath", fstool.StatPathArgs{Path: del.TrashedPath})
	if !trashed.Exists {
		t.Fatalf("expected trashed file to exist, got: %s", debugJSON(t, trashed))
	}

	// Image flow: write a 1x1 PNG and read metadata.
	// This is a known-valid 1x1 transparent PNG.
	const png1x1Base64 = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMB/axlD2kAAAAASUVORK5CYII="
	_ = callJSON[fstool.WriteFileOut](t, h.r, "writefile", fstool.WriteFileArgs{
		Path:     "pixel.png",
		Encoding: "binary",
		Content:  png1x1Base64,
	})

	imgMeta := callJSON[imagetool.ReadImageOut](t, h.r, "readimage", imagetool.ReadImageArgs{
		Path:              "pixel.png",
		IncludeBase64Data: false,
	})
	if !imgMeta.Exists || imgMeta.Width != 1 || imgMeta.Height != 1 {
		t.Fatalf("unexpected image metadata: %s", debugJSON(t, imgMeta))
	}

	// "readfile(binary)" should emit an image output for image/*.
	readImg := callRaw(t, h.r, "readfile", fstool.ReadFileArgs{Path: "pixel.png", Encoding: "binary"})
	imageItem := requireKind(t, readImg, spec.ToolOutputKindImage)
	if imageItem.ImageItem == nil || imageItem.ImageItem.ImageData == "" {
		t.Fatalf("expected image output with base64 data, got: %+v", imageItem)
	}
}
