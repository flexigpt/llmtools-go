package gittool

import (
	"path/filepath"
	"reflect"
	"testing"
)

func TestListTreeRecursiveAndNonRecursive(t *testing.T) {
	base := t.TempDir()
	repoAbs := filepath.Join(base, testRepoDirName)
	repoRel := filepath.FromSlash(testRepoDirName)

	repo := mustInitRepo(t, repoAbs, false)
	mustWriteFile(t, repoAbs, testFileName, testFileContent)
	mustWriteFile(t, repoAbs, filepath.FromSlash("dir/sub.txt"), "sub file\n")
	_ = mustCommitAll(t, repo, "tree fixtures")

	tool := newTestGitTool(t, base)
	ctx := t.Context()

	recursive, err := tool.ListTree(ctx, ListTreeArgs{
		RepoPath:   repoRel,
		Recursive:  new(true),
		Path:       ".",
		MaxEntries: 10,
	})
	if err != nil {
		t.Fatalf("ListTree(recursive) error = %v", err)
	}
	if recursive.Count != 2 {
		t.Fatalf("ListTree(recursive).Count = %d, want 2", recursive.Count)
	}
	if len(recursive.Entries) != 2 {
		t.Fatalf("ListTree(recursive).Entries len = %d, want 2", len(recursive.Entries))
	}
	if !reflect.DeepEqual(
		[]string{recursive.Entries[0].Path, recursive.Entries[1].Path},
		[]string{"dir/sub.txt", filepath.ToSlash(testFileName)},
	) {
		t.Fatalf("ListTree(recursive).Entries = %#v, want nested file and root file sorted by path", recursive.Entries)
	}
	if recursive.Entries[0].Kind != TreeModeKindBlob || recursive.Entries[1].Kind != TreeModeKindBlob {
		t.Fatalf("ListTree(recursive).Kinds = %q, %q, want blobs", recursive.Entries[0].Kind, recursive.Entries[1].Kind)
	}

	nonRecursive, err := tool.ListTree(ctx, ListTreeArgs{
		RepoPath:    repoRel,
		Recursive:   new(false),
		IncludeDirs: true,
		Path:        ".",
	})
	if err != nil {
		t.Fatalf("ListTree(non-recursive) error = %v", err)
	}
	if len(nonRecursive.Entries) != 2 {
		t.Fatalf("ListTree(non-recursive).Entries len = %d, want 2", len(nonRecursive.Entries))
	}
	if nonRecursive.Entries[0].Path != "dir" || nonRecursive.Entries[0].Kind != TreeModeKindTree {
		t.Fatalf("ListTree(non-recursive)[0] = %#v, want tree entry for dir", nonRecursive.Entries[0])
	}
	if nonRecursive.Entries[1].Path != testFileName || nonRecursive.Entries[1].Kind != TreeModeKindBlob {
		t.Fatalf("ListTree(non-recursive)[1] = %#v, want blob entry for root file", nonRecursive.Entries[1])
	}

	fileOnly, err := tool.ListTree(ctx, ListTreeArgs{
		RepoPath: repoRel,
		Path:     testFileName,
	})
	if err != nil {
		t.Fatalf("ListTree(file path) error = %v", err)
	}
	if len(fileOnly.Entries) != 1 || fileOnly.Entries[0].Path != testFileName {
		t.Fatalf("ListTree(file path).Entries = %#v, want single file entry", fileOnly.Entries)
	}
}

func TestListTreeMissingPathErrors(t *testing.T) {
	base := t.TempDir()
	repoAbs := filepath.Join(base, testRepoDirName)
	repoRel := filepath.FromSlash(testRepoDirName)

	repo := mustInitRepo(t, repoAbs, false)
	mustWriteFile(t, repoAbs, testFileName, testFileContent)
	_ = mustCommitAll(t, repo, "tree fixtures")

	tool := newTestGitTool(t, base)
	ctx := t.Context()

	_, err := tool.ListTree(ctx, ListTreeArgs{RepoPath: repoRel, Path: "missing-dir"})
	if err == nil {
		t.Fatal("ListTree(missing path) error = nil, want error")
	}
}
