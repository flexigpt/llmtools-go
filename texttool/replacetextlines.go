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

const replaceTextLinesFuncID spec.FuncID = "github.com/flexigpt/llmtools-go/texttool/replacetextlines.ReplaceTextLines"

var replaceTextLinesTool = spec.Tool{
	SchemaVersion: spec.SchemaVersion,
	ID:            "019c04d3-c723-7dfa-b85a-12ee7d328502",
	Slug:          "replacetextlines",
	Version:       "v1.0.0",
	DisplayName:   "Replace text lines",
	Description: "Replace a uniquely located block of lines in a UTF-8 text file. Matching uses TrimSpace(line). " +
		"Before calling, get exact file text and line numbers with find/read tools unless you already have them. " +
		"Use a pointed locator: copy an exact distinctive block of > 2 consecutive lines, add immediate beforeLines/afterLines when the block may repeat, and pass maybeStartLine from the observed line number when relevant. " +
		"Do not send generic, repeated, overlapping, or conflict-prone edits. The tool fails unless the final match count equals expectedReplacements.",
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
	"description": "Exact consecutive lines to replace, copied from the file. Prefer a distinctive block of > 2 lines. Do not paraphrase. Do not use generic single lines such as blank lines, braces, or repeated return statements."
},
"beforeLines": {
	"type": "array",
	"items": { "type": "string" },
	"description": "Optional exact immediate-adjacent lines copied from the file that must appear directly before matchLines. Use 2-5 lines whenever the target block might repeat."
},
"afterLines": {
	"type": "array",
	"items": { "type": "string" },
	"description": "Optional exact immediate-adjacent lines copied from the file that must appear directly after matchLines. Use 2-5 lines whenever the target block might repeat."
},
"maybeStartLine": {
	"type": "integer",
	"minimum": 1,
	"description": "Optional 1-based start-line hint taken from read/find output. Supply it whenever available to point at the intended occurrence. If several matches still exist, the tool only succeeds when one nearby match is uniquely preferred within a small built-in tolerance."
},
"expectedReplacements": {
	"type": "integer",
	"minimum": 1,
	"default": 1,
	"description": "Fail if replacements made != this value. Keep at 1 for the normal case. Do not raise this to bypass ambiguity."
}
},
"required": ["path", "matchLines", "replaceWithLines"],
"additionalProperties": false
}`),

	GoImpl: spec.GoToolImpl{FuncID: replaceTextLinesFuncID},

	CreatedAt:  spec.SchemaStartTime,
	ModifiedAt: spec.SchemaStartTime,
}

type ReplaceTextLinesArgs struct {
	Path string `json:"path"`

	MatchLines []string `json:"matchLines"`

	// Required; must contain at least one line.
	// This tool does NOT support deletion (use deletetextlines).
	ReplaceWithLines []string `json:"replaceWithLines"`

	BeforeLines    []string `json:"beforeLines,omitempty"`
	AfterLines     []string `json:"afterLines,omitempty"`
	MaybeStartLine *int     `json:"maybeStartLine,omitempty"`

	// Pointer is used so we can distinguish "omitted" (default to 1) from "explicit 0" (error).
	ExpectedReplacements *int `json:"expectedReplacements,omitempty"` // default 1; minimum 1
}

type ReplaceTextLinesOut struct {
	ReplacementsMade int   `json:"replacementsMade"`
	ReplacedAtLines  []int `json:"replacedAtLines"` // 1-based start line of each replacement
}

// replaceTextLines replaces occurrences of MatchLines in a UTF‑8 file.
//
// Behavior notes (entry point):
//   - File must exist, be regular, not a symlink, and valid UTF‑8.
//   - Matching is line-wise using TrimSpace comparisons (for both file + input blocks).
//   - Returned/inserted lines are written exactly as provided (no trimming).
//   - Deterministic / no ambiguity: fails unless match count == expectedReplacements (default 1, minimum 1).
//   - Deletion is not supported here; use deletetextlines.
//   - Writes are atomic and preserve newline style and final newline presence.
func replaceTextLines(
	ctx context.Context,
	args ReplaceTextLinesArgs,
	p fspolicy.FSPolicy,
) (*ReplaceTextLinesOut, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	matchLines := ioutil.NormalizeLineBlockInput(args.MatchLines)
	beforeLines := ioutil.NormalizeLineBlockInput(args.BeforeLines)
	afterLines := ioutil.NormalizeLineBlockInput(args.AfterLines)
	if args.MaybeStartLine != nil && *args.MaybeStartLine < 1 {
		return nil, fmt.Errorf("maybeStartLine must be >= 1 (got %d)", *args.MaybeStartLine)
	}

	if len(matchLines) == 0 {
		return nil, errors.New("matchLines is required")
	}
	// Required field semantics: nil means omitted (programmatic call error).
	if args.ReplaceWithLines == nil {
		return nil, errors.New("replaceWithLines is required")
	}
	replaceWith := ioutil.NormalizeLineBlockInput(args.ReplaceWithLines)
	if len(replaceWith) == 0 {
		return nil, errors.New(
			"replaceWithLines must contain at least one line (deletion is not supported by this tool)",
		)
	}

	expected := 1
	if args.ExpectedReplacements != nil {
		expected = *args.ExpectedReplacements
	}
	if expected < 1 {
		return nil, fmt.Errorf("expectedReplacements must be >= 1 (got %d)", expected)
	}

	tf, err := ioutil.ReadTextFileUTF8(p, args.Path, toolutil.MaxTextProcessingBytes)
	if err != nil {
		return nil, err
	}

	matchIdxs := ioutil.FindTrimmedAdjacentBlockMatches(tf.Lines, beforeLines, matchLines, afterLines)
	// Overlap guard: overlapping matches make replacements ambiguous.
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
			"replace match count mismatch: expected %d, found %d. diagnostics=%s suggestion=%s",
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

	// Replace from the end so earlier indices remain valid.
	for i := len(matchIdxs) - 1; i >= 0; i-- {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		start := matchIdxs[i]
		end := start + len(matchLines) // exclusive
		tf.Lines = replaceLinesSlice(tf.Lines, start, end, replaceWith)
	}

	outStr := tf.Render()
	if err := ioutil.WriteFileAtomicBytesResolved(p, tf.Path, []byte(outStr), tf.Perm, true); err != nil {
		return nil, err
	}

	replacedAt := make([]int, 0, len(matchIdxs))
	for _, idx := range matchIdxs {
		replacedAt = append(replacedAt, idx+1)
	}

	return &ReplaceTextLinesOut{
		ReplacementsMade: len(matchIdxs),
		ReplacedAtLines:  replacedAt,
	}, nil
}

func replaceLinesSlice(lines []string, start, end int, repl []string) []string {
	if start < 0 {
		start = 0
	}
	if end < start {
		end = start
	}
	if start > len(lines) {
		start = len(lines)
	}
	if end > len(lines) {
		end = len(lines)
	}

	out := make([]string, 0, len(lines)-(end-start)+len(repl))
	out = append(out, lines[:start]...)
	out = append(out, repl...)
	out = append(out, lines[end:]...)
	return out
}
