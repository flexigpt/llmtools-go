package fstool

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/flexigpt/llmtools-go/internal/fileutil"
	"github.com/flexigpt/llmtools-go/internal/toolutil"
	"github.com/flexigpt/llmtools-go/spec"
)

const writeFileFuncID spec.FuncID = "github.com/flexigpt/llmtools-go/fstool/writefile.WriteFile"

var writeFileTool = spec.Tool{
	SchemaVersion: spec.SchemaVersion,
	ID:            "019c04cf-72ec-7eed-ab2a-45e6fb9e1a86",
	Slug:          "writefile",
	Version:       "v1.0.0",
	DisplayName:   "Write file",
	Description:   "Write a file to disk. encoding=text writes UTF-8; binary expects base64 string as input and writes raw bytes.",
	Tags:          []string{"fs"},

	ArgSchema: spec.JSONSchema(`{
"$schema": "http://json-schema.org/draft-07/schema#",
"type": "object",
"properties": {
	"path": {
		"type": "string",
		"description": "Absolute path of the file to write."
	},
	"encoding": {
		"type": "string",
		"enum": ["text", "binary"],
		"description": "Write mode.",
		"default": "text"
	},
	"content": {
		"type": "string",
		"description": "If encoding=text, UTF-8 content. If encoding=binary, base64-encoded bytes."
	},
	"overwrite": {
		"type": "boolean",
		"description": "If false and the file exists, return an error.",
		"default": false
	},
	"createParents": {
		"type": "boolean",
		"description": "If true, create missing parent directories. Max new directories created is 8.",
		"default": false
	}
},
"required": ["path", "content"],
"additionalProperties": false
}`),

	GoImpl: spec.GoToolImpl{FuncID: writeFileFuncID},

	CreatedAt:  spec.SchemaStartTime,
	ModifiedAt: spec.SchemaStartTime,
}

func WriteFileTool() spec.Tool {
	return toolutil.CloneTool(writeFileTool)
}

type WriteFileArgs struct {
	Path          string `json:"path"`
	Encoding      string `json:"encoding,omitempty"` // "text"(default) | "binary"
	Content       string `json:"content"`
	Overwrite     bool   `json:"overwrite,omitempty"`
	CreateParents bool   `json:"createParents,omitempty"`
}

type WriteFileOut struct {
	Path         string `json:"path"`
	BytesWritten int64  `json:"bytesWritten"`
}

func WriteFile(ctx context.Context, args WriteFileArgs) (*WriteFileOut, error) {
	return toolutil.WithRecoveryResp(func() (*WriteFileOut, error) {
		return writeFile(ctx, args)
	})
}

func writeFile(ctx context.Context, args WriteFileArgs) (*WriteFileOut, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	p, err := fileutil.NormalizeAbsPath(strings.TrimSpace(args.Path))
	if err != nil {
		return nil, err
	}

	enc := fileutil.ReadEncoding(strings.ToLower(strings.TrimSpace(args.Encoding)))

	if enc == "" {
		enc = fileutil.ReadEncodingText
	}
	if enc != fileutil.ReadEncodingText && enc != fileutil.ReadEncodingBinary {
		return nil, errors.New(`encoding must be "text" or "binary"`)
	}

	// Decode/validate content.
	var data []byte
	switch enc {
	case fileutil.ReadEncodingText:
		// Content is required by schema, but empty string is a valid payload.
		if !utf8.ValidString(args.Content) {
			return nil, errors.New("content is not valid UTF-8")
		}
		data = []byte(args.Content)
	case fileutil.ReadEncodingBinary:
		b64 := strings.TrimSpace(args.Content)
		// Pre-check decoded size to avoid huge allocations.
		if int64(base64.StdEncoding.DecodedLen(len(b64))) > toolutil.MaxFileWriteBytes {
			return nil, fmt.Errorf("content too large (decoded > %d bytes)", toolutil.MaxFileWriteBytes)
		}
		decoded, derr := base64.StdEncoding.DecodeString(b64)
		if derr != nil {
			return nil, fmt.Errorf("invalid base64 content: %w", derr)
		}
		data = decoded
	}

	if int64(len(data)) > toolutil.MaxFileWriteBytes {
		return nil, fmt.Errorf("content too large (%d bytes; max %d)", len(data), toolutil.MaxFileWriteBytes)
	}

	parent := filepath.Dir(p)
	if parent == "" || parent == "." {
		// With absolute paths this should not happen, but keep it defensive.
		return nil, fileutil.ErrInvalidPath
	}

	if args.CreateParents {
		_, err := fileutil.EnsureDirNoSymlink(parent, 8 /*max new dirs*/)
		if err != nil {
			return nil, err
		}

	} else {
		if err := fileutil.VerifyDirNoSymlink(parent); err != nil {
			return nil, err
		}
	}

	// Validate existing destination if present.
	if st, err := os.Lstat(p); err == nil {
		if st.IsDir() {
			return nil, fmt.Errorf("path is a directory, not a file: %s", p)
		}
		// Refuse special files (device nodes, pipes, sockets, etc.)
		if !st.Mode().IsRegular() && (st.Mode()&os.ModeSymlink) == 0 {
			return nil, fmt.Errorf("refusing to write to non-regular file: %s", p)
		}
		if !args.Overwrite {
			return nil, fmt.Errorf("file already exists and overwrite=false: %s", p)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}

	if err := fileutil.WriteFileAtomicBytes(p, data, 0o600, args.Overwrite); err != nil {
		// Provide stable tool error message for the most common case.
		if !args.Overwrite && errors.Is(err, os.ErrExist) {
			return nil, fmt.Errorf("file already exists and overwrite=false: %s", p)
		}
		return nil, err
	}
	return &WriteFileOut{
		Path:         p,
		BytesWritten: int64(len(data)),
	}, nil
}
