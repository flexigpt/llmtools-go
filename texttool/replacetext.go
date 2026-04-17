package texttool

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/flexigpt/llmtools-go/internal/fspolicy"
	"github.com/flexigpt/llmtools-go/internal/ioutil"
	"github.com/flexigpt/llmtools-go/internal/toolutil"
	"github.com/flexigpt/llmtools-go/spec"
)

const replaceTextFuncID spec.FuncID = "github.com/flexigpt/llmtools-go/texttool/replacetext.ReplaceText"

const (
	replaceTextLineHintTolerance                = 8
	replaceTextMaxAmbiguityDiagnosticCandidates = 5
	replaceTextAmbiguityDiagnosticContextLines  = 1
)

var replaceTextTool = spec.Tool{
	SchemaVersion: spec.SchemaVersion,
	ID:            "019c04d3-c723-7dfa-b85a-12ee7d328502",
	Slug:          "replacetext",
	Version:       "v1.0.0",
	DisplayName:   "Replace text",
	Description: "Replace a text block in a UTF-8 text file with new text. Matching uses TrimSpace(line). " +
		"Required params are path, oldText, and newText. Use textAbove and/or textBelow as location hints; " +
		"only blank or whitespace-only lines may lie between these hints and oldText. " +
		"Use lineHint for the normal single-match case. The tool fails unless the number of matches equals expectedCount (default 1). " +
		"newText is required and an empty string means one blank line.",
	Tags: []string{"text"},

	ArgSchema: spec.JSONSchema(`{
"$schema": "http://json-schema.org/draft-07/schema#",
"type": "object",
"properties": {
	"path": {
		"type": "string",
		"minLength": 1,
		"description": "Path of file."
	},
	"oldText": {
		"type": "string",
		"description": "Exact text block to replace."
	},
	"newText": {
		"type": "string",
		"description": "Replacement text block written exactly as provided. An empty string means one blank line."
	},
	"textAbove": {
		"type": "string",
		"description": "Optional copied text above oldText used only to locate the occurrence"
	},
	"textBelow": {
		"type": "string",
		"description": "Optional copied text below oldText used only to locate the intended occurrence"
	},
	"lineHint": {
		"type": "integer",
		"minimum": 1,
		"description": "Optional 1-based hint for the start line of oldText. Use only when expectedCount is 1."
	},
	"expectedCount": {
		"type": "integer",
		"minimum": 1,
		"default": 1,
		"description": "Fail unless exactly this many replacements would be made. Keep this at 1 for the normal case. Do not raise it to bypass ambiguity."
	}
},
"required": ["path", "oldText", "newText"],
"additionalProperties": false
}`),

	GoImpl: spec.GoToolImpl{FuncID: replaceTextFuncID},

	CreatedAt:  spec.SchemaStartTime,
	ModifiedAt: spec.SchemaStartTime,
}

type ReplaceTextArgs struct {
	Path string `json:"path"`

	// Required. Nil means omitted. An empty string means one blank line.
	OldText *string `json:"oldText"`

	// Required. Nil means omitted. An empty string means one blank line, not deletion.
	NewText *string `json:"newText"`

	TextAbove *string `json:"textAbove,omitempty"`
	TextBelow *string `json:"textBelow,omitempty"`

	// Optional 1-based hint for the start line of oldText.
	// May only be used when ExpectedCount is omitted or 1.
	LineHint *int `json:"lineHint,omitempty"`

	// Default 1.
	ExpectedCount *int `json:"expectedCount,omitempty"`
}

type ReplaceTextOut struct {
	ReplacementsMade int   `json:"replacementsMade"`
	ReplacedAtLines  []int `json:"replacedAtLines"` // 1-based start lines of replaced oldText blocks
}

// replaceText replaces occurrences of OldText in a UTF-8 file.
//
// Behavior notes:
//   - File must exist, be regular, not a symlink, and valid UTF-8.
//   - Matching is line-wise using TrimSpace comparisons.
//   - oldText is the exact block that is replaced.
//   - textAbove/textBelow are locator-only context and are not replaced.
//   - Only blank or whitespace-only lines may lie between that context and the target.
//   - lineHint is a soft 1-based hint and may only be used when expectedCount == 1.
//   - newText is written exactly as provided after normalization into logical lines.
//   - newText="" means one blank line, not deletion.
//   - Writes are atomic and preserve newline style and final newline presence.
func replaceText(
	ctx context.Context,
	args ReplaceTextArgs,
	p fspolicy.FSPolicy,
) (*ReplaceTextOut, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if args.OldText == nil {
		return nil, errors.New("oldText is required")
	}
	if args.NewText == nil {
		return nil, errors.New("newText is required")
	}
	if args.LineHint != nil && *args.LineHint < 1 {
		return nil, fmt.Errorf("lineHint must be >= 1 (got %d)", *args.LineHint)
	}

	expected := 1
	if args.ExpectedCount != nil {
		expected = *args.ExpectedCount
	}
	if expected < 1 {
		return nil, fmt.Errorf("expectedCount must be >= 1 (got %d)", expected)
	}
	if args.LineHint != nil && expected != 1 {
		return nil, errors.New("lineHint may only be used when expectedCount == 1")
	}

	oldText := ioutil.NormalizeTextBlockString(*args.OldText)
	newText := ioutil.NormalizeTextBlockString(*args.NewText)
	textAbove := normalizeOptionalTextBlockString(args.TextAbove)
	textBelow := normalizeOptionalTextBlockString(args.TextBelow)

	tf, err := ioutil.ReadTextFileUTF8(p, args.Path, toolutil.MaxTextProcessingBytes)
	if err != nil {
		return nil, err
	}

	matchIdxs := findTrimmedReplaceTextMatchStarts(tf.Lines, textAbove, oldText, textBelow)

	// Overlap guard: overlapping matches make replacements ambiguous.
	if err := ioutil.EnsureNonOverlappingFixedWidth(matchIdxs, len(oldText)); err != nil {
		return nil, err
	}

	var hintDiag *ioutil.LineHintDiagnostic
	if expected == 1 {
		matchIdxs, hintDiag = ioutil.NarrowIndicesByLineHint(
			matchIdxs,
			args.LineHint,
			replaceTextLineHintTolerance,
		)
	}

	if len(matchIdxs) != expected {
		suggestion := "copy a longer unique oldText block from the file and add nearby textAbove/textBelow"
		if expected == 1 {
			suggestion += "; if you know roughly where the block starts, set lineHint near the intended start line"
		}
		return nil, fmt.Errorf(
			"replace match count mismatch: expected %d, found %d. diagnostics=%s suggestion=%s",
			expected,
			len(matchIdxs),
			buildReplaceTextDiagnosticJSON(
				tf.Lines,
				matchIdxs,
				len(oldText),
				hintDiag,
				replaceTextMaxAmbiguityDiagnosticCandidates,
				replaceTextAmbiguityDiagnosticContextLines,
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
		end := start + len(oldText) // exclusive
		tf.Lines = replaceLinesSlice(tf.Lines, start, end, newText)
	}

	outStr := tf.Render()
	if err := ioutil.WriteFileAtomicBytesResolved(p, tf.Path, []byte(outStr), tf.Perm, true); err != nil {
		return nil, err
	}

	replacedAt := make([]int, 0, len(matchIdxs))
	for _, idx := range matchIdxs {
		replacedAt = append(replacedAt, idx+1)
	}

	return &ReplaceTextOut{
		ReplacementsMade: len(matchIdxs),
		ReplacedAtLines:  replacedAt,
	}, nil
}

func normalizeOptionalTextBlockString(s *string) []string {
	if s == nil {
		return nil
	}
	return ioutil.NormalizeTextBlockString(*s)
}

func findTrimmedReplaceTextMatchStarts(lines, textAbove, oldText, textBelow []string) []int {
	if len(oldText) == 0 {
		return nil
	}

	tLines := ioutil.GetTrimmedLines(lines)
	tAbove := trimReplaceTextAboveBoundaryBlankLines(ioutil.GetTrimmedLines(textAbove))
	tOld := ioutil.GetTrimmedLines(oldText)
	tBelow := trimReplaceTextBelowBoundaryBlankLines(ioutil.GetTrimmedLines(textBelow))

	var idxs []int
	for start := 0; start+len(tOld) <= len(tLines); start++ {
		if !ioutil.IsBlockEqualsAt(tLines, tOld, start) {
			continue
		}
		if !replaceTextAboveContextMatchesAt(tLines, tAbove, start) {
			continue
		}
		if !replaceTextBelowContextMatchesAt(tLines, tBelow, start+len(tOld)) {
			continue
		}
		idxs = append(idxs, start)
	}

	return idxs
}

func replaceTextAboveContextMatchesAt(tLines, tAbove []string, targetStart int) bool {
	if len(tAbove) == 0 {
		return true
	}

	for end := targetStart; end >= len(tAbove); end-- {
		if end < targetStart && tLines[end] != "" {
			break
		}
		if ioutil.IsBlockEqualsAt(tLines, tAbove, end-len(tAbove)) {
			return true
		}
	}

	return false
}

func replaceTextBelowContextMatchesAt(tLines, tBelow []string, targetEnd int) bool {
	if len(tBelow) == 0 {
		return true
	}

	for start := targetEnd; start+len(tBelow) <= len(tLines); start++ {
		if start > targetEnd && tLines[start-1] != "" {
			break
		}
		if ioutil.IsBlockEqualsAt(tLines, tBelow, start) {
			return true
		}
	}

	return false
}

func trimReplaceTextAboveBoundaryBlankLines(lines []string) []string {
	if len(lines) == 0 || !replaceTextHasAnyNonBlankTrimmedLine(lines) {
		return replaceTextCloneStringSlice(lines)
	}
	end := len(lines)
	for end > 0 && lines[end-1] == "" {
		end--
	}
	return replaceTextCloneStringSlice(lines[:end])
}

func trimReplaceTextBelowBoundaryBlankLines(lines []string) []string {
	if len(lines) == 0 || !replaceTextHasAnyNonBlankTrimmedLine(lines) {
		return replaceTextCloneStringSlice(lines)
	}
	start := 0
	for start < len(lines) && lines[start] == "" {
		start++
	}
	return replaceTextCloneStringSlice(lines[start:])
}

func replaceTextHasAnyNonBlankTrimmedLine(lines []string) bool {
	for _, line := range lines {
		if line != "" {
			return true
		}
	}
	return false
}

type replaceTextCandidateDiagnostic struct {
	StartLine   int      `json:"startLine"`
	EndLine     int      `json:"endLine"`
	BeforeLines []string `json:"beforeLines,omitempty"`
	OldText     []string `json:"oldText"`
	AfterLines  []string `json:"afterLines,omitempty"`
}

type replaceTextDiagnostic struct {
	CandidateStartLines         []int                            `json:"candidateStartLines,omitempty"`
	AdditionalCandidatesOmitted int                              `json:"additionalCandidatesOmitted,omitempty"`
	SampleCandidates            []replaceTextCandidateDiagnostic `json:"sampleCandidates,omitempty"`
	LineHint                    *ioutil.LineHintDiagnostic       `json:"lineHint,omitempty"`
}

func buildReplaceTextDiagnosticJSON(
	lines []string,
	matchIdxs []int,
	oldWidth int,
	hint *ioutil.LineHintDiagnostic,
	maxCandidates int,
	contextLines int,
) string {
	diag := replaceTextDiagnostic{
		CandidateStartLines: ioutil.OneBasedLineNumbers(matchIdxs),
		LineHint:            hint,
	}

	if maxCandidates < 0 {
		maxCandidates = 0
	}
	if contextLines < 0 {
		contextLines = 0
	}
	if oldWidth < 1 {
		oldWidth = 1
	}

	limit := len(matchIdxs)
	if maxCandidates > 0 && limit > maxCandidates {
		limit = maxCandidates
		diag.AdditionalCandidatesOmitted = len(matchIdxs) - maxCandidates
	}

	if limit > 0 && len(lines) > 0 {
		diag.SampleCandidates = make([]replaceTextCandidateDiagnostic, 0, limit)
		for _, idx := range matchIdxs[:limit] {
			if idx < 0 || idx >= len(lines) {
				continue
			}

			matchEndExclusive := replaceTextMin(idx+oldWidth, len(lines))
			beforeStart := replaceTextMax(idx-contextLines, 0)
			afterEnd := replaceTextMin(matchEndExclusive+contextLines, len(lines))

			diag.SampleCandidates = append(diag.SampleCandidates, replaceTextCandidateDiagnostic{
				StartLine:   idx + 1,
				EndLine:     matchEndExclusive,
				BeforeLines: replaceTextCloneStringSlice(lines[beforeStart:idx]),
				OldText:     replaceTextCloneStringSlice(lines[idx:matchEndExclusive]),
				AfterLines:  replaceTextCloneStringSlice(lines[matchEndExclusive:afterEnd]),
			})
		}
	}

	b, err := json.Marshal(diag)
	if err != nil {
		return fmt.Sprintf("candidateStartLines=%v", ioutil.OneBasedLineNumbers(matchIdxs))
	}
	return string(b)
}

func replaceTextCloneStringSlice(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	out := make([]string, len(in))
	copy(out, in)
	return out
}

func replaceTextMin(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func replaceTextMax(a, b int) int {
	if a > b {
		return a
	}
	return b
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
