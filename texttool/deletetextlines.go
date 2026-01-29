package texttool

import (
	"context"
	"errors"
	"fmt"

	"github.com/flexigpt/llmtools-go/internal/fileutil"
	"github.com/flexigpt/llmtools-go/internal/toolutil"
	"github.com/flexigpt/llmtools-go/spec"
)

const deleteTextLinesFuncID spec.FuncID = "github.com/flexigpt/llmtools-go/texttool/deletetextlines.DeleteTextLines"

var deleteTextLinesTool = spec.Tool{
	SchemaVersion: spec.SchemaVersion,
	ID:            "019c04d3-354f-73dc-909c-1b79f73d0f55",
	Slug:          "deletetextlines",
	Version:       "v1.0.0",
	DisplayName:   "Delete text lines",
	Description:   "Delete one or more exact line-block occurrences from a UTF-8 text file. Matching compares TrimSpace(line). Use beforeLines/afterLines as immediate-adjacent context to disambiguate.",
	Tags:          []string{"text"},

	ArgSchema: spec.JSONSchema(`{
"$schema": "http://json-schema.org/draft-07/schema#",
"type": "object",
"properties": {
	"path": {
		"type": "string",
		"description": "Absolute path of the UTF-8 text file."
	},
	"matchLines": {
		"type": "array",
		"items": { "type": "string" },
		"minItems": 1,
		"description": "Block of lines to delete. Newline characters inside items are allowed and are treated as line breaks."
	},
	"beforeLines": {
		"type": "array",
		"items": { "type": "string" },
		"minItems": 1,
		"description": "Optional block that must appear immediately before matchLines."
	},
	"afterLines": {
		"type": "array",
		"items": { "type": "string" },
		"minItems": 1,
		"description": "Optional block that must appear immediately after matchLines."
	},
	"expectedDeletions": {
		"type": "integer",
		"minimum": 1,
		"default": 1,
		"description": "Fail if the number of matched blocks deleted != this value."
	}
},
"required": ["path", "matchLines"],
"additionalProperties": false
}`),

	GoImpl: spec.GoToolImpl{FuncID: deleteTextLinesFuncID},

	CreatedAt:  spec.SchemaStartTime,
	ModifiedAt: spec.SchemaStartTime,
}

func DeleteTextLinesTool() spec.Tool {
	return toolutil.CloneTool(deleteTextLinesTool)
}

type DeleteTextLinesArgs struct {
	Path              string   `json:"path"`
	MatchLines        []string `json:"matchLines"`
	BeforeLines       []string `json:"beforeLines,omitempty"`
	AfterLines        []string `json:"afterLines,omitempty"`
	ExpectedDeletions int      `json:"expectedDeletions,omitempty"` // default 1
}

type DeleteTextLinesOut struct {
	DeletionsMade  int   `json:"deletionsMade"`
	DeletedAtLines []int `json:"deletedAtLines"` // 1-based start line of each deleted block
}

// DeleteTextLines deletes occurrences of MatchLines from a UTF‑8 file.
// Behavior notes (entry point):
//   - The file must exist, be a regular file, not a symlink, and be valid UTF‑8.
//   - Matching is line-wise using strings.TrimSpace on each line.
//   - If ExpectedDeletions is set, the tool fails unless exactly that many deletions would be made.
//   - Writes are atomic (temp file + fsync + rename) and preserve newline style and final newline.
func DeleteTextLines(ctx context.Context, args DeleteTextLinesArgs) (*DeleteTextLinesOut, error) {
	return toolutil.WithRecoveryResp(func() (*DeleteTextLinesOut, error) {
		return deleteTextLines(ctx, args)
	})
}

func deleteTextLines(ctx context.Context, args DeleteTextLinesArgs) (*DeleteTextLinesOut, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	path, err := fileutil.NormalizeAbsPath(args.Path)
	if err != nil {
		return nil, err
	}

	if len(args.MatchLines) == 0 {
		return nil, errors.New("matchLines is required")
	}

	matchLines := fileutil.NormalizeLineBlockInput(args.MatchLines)
	beforeLines := fileutil.NormalizeLineBlockInput(args.BeforeLines)
	afterLines := fileutil.NormalizeLineBlockInput(args.AfterLines)
	expected := args.ExpectedDeletions
	if expected <= 0 {
		expected = 1
	}

	tf, err := fileutil.ReadTextFileUTF8(path, toolutil.MaxTextProcessingBytes)
	if err != nil {
		return nil, err
	}

	matchIdxs := fileutil.FindTrimmedAdjacentBlockMatches(tf.Lines, beforeLines, matchLines, afterLines)
	if err := fileutil.EnsureNonOverlappingFixedWidth(matchIdxs, len(matchLines)); err != nil {
		return nil, err
	}
	if len(matchIdxs) != expected {
		return nil, fmt.Errorf(
			"delete match count mismatch: expected %d, found %d (provide tighter beforeLines/afterLines to disambiguate)",
			expected,
			len(matchIdxs),
		)
	}

	changed := len(matchIdxs) > 0
	if changed {
		// Delete from the end so earlier indices remain valid.
		for i := len(matchIdxs) - 1; i >= 0; i-- {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			start := matchIdxs[i]
			end := start + len(matchLines)

			tf.Lines = append(tf.Lines[:start], tf.Lines[end:]...)
		}

		// Preserve final newline behavior.
		outStr := tf.Render()
		if err := fileutil.WriteFileAtomicBytes(tf.Path, []byte(outStr), tf.Perm, true); err != nil {
			return nil, err
		}
	}

	deletedAt := make([]int, 0, len(matchIdxs))
	for _, idx := range matchIdxs {
		deletedAt = append(deletedAt, idx+1)
	}

	return &DeleteTextLinesOut{
		DeletionsMade:  len(matchIdxs),
		DeletedAtLines: deletedAt,
	}, nil
}
