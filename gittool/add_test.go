package gittool

import (
	"path/filepath"
	"testing"
)

func TestAddStagesExplicitPathsAndAll(t *testing.T) {
	base := t.TempDir()
	repoAbs := filepath.Join(base, testRepoDirName)
	repoRel := filepath.FromSlash(testRepoDirName)

	repo := mustInitRepo(t, repoAbs, false)
	mustWriteFile(t, repoAbs, testFileName, testFileContent)
	_ = mustCommitAll(t, repo, testCommitMessage)

	mustWriteFile(t, repoAbs, testFileName, testModifiedFileContents)
	mustWriteFile(t, repoAbs, testSecondFileName, testSecondFileContents)

	tool := newTestGitTool(t, base)
	ctx := t.Context()

	explicit, err := tool.Add(ctx, AddArgs{RepoPath: repoRel, Paths: []string{testFileName}})
	if err != nil {
		t.Fatalf("Add(explicit) error = %v", err)
	}
	if explicit.All {
		t.Fatal("Add(explicit).All = true, want false")
	}
	if len(explicit.Paths) != 1 || explicit.Paths[0] != testFileName {
		t.Fatalf("Add(explicit).Paths = %#v, want [%q]", explicit.Paths, testFileName)
	}

	staged, err := tool.Status(ctx, StatusArgs{RepoPath: repoRel})
	if err != nil {
		t.Fatalf("Status(after explicit add) error = %v", err)
	}
	if len(staged.Entries) != 2 {
		t.Fatalf("Status(after explicit add).Entries len = %d, want 2", len(staged.Entries))
	}
	if staged.Entries[0].Path != testFileName || staged.Entries[0].Staging != "M" || staged.Entries[0].Worktree != " " {
		t.Fatalf("Status(after explicit add) staged file = %#v, want staged modification", staged.Entries[0])
	}
	if staged.Entries[1].Path != testSecondFileName || staged.Entries[1].Worktree != "?" {
		t.Fatalf("Status(after explicit add) untracked file = %#v, want untracked", staged.Entries[1])
	}

	all, err := tool.Add(ctx, AddArgs{RepoPath: repoRel, All: true})
	if err != nil {
		t.Fatalf("Add(all) error = %v", err)
	}
	if !all.All {
		t.Fatal("Add(all).All = false, want true")
	}
	if len(all.Paths) != 1 || all.Paths[0] != "." {
		t.Fatalf("Add(all).Paths = %#v, want [\".\"]", all.Paths)
	}

	stagedAll, err := tool.Status(ctx, StatusArgs{RepoPath: repoRel})
	if err != nil {
		t.Fatalf("Status(after all add) error = %v", err)
	}
	if len(stagedAll.Entries) != 2 {
		t.Fatalf("Status(after all add).Entries len = %d, want 2", len(stagedAll.Entries))
	}
	if stagedAll.Entries[0].Path != testFileName || stagedAll.Entries[0].Staging != "M" ||
		stagedAll.Entries[0].Worktree != " " {
		t.Fatalf("Status(after all add) modified file = %#v, want staged modification", stagedAll.Entries[0])
	}
	if stagedAll.Entries[1].Path != testSecondFileName || stagedAll.Entries[1].Staging != "A" ||
		stagedAll.Entries[1].Worktree != " " {
		t.Fatalf("Status(after all add) new file = %#v, want staged addition", stagedAll.Entries[1])
	}
}

func TestAddRequiresPathsUnlessAll(t *testing.T) {
	base := t.TempDir()
	repoAbs := filepath.Join(base, testRepoDirName)
	repoRel := filepath.FromSlash(testRepoDirName)

	repo := mustInitRepo(t, repoAbs, false)
	mustWriteFile(t, repoAbs, testFileName, testFileContent)
	_ = mustCommitAll(t, repo, testCommitMessage)

	tool := newTestGitTool(t, base)
	ctx := t.Context()

	_, err := tool.Add(ctx, AddArgs{RepoPath: repoRel})
	if err == nil {
		t.Fatal("Add(missing paths) error = nil, want error")
	}
}
