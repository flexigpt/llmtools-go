package texttool

import (
	"context"
	"fmt"

	"github.com/flexigpt/llmtools-go/internal/fileutil"
	"github.com/flexigpt/llmtools-go/internal/toolutil"
	"github.com/flexigpt/llmtools-go/spec"
)

const readTextRangeFuncID spec.FuncID = "github.com/flexigpt/llmtools-go/texttool/readtextrange.ReadTextRange"

var readTextRangeTool = spec.Tool{
	SchemaVersion: spec.SchemaVersion,
	ID:            "019c0973-ec5d-7dad-85b2-8048e02deaab",
	Slug:          "readtextrange",
	Version:       "v1.0.0",
	DisplayName:   "Read text range",

	Description: "Read a UTF-8 text file and return lines. Start and end marker lines can be provided to narrow the range.\n" +
		"Matching uses trimmed-space line comparisons.\n" +
		"Returned lines are not trimmed and have an associated line number.",
	Tags: []string{"text"},

	ArgSchema: spec.JSONSchema(`{
"$schema": "http://json-schema.org/draft-07/schema#",
"type": "object",
"properties": {
"path": {
	"type": "string",
	"description": "Absolute path of the UTF-8 text file."
},
"startMatchLines": {
	"type": "array",
	"items": { "type": "string" },
	"minItems": 1,
	"description": "Optional start marker block. Must match exactly once."
},
"endMatchLines": {
	"type": "array",
	"items": { "type": "string" },
	"minItems": 1,
	"description": "Optional end marker block. Must match exactly once."
}
},
"required": ["path"],
"additionalProperties": false
}`),

	GoImpl: spec.GoToolImpl{FuncID: readTextRangeFuncID},

	CreatedAt:  spec.SchemaStartTime,
	ModifiedAt: spec.SchemaStartTime,
}

func ReadTextRangeTool() spec.Tool { return toolutil.CloneTool(readTextRangeTool) }

// Hard cap to keep tool responses bounded (prevents massive JSON payloads).
// If the selected range exceeds this, the tool fails (no truncation).
const maxReadTextRangeOutputLines = 2000

type ReadTextRangeArgs struct {
	Path string `json:"path"`

	StartMatchLines []string `json:"startMatchLines,omitempty"`
	EndMatchLines   []string `json:"endMatchLines,omitempty"`
}

type ReadTextRangeLine struct {
	LineNumber int    `json:"lineNumber"` // 1-based
	Text       string `json:"text"`       // original file line (not trimmed)
}

type ReadTextRangeOut struct {
	StartLine     int                 `json:"startLine,omitempty"` // 1-based
	EndLine       int                 `json:"endLine,omitempty"`   // 1-based
	LinesReturned int                 `json:"linesReturned"`
	Lines         []ReadTextRangeLine `json:"lines"`
}

// ReadTextRange reads a UTF‑8 file and returns a bounded range of lines.
//
// Behavior notes (entry point):
//   - File must exist, be regular, not a symlink, and valid UTF‑8.
//   - Matching uses TrimSpace comparisons (for file + provided blocks).
//   - Deterministic / no ambiguity:
//   - startMatchLines (if provided) must match exactly once.
//   - endMatchLines (if provided) must match exactly once.
//   - if both are provided, end must occur after start block (non-overlapping).
//   - If the selected range exceeds maxReadTextRangeOutputLines, the tool fails.
func ReadTextRange(ctx context.Context, args ReadTextRangeArgs) (*ReadTextRangeOut, error) {
	return toolutil.WithRecoveryResp(func() (*ReadTextRangeOut, error) {
		return readTextRange(ctx, args)
	})
}

func readTextRange(ctx context.Context, args ReadTextRangeArgs) (*ReadTextRangeOut, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	path, err := fileutil.NormalizeAbsPath(args.Path)
	if err != nil {
		return nil, err
	}

	startBlock := fileutil.NormalizeLineBlockInput(args.StartMatchLines)
	endBlock := fileutil.NormalizeLineBlockInput(args.EndMatchLines)

	tf, err := fileutil.ReadTextFileUTF8(path, toolutil.MaxTextProcessingBytes)
	if err != nil {
		return nil, err
	}

	total := len(tf.Lines)
	if total == 0 {
		return &ReadTextRangeOut{
			Lines:         nil,
			LinesReturned: 0,
		}, nil
	}

	// Selection (0-based inclusive).
	selStart := 0
	selEnd := total - 1

	var startIdx int
	var endIdx int
	var haveStartIdx bool
	var haveEndIdx bool

	if len(startBlock) > 0 {
		startIdx, err = fileutil.RequireSingleTrimmedBlockMatch(tf.Lines, startBlock, "startMatchLines")
		if err != nil {
			return nil, err
		}
		haveStartIdx = true
		selStart = startIdx
	}

	if len(endBlock) > 0 {
		endIdx, err = fileutil.RequireSingleTrimmedBlockMatch(tf.Lines, endBlock, "endMatchLines")
		if err != nil {
			return nil, err
		}
		haveEndIdx = true
		selEnd = endIdx + len(endBlock) - 1
		if selEnd >= total {
			// Defensive: should not happen, but keep bounds safe.
			selEnd = total - 1
		}
	}

	// If both markers provided, enforce order (non-overlapping).
	if haveStartIdx && haveEndIdx {
		if endIdx < startIdx+len(startBlock) {
			return nil, fmt.Errorf(
				"endMatchLines occurs before (or overlaps) startMatchLines (start at line %d, end at line %d)",
				startIdx+1,
				endIdx+1,
			)
		}
	}

	if selStart < 0 || selStart >= total {
		return nil, fmt.Errorf("invalid selected start computed: %d", selStart)
	}
	if selEnd < 0 || selEnd >= total {
		return nil, fmt.Errorf("invalid selected end computed: %d", selEnd)
	}
	if selStart > selEnd {
		return nil, fmt.Errorf("invalid selection: start after end (start line %d, end line %d)", selStart+1, selEnd+1)
	}

	nOut := selEnd - selStart + 1
	if nOut > maxReadTextRangeOutputLines {
		return nil, fmt.Errorf(
			"selected range too large: %d lines (max %d). Provide startMatchLines/endMatchLines to narrow the range",
			nOut,
			maxReadTextRangeOutputLines,
		)
	}

	outLines := make([]ReadTextRangeLine, 0, nOut)
	for i := selStart; i <= selEnd; i++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		outLines = append(outLines, ReadTextRangeLine{
			LineNumber: i + 1,
			Text:       tf.Lines[i],
		})
	}

	return &ReadTextRangeOut{
		StartLine:     selStart + 1,
		EndLine:       selEnd + 1,
		LinesReturned: len(outLines),
		Lines:         outLines,
	}, nil
}
