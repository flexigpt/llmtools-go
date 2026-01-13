package fstool

import (
	"context"

	"github.com/ppipada/llmtools-go/internal/fileutil"
	"github.com/ppipada/llmtools-go/internal/toolutil"
	"github.com/ppipada/llmtools-go/spec"
)

const listDirectoryFuncID spec.FuncID = "github.com/ppipada/llmtools-go/fstool/listdirectory.ListDirectory"

var listDirectoryTool = spec.Tool{
	SchemaVersion: spec.SchemaVersion,
	ID:            "018fe0f4-b8cd-7e55-82d5-9df0bd70e4bb",
	Slug:          "listdir",
	Version:       "v1.0.0",
	DisplayName:   "List directory",
	Description:   "Return the names of files/directories at the given path (optionally filtered by glob).",
	Tags:          []string{"fs", "list"},

	ArgSchema: spec.JSONSchema(`{
		"$schema": "http://json-schema.org/draft-07/schema#",
		"type": "object",
		"properties": {
			"path": {
				"type": "string",
				"description": "Directory path to list.",
				"default": "."
			},
			"pattern": {
				"type": "string",
				"description": "Optional glob pattern (e.g. \"*.txt\") to filter results."
			}
		},
		"required": [],
		"additionalProperties": false
	}`),
	GoImpl: spec.GoToolImpl{FuncID: listDirectoryFuncID},

	CreatedAt:  spec.SchemaStartTime,
	ModifiedAt: spec.SchemaStartTime,
}

func ListDirectoryTool() spec.Tool {
	return toolutil.CloneTool(listDirectoryTool)
}

type ListDirectoryArgs struct {
	Path    string `json:"path,omitempty"`    // default "."
	Pattern string `json:"pattern,omitempty"` // Optional glob
}
type ListDirectoryOut struct {
	Entries []string `json:"entries"`
}

// ListDirectory lists files / dirs in Path. If Pattern is supplied, the
// results are filtered via filepath.Match.
func ListDirectory(ctx context.Context, args ListDirectoryArgs) (*ListDirectoryOut, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	entries, err := fileutil.ListDirectory(args.Path, args.Pattern)
	if err != nil {
		return nil, err
	}
	return &ListDirectoryOut{Entries: entries}, nil
}
