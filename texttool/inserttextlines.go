package texttool

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/flexigpt/llmtools-go/internal/fileutil"
	"github.com/flexigpt/llmtools-go/internal/toolutil"
	"github.com/flexigpt/llmtools-go/spec"
)

const insertTextLinesFuncID spec.FuncID = "github.com/flexigpt/llmtools-go/texttool/inserttextlines.InsertTextLines"

var insertTextLinesTool = spec.Tool{
	SchemaVersion: spec.SchemaVersion,
	ID:            "019c04d3-572e-7d26-b4ca-f37feb7e8368",
	Slug:          "inserttextlines",
	Version:       "v1.0.0",
	DisplayName:   "Insert text lines",
	Description:   "Insert lines into a UTF-8 text file at start/end or relative to a uniquely-matched anchor block (TrimSpace line matching).",
	Tags:          []string{"text"},

	ArgSchema: spec.JSONSchema(`{
"$schema": "http://json-schema.org/draft-07/schema#",
"type": "object",
"properties": {
	"path": {
		"type": "string",
		"description": "Absolute path of the UTF-8 text file."
	},
	"position": {
		"type": "string",
		"enum": ["start", "end", "beforeAnchor", "afterAnchor"],
		"description": "Where to insert the new lines.",
		"default": "end"
	},
	"linesToInsert": {
		"type": "array",
		"items": { "type": "string" },
		"minItems": 1,
		"description": "Lines to insert. Newline characters inside items are allowed and are treated as line breaks."
	},
	"anchorMatchLines": {
		"type": "array",
		"items": { "type": "string" },
		"minItems": 1,
		"description": "Anchor block to match (TrimSpace comparison). Required for position=beforeAnchor/afterAnchor and must match exactly once."
	}
},
"required": ["path", "linesToInsert"],
"oneOf": [
  {
    "title": "start-or-end",
    "properties": { "position": { "enum": ["start", "end"] } },
	"required": ["position"],
    "not": { "required": ["anchorMatchLines"] }
  },
  {
    "title": "default-end",
    "not": { "anyOf": [ { "required": ["position"] }, { "required": ["anchorMatchLines"] } ] }
  },
  {
    "title": "anchor-relative",
    "properties": { "position": { "enum": ["beforeAnchor", "afterAnchor"] } },
    "required": ["position", "anchorMatchLines"]
  }
],
"additionalProperties": false
}`),

	GoImpl: spec.GoToolImpl{FuncID: insertTextLinesFuncID},

	CreatedAt:  spec.SchemaStartTime,
	ModifiedAt: spec.SchemaStartTime,
}

func InsertTextLinesTool() spec.Tool {
	return toolutil.CloneTool(insertTextLinesTool)
}

type InsertTextLinesArgs struct {
	Path             string   `json:"path"`
	Position         string   `json:"position,omitempty"` // default "end"
	LinesToInsert    []string `json:"linesToInsert"`
	AnchorMatchLines []string `json:"anchorMatchLines,omitempty"`
}

type InsertTextLinesOut struct {
	InsertedAtLine      int  `json:"insertedAtLine"` // 1-based, where insertion begins
	InsertedLineCount   int  `json:"insertedLineCount"`
	AnchorMatchedAtLine *int `json:"anchorMatchedAtLine,omitempty"` // 1-based start line of anchor block
}

// InsertTextLines inserts LinesToInsert into a UTF‑8 file.
// Behavior notes (entry point):
//   - File must exist, be regular, not a symlink, and valid UTF‑8.
//   - Matching is line-wise using strings.TrimSpace.
//   - For beforeAnchor/afterAnchor: the anchor block must match exactly once; otherwise it fails.
//   - Writes are atomic and preserve newline style and final newline presence.
func InsertTextLines(ctx context.Context, args InsertTextLinesArgs) (*InsertTextLinesOut, error) {
	return toolutil.WithRecoveryResp(func() (*InsertTextLinesOut, error) {
		return insertTextLines(ctx, args)
	})
}

const whereEnd = "end"

func insertTextLines(ctx context.Context, args InsertTextLinesArgs) (*InsertTextLinesOut, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	path, err := fileutil.NormalizeAbsPath(args.Path)
	if err != nil {
		return nil, err
	}
	if len(args.LinesToInsert) == 0 {
		return nil, errors.New("linesToInsert is required")
	}

	linesToInsert := fileutil.NormalizeLineBlockInput(args.LinesToInsert)
	anchorLines := fileutil.NormalizeLineBlockInput(args.AnchorMatchLines)

	pos := strings.TrimSpace(strings.ToLower(args.Position))
	if pos == "" {
		pos = whereEnd
	}

	// Reject irrelevant anchor input to keep calls unambiguous.
	switch pos {
	case "start", whereEnd:
		if len(anchorLines) > 0 {
			return nil, errors.New(`anchorMatchLines must be omitted when position is "start" or "end"`)
		}
	case "beforeanchor", "afteranchor":
		// Anchor required: handled by computeInsertIndex, but we keep this explicit for clarity.
	default:
		// Index will error out.
	}

	tf, err := fileutil.ReadTextFileUTF8(path, toolutil.MaxTextProcessingBytes)
	if err != nil {
		return nil, err
	}

	insertAt, anchorAt, err := computeInsertIndex(tf.Lines, pos, anchorLines)
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	tf.Lines = insertLines(tf.Lines, insertAt, linesToInsert)

	outStr := tf.Render()
	if err := fileutil.WriteFileAtomicBytes(tf.Path, []byte(outStr), tf.Perm, true); err != nil {
		return nil, err
	}

	return &InsertTextLinesOut{
		InsertedAtLine:      insertAt + 1,
		InsertedLineCount:   len(linesToInsert),
		AnchorMatchedAtLine: anchorAt,
	}, nil
}

func computeInsertIndex(lines []string, pos string, anchor []string) (insertAt int, anchorAtLine *int, err error) {
	switch pos {
	case "start":
		return 0, nil, nil
	case whereEnd:
		return len(lines), nil, nil
	case "beforeanchor":
		if len(anchor) == 0 {
			return 0, nil, errors.New(`position="beforeAnchor" requires anchorMatchLines`)
		}
		idxs := fileutil.FindTrimmedBlockMatches(lines, anchor)
		i, e := fileutil.RequireSingleMatch(idxs, "anchorMatchLines")
		if e != nil {
			return 0, nil, e
		}
		a := i + 1
		return i, &a, nil
	case "afteranchor":
		if len(anchor) == 0 {
			return 0, nil, errors.New(`position="afterAnchor" requires anchorMatchLines`)
		}
		idxs := fileutil.FindTrimmedBlockMatches(lines, anchor)
		i, err := fileutil.RequireSingleMatch(idxs, "anchorMatchLines")
		if err != nil {
			return 0, nil, err
		}
		a := i + 1
		return i + len(anchor), &a, nil
	default:
		return 0, nil, fmt.Errorf(
			`invalid position value %q (expected: "start","end","beforeAnchor","afterAnchor")`,
			pos,
		)

	}
}

func insertLines(lines []string, idx int, toInsert []string) []string {
	if idx < 0 {
		idx = 0
	}
	if idx > len(lines) {
		idx = len(lines)
	}
	out := make([]string, 0, len(lines)+len(toInsert))
	out = append(out, lines[:idx]...)
	out = append(out, toInsert...)
	out = append(out, lines[idx:]...)
	return out
}
