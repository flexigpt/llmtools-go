package gittool

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestFindReposFindsNestedAndBareRepositories(t *testing.T) {
	base := t.TempDir()

	workRepoAbs := filepath.Join(base, "work", "repo1")
	nestedRepoAbs := filepath.Join(base, "work", "nested", "repo2")
	bareRepoAbs := filepath.Join(base, "bare.git")

	if err := os.MkdirAll(workRepoAbs, 0o700); err != nil {
		t.Fatalf("MkdirAll(%q) error = %v", workRepoAbs, err)
	}
	if err := os.MkdirAll(nestedRepoAbs, 0o700); err != nil {
		t.Fatalf("MkdirAll(%q) error = %v", nestedRepoAbs, err)
	}
	if err := os.MkdirAll(bareRepoAbs, 0o700); err != nil {
		t.Fatalf("MkdirAll(%q) error = %v", bareRepoAbs, err)
	}

	_ = mustInitRepo(t, workRepoAbs, false)
	_ = mustInitRepo(t, nestedRepoAbs, false)
	_ = mustInitRepo(t, bareRepoAbs, true)

	tool := newTestGitTool(t, base)
	ctx := t.Context()

	withBare, err := tool.FindRepos(ctx, FindReposArgs{
		RootPath:       ".",
		IncludeBare:    new(true),
		MaxDepth:       5,
		MaxRepos:       10,
		MaxVisitedDirs: 100,
	})
	if err != nil {
		t.Fatalf("FindRepos(includeBare=true) error = %v", err)
	}
	if withBare.Count != 3 || len(withBare.Repos) != 3 {
		t.Fatalf("FindRepos(includeBare=true).Repos = %#v, want 3 repos", withBare.Repos)
	}
	paths := []string{withBare.Repos[0].RepoPath, withBare.Repos[1].RepoPath, withBare.Repos[2].RepoPath}
	if !reflect.DeepEqual(paths, sortStringsCopy(paths)) {
		t.Fatalf("FindRepos(includeBare=true).Repos not sorted by path: %#v", withBare.Repos)
	}
	var bareSeen bool
	for _, repo := range withBare.Repos {
		if repo.RepoPath == bareRepoAbs {
			bareSeen = true
			if !repo.Bare {
				t.Fatalf("FindRepos(includeBare=true) bare repo = %#v, want bare", repo)
			}
		}
	}
	if !bareSeen {
		t.Fatalf("FindRepos(includeBare=true) repos = %#v, want bare repo included", withBare.Repos)
	}

	withoutBare, err := tool.FindRepos(ctx, FindReposArgs{
		RootPath:       ".",
		IncludeBare:    new(false),
		MaxDepth:       5,
		MaxRepos:       10,
		MaxVisitedDirs: 100,
	})
	if err != nil {
		t.Fatalf("FindRepos(includeBare=false) error = %v", err)
	}
	if withoutBare.Count != 2 || len(withoutBare.Repos) != 2 {
		t.Fatalf("FindRepos(includeBare=false).Repos = %#v, want 2 repos", withoutBare.Repos)
	}
	for _, repo := range withoutBare.Repos {
		if repo.Bare {
			t.Fatalf("FindRepos(includeBare=false) unexpectedly returned bare repo: %#v", repo)
		}
	}
}

func TestFindReposTruncatesByMaxRepos(t *testing.T) {
	base := t.TempDir()

	firstRepoAbs := filepath.Join(base, "a", "repo1")
	secondRepoAbs := filepath.Join(base, "b", "repo2")
	if err := os.MkdirAll(firstRepoAbs, 0o700); err != nil {
		t.Fatalf("MkdirAll(%q) error = %v", firstRepoAbs, err)
	}
	if err := os.MkdirAll(secondRepoAbs, 0o700); err != nil {
		t.Fatalf("MkdirAll(%q) error = %v", secondRepoAbs, err)
	}
	_ = mustInitRepo(t, firstRepoAbs, false)
	_ = mustInitRepo(t, secondRepoAbs, false)

	tool := newTestGitTool(t, base)
	ctx := t.Context()

	out, err := tool.FindRepos(ctx, FindReposArgs{
		RootPath:       ".",
		IncludeBare:    new(false),
		MaxDepth:       5,
		MaxRepos:       1,
		MaxVisitedDirs: 100,
	})
	if err != nil {
		t.Fatalf("FindRepos(maxRepos=1) error = %v", err)
	}
	if !out.Truncated {
		t.Fatal("FindRepos(maxRepos=1).Truncated = false, want true")
	}
	if out.Count != 1 || len(out.Repos) != 1 {
		t.Fatalf("FindRepos(maxRepos=1).Repos = %#v, want 1 repo", out.Repos)
	}
}

func TestFindReposRejectsInvalidRoots(t *testing.T) {
	base := t.TempDir()
	tool := newTestGitTool(t, base)
	ctx := t.Context()

	_, err := tool.FindRepos(ctx, FindReposArgs{RootPath: ""})
	if err == nil {
		t.Fatal("FindRepos(empty root) error = nil, want error")
	}

	missingFile := filepath.Join(base, "not-a-dir")
	if err := os.WriteFile(missingFile, []byte("x"), 0o600); err != nil {
		t.Fatalf("WriteFile(%q) error = %v", missingFile, err)
	}
	_, err = tool.FindRepos(ctx, FindReposArgs{RootPath: filepath.Base(missingFile)})
	if err == nil {
		t.Fatal("FindRepos(non-directory) error = nil, want error")
	}
}
