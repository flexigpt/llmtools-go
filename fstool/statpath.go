package fstool

import (
	"context"
	"time"

	"github.com/flexigpt/llmtools-go/internal/fileutil"
	"github.com/flexigpt/llmtools-go/internal/toolutil"
	"github.com/flexigpt/llmtools-go/spec"
)

const statPathFuncID spec.FuncID = "github.com/flexigpt/llmtools-go/fstool/statpath.StatPath"

var statPathTool = spec.Tool{
	SchemaVersion: spec.SchemaVersion,
	ID:            "018fe0f4-b8cd-7e55-82d5-9df0bd70e4bd",
	Slug:          "statpath",
	Version:       "v1.0.0",
	DisplayName:   "Inspect path",
	Description:   "Return size, timestamps, and basic metadata for a file-system path without modifying it.",
	Tags:          []string{"fs", "stat"},

	ArgSchema: spec.JSONSchema(`{
		"$schema": "http://json-schema.org/draft-07/schema#",
		"type": "object",
		"properties": {
			"path": {
				"type": "string",
				"description": "Absolute or relative path to inspect."
			}
		},
		"required": ["path"],
		"additionalProperties": false
	}`),
	GoImpl: spec.GoToolImpl{FuncID: statPathFuncID},

	CreatedAt:  spec.SchemaStartTime,
	ModifiedAt: spec.SchemaStartTime,
}

func StatPathTool() spec.Tool {
	return toolutil.CloneTool(statPathTool)
}

type StatPathArgs struct {
	Path string `json:"path"`
}

type StatPathOut struct {
	Path      string     `json:"path"`
	Name      string     `json:"name"`
	Exists    bool       `json:"exists"`
	IsDir     bool       `json:"isDir"`
	SizeBytes int64      `json:"sizeBytes,omitempty"`
	ModTime   *time.Time `json:"modTime,omitempty"`
}

// StatPath returns basic metadata for the supplied path without mutating the file system.
func StatPath(ctx context.Context, args StatPathArgs) (*StatPathOut, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	pathInfo, err := fileutil.StatPath(args.Path)
	if err != nil {
		return nil, err
	}
	return &StatPathOut{
		Path:      pathInfo.Path,
		Name:      pathInfo.Name,
		Exists:    pathInfo.Exists,
		IsDir:     pathInfo.IsDir,
		SizeBytes: pathInfo.Size,
		ModTime:   pathInfo.ModTime,
	}, nil
}
