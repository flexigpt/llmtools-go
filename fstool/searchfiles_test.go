package fstool

import (
	"context"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/flexigpt/llmtools-go/internal/toolutil"
)

func TestSearchFiles(t *testing.T) {
	type cfg struct {
		workBaseDir   string
		allowedRoots  []string
		blockSymlinks bool
	}

	makeTool := func(t *testing.T, c cfg) *FSTool {
		t.Helper()
		opts := []FSToolOption{WithWorkBaseDir(c.workBaseDir), WithBlockSymlinks(c.blockSymlinks)}
		if c.allowedRoots != nil {
			opts = append(opts, WithAllowedRoots(c.allowedRoots))
		}
		return mustNewFSTool(t, opts...)
	}

	seedTree := func(t *testing.T, root string) {
		t.Helper()
		mustWriteFile(t, filepath.Join(root, "foo.txt"), []byte("hello world"))
		mustWriteFile(t, filepath.Join(root, "bar.md"), []byte("goodbye world"))
		mustMkdirAll(t, filepath.Join(root, "sub"))
		mustWriteFile(t, filepath.Join(root, "sub", "baz.txt"), []byte("baz content"))
	}

	tests := []struct {
		name    string
		cfg     func(t *testing.T) cfg
		ctx     func(t *testing.T) context.Context
		args    func(t *testing.T, c cfg) SearchFilesArgs
		wantErr func(error) bool

		wantMatches       []string
		wantReachedMax    bool
		wantMatchCountLen bool
	}{
		{
			name: "context_canceled",
			cfg: func(t *testing.T) cfg {
				t.Helper()
				tmp := t.TempDir()
				seedTree(t, tmp)
				return cfg{workBaseDir: tmp}
			},
			ctx: canceledContext,
			args: func(t *testing.T, c cfg) SearchFilesArgs {
				t.Helper()
				return SearchFilesArgs{Root: ".", Query: "txt"}
			},
			wantErr: wantErrIs(context.Canceled),
		},
		{
			name: "missing_query_errors",
			cfg: func(t *testing.T) cfg {
				t.Helper()
				tmp := t.TempDir()
				seedTree(t, tmp)
				return cfg{workBaseDir: tmp}
			},
			args: func(t *testing.T, c cfg) SearchFilesArgs {
				t.Helper()
				return SearchFilesArgs{Root: "."}
			},
			wantErr: wantErrContains("query is required"),
		},
		{
			name: "invalid_regexp_errors",
			cfg: func(t *testing.T) cfg {
				t.Helper()
				tmp := t.TempDir()
				seedTree(t, tmp)
				return cfg{workBaseDir: tmp}
			},
			args: func(t *testing.T, c cfg) SearchFilesArgs {
				t.Helper()
				return SearchFilesArgs{Root: ".", Query: "[", Regexp: true}
			},
			wantErr: wantErrAny,
		},
		{
			name: "match_file_path_relative_root",
			cfg: func(t *testing.T) cfg {
				t.Helper()
				tmp := t.TempDir()
				seedTree(t, tmp)
				return cfg{workBaseDir: tmp}
			},
			args: func(t *testing.T, c cfg) SearchFilesArgs {
				t.Helper()
				return SearchFilesArgs{Root: ".", Query: `foo\.txt`, Regexp: true}
			},
			wantErr:           wantErrNone,
			wantMatches:       []string{"foo.txt"},
			wantMatchCountLen: true,
		},
		{
			name: "match_file_content",
			cfg: func(t *testing.T) cfg {
				t.Helper()
				tmp := t.TempDir()
				seedTree(t, tmp)
				return cfg{workBaseDir: tmp}
			},
			args: func(t *testing.T, c cfg) SearchFilesArgs {
				t.Helper()
				return SearchFilesArgs{Root: ".", Query: "goodbye"}
			},
			wantErr:           wantErrNone,
			wantMatches:       []string{"bar.md"},
			wantMatchCountLen: true,
		},
		{
			name: "match_in_subdirectory",
			cfg: func(t *testing.T) cfg {
				t.Helper()
				tmp := t.TempDir()
				seedTree(t, tmp)
				return cfg{workBaseDir: tmp}
			},
			args: func(t *testing.T, c cfg) SearchFilesArgs {
				t.Helper()
				return SearchFilesArgs{Root: ".", Query: "baz"}
			},
			wantErr:           wantErrNone,
			wantMatches:       []string{filepath.Join("sub", "baz.txt")},
			wantMatchCountLen: true,
		},
		{
			name: "max_results_limits_output",
			cfg: func(t *testing.T) cfg {
				t.Helper()
				tmp := t.TempDir()
				seedTree(t, tmp)
				return cfg{workBaseDir: tmp}
			},
			args: func(t *testing.T, c cfg) SearchFilesArgs {
				t.Helper()
				return SearchFilesArgs{Root: ".", Query: "txt", MaxResults: 1}
			},
			wantErr:           wantErrNone,
			wantReachedMax:    true,
			wantMatchCountLen: true,
		},
		{
			name: "blockSymlinks_true_skips_symlink_entries",
			cfg: func(t *testing.T) cfg {
				t.Helper()
				if runtime.GOOS == toolutil.GOOSWindows {
					t.Skip("symlink tests are unreliable on Windows CI")
				}
				tmp := t.TempDir()
				seedTree(t, tmp)

				target := filepath.Join(tmp, "foo.txt")
				link := filepath.Join(tmp, "link.txt")
				mustSymlinkOrSkip(t, target, link)

				return cfg{workBaseDir: tmp, blockSymlinks: true}
			},
			args: func(t *testing.T, c cfg) SearchFilesArgs {
				t.Helper()
				return SearchFilesArgs{Root: ".", Query: `link\.txt`, Regexp: true}
			},
			wantErr:           wantErrNone,
			wantMatches:       []string{}, // should skip symlink
			wantMatchCountLen: true,
		},
		{
			name: "allowedRoots_prevents_symlink_escape_even_when_symlinks_allowed",
			cfg: func(t *testing.T) cfg {
				t.Helper()
				if runtime.GOOS == toolutil.GOOSWindows {
					t.Skip("symlink tests are unreliable on Windows CI")
				}
				root := t.TempDir()
				seedTree(t, root)

				outside := t.TempDir()
				outsideFile := filepath.Join(outside, "outside.txt")
				mustWriteFile(t, outsideFile, []byte("needle outside root"))

				link := filepath.Join(root, "escape.txt")
				mustSymlinkOrSkip(t, outsideFile, link)

				return cfg{workBaseDir: root, allowedRoots: []string{root}, blockSymlinks: false}
			},
			args: func(t *testing.T, c cfg) SearchFilesArgs {
				t.Helper()
				return SearchFilesArgs{Root: ".", Query: "needle"}
			},
			wantErr:           wantErrNone,
			wantMatches:       []string{}, // escaped symlink should be skipped by per-file ResolvePath check
			wantMatchCountLen: true,
		},
		{
			name: "root_is_file_errors",
			cfg: func(t *testing.T) cfg {
				t.Helper()
				tmp := t.TempDir()
				seedTree(t, tmp)
				mustWriteFile(t, filepath.Join(tmp, "notadir"), []byte("x"))
				return cfg{workBaseDir: tmp}
			},
			args: func(t *testing.T, c cfg) SearchFilesArgs {
				t.Helper()
				return SearchFilesArgs{Root: "notadir", Query: "x"}
			},
			wantErr: wantErrAny,
		},
		{
			name: "allowedRoots_blocks_outside_root_arg",
			cfg: func(t *testing.T) cfg {
				t.Helper()
				root := t.TempDir()
				seedTree(t, root)
				return cfg{workBaseDir: root, allowedRoots: []string{root}}
			},
			args: func(t *testing.T, c cfg) SearchFilesArgs {
				t.Helper()
				outside := t.TempDir()
				seedTree(t, outside)
				return SearchFilesArgs{Root: outside, Query: "txt"}
			},
			wantErr: wantErrContains("outside allowed roots"),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := tt.cfg(t)
			ft := makeTool(t, c)

			ctx := t.Context()
			if tt.ctx != nil {
				ctx = tt.ctx(t)
			}

			out, err := ft.SearchFiles(ctx, tt.args(t, c))
			if tt.wantErr == nil {
				tt.wantErr = wantErrNone
			}
			if !tt.wantErr(err) {
				t.Fatalf("err=%v did not match expectation", err)
			}
			if err != nil {
				return
			}
			if out == nil {
				t.Fatalf("expected non-nil out")
			}

			if tt.wantMatchCountLen && out.MatchCount != len(out.Items) {
				t.Fatalf(
					"MatchCount=%d want %d",
					out.MatchCount,
					len(out.Items),
				)
			}
			if out.ReachedMaxResults != tt.wantReachedMax {
				t.Fatalf("ReachedMaxResults=%v want=%v", out.ReachedMaxResults, tt.wantReachedMax)
			}

			if tt.wantMatches != nil {
				matches := make([]string, 0, len(out.Items))
				for _, item := range out.Items {
					matches = append(matches, item.Path)
				}
				if !equalStringMultisets(matches, tt.wantMatches) {
					t.Fatalf("Matches=%v want=%v", out.Items, tt.wantMatches)
				}
			}
		})
	}
}
