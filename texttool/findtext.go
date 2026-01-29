package texttool

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/flexigpt/llmtools-go/internal/fileutil"
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
	Description:   "Search a UTF-8 text file and return matching lines/blocks with surrounding context lines. Modes: substring, RE2 regex (line-by-line), or exact line-block match. Matching compares TrimSpace(line).",
	Tags:          []string{"text"},

	ArgSchema: spec.JSONSchema(`{
"$schema": "http://json-schema.org/draft-07/schema#",
"type": "object",
"properties": {
	"path": {
		"type": "string",
		"description": "Absolute path of the UTF-8 text file."
	},
	"queryType": {
		"type": "string",
		"enum": ["substring", "regex", "lineBlock"],
		"default": "substring",
		  "description": "Search mode."
	},
	"query": {
		"type": "string",
		"description": "Query string for queryType=substring or regex (Go/RE2 regex), else omit."
	},
	"matchLines": {
		"type": "array",
		"items": { "type": "string" },
		"minItems": 1,
		"description": "For queryType=lineBlock: block of lines to match. Newline characters in items are allowed and treated as line breaks."
	},
	"contextLines": {
		"type": "integer",
		"minimum": 0,
		"default": 5,
		"description": "Number of lines to include before and after each match (for lineBlock matches, around the entire block)."
	},
	"maxMatches": {
		"type": "integer",
		"minimum": 1,
		"default": 10,
		"description": "Stop after this many matches."
	}
},
"required": ["path"],
"oneOf": [
  {
	"title": "substring-or-regex",
	"properties": {
		"queryType": { "enum": ["substring", "regex"], "default": "substring" },
		"query": { "type": "string", "minLength": 1 }
	},
	"required": ["query"],
	"not": { "required": ["matchLines"] }
  },
  {
	"title": "line-block",
	"properties": {
		"queryType": { "const": "lineBlock" },
		"query": { "type": "string", "maxLength": 0 }
	},
	"required": ["queryType", "matchLines"]
  }
],
"additionalProperties": false
}`),

	GoImpl: spec.GoToolImpl{FuncID: findTextFuncID},

	CreatedAt:  spec.SchemaStartTime,
	ModifiedAt: spec.SchemaStartTime,
}

func FindTextTool() spec.Tool { return toolutil.CloneTool(findTextTool) }

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

	MatchLines []string `json:"matchLines,omitempty"` // required for lineBlock

	ContextLines int `json:"contextLines,omitempty"` // default 5
	MaxMatches   int `json:"maxMatches,omitempty"`   // default 10
}

type FindTextLine struct {
	LineNumber int    `json:"lineNumber"` // 1-based
	Text       string `json:"text"`       // original line
}

type FindTextMatch struct {
	MatchStartLine          int            `json:"matchStartLine"`          // 1-based
	MatchEndLine            int            `json:"matchEndLine"`            // 1-based
	MatchedLinesWithContext []FindTextLine `json:"matchedLinesWithContext"` // includes matched lines as well (window around match)
}

type FindTextOut struct {
	ReachedMaxMatches bool            `json:"reachedMaxMatches"`
	MatchesReturned   int             `json:"matchesReturned"`
	Matches           []FindTextMatch `json:"matches"`
}

// FindText finds occurrences and returns matches with context.
// Behavior notes (entry point):
//   - File must exist, be regular, not a symlink, and valid UTF‑8.
//   - Matching uses TrimSpace per line for both file and input blocks.
//   - Returned lines are original file lines (not trimmed).
//   - Deterministic: matches are returned in ascending file order up to maxMatches.
//   - For queryType=lineBlock, overlapping matches are rejected.
func FindText(ctx context.Context, args FindTextArgs) (*FindTextOut, error) {
	return toolutil.WithRecoveryResp(func() (*FindTextOut, error) {
		return findText(ctx, args)
	})
}

func findText(ctx context.Context, args FindTextArgs) (*FindTextOut, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	path, err := fileutil.NormalizeAbsPath(args.Path)
	if err != nil {
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

	tf, err := fileutil.ReadTextFileUTF8(path, toolutil.MaxTextProcessingBytes)
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

	if qtype == findTypeLineBlock {
		// Normalize input block so accidental embedded newlines in JSON strings behave sensibly.
		block = fileutil.NormalizeLineBlockInput(args.MatchLines)
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

		context := make([]FindTextLine, 0, nCtx)
		for i := ctxStart; i <= ctxEnd; i++ {
			context = append(context, FindTextLine{
				LineNumber: i + 1,
				Text:       tf.Lines[i],
			})
		}

		out.Matches = append(out.Matches, FindTextMatch{
			MatchStartLine:          startIdx + 1,
			MatchEndLine:            endIdx + 1,
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

			if err := addMatch(i, i); err != nil {
				return nil, err
			}
			if len(out.Matches) >= maxMatches {
				out.ReachedMaxMatches = true
				break
			}
		}

	case findTypeLineBlock:
		// Find all occurrences of the trimmed-equal block.
		idxs := fileutil.FindTrimmedBlockMatches(tf.Lines, block)

		// Overlap guard: overlapping matches for blocks are confusing, fail fast.
		if err := fileutil.EnsureNonOverlappingFixedWidth(idxs, len(block)); err != nil {
			return nil, err
		}

		for _, start := range idxs {
			if err := ctx.Err(); err != nil {
				return nil, err
			}

			end := start + len(block) - 1
			if err := addMatch(start, end); err != nil {
				return nil, err
			}
			if len(out.Matches) >= maxMatches {
				out.ReachedMaxMatches = true
				break
			}
		}
	}

	out.MatchesReturned = len(out.Matches)
	return out, nil
}
