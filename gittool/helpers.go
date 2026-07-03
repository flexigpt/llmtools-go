package gittool

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/flexigpt/llmtools-go/internal/fspolicy"
	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/format/index"
	"github.com/go-git/go-git/v5/plumbing/object"
)

const (
	defaultContextLines = 3
	defaultDiffMaxBytes = 1024 * 1024
	hardDiffMaxBytes    = 2 * 1024 * 1024
	hardBlobReadBytes   = 4 * 1024 * 1024
	defaultLogMaxCount  = 10
	hardLogMaxCount     = 100
	maxStagePaths       = 500
	maxRevisionLength   = 256
	maxRefNameLength    = 256
	maxTagMsgBytes      = 128 * 1024
	maxCommitMsgBytes   = 128 * 1024
)

var errStopIteration = errors.New("stop git iteration")

type gitToolSnapshot struct {
	policy             fspolicy.FSPolicy
	defaultAuthorName  string
	defaultAuthorEmail string
}

func (gt *GitTool) snapshot() gitToolSnapshot {
	gt.mu.RLock()
	defer gt.mu.RUnlock()
	return gitToolSnapshot{
		policy:             gt.policy,
		defaultAuthorName:  gt.cfg.defaultAuthorName,
		defaultAuthorEmail: gt.cfg.defaultAuthorEmail,
	}
}

func openRepository(ctx context.Context, snap gitToolSnapshot, repoPath string) (*git.Repository, string, error) {
	if err := ctx.Err(); err != nil {
		return nil, "", err
	}

	if strings.TrimSpace(repoPath) == "" {
		return nil, "", errors.New("repoPath is required")
	}

	abs, err := snap.policy.ResolvePath(repoPath, "")
	if err != nil {
		return nil, "", err
	}

	st, err := os.Stat(abs)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, "", fmt.Errorf("repository path does not exist: %s", abs)
		}
		return nil, "", err
	}
	if !st.IsDir() {
		return nil, "", fmt.Errorf("repository path is not a directory: %s", abs)
	}

	repo, err := git.PlainOpen(abs)
	if err != nil {
		return nil, "", fmt.Errorf("open git repository %q: %w", abs, err)
	}

	return repo, abs, nil
}

func openWorktree(
	ctx context.Context,
	snap gitToolSnapshot,
	repoPath string,
) (*git.Repository, *git.Worktree, string, error) {
	repo, abs, err := openRepository(ctx, snap, repoPath)
	if err != nil {
		return nil, nil, "", err
	}
	wt, err := repo.Worktree()
	if err != nil {
		return nil, nil, "", fmt.Errorf("open git worktree %q: %w", abs, err)
	}
	return repo, wt, abs, nil
}

func resolveCommit(repo *git.Repository, revision string) (*object.Commit, error) {
	rev := strings.TrimSpace(revision)
	if rev == "" {
		rev = revisionHead
	}
	if err := validateRevision(rev); err != nil {
		return nil, err
	}

	hash, err := repo.ResolveRevision(plumbing.Revision(rev))
	if err != nil {
		return nil, fmt.Errorf("resolve revision %q: %w", rev, err)
	}

	commit, err := repo.CommitObject(*hash)
	if err != nil {
		return nil, fmt.Errorf("load commit %q: %w", rev, err)
	}
	return commit, nil
}

func validateRevision(rev string) error {
	if strings.TrimSpace(rev) == "" {
		return errors.New("revision is required")
	}
	if len(rev) > maxRevisionLength {
		return fmt.Errorf("revision too long: max %d bytes", maxRevisionLength)
	}
	if strings.HasPrefix(rev, "-") {
		return fmt.Errorf("revision %q is invalid: must not start with '-'", rev)
	}
	if strings.ContainsRune(rev, '\x00') {
		return errors.New("revision contains NUL byte")
	}
	for _, r := range rev {
		if unicode.IsControl(r) {
			return fmt.Errorf("revision contains control character %U", r)
		}
	}
	return nil
}

func validateLocalBranchName(name string) error {
	n := strings.TrimSpace(name)
	if n == "" {
		return errors.New("branch name is required")
	}
	if len(n) > maxRefNameLength {
		return fmt.Errorf("branch name too long: max %d bytes", maxRefNameLength)
	}
	if strings.HasPrefix(n, "-") {
		return fmt.Errorf("branch name %q is invalid: must not start with '-'", n)
	}
	if strings.ContainsRune(n, '\x00') {
		return errors.New("branch name contains NUL byte")
	}
	for _, r := range n {
		if unicode.IsControl(r) {
			return fmt.Errorf("branch name contains control character %U", r)
		}
	}

	if strings.HasPrefix(n, "/") ||
		strings.HasSuffix(n, "/") ||
		strings.Contains(n, "//") ||
		strings.Contains(n, "..") ||
		strings.Contains(n, "@{") ||
		strings.HasSuffix(n, ".") ||
		strings.HasSuffix(n, ".lock") ||
		strings.ContainsAny(n, ` ~^:?*[\\`) {
		return fmt.Errorf("invalid branch name: %q", n)
	}

	return nil
}

func validateTagName(name string) error {
	n := strings.TrimSpace(name)
	if n == "" {
		return errors.New("tag name is required")
	}
	if len(n) > maxRefNameLength {
		return fmt.Errorf("tag name too long: max %d bytes", maxRefNameLength)
	}
	if strings.HasPrefix(n, "-") {
		return fmt.Errorf("tag name %q is invalid: must not start with '-'", n)
	}
	if strings.HasPrefix(n, "refs/") {
		return fmt.Errorf("tag name %q is invalid: pass a plain tag name, not a full ref", n)
	}
	if strings.ContainsRune(n, '\x00') {
		return errors.New("tag name contains NUL byte")
	}
	for _, r := range n {
		if unicode.IsControl(r) {
			return fmt.Errorf("tag name contains control character %U", r)
		}
	}

	if strings.HasPrefix(n, "/") ||
		strings.HasSuffix(n, "/") ||
		strings.Contains(n, "//") ||
		strings.Contains(n, "..") ||
		strings.Contains(n, "@{") ||
		strings.HasSuffix(n, ".") ||
		strings.HasSuffix(n, ".lock") ||
		strings.ContainsAny(n, ` ~^:?*[\\`) {
		return fmt.Errorf("invalid tag name: %q", n)
	}
	return nil
}

func validateTagPattern(pattern string) error {
	if len(pattern) > maxRefNameLength {
		return fmt.Errorf("pattern too long: max %d bytes", maxRefNameLength)
	}
	if strings.ContainsRune(pattern, '\x00') {
		return errors.New("pattern contains NUL byte")
	}
	for _, r := range pattern {
		if unicode.IsControl(r) {
			return fmt.Errorf("pattern contains control character %U", r)
		}
	}
	return nil
}

func normalizeRepoRelativePath(p string) (string, error) {
	trimmed := strings.TrimSpace(p)
	if trimmed == "" {
		return "", errors.New("path is empty")
	}
	if strings.ContainsRune(trimmed, '\x00') {
		return "", errors.New("path contains NUL byte")
	}
	if filepath.IsAbs(trimmed) || path.IsAbs(filepath.ToSlash(trimmed)) {
		return "", fmt.Errorf("path must be repository-relative: %q", p)
	}

	cleaned := path.Clean(filepath.ToSlash(trimmed))
	if cleaned == "." {
		return ".", nil
	}
	if cleaned == ".." || strings.HasPrefix(cleaned, "../") {
		return "", fmt.Errorf("path escapes repository root: %q", p)
	}
	if strings.HasPrefix(cleaned, "-") {
		return "", fmt.Errorf("path %q is invalid: must not start with '-'", p)
	}
	return cleaned, nil
}

func normalizeRepoRelativePaths(paths []string, allowEmpty bool) ([]string, error) {
	if len(paths) == 0 && allowEmpty {
		return nil, nil
	}
	if len(paths) > maxStagePaths {
		return nil, fmt.Errorf("too many paths: max %d", maxStagePaths)
	}
	out := make([]string, 0, len(paths))
	for _, p := range paths {
		cleaned, err := normalizeRepoRelativePath(p)
		if err != nil {
			return nil, err
		}
		out = append(out, cleaned)
	}
	return sortedStrings(out), nil
}

func normalizePositiveInt(value, def, minNum, maxNum int) int {
	if value == 0 {
		value = def
	}
	if value < minNum {
		value = minNum
	}
	if value > maxNum {
		value = maxNum
	}
	return value
}

func limitStringBytes(s string, maxBytes int) (string, bool) {
	if maxBytes <= 0 {
		maxBytes = defaultDiffMaxBytes
	}
	if maxBytes > hardDiffMaxBytes {
		maxBytes = hardDiffMaxBytes
	}
	if len(s) <= maxBytes {
		return s, false
	}

	cut := s[:maxBytes]
	for cut != "" && !utf8.ValidString(cut) {
		cut = cut[:len(cut)-1]
	}
	return cut + "\n[truncated]", true
}

func headInfo(repo *git.Repository) (branch, hash string, detached bool) {
	ref, err := repo.Head()
	if err != nil {
		return "", "", false
	}
	hash = ref.Hash().String()
	if ref.Name().IsBranch() {
		return ref.Name().Short(), hash, false
	}
	return "", hash, true
}

type CommitInfo struct {
	Hash        string    `json:"hash"`
	ShortHash   string    `json:"shortHash"`
	AuthorName  string    `json:"authorName"`
	AuthorEmail string    `json:"authorEmail"`
	AuthorWhen  time.Time `json:"authorWhen"`
	Subject     string    `json:"subject"`
	Body        string    `json:"body,omitempty"`
}

func commitInfo(c *object.Commit) CommitInfo {
	subject, body := splitCommitMessage(c.Message)
	hash := c.Hash.String()
	short := hash
	if len(short) > 12 {
		short = short[:12]
	}
	return CommitInfo{
		Hash:        hash,
		ShortHash:   short,
		AuthorName:  c.Author.Name,
		AuthorEmail: c.Author.Email,
		AuthorWhen:  c.Author.When,
		Subject:     subject,
		Body:        body,
	}
}

func splitCommitMessage(msg string) (subject, line string) {
	m := strings.TrimRight(msg, "\r\n")
	if m == "" {
		return "", ""
	}
	lines := strings.SplitN(m, "\n", 2)
	subject = strings.TrimSpace(lines[0])
	if len(lines) == 1 {
		return subject, ""
	}
	return subject, strings.TrimSpace(lines[1])
}

func parseTime(name, value string) (*time.Time, error) {
	v := strings.TrimSpace(value)
	if v == "" {
		return nil, errors.New("got empty time")
	}
	if strings.HasPrefix(v, "-") {
		return nil, fmt.Errorf("%s must not start with '-'", name)
	}

	formats := []string{
		time.RFC3339,
		"2006-01-02T15:04:05",
		"2006-01-02",
	}
	for _, format := range formats {
		t, err := time.Parse(format, v)
		if err == nil {
			return &t, nil
		}
	}
	return nil, fmt.Errorf("%s must be RFC3339, YYYY-MM-DDTHH:MM:SS, or YYYY-MM-DD", name)
}

func sortedStrings(in []string) []string {
	out := append([]string(nil), in...)
	sort.Strings(out)
	return out
}

func matchesRepoRelativePathFilter(p string, filters []string) bool {
	if len(filters) == 0 {
		return true
	}
	for _, f := range filters {
		if f == "." || p == f || strings.HasPrefix(p, f+"/") {
			return true
		}
	}
	return false
}

func statusDiffPaths(st git.Status, kind DiffKind, filters []string) []string {
	paths := make([]string, 0, len(st))
	for p, fs := range st {
		if !matchesRepoRelativePathFilter(p, filters) {
			continue
		}
		switch kind {
		case DiffKindWorking:
			if fs.Worktree != git.Unmodified {
				paths = append(paths, p)
			}
		case DiffKindStaged:
			if fs.Staging != git.Unmodified && fs.Staging != git.Untracked {
				paths = append(paths, p)
			}
		default:
		}
	}
	sort.Strings(paths)
	return paths
}

func treeDiffPaths(a, b *object.Tree, filters []string) ([]string, error) {
	paths := make(map[string]bool)
	if a != nil {
		iter := a.Files()
		defer iter.Close()
		if err := iter.ForEach(func(f *object.File) error {
			if matchesRepoRelativePathFilter(f.Name, filters) {
				paths[f.Name] = true
			}
			return nil
		}); err != nil {
			return nil, err
		}
	}
	if b != nil {
		iter := b.Files()
		defer iter.Close()
		if err := iter.ForEach(func(f *object.File) error {
			if matchesRepoRelativePathFilter(f.Name, filters) {
				paths[f.Name] = true
			}
			return nil
		}); err != nil {
			return nil, err
		}
	}
	out := make([]string, 0, len(paths))
	for p := range paths {
		out = append(out, p)
	}
	sort.Strings(out)
	return out, nil
}

func indexEntriesByPath(repo *git.Repository) (map[string]index.Entry, error) {
	idx, err := repo.Storer.Index()
	if err != nil {
		return nil, err
	}
	out := make(map[string]index.Entry, len(idx.Entries))
	for i := range idx.Entries {
		e := idx.Entries[i]
		out[e.Name] = *e
	}
	return out, nil
}

func indexPathContent(
	repo *git.Repository,
	entries map[string]index.Entry,
	p string,
	maxBytes int64,
) (data []byte, exists bool, err error) {
	entry, ok := entries[p]
	if !ok {
		return nil, false, nil
	}
	data, _, err = blobContent(repo, entry.Hash, maxBytes)
	if err != nil {
		return nil, false, err
	}
	return data, true, nil
}

func blobContent(repo *git.Repository, hash plumbing.Hash, maxBytes int64) (data []byte, ok bool, err error) {
	if maxBytes <= 0 || maxBytes > hardBlobReadBytes {
		maxBytes = hardBlobReadBytes
	}
	blob, err := repo.BlobObject(hash)
	if err != nil {
		return nil, false, err
	}
	r, err := blob.Reader()
	if err != nil {
		return nil, false, err
	}
	defer r.Close()
	return readLimited(r, maxBytes)
}

func treePathContent(tree *object.Tree, p string, maxBytes int64) (data []byte, ok bool, err error) {
	if tree == nil {
		return nil, false, nil
	}
	f, err := tree.File(p)
	if err != nil {
		return nil, false, nil
	}
	r, err := f.Reader()
	if err != nil {
		return nil, false, err
	}
	defer r.Close()
	data, _, err = readLimited(r, maxBytes)
	if err != nil {
		return nil, false, err
	}
	return data, true, nil
}

func worktreePathContent(wt *git.Worktree, p string, maxBytes int64) (data []byte, ok bool, err error) {
	f, err := wt.Filesystem.Open(p)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, false, nil
		}
		return nil, false, err
	}
	defer f.Close()
	data, _, err = readLimited(f, maxBytes)
	if err != nil {
		return nil, false, err
	}
	return data, true, nil
}

func readLimited(r io.Reader, maxBytes int64) (data []byte, ok bool, err error) {
	if maxBytes <= 0 || maxBytes > hardBlobReadBytes {
		maxBytes = hardBlobReadBytes
	}
	var b bytes.Buffer
	n, err := io.CopyN(&b, r, maxBytes+1)
	if err != nil && !errors.Is(err, io.EOF) {
		return nil, false, err
	}
	data = b.Bytes()
	if n > maxBytes || int64(len(data)) > maxBytes {
		return data[:maxBytes], true, nil
	}
	return data, false, nil
}

func unifiedFileDiff(
	p string,
	oldData []byte,
	oldExists bool,
	newData []byte,
	newExists bool,
	contextLines int,
) string {
	_ = contextLines
	if oldExists && newExists && bytes.Equal(oldData, newData) {
		return ""
	}
	if !oldExists && !newExists {
		return ""
	}

	var b strings.Builder
	b.WriteString("diff --git ")
	b.WriteString("a/")
	b.WriteString(p)
	b.WriteString(" ")
	b.WriteString("b/")
	b.WriteString(p)
	b.WriteString("\n")

	if !oldExists {
		b.WriteString("new file mode 100644\n")
	}
	if !newExists {
		b.WriteString("deleted file mode 100644\n")
	}

	if isBinaryData(oldData) || isBinaryData(newData) {
		b.WriteString("Binary files ")
		if oldExists {
			b.WriteString("a/")
			b.WriteString(p)
		} else {
			b.WriteString("/dev/null")
		}
		b.WriteString(" and ")
		if newExists {
			b.WriteString("b/")
			b.WriteString(p)
		} else {
			b.WriteString("/dev/null")
		}
		b.WriteString(" differ\n")
		return b.String()
	}

	if oldExists {
		b.WriteString("--- a/")
		b.WriteString(p)
		b.WriteString("\n")
	} else {
		b.WriteString("--- /dev/null\n")
	}
	if newExists {
		b.WriteString("+++ b/")
		b.WriteString(p)
		b.WriteString("\n")
	} else {
		b.WriteString("+++ /dev/null\n")
	}

	oldLines := splitDiffLines(string(oldData))
	newLines := splitDiffLines(string(newData))
	fmt.Fprintf(&b, "@@ -1,%d +1,%d @@\n", len(oldLines), len(newLines))
	for _, line := range oldLines {
		writeDiffLine(&b, "-", line)
	}
	for _, line := range newLines {
		writeDiffLine(&b, "+", line)
	}
	return b.String()
}

func splitDiffLines(s string) []string {
	if s == "" {
		return nil
	}
	lines := strings.SplitAfter(s, "\n")
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}

func writeDiffLine(b *strings.Builder, prefix, line string) {
	b.WriteString(prefix)
	b.WriteString(line)
	if !strings.HasSuffix(line, "\n") {
		b.WriteString("\n")
	}
}

func isBinaryData(data []byte) bool {
	if len(data) == 0 {
		return false
	}
	return bytes.IndexByte(data, 0) >= 0 || !utf8.Valid(data)
}
