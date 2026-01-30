package texttool

import (
	"context"
	"errors"
	"runtime"
	"testing"

	"github.com/flexigpt/llmtools-go/internal/toolutil"
)

func TestFindText_HappyPaths(t *testing.T) {
	dir := newWorkDir(t)

	tests := []struct {
		name        string
		initial     string
		args        func(path string) FindTextArgs
		wantMatches int
		wantReached bool
		assert      func(t *testing.T, out *FindTextOut)
	}{
		{
			name:    "substring_default_queryType_trimmed_line_match_and_context",
			initial: " alpha \nbeta\n gamma alpha \ndelta\n",
			args: func(path string) FindTextArgs {
				return FindTextArgs{
					Path:         path,
					Query:        "alpha",
					ContextLines: 1,
					MaxMatches:   10,
				}
			},
			wantMatches: 2,
			wantReached: false,
			assert: func(t *testing.T, out *FindTextOut) {
				t.Helper()
				if out.Matches[0].MatchStartLine != 1 || out.Matches[0].MatchEndLine != 1 {
					t.Fatalf(
						"match0 range: want 1..1 got %d..%d",
						out.Matches[0].MatchStartLine,
						out.Matches[0].MatchEndLine,
					)
				}
				// ContextLines=1 => for line 1 should include [1..2].
				if len(out.Matches[0].MatchedLinesWithContext) != 2 {
					t.Fatalf("match0 context lines: want 2 got %d", len(out.Matches[0].MatchedLinesWithContext))
				}
				if out.Matches[0].MatchedLinesWithContext[0].LineNumber != 1 ||
					out.Matches[0].MatchedLinesWithContext[0].Text != " alpha " {
					t.Fatalf("match0 ctx[0] unexpected: %+v", out.Matches[0].MatchedLinesWithContext[0])
				}
			},
		},
		{
			name:    "substring_negative_contextLines_treated_as_0",
			initial: "A\nB\nA\n",
			args: func(path string) FindTextArgs {
				return FindTextArgs{
					Path:         path,
					QueryType:    "substring",
					Query:        "A",
					ContextLines: -10,
					MaxMatches:   10,
				}
			},
			wantMatches: 2,
			wantReached: false,
		},
		{
			name:    "substring_query_is_trimmed",
			initial: "alpha\n",
			args: func(path string) FindTextArgs {
				return FindTextArgs{
					Path:         path,
					QueryType:    "substring",
					Query:        "  alpha  ",
					ContextLines: 0,
					MaxMatches:   10,
				}
			},
			wantMatches: 1,
			wantReached: false,
		},
		{
			name:    "queryType_is_trimmed_and_case_insensitive",
			initial: " alpha \nbeta\n",
			args: func(path string) FindTextArgs {
				return FindTextArgs{
					Path:         path,
					QueryType:    "  ReGeX  ",
					Query:        `^alpha$`,
					ContextLines: 0,
					MaxMatches:   10,
				}
			},
			wantMatches: 1,
			wantReached: false,
		},
		{
			name:    "regex_queryType_matches_trimmed_line",
			initial: " alpha \nbeta\n",
			args: func(path string) FindTextArgs {
				return FindTextArgs{
					Path:         path,
					QueryType:    "regex",
					Query:        `^alpha$`,
					ContextLines: 0,
					MaxMatches:   10,
				}
			},
			wantMatches: 1,
			wantReached: false,
		},
		{
			name:    "lineBlock_matches_multiline_block_trimmed_with_linebreaks",
			initial: "a\n beta \n gamma \nd\n",
			args: func(path string) FindTextArgs {
				return FindTextArgs{
					Path:         path,
					QueryType:    "LiNeBlOcK",
					MatchLines:   []string{"beta\ngamma"},
					ContextLines: 0,
					MaxMatches:   10,
				}
			},
			wantMatches: 1,
			wantReached: false,
			assert: func(t *testing.T, out *FindTextOut) {
				t.Helper()
				if out.Matches[0].MatchStartLine != 2 || out.Matches[0].MatchEndLine != 3 {
					t.Fatalf(
						"block match range: want 2..3 got %d..%d",
						out.Matches[0].MatchStartLine,
						out.Matches[0].MatchEndLine,
					)
				}
				if len(out.Matches[0].MatchedLinesWithContext) != 2 {
					t.Fatalf("context lines: want 2 got %d", len(out.Matches[0].MatchedLinesWithContext))
				}
				if out.Matches[0].MatchedLinesWithContext[0].LineNumber != 2 ||
					out.Matches[0].MatchedLinesWithContext[0].Text != " beta " {
					t.Fatalf("ctx[0] unexpected: %+v", out.Matches[0].MatchedLinesWithContext[0])
				}
				if out.Matches[0].MatchedLinesWithContext[1].LineNumber != 3 ||
					out.Matches[0].MatchedLinesWithContext[1].Text != " gamma " {
					t.Fatalf("ctx[1] unexpected: %+v", out.Matches[0].MatchedLinesWithContext[1])
				}
			},
		},
		{
			name:    "maxMatches_enforced_and_reachedMaxMatches_set",
			initial: "hit\nhit\nhit\n", //nolint:dupword // Test.
			args: func(path string) FindTextArgs {
				return FindTextArgs{
					Path:         path,
					QueryType:    "substring",
					Query:        "hit",
					ContextLines: 0,
					MaxMatches:   2,
				}
			},
			wantMatches: 2,
			wantReached: true,
		},
		{
			name:    "maxMatches_0_defaults_to_10_and_sets_reachedMaxMatches",
			initial: makeNLines(11, func(i int) string { return "hit" }, "\n", true),
			args: func(path string) FindTextArgs {
				return FindTextArgs{
					Path:         path,
					QueryType:    "substring",
					Query:        "hit",
					ContextLines: 0,
					MaxMatches:   0, // default to 10
				}
			},
			wantMatches: 10,
			wantReached: true,
			assert: func(t *testing.T, out *FindTextOut) {
				t.Helper()
				if len(out.Matches) != 10 {
					t.Fatalf("len(Matches): want 10 got %d", len(out.Matches))
				}
				// Deterministic order: should return lines 1..10.
				last := out.Matches[9]
				if last.MatchStartLine != 10 || last.MatchEndLine != 10 {
					t.Fatalf("last match range: want 10..10 got %d..%d", last.MatchStartLine, last.MatchEndLine)
				}
			},
		},
		{
			name:    "non_empty_file_no_matches_returns_empty",
			initial: "A\nB\n",
			args: func(path string) FindTextArgs {
				return FindTextArgs{
					Path:         path,
					QueryType:    "substring",
					Query:        "NOPE",
					ContextLines: 0,
					MaxMatches:   10,
				}
			},
			wantMatches: 0,
			wantReached: false,
		},
		{
			name:    "empty_file_returns_empty_deterministically",
			initial: "",
			args: func(path string) FindTextArgs {
				return FindTextArgs{
					Path:         path,
					QueryType:    "substring",
					Query:        "x",
					ContextLines: 5,
					MaxMatches:   10,
				}
			},
			wantMatches: 0,
			wantReached: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := writeTempTextFile(t, dir, "find-*.txt", tt.initial)
			args := tt.args(path)

			out, err := FindText(t.Context(), args)
			mustNoErr(t, err)
			if out.MatchesReturned != len(out.Matches) {
				t.Fatalf("invariant failed: MatchesReturned=%d len(Matches)=%d", out.MatchesReturned, len(out.Matches))
			}
			if out.MatchesReturned != tt.wantMatches {
				t.Fatalf("MatchesReturned: want %d, got %d", tt.wantMatches, out.MatchesReturned)
			}
			if out.ReachedMaxMatches != tt.wantReached {
				t.Fatalf("ReachedMaxMatches: want %v, got %v", tt.wantReached, out.ReachedMaxMatches)
			}
			if tt.assert != nil && tt.wantMatches > 0 {
				tt.assert(t, out)
			}
		})
	}
}

func TestFindText_ErrorAndBoundaryCases(t *testing.T) {
	dir := newWorkDir(t)

	tests := []struct {
		name       string
		setup      func() string
		args       func(path string) FindTextArgs
		wantErrSub string
		wantIsCtx  bool
	}{
		{
			name: "path_must_be_absolute",
			setup: func() string {
				_ = writeTempTextFile(t, dir, "x-*.txt", "A\n")
				return relativeTxt
			},
			args: func(path string) FindTextArgs {
				return FindTextArgs{Path: path, QueryType: "substring", Query: "A"}
			},
			wantErrSub: "path must be absolute",
		},
		{
			name: "invalid_queryType",
			setup: func() string {
				return writeTempTextFile(t, dir, "x-*.txt", "A\n")
			},
			args: func(path string) FindTextArgs {
				return FindTextArgs{Path: path, QueryType: "wat", Query: "A"}
			},
			wantErrSub: "invalid queryType",
		},
		{
			name: "substring_requires_query",
			setup: func() string {
				return writeTempTextFile(t, dir, "x-*.txt", "A\n")
			},
			args: func(path string) FindTextArgs {
				return FindTextArgs{Path: path, QueryType: "substring", Query: "   "}
			},
			wantErrSub: "query is required for queryType=substring",
		},
		{
			name: "regex_requires_query",
			setup: func() string {
				return writeTempTextFile(t, dir, "x-*.txt", "A\n")
			},
			args: func(path string) FindTextArgs {
				return FindTextArgs{Path: path, QueryType: "regex", Query: ""}
			},
			wantErrSub: "query is required for queryType=regex",
		},
		{
			name: "regex_compile_error",
			setup: func() string {
				return writeTempTextFile(t, dir, "x-*.txt", "A\n")
			},
			args: func(path string) FindTextArgs {
				return FindTextArgs{Path: path, QueryType: "regex", Query: "("}
			},
			wantErrSub: "error parsing regexp",
		},
		{
			name: "matchLines_must_be_omitted_for_substring",
			setup: func() string {
				return writeTempTextFile(t, dir, "x-*.txt", "A\n")
			},
			args: func(path string) FindTextArgs {
				return FindTextArgs{
					Path:       path,
					QueryType:  "substring",
					Query:      "A",
					MatchLines: []string{"A"},
				}
			},
			wantErrSub: "matchLines must be omitted",
		},
		{
			name: "matchLines_must_be_omitted_for_regex",
			setup: func() string {
				return writeTempTextFile(t, dir, "x-*.txt", "A\n")
			},
			args: func(path string) FindTextArgs {
				return FindTextArgs{
					Path:       path,
					QueryType:  "regex",
					Query:      "A",
					MatchLines: []string{"A"},
				}
			},
			wantErrSub: "matchLines must be omitted",
		},
		{
			name: "lineBlock_requires_matchLines",
			setup: func() string {
				return writeTempTextFile(t, dir, "x-*.txt", "A\n")
			},
			args: func(path string) FindTextArgs {
				return FindTextArgs{
					Path:       path,
					QueryType:  "lineBlock",
					MatchLines: nil,
				}
			},
			wantErrSub: "matchLines is required for queryType=lineBlock",
		},
		{
			name: "lineBlock_disallows_query",
			setup: func() string {
				return writeTempTextFile(t, dir, "x-*.txt", "A\n")
			},
			args: func(path string) FindTextArgs {
				return FindTextArgs{
					Path:       path,
					QueryType:  "lineBlock",
					Query:      "nope",
					MatchLines: []string{"A"},
				}
			},
			wantErrSub: "query must be omitted/empty",
		},
		{
			name: "contextLines_too_large",
			setup: func() string {
				return writeTempTextFile(t, dir, "x-*.txt", "A\n")
			},
			args: func(path string) FindTextArgs {
				return FindTextArgs{
					Path:         path,
					QueryType:    "substring",
					Query:        "A",
					ContextLines: 2001,
				}
			},
			wantErrSub: "contextLines too large",
		},
		{
			name: "maxMatches_too_large",
			setup: func() string {
				return writeTempTextFile(t, dir, "x-*.txt", "A\n")
			},
			args: func(path string) FindTextArgs {
				return FindTextArgs{
					Path:       path,
					QueryType:  "substring",
					Query:      "A",
					MaxMatches: 501,
				}
			},
			wantErrSub: "maxMatches too large",
		},
		{
			name: "lineBlock_overlapping_matches_rejected",
			setup: func() string {
				return writeTempTextFile(t, dir, "x-*.txt", "X\nX\nX\n") //nolint:dupword // Test.
			},
			args: func(path string) FindTextArgs {
				return FindTextArgs{
					Path:         path,
					QueryType:    "lineBlock",
					MatchLines:   []string{"X", "X"},
					ContextLines: 0,
					MaxMatches:   10,
				}
			},
			wantErrSub: "overlapping matches detected",
		},
		{
			name: "response_too_large_guard",
			setup: func() string {
				// 5000 short lines, one hit in the middle.
				content := makeNLines(5000, func(i int) string {
					if i == 2500 {
						return "HIT"
					}
					return "x"
				}, "\n", true)
				return writeTempTextFile(t, dir, "big-*.txt", content)
			},
			args: func(path string) FindTextArgs {
				return FindTextArgs{
					Path:         path,
					QueryType:    "substring",
					Query:        "HIT",
					ContextLines: 2000, // allowed (cap=2000) but will exceed totalReturnedLines cap (4000)
					MaxMatches:   10,
				}
			},
			wantErrSub: "response too large",
		},
		{
			name: "file_not_found",
			setup: func() string {
				return dir + string(filepathSep()) + "nope-does-not-exist.txt"
			},
			args: func(path string) FindTextArgs {
				return FindTextArgs{
					Path:      path,
					QueryType: "substring",
					Query:     "A",
				}
			},
			wantErrSub: "", // platform dependent; just require non-nil error
		},
		{
			name: "invalid_utf8_rejected",
			setup: func() string {
				return writeTempBytesFile(t, dir, "bad-*.txt", []byte{0xff, 0xfe, 0xfd})
			},
			args: func(path string) FindTextArgs {
				return FindTextArgs{
					Path:      path,
					QueryType: "substring",
					Query:     "A",
				}
			},
			wantErrSub: "not valid UTF-8",
		},
		{
			name: "symlink_file_rejected",
			setup: func() string {
				if runtime.GOOS == toolutil.GOOSWindows {
					t.Skip("symlink behavior is platform/privilege-dependent on Windows")
				}
				target := writeTempTextFile(t, dir, "target-*.txt", "A\n")
				link := dir + string(filepathSep()) + "link-find.txt"
				if err := osSymlink(target, link); err != nil {
					t.Skipf("os.Symlink not available: %v", err)
				}
				abs, err := filepathAbs(link)
				if err != nil {
					t.Fatalf("Abs(%q): %v", link, err)
				}
				return abs
			},
			args: func(path string) FindTextArgs {
				return FindTextArgs{
					Path:      path,
					QueryType: "substring",
					Query:     "A",
				}
			},
			wantErrSub: "symlink",
		},
		{
			name: "context_canceled",
			setup: func() string {
				return writeTempTextFile(t, dir, "x-*.txt", "A\n")
			},
			args: func(path string) FindTextArgs {
				return FindTextArgs{Path: path, QueryType: "substring", Query: "A"}
			},
			wantIsCtx: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := tt.setup()
			args := tt.args(path)

			ctx := t.Context()
			if tt.wantIsCtx {
				cctx, cancel := context.WithCancel(ctx)
				cancel()
				ctx = cctx
			}

			_, err := FindText(ctx, args)
			if tt.wantIsCtx {
				if err == nil || !errors.Is(err, context.Canceled) {
					t.Fatalf("expected context.Canceled, got %v", err)
				}
				return
			}
			if tt.wantErrSub == "" {
				if err == nil {
					t.Fatalf("expected error, got nil")
				}
				return
			}
			mustErrContains(t, err, tt.wantErrSub)
		})
	}
}
