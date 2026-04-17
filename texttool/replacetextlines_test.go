package texttool

import (
	"context"
	"errors"
	"testing"

	"github.com/flexigpt/llmtools-go/internal/fspolicy"
)

func TestReplaceTextLines_HappyPaths(t *testing.T) {
	dir := newWorkDir(t)
	policy, err := fspolicy.New("", nil, true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	tests := []struct {
		name        string
		initial     string
		args        func(path string) ReplaceTextLinesArgs
		wantContent string
		wantMade    int
		wantAtLines []int
	}{
		{
			name:    "replace_single_line_with_two_lines_default_expected_1",
			initial: "A\nB\nC\n",
			args: func(path string) ReplaceTextLinesArgs {
				return ReplaceTextLinesArgs{
					Path:    path,
					OldText: stringPtr("B"),
					NewText: stringPtr("X\nY"),
				}
			},
			wantContent: "A\nX\nY\nC\n",
			wantMade:    1,
			wantAtLines: []int{2},
		},
		{
			name:    "replace_disambiguated_by_textAbove_textBelow_with_blank_gap_tolerance",
			initial: "hdr\nctx1\n\nX\n\nctx2\nctx1\nX\nctx3\n",
			args: func(path string) ReplaceTextLinesArgs {
				return ReplaceTextLinesArgs{
					Path:      path,
					OldText:   stringPtr("X"),
					NewText:   stringPtr("REPL"),
					TextAbove: stringPtr("ctx1\n\n\n"),
					TextBelow: stringPtr("\n\nctx2"),
				}
			},
			wantContent: "hdr\nctx1\n\nREPL\n\nctx2\nctx1\nX\nctx3\n",
			wantMade:    1,
			wantAtLines: []int{4},
		},
		{
			name:    "preserves_crlf_newlines_and_final_newline",
			initial: "A\r\nB\r\n",
			args: func(path string) ReplaceTextLinesArgs {
				return ReplaceTextLinesArgs{
					Path:    path,
					OldText: stringPtr("B"),
					NewText: stringPtr("X"),
				}
			},
			wantContent: "A\r\nX\r\n",
			wantMade:    1,
			wantAtLines: []int{2},
		},
		{
			name:    "preserves_absence_of_final_newline",
			initial: "A\nB",
			args: func(path string) ReplaceTextLinesArgs {
				return ReplaceTextLinesArgs{
					Path:    path,
					OldText: stringPtr("B"),
					NewText: stringPtr("X"),
				}
			},
			wantContent: "A\nX",
			wantMade:    1,
			wantAtLines: []int{2},
		},
		{
			name:    "matching_uses_trimspace_but_newText_written_verbatim",
			initial: "A\n  B  \n",
			args: func(path string) ReplaceTextLinesArgs {
				return ReplaceTextLinesArgs{
					Path:    path,
					OldText: stringPtr("B"),
					NewText: stringPtr("  X  "),
				}
			},
			wantContent: "A\n  X  \n",
			wantMade:    1,
			wantAtLines: []int{2},
		},
		{
			name:    "multiple_replacements_expected_2_reports_original_line_numbers",
			initial: "A\nX\nB\nX\nC\n",
			args: func(path string) ReplaceTextLinesArgs {
				return ReplaceTextLinesArgs{
					Path:          path,
					OldText:       stringPtr("X"),
					NewText:       stringPtr("Y"),
					ExpectedCount: intPtr(2),
				}
			},
			wantContent: "A\nY\nB\nY\nC\n",
			wantMade:    2,
			wantAtLines: []int{2, 4},
		},
		{
			name:    "newText_embedded_newlines_splits_into_multiple_lines",
			initial: "A\nX\n",
			args: func(path string) ReplaceTextLinesArgs {
				return ReplaceTextLinesArgs{
					Path:    path,
					OldText: stringPtr("X"),
					NewText: stringPtr("Y\nZ"),
				}
			},
			wantContent: "A\nY\nZ\n",
			wantMade:    1,
			wantAtLines: []int{2},
		},
		{
			name:    "newText_empty_string_means_one_blank_line_not_deletion",
			initial: "A\nX\nB\n",
			args: func(path string) ReplaceTextLinesArgs {
				return ReplaceTextLinesArgs{
					Path:    path,
					OldText: stringPtr("X"),
					NewText: stringPtr(""),
				}
			},
			wantContent: "A\n\nB\n",
			wantMade:    1,
			wantAtLines: []int{2},
		},
		{
			name:    "oldText_empty_string_can_replace_blank_line",
			initial: "A\n\nB\n",
			args: func(path string) ReplaceTextLinesArgs {
				return ReplaceTextLinesArgs{
					Path:      path,
					OldText:   stringPtr(""),
					NewText:   stringPtr("X"),
					TextAbove: stringPtr("A"),
					TextBelow: stringPtr("B"),
				}
			},
			wantContent: "A\nX\nB\n",
			wantMade:    1,
			wantAtLines: []int{2},
		},
		{
			name:    "lineHint_narrows_ambiguous_match",
			initial: "A\nX\nB\nX\nC\n",
			args: func(path string) ReplaceTextLinesArgs {
				return ReplaceTextLinesArgs{
					Path:     path,
					OldText:  stringPtr("X"),
					NewText:  stringPtr("Y"),
					LineHint: intPtr(4),
				}
			},
			wantContent: "A\nX\nB\nY\nC\n",
			wantMade:    1,
			wantAtLines: []int{4},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := writeTempTextFile(t, dir, "repl-*.txt", tt.initial)
			args := tt.args(path)

			out, err := replaceTextLines(t.Context(), args, policy)
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
	policy, err := fspolicy.New("", nil, true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

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
			name: "oldText_required",
			setup: func() string {
				return writeTempTextFile(t, dir, "x-*.txt", "A\n")
			},
			args: func(path string) ReplaceTextLinesArgs {
				return ReplaceTextLinesArgs{
					Path:    path,
					OldText: nil,
					NewText: stringPtr("X"),
				}
			},
			wantErrSub: "oldText is required",
		},
		{
			name: "newText_required_nil",
			setup: func() string {
				return writeTempTextFile(t, dir, "x-*.txt", "A\n")
			},
			args: func(path string) ReplaceTextLinesArgs {
				return ReplaceTextLinesArgs{
					Path:    path,
					OldText: stringPtr("A"),
					NewText: nil,
				}
			},
			wantErrSub: "newText is required",
		},
		{
			name: "expectedCount_must_be_ge_1",
			setup: func() string {
				return writeTempTextFile(t, dir, "x-*.txt", "A\n")
			},
			args: func(path string) ReplaceTextLinesArgs {
				return ReplaceTextLinesArgs{
					Path:          path,
					OldText:       stringPtr("A"),
					NewText:       stringPtr("X"),
					ExpectedCount: intPtr(0),
				}
			},
			wantErrSub: "expectedCount must be >= 1",
		},
		{
			name: "lineHint_must_be_ge_1",
			setup: func() string {
				return writeTempTextFile(t, dir, "x-*.txt", "A\n")
			},
			args: func(path string) ReplaceTextLinesArgs {
				return ReplaceTextLinesArgs{
					Path:     path,
					OldText:  stringPtr("A"),
					NewText:  stringPtr("X"),
					LineHint: intPtr(0),
				}
			},
			wantErrSub: "lineHint must be >= 1",
		},
		{
			name: "lineHint_may_only_be_used_when_expectedCount_is_1",
			setup: func() string {
				return writeTempTextFile(t, dir, "x-*.txt", "A\nA\n")
			},
			args: func(path string) ReplaceTextLinesArgs {
				return ReplaceTextLinesArgs{
					Path:          path,
					OldText:       stringPtr("A"),
					NewText:       stringPtr("X"),
					LineHint:      intPtr(1),
					ExpectedCount: intPtr(2),
				}
			},
			wantErrSub: "lineHint may only be used when expectedCount == 1",
		},
		{
			name: "match_count_mismatch_does_not_modify_file",
			setup: func() string {
				return writeTempTextFile(t, dir, "x-*.txt", "A\nA\n")
			},
			args: func(path string) ReplaceTextLinesArgs {
				return ReplaceTextLinesArgs{
					Path:    path,
					OldText: stringPtr("A"),
					NewText: stringPtr("X"),
				}
			},
			wantErrSub:        "replace match count mismatch",
			checkContentAfter: true,
			wantContentAfter:  "A\nA\n",
		},
		{
			name: "lineHint_tie_does_not_break_ambiguity",
			setup: func() string {
				return writeTempTextFile(t, dir, "x-*.txt", "A\nX\nB\nX\nC\n")
			},
			args: func(path string) ReplaceTextLinesArgs {
				return ReplaceTextLinesArgs{
					Path:     path,
					OldText:  stringPtr("X"),
					NewText:  stringPtr("Y"),
					LineHint: intPtr(3),
				}
			},
			wantErrSub:        "replace match count mismatch",
			checkContentAfter: true,
			wantContentAfter:  "A\nX\nB\nX\nC\n",
		},
		{
			name: "textAbove_does_not_skip_nonblank_lines",
			setup: func() string {
				return writeTempTextFile(t, dir, "x-*.txt", "A\nmid\nX\n")
			},
			args: func(path string) ReplaceTextLinesArgs {
				return ReplaceTextLinesArgs{
					Path:      path,
					OldText:   stringPtr("X"),
					NewText:   stringPtr("Y"),
					TextAbove: stringPtr("A"),
				}
			},
			wantErrSub:        "replace match count mismatch",
			checkContentAfter: true,
			wantContentAfter:  "A\nmid\nX\n",
		},
		{
			name: "overlapping_matches_rejected",
			setup: func() string {
				return writeTempTextFile(t, dir, "x-*.txt", "X\nX\nX\n") //nolint:dupword // Test.
			},
			args: func(path string) ReplaceTextLinesArgs {
				return ReplaceTextLinesArgs{
					Path:          path,
					OldText:       stringPtr("X\nX"),
					NewText:       stringPtr("Y\nY"),
					ExpectedCount: intPtr(2),
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
					Path:    path,
					OldText: stringPtr("A"),
					NewText: stringPtr("X"),
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

			_, err := replaceTextLines(ctx, args, policy)
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
