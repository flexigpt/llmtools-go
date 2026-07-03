package gittool

import (
	"path/filepath"
	"testing"
)

func TestStatusReportsCleanAndDirtyState(t *testing.T) {
	base := t.TempDir()
	repoRel := filepath.FromSlash(testRepoDirName)
	repoAbs := filepath.Join(base, repoRel)
	remoteAbs := filepath.Join(base, "remote.git")

	repo := mustInitRepo(t, repoAbs, false)
	mustWriteFile(t, repoAbs, testFileName, testFileContent)
	commitHash := mustCommitAll(t, repo, testCommitMessage)
	mustInitRepo(t, remoteAbs, true)
	mustCreateRemote(t, repo, testOriginRemoteName, remoteAbs)

	tool := newTestGitTool(t, base)
	ctx := t.Context()

	clean, err := tool.Status(ctx, StatusArgs{RepoPath: repoRel})
	if err != nil {
		t.Fatalf("Status(clean) error = %v", err)
	}
	branch, headHash, _ := headInfo(repo)
	if clean.RepoPath != repoAbs {
		t.Fatalf("Status(clean).RepoPath = %q, want %q", clean.RepoPath, repoAbs)
	}
	if clean.HeadHash != headHash {
		t.Fatalf("Status(clean).HeadHash = %q, want %q", clean.HeadHash, headHash)
	}
	if clean.HeadHash != commitHash.String() {
		t.Fatalf("Status(clean).HeadHash = %q, want %q", clean.HeadHash, commitHash.String())
	}
	if clean.Branch != branch {
		t.Fatalf("Status(clean).Branch = %q, want %q", clean.Branch, branch)
	}
	if !clean.IsClean {
		t.Fatal("Status(clean).IsClean = false, want true")
	}
	if len(clean.Entries) != 0 {
		t.Fatalf("Status(clean).Entries = %#v, want empty", clean.Entries)
	}
	if len(clean.Remotes) != 1 || clean.Remotes[0].Name != testOriginRemoteName {
		t.Fatalf("Status(clean).Remotes = %#v, want origin remote", clean.Remotes)
	}
	if len(clean.Remotes[0].URLs) != 1 || clean.Remotes[0].URLs[0] != remoteAbs {
		t.Fatalf("Status(clean).remote URL = %#v, want %q", clean.Remotes[0].URLs, remoteAbs)
	}
	if clean.IndexState.HasConflicts {
		t.Fatal("Status(clean).IndexState.HasConflicts = true, want false")
	}

	mustWriteFile(t, repoAbs, testFileName, testModifiedFileContents)
	mustWriteFile(t, repoAbs, testSecondFileName, testSecondFileContents)

	dirty, err := tool.Status(ctx, StatusArgs{RepoPath: repoRel})
	if err != nil {
		t.Fatalf("Status(dirty) error = %v", err)
	}
	if dirty.IsClean {
		t.Fatal("Status(dirty).IsClean = true, want false")
	}
	if len(dirty.Entries) != 2 {
		t.Fatalf("Status(dirty).Entries len = %d, want 2", len(dirty.Entries))
	}
	if dirty.Entries[0].Path != testFileName || dirty.Entries[1].Path != testSecondFileName {
		t.Fatalf("Status(dirty).Entries = %#v, want sorted file paths", dirty.Entries)
	}
	if dirty.Entries[0].Worktree != "M" || dirty.Entries[0].Staging != " " {
		t.Fatalf("Status(dirty) modified entry = %#v, want worktree M / staging space", dirty.Entries[0])
	}
	if dirty.Entries[1].Worktree != "?" {
		t.Fatalf("Status(dirty) untracked entry = %#v, want worktree ?", dirty.Entries[1])
	}
}

func TestStatusMissingRepository(t *testing.T) {
	base := t.TempDir()
	tool := newTestGitTool(t, base)
	ctx := t.Context()

	_, err := tool.Status(ctx, StatusArgs{RepoPath: filepath.FromSlash("missing/repo")})
	if err == nil {
		t.Fatal("Status(missing) error = nil, want error")
	}
}
