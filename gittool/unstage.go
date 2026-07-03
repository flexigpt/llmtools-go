package gittool

import (
	"context"

	"github.com/flexigpt/llmtools-go/spec"
	"github.com/go-git/go-git/v5"
)

const resetFuncID spec.FuncID = "github.com/flexigpt/llmtools-go/gittool/reset.Reset"

var resetTool = spec.Tool{
	SchemaVersion: spec.SchemaVersion,
	ID:            "019b8358-3d2f-7d35-a391-85b7f5e4c009",
	Slug:          "gitreset",
	Version:       spec.VersionOne,
	DisplayName:   "Git reset",
	Description:   "Run a safe mixed reset to unstage all staged changes while preserving the working tree.",
	Tags:          []string{toolTagGit, toolTagWrite},

	ArgSchema: spec.JSONSchema(`{
"$schema": "http://json-schema.org/draft-07/schema#",
"type": "object",
"properties": {
	"repoPath": {
		"type": "string",
		"description": "Path to an existing local Git repository."
	}
},
"required": ["repoPath"],
"additionalProperties": false
}`),
	GoImpl: spec.GoToolImpl{FuncID: resetFuncID},

	CreatedAt:  spec.SchemaStartTime,
	ModifiedAt: spec.SchemaStartTime,
}

type ResetArgs struct {
	RepoPath string `json:"repoPath"`
}

type ResetOut struct {
	RepoPath string `json:"repoPath"`
	Action   string `json:"action"`
}

func reset(ctx context.Context, snap gitToolSnapshot, args ResetArgs) (*ResetOut, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	repo, wt, abs, err := openWorktree(ctx, snap, args.RepoPath)
	if err != nil {
		return nil, err
	}

	head, err := repo.Head()
	if err != nil {
		return nil, err
	}

	if err := wt.Reset(&git.ResetOptions{
		Commit: head.Hash(),
		Mode:   git.MixedReset,
	}); err != nil {
		return nil, err
	}

	return &ResetOut{
		RepoPath: abs,
		Action:   "mixed-reset",
	}, nil
}
