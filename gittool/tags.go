package gittool

import (
	"context"
	"path/filepath"
	"sort"
	"strings"

	"github.com/flexigpt/llmtools-go/spec"
	"github.com/go-git/go-git/v5/plumbing"
)

const tagsFuncID spec.FuncID = "github.com/flexigpt/llmtools-go/gittool/tags.Tags"

var tagsTool = spec.Tool{
	SchemaVersion: spec.SchemaVersion,
	ID:            "019b8358-3d2f-7d35-a391-85b7f5e4c007",
	Slug:          "gittag",
	Version:       spec.VersionOne,
	DisplayName:   "Git tag",
	Description:   "List tags in a local Git repository, optionally filtered by a glob pattern.",
	Tags:          []string{toolTagGit, toolTagRead},

	ArgSchema: spec.JSONSchema(`{
"$schema": "http://json-schema.org/draft-07/schema#",
"type": "object",
"properties": {
	"repoPath": {
		"type": "string",
		"description": "Path to an existing local Git repository."
	},
	"pattern": {
		"type": "string",
		"description": "Optional glob pattern for tag names, for example v*."
	}
},
"required": ["repoPath"],
"additionalProperties": false
}`),
	GoImpl: spec.GoToolImpl{FuncID: tagsFuncID},

	CreatedAt:  spec.SchemaStartTime,
	ModifiedAt: spec.SchemaStartTime,
}

type TagsArgs struct {
	RepoPath string `json:"repoPath"`
	Pattern  string `json:"pattern,omitempty"`
}

type TagInfo struct {
	Name string `json:"name"`
	Hash string `json:"hash"`
}

type TagsOut struct {
	RepoPath string    `json:"repoPath"`
	Pattern  string    `json:"pattern,omitempty"`
	Tags     []TagInfo `json:"tags"`
}

func tags(ctx context.Context, snap gitToolSnapshot, args TagsArgs) (*TagsOut, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	pattern := strings.TrimSpace(args.Pattern)
	if err := validateTagPattern(pattern); err != nil {
		return nil, err
	}

	repo, abs, err := openRepository(ctx, snap, args.RepoPath)
	if err != nil {
		return nil, err
	}

	iter, err := repo.Tags()
	if err != nil {
		return nil, err
	}
	defer iter.Close()

	out := &TagsOut{
		RepoPath: abs,
		Pattern:  pattern,
	}

	if err := iter.ForEach(func(ref *plumbing.Reference) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		name := strings.TrimPrefix(string(ref.Name()), "refs/tags/")
		if pattern != "" {
			ok, err := filepath.Match(pattern, name)
			if err != nil {
				return err
			}
			if !ok {
				return nil
			}
		}
		out.Tags = append(out.Tags, TagInfo{
			Name: name,
			Hash: ref.Hash().String(),
		})
		return nil
	}); err != nil {
		return nil, err
	}

	sort.Slice(out.Tags, func(i, j int) bool {
		return out.Tags[i].Name < out.Tags[j].Name
	})

	return out, nil
}
