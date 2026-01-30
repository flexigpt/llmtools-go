package texttool

import (
	"context"
	"errors"
	"testing"
)

func TestReplaceTextLines_HappyPaths(t *testing.T) {
	dir := newWorkDir(t)

	tests := []struct {
		name         string
		initial      string
		args         func(path string) ReplaceTextLinesArgs
		wantContent  string
		wantMade     int
		wantAtLines  []int
		wantErrSub   string
		wantErrIsNil bool
	}{
		{
			name:    "replace_single_line_with_two_lines_default_expected_1",
			initial: "A\nB\nC\n",
			args: func(path string) ReplaceTextLinesArgs {
				return ReplaceTextLinesArgs{
					Path:             path,
					MatchLines:       []string{"B"},
					ReplaceWithLines: []string{"X", "Y"},
				}
			},
			wantContent:  "A\nX\nY\nC\n",
			wantMade:     1,
			wantAtLines:  []int{2},
			wantErrIsNil: true,
		},
		{
			name:    "replace_disambiguated_by_before_after",
			initial: "hdr\nctx1\nX\nctx2\nctx1\nX\nctx3\n",
			args: func(path string) ReplaceTextLinesArgs {
				return ReplaceTextLinesArgs{
					Path:                 path,
					BeforeLines:          []string{"ctx1"},
					MatchLines:           []string{"X"},
					AfterLines:           []string{"ctx2"},
					ReplaceWithLines:     []string{"REPL"},
					ExpectedReplacements: ptrInt(1),
				}
			},
			wantContent:  "hdr\nctx1\nREPL\nctx2\nctx1\nX\nctx3\n",
			wantMade:     1,
			wantAtLines:  []int{3},
			wantErrIsNil: true,
		},
		{
			name:    "preserves_crlf_newlines_and_final_newline",
			initial: "A\r\nB\r\n",
			args: func(path string) ReplaceTextLinesArgs {
				return ReplaceTextLinesArgs{
					Path:             path,
					MatchLines:       []string{"B"},
					ReplaceWithLines: []string{"X"},
				}
			},
			wantContent:  "A\r\nX\r\n",
			wantMade:     1,
			wantAtLines:  []int{2},
			wantErrIsNil: true,
		},
		{
			name:    "replacement_lines_are_written_verbatim_not_trimmed",
			initial: "A\nB\n",
			args: func(path string) ReplaceTextLinesArgs {
				return ReplaceTextLinesArgs{
					Path:             path,
					MatchLines:       []string{"B"},
					ReplaceWithLines: []string{"  X  "},
				}
			},
			wantContent:  "A\n  X  \n",
			wantMade:     1,
			wantAtLines:  []int{2},
			wantErrIsNil: true,
		},
		{
			name:    "multiple_replacements_expected_2_reports_original_line_numbers",
			initial: "A\nX\nB\nX\nC\n",
			args: func(path string) ReplaceTextLinesArgs {
				return ReplaceTextLinesArgs{
					Path:                 path,
					MatchLines:           []string{"X"},
					ReplaceWithLines:     []string{"Y"},
					ExpectedReplacements: ptrInt(2),
				}
			},
			wantContent:  "A\nY\nB\nY\nC\n",
			wantMade:     2,
			wantAtLines:  []int{2, 4},
			wantErrIsNil: true,
		},
		{
			name:    "replaceWithLines_embedded_newlines_splits_into_multiple_lines",
			initial: "A\nX\n",
			args: func(path string) ReplaceTextLinesArgs {
				return ReplaceTextLinesArgs{
					Path:             path,
					MatchLines:       []string{"X"},
					ReplaceWithLines: []string{"Y\nZ"},
				}
			},
			wantContent:  "A\nY\nZ\n",
			wantMade:     1,
			wantAtLines:  []int{2},
			wantErrIsNil: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := writeTempTextFile(t, dir, "repl-*.txt", tt.initial)
			args := tt.args(path)

			out, err := ReplaceTextLines(t.Context(), args)
			mustNoErr(t, err)
			if out.ReplacementsMade != len(out.ReplacedAtLines) {
				t.Fatalf(
					"invariant failed: ReplacementsMade=%d but len(ReplacedAtLines)=%d",
					out.ReplacementsMade,
					len(out.ReplacedAtLines),
				)
			}

			if out.ReplacementsMade != tt.wantMade {
				t.Fatalf("ReplacementsMade: want %d, got %d", tt.wantMade, out.ReplacementsMade)
			}
			if len(out.ReplacedAtLines) != len(tt.wantAtLines) {
				t.Fatalf(
					"ReplacedAtLines len: want %d, got %d (%v)",
					len(tt.wantAtLines),
					len(out.ReplacedAtLines),
					out.ReplacedAtLines,
				)
			}
			for i := range tt.wantAtLines {
				if out.ReplacedAtLines[i] != tt.wantAtLines[i] {
					t.Fatalf("ReplacedAtLines[%d]: want %d, got %d", i, tt.wantAtLines[i], out.ReplacedAtLines[i])
				}
			}

			got := readFileString(t, path)
			if got != tt.wantContent {
				t.Fatalf("content mismatch\nwant:\n%q\ngot:\n%q", tt.wantContent, got)
			}
		})
	}
}

func TestReplaceTextLines_ErrorCases(t *testing.T) {
	dir := newWorkDir(t)

	tests := []struct {
		name              string
		setup             func() string
		args              func(path string) ReplaceTextLinesArgs
		wantErrSub        string
		wantIsCtx         bool
		checkContentAfter bool
		wantContentAfter  string
	}{
		{
			name: "path_must_be_absolute",
			setup: func() string {
				_ = writeTempTextFile(t, dir, "x-*.txt", "A\n")
				return relativeTxt
			},
			args: func(path string) ReplaceTextLinesArgs {
				return ReplaceTextLinesArgs{Path: path, MatchLines: []string{"A"}, ReplaceWithLines: []string{"X"}}
			},
			wantErrSub: "path must be absolute",
		},
		{
			name: "matchLines_required",
			setup: func() string {
				return writeTempTextFile(t, dir, "x-*.txt", "A\n")
			},
			args: func(path string) ReplaceTextLinesArgs {
				return ReplaceTextLinesArgs{Path: path, MatchLines: nil, ReplaceWithLines: []string{"X"}}
			},
			wantErrSub: "matchLines is required",
		},
		{
			name: "replaceWithLines_required_nil",
			setup: func() string {
				return writeTempTextFile(t, dir, "x-*.txt", "A\n")
			},
			args: func(path string) ReplaceTextLinesArgs {
				return ReplaceTextLinesArgs{Path: path, MatchLines: []string{"A"}, ReplaceWithLines: nil}
			},
			wantErrSub: "replaceWithLines is required",
		},
		{
			name: "replaceWithLines_must_have_at_least_one_line",
			setup: func() string {
				return writeTempTextFile(t, dir, "x-*.txt", "A\n")
			},
			args: func(path string) ReplaceTextLinesArgs {
				return ReplaceTextLinesArgs{Path: path, MatchLines: []string{"A"}, ReplaceWithLines: []string{}}
			},
			wantErrSub: "must contain at least one line",
		},
		{
			name: "expectedReplacements_must_be_ge_1",
			setup: func() string {
				return writeTempTextFile(t, dir, "x-*.txt", "A\n")
			},
			args: func(path string) ReplaceTextLinesArgs {
				return ReplaceTextLinesArgs{
					Path:                 path,
					MatchLines:           []string{"A"},
					ReplaceWithLines:     []string{"X"},
					ExpectedReplacements: ptrInt(0),
				}
			},
			wantErrSub: "expectedReplacements must be >= 1",
		},
		{
			name: "match_count_mismatch",
			setup: func() string {
				return writeTempTextFile(t, dir, "x-*.txt", "A\nA\n")
			},
			args: func(path string) ReplaceTextLinesArgs {
				return ReplaceTextLinesArgs{
					Path:             path,
					MatchLines:       []string{"A"},
					ReplaceWithLines: []string{"X"},
					// Default expected=1, but found=2.
				}
			},
			wantErrSub: "replace match count mismatch",
		},
		{
			name: "match_count_mismatch_does_not_modify_file",
			setup: func() string {
				return writeTempTextFile(t, dir, "x-*.txt", "A\nA\n")
			},
			args: func(path string) ReplaceTextLinesArgs {
				return ReplaceTextLinesArgs{
					Path:             path,
					MatchLines:       []string{"A"},
					ReplaceWithLines: []string{"X"},
					// Default expected=1, but found=2.
				}
			},
			wantErrSub:        "replace match count mismatch",
			checkContentAfter: true,
			wantContentAfter:  "A\nA\n",
		},
		{
			name: "overlapping_matches_rejected",
			setup: func() string {
				return writeTempTextFile(t, dir, "x-*.txt", "X\nX\nX\n") //nolint:dupword // Test.
			},
			args: func(path string) ReplaceTextLinesArgs {
				return ReplaceTextLinesArgs{
					Path:                 path,
					MatchLines:           []string{"X", "X"},
					ReplaceWithLines:     []string{"Y", "Y"},
					ExpectedReplacements: ptrInt(2),
				}
			},
			wantErrSub:        "overlapping matches detected",
			checkContentAfter: true,
			wantContentAfter:  "X\nX\nX\n", //nolint:dupword // Test.
		},
		{
			name: "context_canceled",
			setup: func() string {
				return writeTempTextFile(t, dir, "x-*.txt", "A\n")
			},
			args: func(path string) ReplaceTextLinesArgs {
				return ReplaceTextLinesArgs{
					Path:             path,
					MatchLines:       []string{"A"},
					ReplaceWithLines: []string{"X"},
				}
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

			_, err := ReplaceTextLines(ctx, args)
			if tt.wantIsCtx {
				if err == nil || !errors.Is(err, context.Canceled) {
					t.Fatalf("expected context.Canceled, got %v", err)
				}
				return
			}
			mustErrContains(t, err, tt.wantErrSub)
			if tt.checkContentAfter {
				got := readFileString(t, path)
				if got != tt.wantContentAfter {
					t.Fatalf("file changed on error\nwant:\n%q\ngot:\n%q", tt.wantContentAfter, got)
				}
			}
		})
	}
}
