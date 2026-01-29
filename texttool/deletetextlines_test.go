package texttool

import (
	"context"
	"errors"
	"runtime"
	"testing"

	"github.com/flexigpt/llmtools-go/internal/toolutil"
)

func TestDeleteTextLines_HappyPaths(t *testing.T) {
	dir := newWorkDir(t)

	tests := []struct {
		name            string
		initial         string
		args            func(path string) DeleteTextLinesArgs
		wantContent     string
		wantDeletions   int
		wantDeletedAt   []int
		wantErrSub      string
		wantErrIsNil    bool
		skipOnWindows   bool
		requiresSymlink bool
	}{
		{
			name:    "delete_single_line_default_expected_1",
			initial: "A\nB\nC\n",
			args: func(path string) DeleteTextLinesArgs {
				return DeleteTextLinesArgs{
					Path:       path,
					MatchLines: []string{"B"},
				}
			},
			wantContent:   "A\nC\n",
			wantDeletions: 1,
			wantDeletedAt: []int{2},
			wantErrIsNil:  true,
		},
		{
			name:    "delete_multiline_with_before_after_disambiguation",
			initial: "hdr\nctx1\nX\nY\nctx2\nctx1\nX\nY\nctx3\n",
			args: func(path string) DeleteTextLinesArgs {
				return DeleteTextLinesArgs{
					Path:              path,
					BeforeLines:       []string{"ctx1"},
					MatchLines:        []string{"X", "Y"},
					AfterLines:        []string{"ctx2"},
					ExpectedDeletions: 1,
				}
			},
			wantContent:   "hdr\nctx1\nctx2\nctx1\nX\nY\nctx3\n",
			wantDeletions: 1,
			wantDeletedAt: []int{3},
			wantErrIsNil:  true,
		},
		{
			name:    "expectedDeletions_0_defaults_to_1",
			initial: "A\nB\n",
			args: func(path string) DeleteTextLinesArgs {
				return DeleteTextLinesArgs{
					Path:              path,
					MatchLines:        []string{"B"},
					ExpectedDeletions: 0,
				}
			},
			wantContent:   "A\n",
			wantDeletions: 1,
			wantDeletedAt: []int{2},
			wantErrIsNil:  true,
		},
		{
			name:    "delete_all_lines_preserves_final_newline",
			initial: "A\n",
			args: func(path string) DeleteTextLinesArgs {
				return DeleteTextLinesArgs{
					Path:       path,
					MatchLines: []string{"A"},
				}
			},
			// When Lines becomes empty and original had final newline, Render() returns "\n".
			wantContent:   "\n",
			wantDeletions: 1,
			wantDeletedAt: []int{1},
			wantErrIsNil:  true,
		},
		{
			name:    "preserves_crlf_newlines",
			initial: "A\r\nB\r\n",
			args: func(path string) DeleteTextLinesArgs {
				return DeleteTextLinesArgs{
					Path:       path,
					MatchLines: []string{"A"},
				}
			},
			wantContent:   "B\r\n",
			wantDeletions: 1,
			wantDeletedAt: []int{1},
			wantErrIsNil:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := writeTempTextFile(t, dir, "del-*.txt", tt.initial)
			args := tt.args(path)

			out, err := DeleteTextLines(t.Context(), args)
			mustNoErr(t, err)

			if out.DeletionsMade != tt.wantDeletions {
				t.Fatalf("DeletionsMade: want %d, got %d", tt.wantDeletions, out.DeletionsMade)
			}
			if len(out.DeletedAtLines) != len(tt.wantDeletedAt) {
				t.Fatalf(
					"DeletedAtLines len: want %d, got %d (%v)",
					len(tt.wantDeletedAt),
					len(out.DeletedAtLines),
					out.DeletedAtLines,
				)
			}
			for i := range tt.wantDeletedAt {
				if out.DeletedAtLines[i] != tt.wantDeletedAt[i] {
					t.Fatalf("DeletedAtLines[%d]: want %d, got %d", i, tt.wantDeletedAt[i], out.DeletedAtLines[i])
				}
			}

			got := readFileString(t, path)
			if got != tt.wantContent {
				t.Fatalf("content mismatch\nwant:\n%q\ngot:\n%q", tt.wantContent, got)
			}
		})
	}
}

func TestDeleteTextLines_ErrorCases(t *testing.T) {
	dir := newWorkDir(t)

	tests := []struct {
		name       string
		setup      func() (path string)
		args       func(path string) DeleteTextLinesArgs
		wantErrSub string
		wantIsCtx  bool
	}{
		{
			name: "path_must_be_absolute",
			setup: func() string {
				_ = writeTempTextFile(t, dir, "x-*.txt", "A\n")
				return relativeTxt
			},
			args: func(path string) DeleteTextLinesArgs {
				return DeleteTextLinesArgs{Path: path, MatchLines: []string{"A"}}
			},
			wantErrSub: "path must be absolute",
		},
		{
			name: "matchLines_required",
			setup: func() string {
				return writeTempTextFile(t, dir, "x-*.txt", "A\n")
			},
			args: func(path string) DeleteTextLinesArgs {
				return DeleteTextLinesArgs{Path: path, MatchLines: nil}
			},
			wantErrSub: "matchLines is required",
		},
		{
			name: "file_not_found",
			setup: func() string {
				// Create an absolute-but-nonexistent path inside work dir.
				return dir + string(filepathSep()) + "nope-does-not-exist.txt"
			},
			args: func(path string) DeleteTextLinesArgs {
				return DeleteTextLinesArgs{Path: path, MatchLines: []string{"A"}}
			},
			wantErrSub: "", // platform dependent
		},
		{
			name: "expected_deletions_mismatch",
			setup: func() string {
				return writeTempTextFile(t, dir, "x-*.txt", "A\nX\nA\nX\n")
			},
			args: func(path string) DeleteTextLinesArgs {
				return DeleteTextLinesArgs{
					Path:              path,
					MatchLines:        []string{"A"},
					ExpectedDeletions: 1,
				}
			},
			wantErrSub: "delete match count mismatch",
		},
		{
			name: "overlapping_matches_rejected",
			setup: func() string {
				return writeTempTextFile(t, dir, "x-*.txt", "X\nX\nX\n") //nolint:dupword // Test.
			},
			args: func(path string) DeleteTextLinesArgs {
				return DeleteTextLinesArgs{
					Path:              path,
					MatchLines:        []string{"X", "X"},
					ExpectedDeletions: 2, // won't be reached; overlap check happens first
				}
			},
			wantErrSub: "overlapping matches detected",
		},
		{
			name: "invalid_utf8_rejected",
			setup: func() string {
				return writeTempBytesFile(t, dir, "x-*.txt", []byte{0xff, 0xfe, 0xfd})
			},
			args: func(path string) DeleteTextLinesArgs {
				return DeleteTextLinesArgs{Path: path, MatchLines: []string{"A"}}
			},
			wantErrSub: "not valid UTF-8",
		},
		{
			name: "context_canceled",
			setup: func() string {
				return writeTempTextFile(t, dir, "x-*.txt", "A\n")
			},
			args: func(path string) DeleteTextLinesArgs {
				return DeleteTextLinesArgs{Path: path, MatchLines: []string{"A"}}
			},
			wantIsCtx: true,
		},
		{
			name: "symlink_file_rejected",
			setup: func() string {
				if runtime.GOOS == toolutil.GOOSWindows {
					return ""
				}
				target := writeTempTextFile(t, dir, "target-*.txt", "A\n")
				link := dir + string(filepathSep()) + "link.txt"
				if err := osSymlink(target, link); err != nil {
					t.Skipf("os.Symlink not available: %v", err)
				}
				abs, _ := filepathAbs(link)
				return abs
			},
			args: func(path string) DeleteTextLinesArgs {
				return DeleteTextLinesArgs{Path: path, MatchLines: []string{"A"}}
			},
			wantErrSub: "symlink",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := tt.setup()
			if path == "" {
				return
			}
			args := tt.args(path)

			ctx := t.Context()
			if tt.wantIsCtx {
				cctx, cancel := context.WithCancel(ctx)
				cancel()
				ctx = cctx
			}

			_, err := DeleteTextLines(ctx, args)
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
