package texttool

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/flexigpt/llmtools-go/internal/fspolicy"
	"github.com/flexigpt/llmtools-go/internal/ioutil"
	"github.com/flexigpt/llmtools-go/internal/toolutil"
	"github.com/flexigpt/llmtools-go/spec"
)

const findTextFuncID spec.FuncID = "github.com/flexigpt/llmtools-go/texttool/findtext.FindText"

var findTextTool = spec.Tool{
	SchemaVersion: spec.SchemaVersion,
	ID:            "019c04d3-fba2-7a49-b1ed-8bdee5055db4",
	Slug:          "findtext",
	Version:       "v1.0.0",
	DisplayName:   "Find text matches with context",
	Description: "Locate exact edit targets in a UTF-8 text file and return matches with surrounding context and line numbers. " +
		"Request enough context lines to build a unique follow-up change block. " +
		"Use substring or regex to discover candidate areas, then reuse the returned exact text and line numbers in edit calls. Use lineBlock to validate that a pointed exact multi-line block is unique before editing. " +
		"For lineBlock mode, matching compares TrimSpace(line) and works best with a distinctive > 2 line block plus beforeLines/afterLines when the block may repeat.",
	Tags: []string{"text"},

	ArgSchema: spec.JSONSchema(`{
"$schema": "http://json-schema.org/draft-07/schema#",
"type": "object",
"properties": {
"path": {
	"type": "string",
	"description": "Path of the UTF-8 text file."
},
"queryType": {
	"type": "string",
	"enum": ["substring", "regex", "lineBlock"],
	"description": "Search mode. Use substring or regex to discover candidate regions. Use lineBlock to verify an exact multi-line edit locator before calling an edit tool."
},
"query": {
	"type": "string",
	"default": "",
	"description": "Required for queryType=substring/regex. The string/regex to find."
},
"matchLines": {
	"type": "array",
	"items": { "type": "string" },
	"minItems": 1,
	"description": "For queryType=lineBlock only: exact consecutive lines copied from the file to validate an edit locator. Prefer > 2 distinctive lines. Avoid generic single lines such as blank lines, braces, or repeated return statements. Newline characters in items are allowed and treated as line breaks."
},
"beforeLines": {
	"type": "array",
	"items": { "type": "string" },
	"minItems": 1,
	"description": "For queryType=lineBlock only: optional exact immediate-adjacent lines copied from the file that must appear directly before matchLines. Use 2-5 lines to disambiguate repeated blocks."
},
"afterLines": {
	"type": "array",
	"items": { "type": "string" },
	"minItems": 1,
	"description": "For queryType=lineBlock only: optional exact immediate-adjacent lines copied from the file that must appear directly after matchLines. Use 2-5 lines to disambiguate repeated blocks."
},
"contextLines": {
	"type": "integer",
	"minimum": 0,
	"default": 5,
	"description": "Number of lines to include before and after each returned match. Use enough context, usually 5-20 lines, to build a unique follow-up edit call and capture a useful maybeStartLine."
},
"maxMatches": {
	"type": "integer",
	"minimum": 1,
	"default": 10,
	"description": "Maximum number of matches to return. Keep this reasonably small while narrowing to a unique target."
}
},
"required": ["path", "queryType"],
"additionalProperties": false
}`),

	GoImpl: spec.GoToolImpl{FuncID: findTextFuncID},

	CreatedAt:  spec.SchemaStartTime,
	ModifiedAt: spec.SchemaStartTime,
}

const (
	findTypeSubstring = "substring"
	findTypeRegex     = "regex"
	findTypeLineBlock = "lineblock"
)

// Hard caps to keep responses sane for tool callers.
const (
	maxFindTextContextLines = 2000
	maxFindTextMaxMatches   = 500
	// Total number of context lines across all matches (rough bound).
	// This avoids exploding JSON outputs for large contextLines * maxMatches.
	maxFindTextTotalReturnedLines = 4000
)

type FindTextArgs struct {
	Path string `json:"path"`

	QueryType string `json:"queryType,omitempty"` // substring (default) | regex | lineBlock
	Query     string `json:"query,omitempty"`     // required for substring/regex

	MatchLines  []string `json:"matchLines,omitempty"` // required for lineBlock
	BeforeLines []string `json:"beforeLines,omitempty"`
	AfterLines  []string `json:"afterLines,omitempty"`

	ContextLines int `json:"contextLines,omitempty"` // default 5
	MaxMatches   int `json:"maxMatches,omitempty"`   // default 10
}

type FindTextLine struct {
	LineNumber int    `json:"lineNumber"` // 1-based
	Text       string `json:"text"`       // original line
}

type FindTextMatch struct {
	MatchStartLine          int            `json:"matchStartLine"` // 1-based
	MatchEndLine            int            `json:"matchEndLine"`   // 1-based
	BeforeContextLines      []FindTextLine `json:"beforeContextLines,omitempty"`
	MatchedLines            []FindTextLine `json:"matchedLines"`
	AfterContextLines       []FindTextLine `json:"afterContextLines,omitempty"`
	MatchedLinesWithContext []FindTextLine `json:"matchedLinesWithContext"` // includes matched lines as well (window around match)
}

type FindTextOut struct {
	ReachedMaxMatches        bool            `json:"reachedMaxMatches"`
	AdditionalMatchesOmitted int             `json:"additionalMatchesOmitted,omitempty"`
	MatchesReturned          int             `json:"matchesReturned"`
	Matches                  []FindTextMatch `json:"matches"`
}

// findText finds occurrences and returns matches with context.
// Behavior notes (entry point):
//   - File must exist, be regular, not a symlink, and valid UTF‑8.
//   - Matching uses TrimSpace per line for both file and input blocks.
//   - Returned lines are original file lines (not trimmed).
//   - Deterministic: matches are returned in ascending file order up to maxMatches.
//   - For queryType=lineBlock, overlapping matches are rejected.
func findText(ctx context.Context, args FindTextArgs, p fspolicy.FSPolicy) (*FindTextOut, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	qtype := strings.ToLower(strings.TrimSpace(args.QueryType))
	if qtype == "" {
		qtype = findTypeSubstring
	}
	switch qtype {
	case findTypeSubstring, findTypeRegex, findTypeLineBlock:
	default:
		return nil, fmt.Errorf(`invalid queryType %q (expected "substring", "regex", "lineBlock")`, args.QueryType)
	}

	contextLines := max(args.ContextLines, 0)
	if contextLines > maxFindTextContextLines {
		return nil, fmt.Errorf("contextLines too large: %d", contextLines)
	}

	maxMatches := args.MaxMatches
	if maxMatches <= 0 {
		maxMatches = 10
	}
	if maxMatches > maxFindTextMaxMatches {
		return nil, fmt.Errorf("maxMatches too large: %d", maxMatches)
	}

	tf, err := ioutil.ReadTextFileUTF8(p, args.Path, toolutil.MaxTextProcessingBytes)
	if err != nil {
		return nil, err
	}
	total := len(tf.Lines)

	// Empty file: deterministic empty output.
	if total == 0 {
		return &FindTextOut{Matches: nil, ReachedMaxMatches: false, MatchesReturned: 0}, nil
	}

	var (
		re          *regexp.Regexp
		substrQuery string
		block       []string
		beforeBlock []string
		afterBlock  []string
	)

	if qtype == findTypeRegex {
		if strings.TrimSpace(args.Query) == "" {
			return nil, errors.New("query is required for queryType=regex")
		}
		re, err = regexp.Compile(args.Query)
		if err != nil {
			return nil, err
		}
	}

	if qtype == findTypeSubstring {
		if strings.TrimSpace(args.Query) == "" {
			return nil, errors.New("query is required for queryType=substring")
		}
		substrQuery = strings.TrimSpace(args.Query)
	}

	// Reject irrelevant fields to reduce caller confusion.
	if qtype != findTypeLineBlock && len(args.MatchLines) > 0 {
		return nil, errors.New(`matchLines must be omitted when queryType is "substring" or "regex"`)
	}
	if qtype != findTypeLineBlock && (len(args.BeforeLines) > 0 || len(args.AfterLines) > 0) {
		return nil, errors.New(`beforeLines/afterLines must be omitted when queryType is "substring" or "regex"`)
	}

	if qtype == findTypeLineBlock {
		// Normalize input block so accidental embedded newlines in JSON strings behave sensibly.
		block = ioutil.NormalizeLineBlockInput(args.MatchLines)
		beforeBlock = ioutil.NormalizeLineBlockInput(args.BeforeLines)
		afterBlock = ioutil.NormalizeLineBlockInput(args.AfterLines)

		if len(block) == 0 {
			return nil, errors.New("matchLines is required for queryType=lineBlock")
		}
		// Disallow also supplying query to reduce confusion.
		if strings.TrimSpace(args.Query) != "" {
			return nil, errors.New(`query must be omitted/empty when queryType="lineBlock"`)
		}
	}

	out := &FindTextOut{
		Matches: make([]FindTextMatch, 0, min(maxMatches, 16)),
	}

	// Helper to enforce rough output bound.
	totalReturnedLines := 0
	addMatch := func(startIdx, endIdx int) error {
		// "startIdx/endIdx" are 0-based inclusive indices of the core match.
		if startIdx < 0 || endIdx < startIdx || endIdx >= total {
			return fmt.Errorf("internal error: invalid match range %d..%d", startIdx, endIdx)
		}

		ctxStart := max(0, startIdx-contextLines)
		ctxEnd := min(total-1, endIdx+contextLines)

		nCtx := ctxEnd - ctxStart + 1
		totalReturnedLines += nCtx
		if totalReturnedLines > maxFindTextTotalReturnedLines {
			return fmt.Errorf(
				"response too large (context window lines exceed %d). Reduce contextLines or maxMatches",
				maxFindTextTotalReturnedLines,
			)
		}

		beforeContext := make([]FindTextLine, 0, max(0, startIdx-ctxStart))
		matched := make([]FindTextLine, 0, endIdx-startIdx+1)
		afterContext := make([]FindTextLine, 0, max(0, ctxEnd-endIdx))
		context := make([]FindTextLine, 0, nCtx)
		for i := ctxStart; i <= ctxEnd; i++ {
			line := FindTextLine{
				LineNumber: i + 1,
				Text:       tf.Lines[i],
			}
			context = append(context, line)
			switch {
			case i < startIdx:
				beforeContext = append(beforeContext, line)
			case i <= endIdx:
				matched = append(matched, line)
			default:
				afterContext = append(afterContext, line)
			}
		}

		out.Matches = append(out.Matches, FindTextMatch{
			MatchStartLine:          startIdx + 1,
			MatchEndLine:            endIdx + 1,
			BeforeContextLines:      beforeContext,
			MatchedLines:            matched,
			AfterContextLines:       afterContext,
			MatchedLinesWithContext: context,
		})
		return nil
	}

	switch qtype {
	case findTypeSubstring, findTypeRegex:
		for i := range total {
			if err := ctx.Err(); err != nil {
				return nil, err
			}

			line := tf.Lines[i]
			lineForMatch := strings.TrimSpace(line)

			var ok bool
			if qtype == findTypeRegex {
				ok = re.MatchString(lineForMatch)
			} else {
				ok = strings.Contains(lineForMatch, substrQuery)
			}
			if !ok {
				continue
			}

			if len(out.Matches) < maxMatches {
				if err := addMatch(i, i); err != nil {
					return nil, err
				}
			} else {
				out.AdditionalMatchesOmitted++
			}
		}

	case findTypeLineBlock:
		// Find all occurrences of the trimmed-equal block with optional immediate context.
		idxs := ioutil.FindTrimmedAdjacentBlockMatches(tf.Lines, beforeBlock, block, afterBlock)

		// Overlap guard: overlapping matches for blocks are confusing, fail fast.
		if err := ioutil.EnsureNonOverlappingFixedWidth(idxs, len(block)); err != nil {
			return nil, err
		}

		for _, start := range idxs {
			if err := ctx.Err(); err != nil {
				return nil, err
			}

			end := start + len(block) - 1
			if len(out.Matches) < maxMatches {
				if err := addMatch(start, end); err != nil {
					return nil, err
				}
			} else {
				out.AdditionalMatchesOmitted++
			}
		}
	}

	out.ReachedMaxMatches = out.AdditionalMatchesOmitted > 0
	out.MatchesReturned = len(out.Matches)
	return out, nil
}
