package imagetool

import (
	"context"
	"time"

	"github.com/flexigpt/llmtools-go/internal/fileutil"
	"github.com/flexigpt/llmtools-go/internal/toolutil"
	"github.com/flexigpt/llmtools-go/spec"
)

const inspectImageFuncID spec.FuncID = "github.com/flexigpt/llmtools-go/imagetool/inspectimage.InspectImage"

var inspectImageTool = spec.Tool{
	SchemaVersion: spec.SchemaVersion,
	ID:            "018fe0f4-b8cd-7e55-82d5-9df0bd70e4be",
	Slug:          "inspectimage",
	Version:       "v1.0.0",
	DisplayName:   "Inspect image",
	Description:   "Return intrinsic metadata (dimensions, format, timestamps) for a local image file.",
	Tags:          []string{"image"},

	ArgSchema: spec.JSONSchema(`{
		"$schema": "http://json-schema.org/draft-07/schema#",
		"type": "object",
		"properties": {
			"path": {
				"type": "string",
				"description": "Absolute or relative path of the image to inspect."
			}
		},
		"required": ["path"],
		"additionalProperties": false
	}`),
	GoImpl: spec.GoToolImpl{FuncID: inspectImageFuncID},

	CreatedAt:  spec.SchemaStartTime,
	ModifiedAt: spec.SchemaStartTime,
}

func InspectImageTool() spec.Tool {
	return toolutil.CloneTool(inspectImageTool)
}

type InspectImageArgs struct {
	Path string `json:"path"`
}

type InspectImageOut struct {
	Exists    bool       `json:"exists"`
	Width     int        `json:"width,omitempty"`
	Height    int        `json:"height,omitempty"`
	Format    string     `json:"format,omitempty"`
	SizeBytes int64      `json:"sizeBytes,omitempty"`
	ModTime   *time.Time `json:"modTime,omitempty"`
}

// InspectImage inspects an image file and returns its intrinsic metadata.
func InspectImage(ctx context.Context, args InspectImageArgs) (*InspectImageOut, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	info, err := fileutil.ReadImage(args.Path, false)
	if err != nil {
		return nil, err
	}
	return &InspectImageOut{
		Exists:    info.Exists,
		Width:     info.Width,
		Height:    info.Height,
		Format:    info.Format,
		SizeBytes: info.Size,
		ModTime:   info.ModTime,
	}, nil
}
