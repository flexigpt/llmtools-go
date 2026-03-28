package texttool

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/flexigpt/llmtools-go/internal/fspolicy"
	"github.com/flexigpt/llmtools-go/internal/ioutil"
	"github.com/flexigpt/llmtools-go/internal/toolutil"
	"github.com/flexigpt/llmtools-go/spec"
)

const (
	insertTextLinesFuncID spec.FuncID = "github.com/flexigpt/llmtools-go/texttool/inserttextlines.InsertTextLines"
	whereEnd              string      = "end"
)

var insertTextLinesTool = spec.Tool{
	SchemaVersion: spec.SchemaVersion,
	ID:            "019c04d3-572e-7d26-b4ca-f37feb7e8368",
	Slug:          "inserttextlines",
	Version:       "v1.0.0",
	DisplayName:   "Insert text lines",
	Description: "Insert lines into a UTF-8 text file at start/end or relative to a uniquely matched anchor block. " +
		"Anchor matching compares TrimSpace(line). Prefer a distinctive multi-line anchor. " +
		"Avoid generic anchors such as blank lines, braces, import blocks, or repeated one-line statements. " +
		"If the anchor may repeat, add immediate anchorBeforeLines/anchorAfterLines. " +
		"maybeStartLine can softly prefer a nearby anchor, but the tool still fails if the call is not unambiguous.",
	Tags: []string{"text"},

	ArgSchema: spec.JSONSchema(`{
"$schema": "http://json-schema.org/draft-07/schema#",
"type": "object",
"properties": {
	"path": {
		"type": "string",
		"description": "Path of the UTF-8 text file."
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
		"description": "Lines to insert. These are written exactly as provided. Newline characters inside items are allowed and are treated as line breaks."
	},
	"anchorMatchLines": {
		"type": "array",
		"items": { "type": "string" },
		"minItems": 1,
		"description": "Anchor block to match using TrimSpace comparison. Required for position=beforeAnchor/afterAnchor. Prefer > 2 consecutive lines; avoid short generic anchors."
	},
	"anchorBeforeLines": {
		"type": "array",
		"items": { "type": "string" },
		"minItems": 1,
		"description": "Optional immediate-adjacent lines that must appear directly before anchorMatchLines. Use 2-5 neighboring lines to disambiguate."
	},
	"anchorAfterLines": {
		"type": "array",
		"items": { "type": "string" },
		"minItems": 1,
		"description": "Optional immediate-adjacent lines that must appear directly after anchorMatchLines. Use 2-5 neighboring lines to disambiguate."
	},
	"maybeStartLine": {
		"type": "integer",
		"minimum": 1,
		"description": "Optional 1-based approximate start line hint for the anchor. If several anchors match, the tool will prefer a uniquely closest nearby match within a small built-in tolerance; otherwise it still fails and reports candidates."
	}
},
"required": ["path", "linesToInsert"],
"additionalProperties": false
}`),

	GoImpl: spec.GoToolImpl{FuncID: insertTextLinesFuncID},

	CreatedAt:  spec.SchemaStartTime,
	ModifiedAt: spec.SchemaStartTime,
}

type InsertTextLinesArgs struct {
	Path              string   `json:"path"`
	Position          string   `json:"position,omitempty"` // default "end"
	LinesToInsert     []string `json:"linesToInsert"`
	AnchorMatchLines  []string `json:"anchorMatchLines,omitempty"`
	AnchorBeforeLines []string `json:"anchorBeforeLines,omitempty"`
	AnchorAfterLines  []string `json:"anchorAfterLines,omitempty"`
	MaybeStartLine    *int     `json:"maybeStartLine,omitempty"`
}

type InsertTextLinesOut struct {
	InsertedAtLine      int  `json:"insertedAtLine"` // 1-based, where insertion begins
	InsertedLineCount   int  `json:"insertedLineCount"`
	AnchorMatchedAtLine *int `json:"anchorMatchedAtLine,omitempty"` // 1-based start line of anchor block
}

// insertTextLines inserts LinesToInsert into a UTF‑8 file.
// Behavior notes (entry point):
//   - File must exist, be regular, not a symlink, and valid UTF‑8.
//   - Matching is line-wise using strings.TrimSpace.
//   - For beforeAnchor/afterAnchor: the anchor block must match exactly once; otherwise it fails.
//   - Writes are atomic and preserve newline style and final newline presence.
func insertTextLines(
	ctx context.Context,
	args InsertTextLinesArgs,
	p fspolicy.FSPolicy,
) (*InsertTextLinesOut, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	if len(args.LinesToInsert) == 0 {
		return nil, errors.New("linesToInsert is required")
	}

	linesToInsert := ioutil.NormalizeLineBlockInput(args.LinesToInsert)
	anchorLines := ioutil.NormalizeLineBlockInput(args.AnchorMatchLines)
	anchorBeforeLines := ioutil.NormalizeLineBlockInput(args.AnchorBeforeLines)
	anchorAfterLines := ioutil.NormalizeLineBlockInput(args.AnchorAfterLines)
	pos := strings.TrimSpace(strings.ToLower(args.Position))
	if pos == "" {
		pos = whereEnd
	}
	if args.MaybeStartLine != nil && *args.MaybeStartLine < 1 {
		args.MaybeStartLine = nil
	}

	// Reject irrelevant anchor input to keep calls unambiguous.
	switch pos {
	case "start", whereEnd:
		if len(anchorLines) > 0 {
			return nil, errors.New(`anchorMatchLines must be omitted when position is "start" or "end"`)
		}
		if len(anchorBeforeLines) > 0 || len(anchorAfterLines) > 0 {
			return nil, errors.New(
				`anchorBeforeLines/anchorAfterLines must be omitted when position is "start" or "end"`,
			)
		}
		if args.MaybeStartLine != nil {
			return nil, errors.New(`maybeStartLine must be omitted when position is "start" or "end"`)
		}
	case "beforeanchor", "afteranchor":
		// Anchor required: handled by computeInsertIndex, but we keep this explicit for clarity.
	default:
		// Index will error out.
	}

	tf, err := ioutil.ReadTextFileUTF8(p, args.Path, toolutil.MaxTextProcessingBytes)
	if err != nil {
		return nil, err
	}

	insertAt, anchorAt, err := computeInsertIndex(
		tf.Lines,
		pos,
		anchorLines,
		anchorBeforeLines,
		anchorAfterLines,
		args.MaybeStartLine,
	)
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	tf.Lines = insertLines(tf.Lines, insertAt, linesToInsert)

	outStr := tf.Render()
	if err := ioutil.WriteFileAtomicBytesResolved(p, tf.Path, []byte(outStr), tf.Perm, true); err != nil {
		return nil, err
	}

	return &InsertTextLinesOut{
		InsertedAtLine:      insertAt + 1,
		InsertedLineCount:   len(linesToInsert),
		AnchorMatchedAtLine: anchorAt,
	}, nil
}

func computeInsertIndex(
	lines []string,
	pos string,
	anchor []string,
	anchorBefore []string,
	anchorAfter []string,
	maybeStartLine *int,
) (insertAt int, anchorAtLine *int, err error) {
	switch pos {
	case "start":
		return 0, nil, nil
	case whereEnd:
		return len(lines), nil, nil
	case "beforeanchor":
		if len(anchor) == 0 {
			return 0, nil, errors.New(`position="beforeAnchor" requires anchorMatchLines`)
		}
		idxs := ioutil.FindTrimmedAdjacentBlockMatches(lines, anchorBefore, anchor, anchorAfter)
		if err := ioutil.EnsureNonOverlappingFixedWidth(idxs, len(anchor)); err != nil {
			return 0, nil, err
		}
		idxs, hintDiag := ioutil.NarrowMatchIndicesByMaybeStartLine(idxs, maybeStartLine, maybeStartLineTolerance)
		if len(idxs) != 1 {
			diag := ioutil.BuildBlockMatchDiagnosticJSON(
				lines,
				idxs,
				len(anchor),
				hintDiag,
				maxAmbiguityDiagnosticCandidates,
				ambiguityDiagnosticContextLines,
			)
			if len(idxs) == 0 {
				return 0, nil, fmt.Errorf(
					"no match found for anchorMatchLines. diagnostics=%s suggestion=copy a longer distinctive anchor block from the file, add immediate anchorBeforeLines/anchorAfterLines, and if you know roughly where it is set maybeStartLine near it",
					diag,
				)
			}
			return 0, nil, fmt.Errorf(
				"ambiguous match for anchorMatchLines: found %d occurrences. diagnostics=%s suggestion=copy a longer distinctive anchor block from the file, add immediate anchorBeforeLines/anchorAfterLines, and if you know roughly where it is set maybeStartLine near it",
				len(idxs),
				diag,
			)
		}
		i := idxs[0]
		a := i + 1
		return i, &a, nil
	case "afteranchor":
		if len(anchor) == 0 {
			return 0, nil, errors.New(`position="afterAnchor" requires anchorMatchLines`)
		}
		idxs := ioutil.FindTrimmedAdjacentBlockMatches(lines, anchorBefore, anchor, anchorAfter)
		if err := ioutil.EnsureNonOverlappingFixedWidth(idxs, len(anchor)); err != nil {
			return 0, nil, err
		}
		idxs, hintDiag := ioutil.NarrowMatchIndicesByMaybeStartLine(idxs, maybeStartLine, maybeStartLineTolerance)
		if len(idxs) != 1 {
			diag := ioutil.BuildBlockMatchDiagnosticJSON(
				lines,
				idxs,
				len(anchor),
				hintDiag,
				maxAmbiguityDiagnosticCandidates,
				ambiguityDiagnosticContextLines,
			)
			if len(idxs) == 0 {
				return 0, nil, fmt.Errorf(
					"no match found for anchorMatchLines. diagnostics=%s suggestion=copy a longer distinctive anchor block from the file, add immediate anchorBeforeLines/anchorAfterLines, and if you know roughly where it is set maybeStartLine near it",
					diag,
				)
			}
			return 0, nil, fmt.Errorf(
				"ambiguous match for anchorMatchLines: found %d occurrences. diagnostics=%s suggestion=copy a longer distinctive anchor block from the file, add immediate anchorBeforeLines/anchorAfterLines, and if you know roughly where it is set maybeStartLine near it",
				len(idxs),
				diag,
			)
		}
		i := idxs[0]
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
