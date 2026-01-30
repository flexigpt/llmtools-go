package texttool

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestInsertTextLines_HappyPaths(t *testing.T) {
	dir := newWorkDir(t)

	type want struct {
		content            string
		insertedAtLine     int
		insertedLineCount  int
		anchorMatchedAtPtr *int
	}

	tests := []struct {
		name    string
		initial string
		args    InsertTextLinesArgs
		want    want
	}{
		{
			name:    "default_position_end_inserts_at_end_preserves_final_newline",
			initial: "A\nB\n",
			args: InsertTextLinesArgs{
				LinesToInsert: []string{"X"},
			},
			want: want{
				content:           "A\nB\nX\n",
				insertedAtLine:    3,
				insertedLineCount: 1,
			},
		},
		{
			name:    "position_start_inserts_at_start",
			initial: "A\nB\nC\n",
			args: InsertTextLinesArgs{
				Position:      "start",
				LinesToInsert: []string{"X", "Y"},
			},
			want: want{
				content:           "X\nY\nA\nB\nC\n",
				insertedAtLine:    1,
				insertedLineCount: 2,
			},
		},
		{
			name:    "before_anchor_trimspace_match",
			initial: "a\n  ANCHOR  \nb\n",
			args: InsertTextLinesArgs{
				Position:         "beforeAnchor",
				LinesToInsert:    []string{"X"},
				AnchorMatchLines: []string{"ANCHOR"},
			},
			want: want{
				content:           "a\nX\n  ANCHOR  \nb\n",
				insertedAtLine:    2,
				insertedLineCount: 1,
				anchorMatchedAtPtr: func() *int {
					v := 2
					return &v
				}(),
			},
		},
		{
			name:    "after_anchor_trimspace_match",
			initial: "a\n  ANCHOR  \nb\n",
			args: InsertTextLinesArgs{
				Position:         "afterAnchor",
				LinesToInsert:    []string{"X"},
				AnchorMatchLines: []string{"ANCHOR"},
			},
			want: want{
				content:           "a\n  ANCHOR  \nX\nb\n",
				insertedAtLine:    3,
				insertedLineCount: 1,
				anchorMatchedAtPtr: func() *int {
					v := 2
					return &v
				}(),
			},
		},
		{
			name:    "linesToInsert_with_embedded_newlines_splits_into_multiple_lines",
			initial: "A\nB\n",
			args: InsertTextLinesArgs{
				Position:      "end",
				LinesToInsert: []string{"X\nY"},
			},
			want: want{
				content:           "A\nB\nX\nY\n",
				insertedAtLine:    3,
				insertedLineCount: 2,
			},
		},
		{
			name:    "preserves_crlf_newlines_and_final_newline",
			initial: "A\r\nB\r\n",
			args: InsertTextLinesArgs{
				Position:      "start",
				LinesToInsert: []string{"X"},
			},
			want: want{
				content:           "X\r\nA\r\nB\r\n",
				insertedAtLine:    1,
				insertedLineCount: 1,
			},
		},
		{
			name:    "empty_file_no_final_newline_preserved",
			initial: "",
			args: InsertTextLinesArgs{
				LinesToInsert: []string{"A"},
			},
			want: want{
				content:           "A",
				insertedAtLine:    1,
				insertedLineCount: 1,
			},
		},
		{
			name:    "file_with_single_empty_line_and_final_newline_keeps_final_newline",
			initial: "\n", // one empty line + final newline
			args: InsertTextLinesArgs{
				Position:      "start",
				LinesToInsert: []string{"A"},
			},
			want: want{
				content:           "A\n\n",
				insertedAtLine:    1,
				insertedLineCount: 1,
			},
		},
		{
			name:    "non_empty_file_without_final_newline_preserved",
			initial: "A", // no trailing newline
			args: InsertTextLinesArgs{
				Position:      "end",
				LinesToInsert: []string{"B"},
			},
			want: want{
				content:           "A\nB", // still no final newline
				insertedAtLine:    2,
				insertedLineCount: 1,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := writeTempTextFile(t, dir, "insert-*.txt", tt.initial)
			tt.args.Path = path

			out, err := InsertTextLines(t.Context(), tt.args)
			mustNoErr(t, err)

			got := readFileString(t, path)
			if got != tt.want.content {
				t.Fatalf("content mismatch\nwant:\n%q\ngot:\n%q", tt.want.content, got)
			}

			if out.InsertedAtLine != tt.want.insertedAtLine {
				t.Fatalf("InsertedAtLine: want %d, got %d", tt.want.insertedAtLine, out.InsertedAtLine)
			}
			if out.InsertedLineCount != tt.want.insertedLineCount {
				t.Fatalf("InsertedLineCount: want %d, got %d", tt.want.insertedLineCount, out.InsertedLineCount)
			}

			if tt.want.anchorMatchedAtPtr == nil {
				if out.AnchorMatchedAtLine != nil {
					t.Fatalf("AnchorMatchedAtLine: want nil, got %v", *out.AnchorMatchedAtLine)
				}
			} else {
				if out.AnchorMatchedAtLine == nil {
					t.Fatalf("AnchorMatchedAtLine: want %d, got nil", *tt.want.anchorMatchedAtPtr)
				}
				if *out.AnchorMatchedAtLine != *tt.want.anchorMatchedAtPtr {
					t.Fatalf(
						"AnchorMatchedAtLine: want %d, got %d",
						*tt.want.anchorMatchedAtPtr,
						*out.AnchorMatchedAtLine,
					)
				}
			}
		})
	}
}

func TestInsertTextLines_ErrorCases(t *testing.T) {
	dir := newWorkDir(t)

	tests := []struct {
		name              string
		setupFile         func() string
		args              func(path string) InsertTextLinesArgs
		wantErrSub        string
		wantIsCtx         bool
		checkContentAfter bool
		wantContentAfter  string
	}{
		{
			name: "path_must_be_absolute",
			setupFile: func() string {
				_ = writeTempTextFile(t, dir, "dummy-*.txt", "A\n")
				return relativeTxt
			},
			args: func(path string) InsertTextLinesArgs {
				return InsertTextLinesArgs{Path: path, LinesToInsert: []string{"X"}}
			},
			wantErrSub: "path must be absolute",
		},
		{
			name: "linesToInsert_required",
			setupFile: func() string {
				return writeTempTextFile(t, dir, "ins-*.txt", "A\n")
			},
			args: func(path string) InsertTextLinesArgs {
				return InsertTextLinesArgs{Path: path, LinesToInsert: nil}
			},
			wantErrSub: "linesToInsert is required",
		},
		{
			name: "invalid_position",
			setupFile: func() string {
				return writeTempTextFile(t, dir, "ins-*.txt", "A\n")
			},
			args: func(path string) InsertTextLinesArgs {
				return InsertTextLinesArgs{Path: path, Position: "middle", LinesToInsert: []string{"X"}}
			},
			wantErrSub: "invalid position value",
		},
		{
			name: "anchor_forbidden_when_position_end",
			setupFile: func() string {
				return writeTempTextFile(t, dir, "ins-*.txt", "A\n")
			},
			args: func(path string) InsertTextLinesArgs {
				return InsertTextLinesArgs{
					Path:             path,
					Position:         "end",
					LinesToInsert:    []string{"X"},
					AnchorMatchLines: []string{"A"},
				}
			},
			wantErrSub: `anchorMatchLines must be omitted`,
		},
		{
			name: "beforeAnchor_requires_anchor",
			setupFile: func() string {
				return writeTempTextFile(t, dir, "ins-*.txt", "A\n")
			},
			args: func(path string) InsertTextLinesArgs {
				return InsertTextLinesArgs{
					Path:          path,
					Position:      "beforeAnchor",
					LinesToInsert: []string{"X"},
				}
			},
			wantErrSub: `requires anchorMatchLines`,
		},
		{
			name: "anchor_no_match",
			setupFile: func() string {
				return writeTempTextFile(t, dir, "ins-*.txt", "A\nB\n")
			},
			args: func(path string) InsertTextLinesArgs {
				return InsertTextLinesArgs{
					Path:             path,
					Position:         "afterAnchor",
					LinesToInsert:    []string{"X"},
					AnchorMatchLines: []string{"NOPE"},
				}
			},
			wantErrSub: "no match found for anchorMatchLines",
		},
		{
			name: "anchor_ambiguous_match",
			setupFile: func() string {
				return writeTempTextFile(t, dir, "ins-*.txt", "ANCHOR\nx\nANCHOR\n")
			},
			args: func(path string) InsertTextLinesArgs {
				return InsertTextLinesArgs{
					Path:             path,
					Position:         "afterAnchor",
					LinesToInsert:    []string{"X"},
					AnchorMatchLines: []string{"ANCHOR"},
				}
			},
			wantErrSub: "ambiguous match for anchorMatchLines",
		},
		{
			name: "default_end_rejects_anchorMatchLines",
			setupFile: func() string {
				return writeTempTextFile(t, dir, "ins-*.txt", "A\n")
			},
			args: func(path string) InsertTextLinesArgs {
				return InsertTextLinesArgs{
					Path: path,
					// Position omitted => default "end".
					LinesToInsert:    []string{"X"},
					AnchorMatchLines: []string{"A"},
				}
			},
			wantErrSub:        `anchorMatchLines must be omitted`,
			checkContentAfter: true,
			wantContentAfter:  "A\n",
		},
		{
			name: "context_canceled",
			setupFile: func() string {
				return writeTempTextFile(t, dir, "ins-*.txt", "A\n")
			},
			args: func(path string) InsertTextLinesArgs {
				return InsertTextLinesArgs{
					Path:          path,
					Position:      "end",
					LinesToInsert: []string{"X"},
				}
			},

			wantIsCtx: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := tt.setupFile()
			args := tt.args(path)

			ctx := t.Context()
			if tt.wantIsCtx {
				cctx, cancel := context.WithCancel(ctx)
				cancel()
				ctx = cctx
			}

			_, err := InsertTextLines(ctx, args)
			if strings.Contains(tt.name, "context_canceled") {
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
