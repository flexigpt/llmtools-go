package fstool

import (
	"context"
	"errors"
	"fmt"
	"mime"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/flexigpt/llmtools-go/internal/fileutil"
	"github.com/flexigpt/llmtools-go/internal/pdfutil"
	"github.com/flexigpt/llmtools-go/internal/toolutil"
	"github.com/flexigpt/llmtools-go/spec"
)

const readFileFuncID spec.FuncID = "github.com/flexigpt/llmtools-go/fstool/readfile.ReadFile"

var readFileTool = spec.Tool{
	SchemaVersion: spec.SchemaVersion,
	ID:            "018fe0f4-b8cd-7e55-82d5-9df0bd70e4ba",
	Slug:          "readfile",
	Version:       "v1.0.0",
	DisplayName:   "Read file",
	Description:   "Read a local file from disk and return its contents (text or base64).",
	Tags:          []string{"fs", "read"},

	ArgSchema: spec.JSONSchema(`{
"$schema": "http://json-schema.org/draft-07/schema#",
"type": "object",
"properties": {
	"path": {
		"type": "string",
		"description": "Absolute or relative path of the file to read."
	},
	"encoding": {
		"type": "string",
		"enum": ["text", "binary"],
		"description": "Return mode: \"text\" reads file as UTF-8, \"binary\" returns base64 string.",
		"default": "text"
	}
},
"required": ["path"],
"additionalProperties": false
}`),
	GoImpl: spec.GoToolImpl{FuncID: readFileFuncID},

	CreatedAt:  spec.SchemaStartTime,
	ModifiedAt: spec.SchemaStartTime,
}

func ReadFileTool() spec.Tool {
	return toolutil.CloneTool(readFileTool)
}

type ReadFileArgs struct {
	Path     string `json:"path"`               // required
	Encoding string `json:"encoding,omitempty"` // "text" (default) | "binary"
}

// ReadFile reads a file from disk and returns its contents.
// If Encoding == "binary" the output is base64-encoded.
func ReadFile(ctx context.Context, args ReadFileArgs) ([]spec.ToolStoreOutputUnion, error) {
	return toolutil.WithRecoveryResp(func() ([]spec.ToolStoreOutputUnion, error) {
		return readFile(ctx, args)
	})
}

func readFile(ctx context.Context, args ReadFileArgs) ([]spec.ToolStoreOutputUnion, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	// Normalize and validate encoding.
	enc := fileutil.ReadEncoding(strings.ToLower(strings.TrimSpace(args.Encoding)))
	if enc == "" {
		enc = fileutil.ReadEncodingText
	}
	if enc != fileutil.ReadEncodingText && enc != fileutil.ReadEncodingBinary {
		return nil, errors.New(`encoding must be "text" or "binary"`)
	}

	path := strings.TrimSpace(args.Path)
	if path == "" {
		return nil, fileutil.ErrInvalidPath
	}

	p, err := fileutil.NormalizePath(path)
	if err != nil {
		return nil, err
	}

	// Refuse symlink traversal (file and parent dirs), and require regular file.
	st, err := fileutil.RequireExistingRegularFileNoSymlink(p)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("path does not exist: %s", p)
		}
		return nil, err
	}
	if st.Size() > toolutil.MaxFileReadBytes {
		return nil, fmt.Errorf(
			"file %q is too large to read (%d bytes; max %d)",
			p, st.Size(), toolutil.MaxFileReadBytes,
		)
	}

	// Detect MIME / extension where possible.
	mimeType, extMode, _, mimeErr := fileutil.MIMEForLocalFile(p)
	ext := strings.ToLower(filepath.Ext(p))

	isPDFByExt := ext == string(fileutil.ExtPDF)
	isPDFByMime := mimeErr == nil && mimeType == fileutil.MIMEApplicationPDF
	isPDF := isPDFByExt || isPDFByMime

	if enc == fileutil.ReadEncodingText {
		// For non-PDFs, fail if MIME detection fails (conservative).
		// For PDFs, allow text extraction even if MIME sniffing fails,
		// as long as the extension is .pdf.
		if !isPDF && mimeErr != nil {
			return nil, fmt.Errorf("cannot read %q as text (MIME detection failed: %w)", p, mimeErr)
		}

		if isPDF {
			// PDF: use the same extraction logic as attachments.
			// Extraction itself is limited to toolutil.MaxFileReadBytes via LimitedReader.
			text, err := pdfutil.ExtractPDFTextSafe(ctx, p, toolutil.MaxFileReadBytes)
			if err != nil {
				return nil, err
			}

			return []spec.ToolStoreOutputUnion{
				{
					Kind: spec.ToolStoreOutputKindText,
					TextItem: &spec.ToolStoreOutputText{
						Text: text,
					},
				},
			}, nil
		}

		// Non‑PDF: only allow clearly text-like files.
		if extMode != fileutil.ExtensionModeText {
			return nil, fmt.Errorf(
				"cannot read non-text file %q as text; use encoding \"binary\" instead",
				p,
			)
		}

		// Normal text file: read and validate UTF‑8.
		data, err := fileutil.ReadFile(p, fileutil.ReadEncodingText, toolutil.MaxFileReadBytes)
		if err != nil {
			return nil, err
		}
		if !utf8.ValidString(data) {
			return nil, fmt.Errorf(
				"file %q is not valid UTF-8 text; use encoding \"binary\" instead",
				p,
			)
		}

		return []spec.ToolStoreOutputUnion{
			{
				Kind: spec.ToolStoreOutputKindText,
				TextItem: &spec.ToolStoreOutputText{
					Text: data,
				},
			},
		}, nil
	}

	// Binary mode: base64-encode and return, like before.
	data, err := fileutil.ReadFile(p, fileutil.ReadEncodingBinary, toolutil.MaxFileReadBytes)
	if err != nil {
		return nil, err
	}

	baseName := filepath.Base(p)
	if baseName == "" {
		baseName = "file"
	}

	// Prefer MIMEForLocalFile result if available; otherwise fall back to extension mapping.
	var mt string
	if mimeErr == nil && mimeType != "" {
		mt = string(mimeType)
	} else {
		if ext == "" {
			ext = strings.ToLower(filepath.Ext(baseName))
		}
		mt = mime.TypeByExtension(ext)
	}
	if mt == "" {
		mt = "application/octet-stream"
	}

	if strings.HasPrefix(mt, "image/") {
		return []spec.ToolStoreOutputUnion{
			{
				Kind: spec.ToolStoreOutputKindImage,
				ImageItem: &spec.ToolStoreOutputImage{
					Detail:    spec.ImageDetailAuto,
					ImageName: baseName,
					ImageMIME: mt,
					ImageData: data, // base64-encoded
				},
			},
		}, nil
	}

	return []spec.ToolStoreOutputUnion{
		{
			Kind: spec.ToolStoreOutputKindFile,
			FileItem: &spec.ToolStoreOutputFile{
				FileName: baseName,
				FileMIME: mt,
				FileData: data, // base64-encoded
			},
		},
	}, nil
}
