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
	whereBetween          string      = "between"
	whereEnd              string      = "end"
)

var insertTextLinesTool = spec.Tool{
	SchemaVersion: spec.SchemaVersion,
	ID:            "019c04d3-572e-7d26-b4ca-f37feb7e8368",
	Slug:          "inserttextlines",
	Version:       "v1.0.0",
	DisplayName:   "Insert text",
	Description: "Insert a text block into a text file at the start, end, or between text. " +
		"Between mode needs atleast one of textAbove or textBelow. Blank or whitespace-only lines are allowed between above and below texts and insertion is at the start of blank space. " +
		"Include larger textAbove and/or textBelow text when the location may repeat, and pass lineHint from read/find output when relevant. Do not target a generic repeated location.",
	Tags: []string{"text"},

	ArgSchema: spec.JSONSchema(`{
"$schema": "http://json-schema.org/draft-07/schema#",
"type": "object",
"properties": {
	"path": {
		"type": "string",
		"minLength": 1,
		"description": "Path of the file."
	},
	"text": {
		"type": "string",
		"description": "Text block to insert. Newline characters are treated as line breaks. A single trailing newline is treated as a line terminator, not as an additional empty line. An empty string inserts one blank line"
	},
	"position": {
		"type": "string",
		"enum": ["start", "end", "between"],
		"description": "Where to insert the text block"
	},
	"textAbove": {
		"type": "string",
		"description": "Text above the insertion point"
	},
	"textBelow": {
		"type": "string",
		"description": "Text below the insertion point"
	},
	"lineHint": {
		"type": "integer",
		"minimum": 1,
		"description": "Optional 1-based hint for the line where inserted text should begin, taken from read/find output. If several locations still match, the tool only succeeds when one nearby location is uniquely preferred within a small built-in tolerance"
	}
},
"required": ["path", "text", "position"],
"additionalProperties": false
}`),

	GoImpl: spec.GoToolImpl{FuncID: insertTextLinesFuncID},

	CreatedAt:  spec.SchemaStartTime,
	ModifiedAt: spec.SchemaStartTime,
}

type InsertTextLinesArgs struct {
	Path      string  `json:"path"`
	Text      *string `json:"text"`
	Position  string  `json:"position"`
	TextAbove *string `json:"textAbove,omitempty"`
	TextBelow *string `json:"textBelow,omitempty"`
	LineHint  *int    `json:"lineHint,omitempty"`
}

type InsertTextLinesOut struct {
	InsertedAtLine         int  `json:"insertedAtLine"` // 1-based, where insertion begins
	InsertedLineCount      int  `json:"insertedLineCount"`
	TextAboveMatchedAtLine *int `json:"textAboveMatchedAtLine,omitempty"` // 1-based start line of the matched textAbove boundary block used in the original file
	TextBelowMatchedAtLine *int `json:"textBelowMatchedAtLine,omitempty"` // 1-based start line of the matched textBelow boundary block used in the original file
}

func normalizeOptionalTextBlock(s *string) []string {
	if s == nil {
		return nil
	}
	return ioutil.NormalizeTextBlockString(*s)
}

// insertTextLines inserts Text into a UTF‑8 file.
// Behavior notes (entry point):
//
//   - File must exist, be regular, not a symlink, and valid UTF‑8.
//   - Matching is line-wise using strings.TrimSpace.
//   - For position="between": at least one of textAbove/textBelow is required.
//   - If both textAbove and textBelow are provided, only blank/whitespace-only lines may lie between them
//     and insertion occurs at the start of that blank gap.
//   - The resulting insertion location must match exactly once; otherwise it fails.
//   - Writes are atomic and preserve newline style and final newline presence.
func insertTextLines(
	ctx context.Context,
	args InsertTextLinesArgs,
	p fspolicy.FSPolicy,
) (*InsertTextLinesOut, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	if args.Path == "" {
		return nil, errors.New("path is required")
	}
	if args.Text == nil {
		return nil, errors.New("text is required")
	}

	pos := strings.TrimSpace(strings.ToLower(args.Position))
	if pos == "" {
		return nil, errors.New("position is required")
	}

	textToInsert := normalizeOptionalTextBlock(args.Text)
	textAbove := normalizeOptionalTextBlock(args.TextAbove)
	textBelow := normalizeOptionalTextBlock(args.TextBelow)

	switch pos {
	case "start", whereEnd:
		if args.TextAbove != nil || args.TextBelow != nil || args.LineHint != nil {
			return nil, errors.New(`textAbove/textBelow/lineHint must be omitted when position is "start" or "end"`)
		}
	case whereBetween:
		if args.TextAbove == nil && args.TextBelow == nil {
			return nil, errors.New(`position="between" requires textAbove or textBelow`)
		}
		if args.LineHint != nil && *args.LineHint < 1 {
			return nil, errors.New("lineHint must be >= 1")
		}
	default:
		// "computeInsertIndex" will return a clear invalid-position error.
	}

	tf, err := ioutil.ReadTextFileUTF8(p, args.Path, toolutil.MaxTextProcessingBytes)
	if err != nil {
		return nil, err
	}

	insertAt, textAboveAt, textBelowAt, err := computeInsertIndex(
		tf.Lines,
		pos,
		textAbove,
		textBelow,
		args.LineHint,
	)
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	tf.Lines = insertLines(tf.Lines, insertAt, textToInsert)

	outStr := tf.Render()
	if err := ioutil.WriteFileAtomicBytesResolved(p, tf.Path, []byte(outStr), tf.Perm, true); err != nil {
		return nil, err
	}

	return &InsertTextLinesOut{
		InsertedAtLine:         insertAt + 1,
		InsertedLineCount:      len(textToInsert),
		TextAboveMatchedAtLine: textAboveAt,
		TextBelowMatchedAtLine: textBelowAt,
	}, nil
}

func computeInsertIndex(
	lines []string,
	pos string,
	textAbove []string,
	textBelow []string,
	lineHint *int,
) (insertAt int, textAboveMatchedAtLine, textBelowMatchedAtLine *int, err error) {
	switch pos {
	case "start":
		return 0, nil, nil, nil
	case whereEnd:
		return len(lines), nil, nil, nil
	case whereBetween:
		if len(textAbove) == 0 && len(textBelow) == 0 {
			return 0, nil, nil, errors.New(`position="between" requires textAbove or textBelow`)
		}

		matches := ioutil.FindTrimmedInsertionPointMatchCandidates(lines, textAbove, textBelow)
		idxs := ioutil.InsertionPointMatchIndices(matches)
		idxs, hintDiag := ioutil.NarrowIndicesByLineHint(idxs, lineHint, maybeStartLineTolerance)

		if len(idxs) != 1 {
			diag := ioutil.BuildInsertionPointDiagnosticJSON(
				lines,
				idxs,
				len(textAbove),
				len(textBelow),
				hintDiag,
				maxAmbiguityDiagnosticCandidates,
				ambiguityDiagnosticContextLines,
			)

			if len(idxs) == 0 {
				return 0, nil, nil, fmt.Errorf(
					"no insertion point matched the provided textAbove/textBelow. diagnostics=%s suggestion=copy exact text immediately above and/or below the intended insertion point, include more surrounding lines, remember that when both are provided the tool only skips blank or whitespace-only lines between them, and if you know roughly where the text should be inserted set lineHint near that line",
					diag,
				)
			}

			return 0, nil, nil, fmt.Errorf(
				"ambiguous insertion point: found %d candidate locations. diagnostics=%s suggestion=copy exact text immediately above and/or below the intended insertion point, include more surrounding lines, remember that when both are provided the tool only skips blank or whitespace-only lines between them, and if you know roughly where the text should be inserted set lineHint near that line",
				len(idxs),
				diag,
			)
		}

		insertAt = idxs[0]

		matchIdx := -1
		for i := range matches {
			if matches[i].InsertAt == insertAt {
				matchIdx = i
				break
			}
		}
		if matchIdx < 0 {
			return 0, nil, nil, errors.New("internal error resolving insertion point candidate")
		}

		m := matches[matchIdx]
		if m.AboveStart != nil {
			a := *m.AboveStart + 1
			textAboveMatchedAtLine = &a
		}
		if m.BelowStart != nil {
			b := *m.BelowStart + 1
			textBelowMatchedAtLine = &b
		}

		return m.InsertAt, textAboveMatchedAtLine, textBelowMatchedAtLine, nil
	default:
		return 0, nil, nil, fmt.Errorf(
			`invalid position value %q (expected: "start","end","between")`,
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
