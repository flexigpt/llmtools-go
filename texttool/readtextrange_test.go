package texttool

import (
	"context"
	"errors"
	"testing"
)

func TestReadTextRange_HappyPaths(t *testing.T) {
	dir := newWorkDir(t)

	tests := []struct {
		name         string
		initial      string
		args         func(path string) ReadTextRangeArgs
		wantStart    int
		wantEnd      int
		wantCount    int
		wantLine1    *ReadTextRangeLine
		wantErrSub   string
		wantErrIsNil bool
	}{
		{
			name:    "no_markers_returns_entire_file",
			initial: "A\nB\nC\n",
			args: func(path string) ReadTextRangeArgs {
				return ReadTextRangeArgs{Path: path}
			},
			wantStart:    1,
			wantEnd:      3,
			wantCount:    3,
			wantErrIsNil: true,
		},
		{
			name:    "start_marker_only_includes_start_block",
			initial: "a\nSTART\nb\nc\n",
			args: func(path string) ReadTextRangeArgs {
				return ReadTextRangeArgs{
					Path:            path,
					StartMatchLines: []string{"START"},
				}
			},
			wantStart:    2,
			wantEnd:      4,
			wantCount:    3,
			wantErrIsNil: true,
		},
		{
			name:    "end_marker_only_includes_end_block",
			initial: "a\nb\nEND\nc\n",
			args: func(path string) ReadTextRangeArgs {
				return ReadTextRangeArgs{
					Path:          path,
					EndMatchLines: []string{"END"},
				}
			},
			wantStart:    1,
			wantEnd:      3,
			wantCount:    3,
			wantErrIsNil: true,
		},
		{
			name:    "both_markers_trimmed_match_and_order_enforced",
			initial: "a\n  START  \nb\nEND\nc\n",
			args: func(path string) ReadTextRangeArgs {
				return ReadTextRangeArgs{
					Path:            path,
					StartMatchLines: []string{"START"},
					EndMatchLines:   []string{"END"},
				}
			},
			wantStart:    2,
			wantEnd:      4,
			wantCount:    3,
			wantErrIsNil: true,
		},
		{
			name:    "file_with_single_empty_line_and_final_newline",
			initial: "\n",
			args: func(path string) ReadTextRangeArgs {
				return ReadTextRangeArgs{Path: path}
			},
			wantStart:    1,
			wantEnd:      1,
			wantCount:    1,
			wantLine1:    &ReadTextRangeLine{LineNumber: 1, Text: ""},
			wantErrIsNil: true,
		},
		{
			name:    "multi_line_start_and_end_markers_select_including_full_blocks",
			initial: "hdr\nSTART1\nSTART2\nx\nEND1\nEND2\ntail\n",
			args: func(path string) ReadTextRangeArgs {
				return ReadTextRangeArgs{
					Path:            path,
					StartMatchLines: []string{"START1", "START2"},
					EndMatchLines:   []string{"END1", "END2"},
				}
			},
			wantStart:    2,
			wantEnd:      6,
			wantCount:    5,
			wantLine1:    &ReadTextRangeLine{LineNumber: 2, Text: "START1"},
			wantErrIsNil: true,
		},
		{
			name:    "empty_file_returns_empty_deterministically",
			initial: "",
			args: func(path string) ReadTextRangeArgs {
				return ReadTextRangeArgs{Path: path}
			},
			wantStart:    0,
			wantEnd:      0,
			wantCount:    0,
			wantErrIsNil: true,
		},
		{
			name:    "exactly_max_output_lines_allowed",
			initial: makeNLines(2000, func(i int) string { return "x" }, "\n", true),
			args: func(path string) ReadTextRangeArgs {
				return ReadTextRangeArgs{Path: path}
			},
			wantStart:    1,
			wantEnd:      2000,
			wantCount:    2000,
			wantErrIsNil: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := writeTempTextFile(t, dir, "range-*.txt", tt.initial)
			args := tt.args(path)

			out, err := ReadTextRange(t.Context(), args)
			mustNoErr(t, err)
			if len(out.Lines) != out.LinesReturned {
				t.Fatalf("invariant failed: len(Lines)=%d but LinesReturned=%d", len(out.Lines), out.LinesReturned)
			}
			if out.LinesReturned != tt.wantCount {
				t.Fatalf("LinesReturned: want %d, got %d", tt.wantCount, out.LinesReturned)
			}
			if tt.wantCount == 0 {
				// For empty file, StartLine/EndLine are left as zero values.
				if out.StartLine != tt.wantStart || out.EndLine != tt.wantEnd {
					t.Fatalf("Start/End: want %d/%d got %d/%d", tt.wantStart, tt.wantEnd, out.StartLine, out.EndLine)
				}
				return
			}

			if out.StartLine != tt.wantStart || out.EndLine != tt.wantEnd {
				t.Fatalf("Start/End: want %d/%d got %d/%d", tt.wantStart, tt.wantEnd, out.StartLine, out.EndLine)
			}

			if tt.wantLine1 != nil {
				if len(out.Lines) < 1 {
					t.Fatalf("expected at least 1 line, got 0")
				}
				if out.Lines[0].LineNumber != tt.wantLine1.LineNumber || out.Lines[0].Text != tt.wantLine1.Text {
					t.Fatalf("first line mismatch: want %+v got %+v", *tt.wantLine1, out.Lines[0])
				}
			}
		})
	}
}

func TestReadTextRange_ErrorCases(t *testing.T) {
	dir := newWorkDir(t)

	tests := []struct {
		name       string
		setup      func() string
		args       func(path string) ReadTextRangeArgs
		wantErrSub string
		wantIsCtx  bool
	}{
		{
			name: "path_must_be_absolute",
			setup: func() string {
				_ = writeTempTextFile(t, dir, "x-*.txt", "A\n")
				return relativeTxt
			},
			args: func(path string) ReadTextRangeArgs {
				return ReadTextRangeArgs{Path: path}
			},
			wantErrSub: "path must be absolute",
		},
		{
			name: "no_match_startMarker",
			setup: func() string {
				return writeTempTextFile(t, dir, "x-*.txt", "A\nB\n")
			},
			args: func(path string) ReadTextRangeArgs {
				return ReadTextRangeArgs{Path: path, StartMatchLines: []string{"NOPE"}}
			},
			wantErrSub: "no match found for startMatchLines",
		},
		{
			name: "ambiguous_startMarker",
			setup: func() string {
				return writeTempTextFile(t, dir, "x-*.txt", "START\nx\nSTART\n")
			},
			args: func(path string) ReadTextRangeArgs {
				return ReadTextRangeArgs{Path: path, StartMatchLines: []string{"START"}}
			},
			wantErrSub: "ambiguous match for startMatchLines",
		},
		{
			name: "no_match_endMarker",
			setup: func() string {
				return writeTempTextFile(t, dir, "x-*.txt", "A\nB\n")
			},
			args: func(path string) ReadTextRangeArgs {
				return ReadTextRangeArgs{Path: path, EndMatchLines: []string{"NOPE"}}
			},
			wantErrSub: "no match found for endMatchLines",
		},
		{
			name: "ambiguous_endMarker",
			setup: func() string {
				return writeTempTextFile(t, dir, "x-*.txt", "END\nx\nEND\n")
			},
			args: func(path string) ReadTextRangeArgs {
				return ReadTextRangeArgs{Path: path, EndMatchLines: []string{"END"}}
			},
			wantErrSub: "ambiguous match for endMatchLines",
		},
		{
			name: "end_before_or_overlaps_start_rejected",
			setup: func() string {
				return writeTempTextFile(t, dir, "x-*.txt", "END\nSTART\n")
			},
			args: func(path string) ReadTextRangeArgs {
				return ReadTextRangeArgs{
					Path:            path,
					StartMatchLines: []string{"START"},
					EndMatchLines:   []string{"END"},
				}
			},
			wantErrSub: "endMatchLines occurs before",
		},
		{
			name: "range_too_large_without_markers",
			setup: func() string {
				content := makeNLines(2001, func(i int) string { return "x" }, "\n", true)
				return writeTempTextFile(t, dir, "big-*.txt", content)
			},
			args: func(path string) ReadTextRangeArgs {
				return ReadTextRangeArgs{Path: path}
			},
			wantErrSub: "selected range too large",
		},
		{
			name: "context_canceled",
			setup: func() string {
				return writeTempTextFile(t, dir, "x-*.txt", "A\n")
			},
			args: func(path string) ReadTextRangeArgs {
				return ReadTextRangeArgs{Path: path}
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

			_, err := ReadTextRange(ctx, args)
			if tt.wantIsCtx {
				if err == nil || !errors.Is(err, context.Canceled) {
					t.Fatalf("expected context.Canceled, got %v", err)
				}
				return
			}
			mustErrContains(t, err, tt.wantErrSub)
		})
	}
}
