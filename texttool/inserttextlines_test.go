package texttool

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/flexigpt/llmtools-go/internal/fspolicy"
)

func TestInsertText_HappyPaths(t *testing.T) {
	dir := newWorkDir(t)
	policy, err := fspolicy.New("", nil, true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	type want struct {
		content               string
		insertedAtLine        int
		insertedLineCount     int
		textAboveMatchedAtPtr *int
		textBelowMatchedAtPtr *int
	}

	tests := []struct {
		name    string
		initial string
		args    InsertTextArgs
		want    want
	}{
		{
			name:    "position_end_inserts_at_end_preserves_final_newline",
			initial: "A\nB\n",
			args: InsertTextArgs{
				Position: "end",
				Text:     stringPtr("X"),
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
			args: InsertTextArgs{
				Position: "start",
				Text:     stringPtr("X"),
			},
			want: want{
				content:           "X\nA\nB\nC\n",
				insertedAtLine:    1,
				insertedLineCount: 1,
			},
		},
		{
			name:    "between_with_textBelow_inserts_before_matching_block_trimspace_match",
			initial: "a\n  ANCHOR  \nb\n",
			args: InsertTextArgs{
				Position:  "between",
				Text:      stringPtr("X"),
				TextBelow: stringPtr("ANCHOR"),
			},
			want: want{
				content:               "a\nX\n  ANCHOR  \nb\n",
				insertedAtLine:        2,
				insertedLineCount:     1,
				textBelowMatchedAtPtr: intPtr(2),
			},
		},
		{
			name:    "between_with_textAbove_inserts_after_matching_block_trimspace_match",
			initial: "a\n  ANCHOR  \nb\n",
			args: InsertTextArgs{
				Position:  "between",
				Text:      stringPtr("X"),
				TextAbove: stringPtr("ANCHOR"),
			},
			want: want{
				content:               "a\n  ANCHOR  \nX\nb\n",
				insertedAtLine:        3,
				insertedLineCount:     1,
				textAboveMatchedAtPtr: intPtr(2),
			},
		},
		{
			name:    "between_with_textAbove_and_textBelow_inserts_at_exact_boundary",
			initial: "## Intro\nParagraph\n## Usage\nParagraph\n",
			args: InsertTextArgs{
				Position:  "between",
				Text:      stringPtr("Inserted"),
				TextAbove: stringPtr("## Usage"),
				TextBelow: stringPtr("Paragraph"),
			},
			want: want{
				content:               "## Intro\nParagraph\n## Usage\nInserted\nParagraph\n",
				insertedAtLine:        4,
				insertedLineCount:     1,
				textAboveMatchedAtPtr: intPtr(3),
				textBelowMatchedAtPtr: intPtr(4),
			},
		},
		{
			name:    "between_with_blank_gap_inserts_at_start_of_gap",
			initial: "A\n\nB\n",
			args: InsertTextArgs{
				Position:  "between",
				Text:      stringPtr("X"),
				TextAbove: stringPtr("A"),
				TextBelow: stringPtr("B"),
			},
			want: want{
				content:               "A\nX\n\nB\n",
				insertedAtLine:        2,
				insertedLineCount:     1,
				textAboveMatchedAtPtr: intPtr(1),
				textBelowMatchedAtPtr: intPtr(3),
			},
		},
		{
			name:    "between_tolerates_blank_line_drift_on_boundary_blocks",
			initial: "<!-- A END -->\n\n## Section B\n",
			args: InsertTextArgs{
				Position:  "between",
				Text:      stringPtr("\nInserted after A"),
				TextAbove: stringPtr("<!-- A END -->\n\n"),
				TextBelow: stringPtr("\n## Section B"),
			},
			want: want{
				content:               "<!-- A END -->\n\nInserted after A\n\n## Section B\n",
				insertedAtLine:        2,
				insertedLineCount:     2,
				textAboveMatchedAtPtr: intPtr(1),
				textBelowMatchedAtPtr: intPtr(3),
			},
		},
		{
			name:    "between_lineHint_narrows_ambiguous_location",
			initial: "ANCHOR\nx\nANCHOR\ny\n",
			args: InsertTextArgs{
				Position:  "between",
				Text:      stringPtr("X"),
				TextBelow: stringPtr("ANCHOR"),
				LineHint:  intPtr(3),
			},
			want: want{
				content:               "ANCHOR\nx\nX\nANCHOR\ny\n",
				insertedAtLine:        3,
				insertedLineCount:     1,
				textBelowMatchedAtPtr: intPtr(3),
			},
		},
		{
			name:    "text_with_embedded_newlines_splits_into_multiple_lines",
			initial: "A\nB\n",
			args: InsertTextArgs{
				Position: "end",
				Text:     stringPtr("X\nY"),
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
			args: InsertTextArgs{
				Position: "start",
				Text:     stringPtr("X"),
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
			args: InsertTextArgs{
				Position: "end",
				Text:     stringPtr("A"),
			},
			want: want{
				content:           "A",
				insertedAtLine:    1,
				insertedLineCount: 1,
			},
		},
		{
			name:    "file_with_single_empty_line_and_final_newline_keeps_final_newline",
			initial: "\n",
			args: InsertTextArgs{
				Position: "start",
				Text:     stringPtr("A"),
			},
			want: want{
				content:           "A\n\n",
				insertedAtLine:    1,
				insertedLineCount: 1,
			},
		},
		{
			name:    "non_empty_file_without_final_newline_preserved",
			initial: "A",
			args: InsertTextArgs{
				Position: "end",
				Text:     stringPtr("B"),
			},
			want: want{
				content:           "A\nB",
				insertedAtLine:    2,
				insertedLineCount: 1,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := writeTempTextFile(t, dir, "insert-*.txt", tt.initial)
			tt.args.Path = path

			out, err := insertText(t.Context(), tt.args, policy)
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

			if tt.want.textAboveMatchedAtPtr == nil {
				if out.TextAboveMatchedAtLine != nil {
					t.Fatalf("TextAboveMatchedAtLine: want nil, got %v", *out.TextAboveMatchedAtLine)
				}
			} else {
				if out.TextAboveMatchedAtLine == nil {
					t.Fatalf("TextAboveMatchedAtLine: want %d, got nil", *tt.want.textAboveMatchedAtPtr)
				}
				if *out.TextAboveMatchedAtLine != *tt.want.textAboveMatchedAtPtr {
					t.Fatalf(
						"TextAboveMatchedAtLine: want %d, got %d",
						*tt.want.textAboveMatchedAtPtr,
						*out.TextAboveMatchedAtLine,
					)
				}
			}

			if tt.want.textBelowMatchedAtPtr == nil {
				if out.TextBelowMatchedAtLine != nil {
					t.Fatalf("TextBelowMatchedAtLine: want nil, got %v", *out.TextBelowMatchedAtLine)
				}
			} else {
				if out.TextBelowMatchedAtLine == nil {
					t.Fatalf("TextBelowMatchedAtLine: want %d, got nil", *tt.want.textBelowMatchedAtPtr)
				}
				if *out.TextBelowMatchedAtLine != *tt.want.textBelowMatchedAtPtr {
					t.Fatalf(
						"TextBelowMatchedAtLine: want %d, got %d",
						*tt.want.textBelowMatchedAtPtr,
						*out.TextBelowMatchedAtLine,
					)
				}
			}
		})
	}
}

func TestInsertText_ErrorCases(t *testing.T) {
	dir := newWorkDir(t)
	policy, err := fspolicy.New("", nil, true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	tests := []struct {
		name              string
		setupFile         func() string
		args              func(path string) InsertTextArgs
		wantErrSub        string
		wantIsCtx         bool
		checkContentAfter bool
		wantContentAfter  string
	}{
		{
			name: "text_required",
			setupFile: func() string {
				return writeTempTextFile(t, dir, "ins-*.txt", "A\n")
			},
			args: func(path string) InsertTextArgs {
				return InsertTextArgs{
					Path:     path,
					Position: "end",
				}
			},
			wantErrSub: "text is required",
		},
		{
			name: "position_required",
			setupFile: func() string {
				return writeTempTextFile(t, dir, "ins-*.txt", "A\n")
			},
			args: func(path string) InsertTextArgs {
				return InsertTextArgs{
					Path: path,
					Text: stringPtr("X"),
				}
			},
			wantErrSub: "position is required",
		},
		{
			name: "invalid_position",
			setupFile: func() string {
				return writeTempTextFile(t, dir, "ins-*.txt", "A\n")
			},
			args: func(path string) InsertTextArgs {
				return InsertTextArgs{
					Path:     path,
					Position: "middle",
					Text:     stringPtr("X"),
				}
			},
			wantErrSub: "invalid position value",
		},
		{
			name: "boundary_fields_forbidden_when_position_end",
			setupFile: func() string {
				return writeTempTextFile(t, dir, "ins-*.txt", "A\n")
			},
			args: func(path string) InsertTextArgs {
				return InsertTextArgs{
					Path:      path,
					Position:  "end",
					Text:      stringPtr("X"),
					TextBelow: stringPtr("A"),
				}
			},
			wantErrSub: `textAbove/textBelow/lineHint must be omitted`,
		},
		{
			name: "between_requires_textAbove_or_textBelow",
			setupFile: func() string {
				return writeTempTextFile(t, dir, "ins-*.txt", "A\n")
			},
			args: func(path string) InsertTextArgs {
				return InsertTextArgs{
					Path:     path,
					Position: "between",
					Text:     stringPtr("X"),
				}
			},
			wantErrSub: `requires textAbove or textBelow`,
		},
		{
			name: "lineHint_must_be_positive",
			setupFile: func() string {
				return writeTempTextFile(t, dir, "ins-*.txt", "A\n")
			},
			args: func(path string) InsertTextArgs {
				return InsertTextArgs{
					Path:      path,
					Position:  "between",
					Text:      stringPtr("X"),
					TextBelow: stringPtr("A"),
					LineHint:  intPtr(0),
				}
			},
			wantErrSub: "lineHint must be >= 1",
		},
		{
			name: "insertion_point_no_match",
			setupFile: func() string {
				return writeTempTextFile(t, dir, "ins-*.txt", "A\nB\n")
			},
			args: func(path string) InsertTextArgs {
				return InsertTextArgs{
					Path:      path,
					Position:  "between",
					Text:      stringPtr("X"),
					TextBelow: stringPtr("NOPE"),
				}
			},
			wantErrSub: "no insertion point matched",
		},
		{
			name: "between_does_not_skip_nonblank_lines",
			setupFile: func() string {
				return writeTempTextFile(t, dir, "ins-*.txt", "A\nnote\nB\n")
			},
			args: func(path string) InsertTextArgs {
				return InsertTextArgs{
					Path:      path,
					Position:  "between",
					Text:      stringPtr("X"),
					TextAbove: stringPtr("A"),
					TextBelow: stringPtr("B"),
				}
			},
			wantErrSub: "no insertion point matched",
		},

		{
			name: "insertion_point_ambiguous_match",
			setupFile: func() string {
				return writeTempTextFile(t, dir, "ins-*.txt", "ANCHOR\nx\nANCHOR\n")
			},
			args: func(path string) InsertTextArgs {
				return InsertTextArgs{
					Path:      path,
					Position:  "between",
					Text:      stringPtr("X"),
					TextBelow: stringPtr("ANCHOR"),
				}
			},
			wantErrSub: "ambiguous insertion point",
		},
		{
			name: "position_end_error_does_not_modify_file",
			setupFile: func() string {
				return writeTempTextFile(t, dir, "ins-*.txt", "A\n")
			},
			args: func(path string) InsertTextArgs {
				return InsertTextArgs{
					Path:      path,
					Position:  "end",
					Text:      stringPtr("X"),
					TextAbove: stringPtr("A"),
				}
			},
			wantErrSub:        `textAbove/textBelow/lineHint must be omitted`,
			checkContentAfter: true,
			wantContentAfter:  "A\n",
		},
		{
			name: "context_canceled",
			setupFile: func() string {
				return writeTempTextFile(t, dir, "ins-*.txt", "A\n")
			},
			args: func(path string) InsertTextArgs {
				return InsertTextArgs{
					Path:     path,
					Position: "end",
					Text:     stringPtr("X"),
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

			_, err := insertText(ctx, args, policy)
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
