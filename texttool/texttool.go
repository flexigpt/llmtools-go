package texttool

import (
	"context"
	"sync"

	"github.com/flexigpt/llmtools-go/internal/fspolicy"
	"github.com/flexigpt/llmtools-go/internal/toolutil"
	"github.com/flexigpt/llmtools-go/spec"
)

const (
	maybeStartLineTolerance          = 3
	maxAmbiguityDiagnosticCandidates = 5
	ambiguityDiagnosticContextLines  = 1
)

type textToolConfig struct {
	allowedRoots  []string
	workBaseDir   string
	blockSymlinks bool
}

// TextTool is an instance-owned text tool runner.
// It centralizes path resolution and sandbox policy:
//   - workBaseDir: base for resolving relative paths
//   - allowedRoots: optional restriction; if empty/nil, allow all
//   - blockSymlinks: blocks symlink traversal (if enforced downstream).
type TextTool struct {
	mu     sync.RWMutex
	cfg    textToolConfig
	policy fspolicy.FSPolicy
}

type TextToolOption func(*TextTool) error

func WithAllowedRoots(roots []string) TextToolOption {
	return func(tt *TextTool) error {
		tt.cfg.allowedRoots = roots
		return nil
	}
}

func WithWorkBaseDir(base string) TextToolOption {
	return func(tt *TextTool) error {
		tt.cfg.workBaseDir = base
		return nil
	}
}

// WithBlockSymlinks configures whether symlink traversal should be blocked (if supported downstream).
func WithBlockSymlinks(block bool) TextToolOption {
	return func(tt *TextTool) error {
		tt.cfg.blockSymlinks = block
		return nil
	}
}

func NewTextTool(opts ...TextToolOption) (*TextTool, error) {
	tt := &TextTool{
		cfg: textToolConfig{
			allowedRoots:  nil,
			workBaseDir:   "",
			blockSymlinks: false,
		},
	}

	for _, opt := range opts {
		if opt == nil {
			continue
		}
		if err := opt(tt); err != nil {
			return nil, err
		}
	}

	pol, err := fspolicy.New(tt.cfg.workBaseDir, tt.cfg.allowedRoots, tt.cfg.blockSymlinks)
	if err != nil {
		return nil, err
	}
	tt.policy = pol
	return tt, nil
}

func (tt *TextTool) DeleteTextLinesTool() spec.Tool { return toolutil.CloneTool(deleteTextLinesTool) }
func (tt *TextTool) FindTextTool() spec.Tool        { return toolutil.CloneTool(findTextTool) }
func (tt *TextTool) InsertTextLinesTool() spec.Tool { return toolutil.CloneTool(insertTextLinesTool) }
func (tt *TextTool) ReadTextRangeTool() spec.Tool   { return toolutil.CloneTool(readTextRangeTool) }
func (tt *TextTool) ReplaceTextTool() spec.Tool     { return toolutil.CloneTool(replaceTextTool) }

func (tt *TextTool) DeleteTextLines(ctx context.Context, args DeleteTextLinesArgs) (*DeleteTextLinesOut, error) {
	return toolutil.WithRecoveryResp(func() (*DeleteTextLinesOut, error) {
		p := tt.snapshotPolicy()
		return deleteTextLines(ctx, args, p)
	})
}

func (tt *TextTool) FindText(ctx context.Context, args FindTextArgs) (*FindTextOut, error) {
	return toolutil.WithRecoveryResp(func() (*FindTextOut, error) {
		p := tt.snapshotPolicy()
		return findText(ctx, args, p)
	})
}

func (tt *TextTool) InsertTextLines(ctx context.Context, args InsertTextLinesArgs) (*InsertTextLinesOut, error) {
	return toolutil.WithRecoveryResp(func() (*InsertTextLinesOut, error) {
		p := tt.snapshotPolicy()
		return insertTextLines(ctx, args, p)
	})
}

func (tt *TextTool) ReadTextRange(ctx context.Context, args ReadTextRangeArgs) (*ReadTextRangeOut, error) {
	return toolutil.WithRecoveryResp(func() (*ReadTextRangeOut, error) {
		p := tt.snapshotPolicy()
		return readTextRange(ctx, args, p)
	})
}

func (tt *TextTool) ReplaceText(ctx context.Context, args ReplaceTextArgs) (*ReplaceTextOut, error) {
	return toolutil.WithRecoveryResp(func() (*ReplaceTextOut, error) {
		p := tt.snapshotPolicy()
		return replaceText(ctx, args, p)
	})
}

func (tt *TextTool) snapshotPolicy() fspolicy.FSPolicy {
	tt.mu.RLock()
	p := tt.policy
	tt.mu.RUnlock()
	return p
}
