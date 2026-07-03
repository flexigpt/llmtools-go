package gittool

import (
	"path/filepath"
	"testing"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/config"
)

func TestRepoInfoReportsRepositoryMetadata(t *testing.T) {
	base := t.TempDir()
	repoAbs := filepath.Join(base, testRepoDirName)
	repoRel := filepath.FromSlash(testRepoDirName)
	remoteAbs := filepath.Join(base, "remote.git")

	repo := mustInitRepo(t, repoAbs, false)
	mustWriteFile(t, repoAbs, testFileName, testFileContent)
	commitHash := mustCommitAll(t, repo, testCommitMessage)
	baseBranch, _, _ := headInfo(repo)
	mustInitRepo(t, remoteAbs, true)
	remote := mustCreateRemote(t, repo, testOriginRemoteName, remoteAbs)
	ctx := t.Context()
	mustPushRefs(t, ctx, remote, []config.RefSpec{config.RefSpec("+refs/heads/*:refs/heads/*")})
	mustFetchRefs(t, ctx, remote, []config.RefSpec{config.RefSpec("+refs/heads/*:refs/remotes/origin/*")})

	tool := newTestGitTool(t, base)
	if _, err := tool.CreateTag(
		ctx,
		CreateTagArgs{RepoPath: repoRel, Name: "v1.0.0", Target: commitHash.String()},
	); err != nil {
		t.Fatalf("CreateTag(before repoinfo) error = %v", err)
	}

	out, err := tool.RepoInfo(ctx, RepoInfoArgs{RepoPath: repoRel})
	if err != nil {
		t.Fatalf("RepoInfo() error = %v", err)
	}
	if out.RepoPath != repoAbs {
		t.Fatalf("RepoInfo().RepoPath = %q, want %q", out.RepoPath, repoAbs)
	}
	if out.GitDir != filepath.Join(repoAbs, ".git") {
		t.Fatalf("RepoInfo().GitDir = %q, want %q", out.GitDir, filepath.Join(repoAbs, ".git"))
	}
	if out.Bare {
		t.Fatal("RepoInfo().Bare = true, want false")
	}
	if out.Branch != baseBranch {
		t.Fatalf("RepoInfo().Branch = %q, want %q", out.Branch, baseBranch)
	}
	if out.LocalBranchCount != 1 {
		t.Fatalf("RepoInfo().LocalBranchCount = %d, want 1", out.LocalBranchCount)
	}
	if out.RemoteBranchCount != 1 {
		t.Fatalf("RepoInfo().RemoteBranchCount = %d, want 1", out.RemoteBranchCount)
	}
	if out.TagCount != 1 {
		t.Fatalf("RepoInfo().TagCount = %d, want 1", out.TagCount)
	}
	if len(out.Remotes) != 1 || out.Remotes[0].Name != testOriginRemoteName {
		t.Fatalf("RepoInfo().Remotes = %#v, want origin remote", out.Remotes)
	}
	if out.IndexState.HasConflicts {
		t.Fatal("RepoInfo().IndexState.HasConflicts = true, want false")
	}
}

func TestRepoInfoDetectsDetachedAndBareRepository(t *testing.T) {
	base := t.TempDir()
	repoAbs := filepath.Join(base, testRepoDirName)
	bareAbs := filepath.Join(base, testBareRepoDirName)

	repo := mustInitRepo(t, repoAbs, false)
	mustWriteFile(t, repoAbs, testFileName, testFileContent)
	commitHash := mustCommitAll(t, repo, "detached target")

	wt, err := repo.Worktree()
	if err != nil {
		t.Fatalf("Worktree() error = %v", err)
	}
	if err := wt.Checkout(&git.CheckoutOptions{Hash: commitHash, Force: true}); err != nil {
		t.Fatalf("Checkout(detached) error = %v", err)
	}

	mustInitRepo(t, bareAbs, true)
	tool := newTestGitTool(t, base)
	ctx := t.Context()

	detachedOut, err := tool.RepoInfo(ctx, RepoInfoArgs{RepoPath: filepath.FromSlash(testRepoDirName)})
	if err != nil {
		t.Fatalf("RepoInfo(detached) error = %v", err)
	}
	if !detachedOut.DetachedHead {
		t.Fatal("RepoInfo(detached).DetachedHead = false, want true")
	}
	if detachedOut.Branch != "" {
		t.Fatalf("RepoInfo(detached).Branch = %q, want empty", detachedOut.Branch)
	}
	if detachedOut.HeadHash != commitHash.String() {
		t.Fatalf("RepoInfo(detached).HeadHash = %q, want %q", detachedOut.HeadHash, commitHash.String())
	}

	bareOut, err := tool.RepoInfo(ctx, RepoInfoArgs{RepoPath: filepath.FromSlash(testBareRepoDirName)})
	if err != nil {
		t.Fatalf("RepoInfo(bare) error = %v", err)
	}
	if !bareOut.Bare {
		t.Fatal("RepoInfo(bare).Bare = false, want true")
	}
	if bareOut.GitDir != bareAbs {
		t.Fatalf("RepoInfo(bare).GitDir = %q, want %q", bareOut.GitDir, bareAbs)
	}
}
