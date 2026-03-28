package texttool

import (
	"context"
	"errors"
	"fmt"

	"github.com/flexigpt/llmtools-go/internal/fspolicy"
	"github.com/flexigpt/llmtools-go/internal/ioutil"
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
	Description: "Delete one or more exact line-block occurrences from a UTF-8 text file. Matching compares TrimSpace(line). " +
		"For reliable calls, prefer editing > 2 consecutive lines and use immediate beforeLines/afterLines to reduce ambiguity. " +
		"maybeStartLine can softly prefer a nearby occurrence, but the tool still fails unless the final deletion count equals expectedDeletions.",
	Tags: []string{"text"},

	ArgSchema: spec.JSONSchema(`{
"$schema": "http://json-schema.org/draft-07/schema#",
"type": "object",
"properties": {
	"path": {
		"type": "string",
		"description": "Path of the UTF-8 text file."
	},
	"matchLines": {
		"type": "array",
		"items": { "type": "string" },
		"minItems": 1,
		"description": "Distinctive block of lines to delete. Prefer editing > 2 consecutive lines. Avoid generic single lines such as blank lines, braces, or repeated return statements. Newline characters inside items are allowed and treated as line breaks."
	},
	"beforeLines": {
		"type": "array",
		"items": { "type": "string" },
		"minItems": 1,
		"description": "Optional immediate-adjacent lines that must appear directly before matchLines. Use 2-5 neighboring lines to disambiguate."
	},
	"afterLines": {
		"type": "array",
		"items": { "type": "string" },
		"minItems": 1,
		"description": "Optional immediate-adjacent lines that must appear directly after matchLines. Use 2-5 neighboring lines to disambiguate."
	},
	"maybeStartLine": {
		"type": "integer",
		"minimum": 1,
		"description": "Optional 1-based approximate start line hint. Best used when you expect one deletion. If several matches exist, the tool will prefer a uniquely closest nearby match within a small built-in tolerance; otherwise it still fails and reports candidates."
	},
	"expectedDeletions": {
		"type": "integer",
		"minimum": 1,
		"default": 1,
		"description": "Fail unless the number of deleted blocks equals this value. Leave at 1 for the common case of a single intended deletion."
	}
},
"required": ["path", "matchLines"],
"additionalProperties": false
}`),

	GoImpl: spec.GoToolImpl{FuncID: deleteTextLinesFuncID},

	CreatedAt:  spec.SchemaStartTime,
	ModifiedAt: spec.SchemaStartTime,
}

type DeleteTextLinesArgs struct {
	Path              string   `json:"path"`
	MatchLines        []string `json:"matchLines"`
	BeforeLines       []string `json:"beforeLines,omitempty"`
	AfterLines        []string `json:"afterLines,omitempty"`
	MaybeStartLine    *int     `json:"maybeStartLine,omitempty"`
	ExpectedDeletions int      `json:"expectedDeletions,omitempty"` // default 1
}

type DeleteTextLinesOut struct {
	DeletionsMade  int   `json:"deletionsMade"`
	DeletedAtLines []int `json:"deletedAtLines"` // 1-based start line of each deleted block
}

// deleteTextLines deletes occurrences of MatchLines from a UTF‑8 file.
// Behavior notes (entry point):
//   - The file must exist, be a regular file, not a symlink, and be valid UTF‑8.
//   - Matching is line-wise using strings.TrimSpace on each line.
//   - If ExpectedDeletions is set, the tool fails unless exactly that many deletions would be made.
//   - Writes are atomic (temp file + fsync + rename) and preserve newline style and final newline.
func deleteTextLines(
	ctx context.Context,
	args DeleteTextLinesArgs,
	p fspolicy.FSPolicy,
) (*DeleteTextLinesOut, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	if len(args.MatchLines) == 0 {
		return nil, errors.New("matchLines is required")
	}

	matchLines := ioutil.NormalizeLineBlockInput(args.MatchLines)
	beforeLines := ioutil.NormalizeLineBlockInput(args.BeforeLines)
	afterLines := ioutil.NormalizeLineBlockInput(args.AfterLines)
	if args.MaybeStartLine != nil && *args.MaybeStartLine < 1 {
		args.MaybeStartLine = nil
	}
	expected := args.ExpectedDeletions
	if expected <= 0 {
		expected = 1
	}

	tf, err := ioutil.ReadTextFileUTF8(p, args.Path, toolutil.MaxTextProcessingBytes)
	if err != nil {
		return nil, err
	}

	matchIdxs := ioutil.FindTrimmedAdjacentBlockMatches(tf.Lines, beforeLines, matchLines, afterLines)
	if err := ioutil.EnsureNonOverlappingFixedWidth(matchIdxs, len(matchLines)); err != nil {
		return nil, err
	}
	var hintDiag *ioutil.MaybeStartLineDiagnostic
	if expected == 1 {
		matchIdxs, hintDiag = ioutil.NarrowMatchIndicesByMaybeStartLine(
			matchIdxs,
			args.MaybeStartLine,
			maybeStartLineTolerance,
		)
	}
	if len(matchIdxs) != expected {
		suggestion := "copy a longer unique matchLines block from the file and add immediate beforeLines/afterLines"
		if expected == 1 {
			suggestion += "; if you know roughly where the block is, set maybeStartLine near the intended start line"
		}
		return nil, fmt.Errorf(
			"delete match count mismatch: expected %d, found %d. diagnostics=%s suggestion=%s",
			expected,
			len(matchIdxs),
			ioutil.BuildBlockMatchDiagnosticJSON(
				tf.Lines,
				matchIdxs,
				len(matchLines),
				hintDiag,
				maxAmbiguityDiagnosticCandidates,
				ambiguityDiagnosticContextLines,
			),
			suggestion,
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
		if err := ioutil.WriteFileAtomicBytesResolved(p, tf.Path, []byte(outStr), tf.Perm, true); err != nil {
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
