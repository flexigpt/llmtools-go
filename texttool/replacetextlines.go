package texttool

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"

	"github.com/flexigpt/llmtools-go/internal/fileutil"
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
	Description: "Replace a block of lines in a UTF-8 text file; use beforeLines/afterLines to make the match more specific.\n" +
		"Matching uses trimmed space comparisons. Fails unless the number of replacements equals expectedReplacements.",
	Tags: []string{"text"},

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
		"description": "The exact block of lines to find."
	},
	"replaceWithLines": {
		"type": "array",
		"items": { "type": "string" },
		"minItems": 1,
		"description": "Replacement block of lines."
	},
	"beforeLines": {
		"type": "array",
		"items": { "type": "string" },
		"description": "Optional block of lines that must appear immediately before matchLines."
	},
	"afterLines": {
		"type": "array",
		"items": { "type": "string" },
		"description": "Optional block of lines that must appear immediately after matchLines."
	},
	"expectedReplacements": {
		"type": "integer",
		"minimum": 1,
		"default": 1,
		"description": "Fail if replacements made != this value."
	}
},
"required": ["path", "matchLines", "replaceWithLines"],
"additionalProperties": false
}`),

	GoImpl: spec.GoToolImpl{FuncID: replaceTextLinesFuncID},

	CreatedAt:  spec.SchemaStartTime,
	ModifiedAt: spec.SchemaStartTime,
}

func ReplaceTextLinesTool() spec.Tool { return toolutil.CloneTool(replaceTextLinesTool) }

type ReplaceTextLinesArgs struct {
	Path string `json:"path"`

	MatchLines []string `json:"matchLines"`

	// Required; must contain at least one line.
	// This tool does NOT support deletion (use deletetextlines).
	ReplaceWithLines []string `json:"replaceWithLines"`

	BeforeLines []string `json:"beforeLines,omitempty"`
	AfterLines  []string `json:"afterLines,omitempty"`

	// Pointer is used so we can distinguish "omitted" (default to 1) from "explicit 0" (error).
	ExpectedReplacements *int `json:"expectedReplacements,omitempty"` // default 1; minimum 1
}

type ReplaceTextLinesOut struct {
	ReplacementsMade int   `json:"replacementsMade"`
	ReplacedAtLines  []int `json:"replacedAtLines"` // 1-based start line of each replacement
}

// ReplaceTextLines replaces occurrences of MatchLines in a UTF‑8 file.
//
// Behavior notes (entry point):
//   - File must exist, be regular, not a symlink, and valid UTF‑8.
//   - Matching is line-wise using TrimSpace comparisons (for both file + input blocks).
//   - Returned/inserted lines are written exactly as provided (no trimming).
//   - Deterministic / no ambiguity: fails unless match count == expectedReplacements (default 1, minimum 1).
//   - Deletion is not supported here; use deletetextlines.
//   - Writes are atomic and preserve newline style and final newline presence.
func ReplaceTextLines(ctx context.Context, args ReplaceTextLinesArgs) (*ReplaceTextLinesOut, error) {
	return toolutil.WithRecoveryResp(func() (*ReplaceTextLinesOut, error) {
		return replaceTextLines(ctx, args)
	})
}

func replaceTextLines(ctx context.Context, args ReplaceTextLinesArgs) (*ReplaceTextLinesOut, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	path, err := fileutil.NormalizePath(args.Path)
	if err != nil {
		return nil, err
	}
	if !filepath.IsAbs(path) {
		return nil, errors.New("path must be absolute")
	}

	matchLines := fileutil.NormalizeLineBlockInput(args.MatchLines)
	beforeLines := fileutil.NormalizeLineBlockInput(args.BeforeLines)
	afterLines := fileutil.NormalizeLineBlockInput(args.AfterLines)

	if len(matchLines) == 0 {
		return nil, errors.New("matchLines is required")
	}
	// Required field semantics: nil means omitted (programmatic call error).
	if args.ReplaceWithLines == nil {
		return nil, errors.New("replaceWithLines is required")
	}
	replaceWith := fileutil.NormalizeLineBlockInput(args.ReplaceWithLines)
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

	tf, err := fileutil.ReadTextFileUTF8(path, toolutil.MaxTextProcessingBytes)
	if err != nil {
		return nil, err
	}

	matchIdxs := fileutil.FindTrimmedAdjacentBlockMatches(tf.Lines, beforeLines, matchLines, afterLines)

	// Overlap guard: overlapping matches make replacements ambiguous.
	if err := fileutil.EnsureNonOverlappingFixedWidth(matchIdxs, len(matchLines)); err != nil {
		return nil, err
	}

	if len(matchIdxs) != expected {
		return nil, fmt.Errorf(
			"replace match count mismatch: expected %d, found %d (provide tighter beforeLines/afterLines to disambiguate)",
			expected,
			len(matchIdxs),
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
	if err := fileutil.WriteFileAtomicBytes(tf.Path, []byte(outStr), tf.Perm, true); err != nil {
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
