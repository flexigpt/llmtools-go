package texttool

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestApplyUnifiedDiffEndToEnd(t *testing.T) {
	dir := t.TempDir()
	tt := mustNewTextTool(t, dir)

	t.Run("plain_header_dry_run_apply_and_reapply", func(t *testing.T) {
		path := writeTextFile(t, dir, "alpha.txt", "A\nB\nC\n")
		diff := makePatchText(
			"--- a/alpha.txt",
			"+++ b/alpha.txt",
			"@@ -1,3 +1,3 @@",
			" A",
			"-B",
			"+X",
			" C",
		)

		out, err := tt.ApplyUnifiedDiff(t.Context(), ApplyUnifiedDiffArgs{DiffText: diff, DryRun: true})
		mustNoErr(t, err)
		if !out.OK || !out.DryRun || out.Status != ApplyUnifiedDiffStatusApplicable {
			t.Fatalf("dry run status mismatch: ok=%v dryRun=%v status=%s", out.OK, out.DryRun, out.Status)
		}
		if len(out.Files) != 1 || out.Files[0].Status != ApplyUnifiedDiffStatusApplicable {
			t.Fatalf("dry run file status mismatch: %#v", out.Files)
		}
		if len(out.FileTargets) != 1 || out.FileTargets[0].TargetPath != "alpha.txt" {
			t.Fatalf("dry run file targets mismatch: %#v", out.FileTargets)
		}
		if got := readFileString(t, path); got != "A\nB\nC\n" {
			t.Fatalf("dry run should not modify file: got %q", got)
		}

		out, err = tt.ApplyUnifiedDiff(t.Context(), ApplyUnifiedDiffArgs{DiffText: diff})
		mustNoErr(t, err)
		if !out.OK || out.Status != ApplyUnifiedDiffStatusApplied || out.DryRun {
			t.Fatalf("apply status mismatch: ok=%v dryRun=%v status=%s", out.OK, out.DryRun, out.Status)
		}
		if got := readFileString(t, path); got != "A\nX\nC\n" {
			t.Fatalf("apply content mismatch: got %q", got)
		}
		if out.Summary.Files != 1 || out.Summary.Hunks != 1 || out.Summary.AppliedHunks != 1 ||
			out.Summary.AlreadyAppliedHunks != 0 ||
			out.Summary.AddedLines != 1 ||
			out.Summary.DeletedLines != 1 {
			t.Fatalf("apply summary mismatch: %#v", out.Summary)
		}
		if len(out.FileTargets) != 1 || out.FileTargets[0].TargetPath != "alpha.txt" {
			t.Fatalf("apply file targets mismatch: %#v", out.FileTargets)
		}

		out, err = tt.ApplyUnifiedDiff(t.Context(), ApplyUnifiedDiffArgs{DiffText: diff})
		mustNoErr(t, err)
		if out.Status != ApplyUnifiedDiffStatusAlreadyApplied || !out.OK {
			t.Fatalf("reapply status mismatch: ok=%v status=%s", out.OK, out.Status)
		}
		if len(out.Files) != 1 || out.Files[0].Status != ApplyUnifiedDiffStatusAlreadyApplied ||
			out.Files[0].AlreadyAppliedHunks != 1 {
			t.Fatalf("reapply file status mismatch: %#v", out.Files)
		}
		if got := readFileString(t, path); got != "A\nX\nC\n" {
			t.Fatalf("reapply should not change file: got %q", got)
		}
	})

	t.Run("fuzzy_trimmed_match_works_and_strict_fails", func(t *testing.T) {
		path := writeTextFile(t, dir, "trimmed.txt", "A\n  B  \nC\n")
		diff := makePatchText(
			"--- a/trimmed.txt",
			"+++ b/trimmed.txt",
			"@@ -1,3 +1,3 @@",
			" A",
			"-B",
			"+X",
			" C",
		)

		out, err := tt.ApplyUnifiedDiff(t.Context(), ApplyUnifiedDiffArgs{DiffText: diff})
		mustNoErr(t, err)
		if out.Status != ApplyUnifiedDiffStatusApplied {
			t.Fatalf("fuzzy apply status mismatch: %s", out.Status)
		}
		if got := readFileString(t, path); got != "A\nX\nC\n" {
			t.Fatalf("fuzzy apply content mismatch: got %q", got)
		}

		strictPath := writeTextFile(t, dir, "strict.txt", "A\n  B  \nC\n")
		strictDiff := makePatchText(
			"--- a/strict.txt",
			"+++ b/strict.txt",
			"@@ -1,3 +1,3 @@",
			" A",
			"-B",
			"+X",
			" C",
		)
		out, err = tt.ApplyUnifiedDiff(t.Context(), ApplyUnifiedDiffArgs{DiffText: strictDiff, Strict: true})
		mustNoErr(t, err)
		if out.Status != ApplyUnifiedDiffStatusConflict {
			t.Fatalf("strict apply status mismatch: %s", out.Status)
		}
		if got := readFileString(t, strictPath); got != "A\n  B  \nC\n" {
			t.Fatalf("strict failure must not modify file: got %q", got)
		}
	})

	t.Run("prefers_matching_old_block_over_nearby_new_block", func(t *testing.T) {
		path := writeTextFile(t, dir, "prefer-old.txt", "A\nX\nC\nA\nB\nC\n")
		diff := makePatchText(
			"--- a/prefer-old.txt",
			"+++ b/prefer-old.txt",
			"@@ -4,3 +4,3 @@",
			" A",
			"-B",
			"+X",
			" C",
		)

		out, err := tt.ApplyUnifiedDiff(t.Context(), ApplyUnifiedDiffArgs{DiffText: diff})
		mustNoErr(t, err)
		if out.Status != ApplyUnifiedDiffStatusApplied {
			t.Fatalf("status: want applied got %s files=%#v", out.Status, out.Files)
		}
		if got := readFileString(t, path); got != "A\nX\nC\nA\nX\nC\n" {
			t.Fatalf("old block should have been patched, got %q", got)
		}
	})

	t.Run("crlf_existing_file_keeps_crlf", func(t *testing.T) {
		path := writeTextFile(t, dir, "crlf.txt", "A\r\nB\r\nC\r\n")
		diff := makePatchText(
			"--- a/crlf.txt",
			"+++ b/crlf.txt",
			"@@ -1,3 +1,3 @@",
			" A",
			"-B",
			"+X",
			" C",
		)

		out, err := tt.ApplyUnifiedDiff(t.Context(), ApplyUnifiedDiffArgs{DiffText: diff})
		mustNoErr(t, err)
		if out.Status != ApplyUnifiedDiffStatusApplied {
			t.Fatalf("status: want applied got %s", out.Status)
		}
		if got := readFileString(t, path); got != "A\r\nX\r\nC\r\n" {
			t.Fatalf("CRLF should be preserved, got %q", got)
		}
	})
	t.Run("create_patch_creates_file_with_no_newline_marker_and_is_idempotent", func(t *testing.T) {
		path := filepath.Join(dir, "created.txt")
		diff := makePatchText(
			"diff --git a/created.txt b/created.txt",
			"new file mode 100644",
			"--- /dev/null",
			"+++ b/created.txt",
			"@@ -0,0 +1,1 @@",
			"+created",
			"\\ No newline at end of file",
		)

		out, err := tt.ApplyUnifiedDiff(t.Context(), ApplyUnifiedDiffArgs{DiffText: diff})
		mustNoErr(t, err)
		if out.Status != ApplyUnifiedDiffStatusApplied {
			t.Fatalf("create status mismatch: %s", out.Status)
		}
		if got := readFileString(t, path); got != "created" {
			t.Fatalf("created content mismatch: got %q", got)
		}

		out, err = tt.ApplyUnifiedDiff(t.Context(), ApplyUnifiedDiffArgs{DiffText: diff})
		mustNoErr(t, err)
		if out.Status != ApplyUnifiedDiffStatusAlreadyApplied {
			t.Fatalf("create reapply status mismatch: %s", out.Status)
		}
		if got := readFileString(t, path); got != "created" {
			t.Fatalf("create reapply should not change file: got %q", got)
		}
	})

	t.Run("llm_add_only_missing_file_without_new_file_mode_creates_file", func(t *testing.T) {
		path := filepath.Join(dir, "llm-add-only.txt")
		diff := makePatchText(
			"diff --git a/llm-add-only.txt b/llm-add-only.txt",
			"@@ -0,0 +1,2 @@",
			"+hello",
			"+world",
		)

		out, err := tt.ApplyUnifiedDiff(t.Context(), ApplyUnifiedDiffArgs{DiffText: diff})
		mustNoErr(t, err)
		if out.Status != ApplyUnifiedDiffStatusApplied {
			t.Fatalf("add-only create status mismatch: %s files=%#v", out.Status, out.Files)
		}
		if got := readFileString(t, path); got != "hello\nworld\n" {
			t.Fatalf("add-only created content mismatch: got %q", got)
		}
	})

	t.Run("create_patch_dry_run_rejects_too_many_new_parent_dirs", func(t *testing.T) {
		rel := "deep-new/a/b/c/d/e/f/g/h/i/too-deep.txt"
		diff := makePatchText(
			"diff --git a/"+rel+" b/"+rel,
			"--- /dev/null",
			"+++ b/"+rel,
			"@@ -0,0 +1,1 @@",
			"+x",
		)

		out, err := tt.ApplyUnifiedDiff(t.Context(), ApplyUnifiedDiffArgs{DiffText: diff, DryRun: true})
		mustNoErr(t, err)
		if out.Status != ApplyUnifiedDiffStatusError {
			t.Fatalf("deep create dry-run status: want error got %s files=%#v", out.Status, out.Files)
		}
		if _, err := os.Stat(filepath.Join(dir, "deep-new")); !os.IsNotExist(err) {
			t.Fatalf("dry-run must not create parent dirs, stat err=%v", err)
		}
	})

	t.Run("create_patch_conflicts_when_target_already_exists_with_different_content", func(t *testing.T) {
		path := writeTextFile(t, dir, "create-conflict.txt", "different\n")
		diff := makePatchText(
			"diff --git a/create-conflict.txt b/create-conflict.txt",
			"new file mode 100644",
			"--- /dev/null",
			"+++ b/create-conflict.txt",
			"@@ -0,0 +1,1 @@",
			"+created",
			"\\ No newline at end of file",
		)

		out, err := tt.ApplyUnifiedDiff(t.Context(), ApplyUnifiedDiffArgs{DiffText: diff})
		mustNoErr(t, err)
		if out.Status != ApplyUnifiedDiffStatusConflict {
			t.Fatalf("create conflict status mismatch: %s", out.Status)
		}
		if got := readFileString(t, path); got != "different\n" {
			t.Fatalf("create conflict must not modify file: got %q", got)
		}
	})

	t.Run("already_applied_content_with_new_mode_still_updates_mode", func(t *testing.T) {
		if runtime.GOOS == "windows" {
			t.Skip("windows chmod does not reliably expose unix executable mode bits")
		}
		path := writeTextFile(t, dir, "mode-already.txt", "A\nX\n")
		if err := os.Chmod(path, 0o644); err != nil {
			t.Fatalf("chmod setup: %v", err)
		}
		diff := makePatchText(
			"diff --git a/mode-already.txt b/mode-already.txt",
			"old mode 100644",
			"new mode 100755",
			"--- a/mode-already.txt",
			"+++ b/mode-already.txt",
			"@@ -1,2 +1,2 @@",
			" A",
			"-B",
			"+X",
		)
		out, err := tt.ApplyUnifiedDiff(t.Context(), ApplyUnifiedDiffArgs{DiffText: diff})
		mustNoErr(t, err)
		if out.Status != ApplyUnifiedDiffStatusApplied {
			t.Fatalf("mode update status: want applied got %s files=%#v", out.Status, out.Files)
		}
		if got := readFileString(t, path); got != "A\nX\n" {
			t.Fatalf("mode update should not change content, got %q", got)
		}
		st, err := os.Stat(path)
		mustNoErr(t, err)
		if st.Mode().Perm() != 0o755 {
			t.Fatalf("mode update mismatch: want 0755 got %#o", st.Mode().Perm())
		}
	})
	t.Run("delete_patch_deletes_file_and_is_idempotent", func(t *testing.T) {
		path := writeTextFile(t, dir, "delete.txt", "gone\n")
		diff := makePatchText(
			"diff --git a/delete.txt b/delete.txt",
			"deleted file mode 100644",
			"--- a/delete.txt",
			"+++ /dev/null",
			"@@ -1,1 +0,0 @@",
			"-gone",
		)

		out, err := tt.ApplyUnifiedDiff(t.Context(), ApplyUnifiedDiffArgs{DiffText: diff})
		mustNoErr(t, err)
		if out.Status != ApplyUnifiedDiffStatusApplied {
			t.Fatalf("delete status mismatch: %s", out.Status)
		}
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("delete should remove file, stat err=%v", err)
		}

		_, err = tt.ApplyUnifiedDiff(t.Context(), ApplyUnifiedDiffArgs{DiffText: diff})
		mustNoErr(t, err)

		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("delete reapply should keep file absent, stat err=%v", err)
		}
	})

	t.Run("delete_patch_conflicts_when_content_remains", func(t *testing.T) {
		path := writeTextFile(t, dir, "delete-conflict.txt", "A\nB\n")
		diff := makePatchText(
			"diff --git a/delete-conflict.txt b/delete-conflict.txt",
			"deleted file mode 100644",
			"--- a/delete-conflict.txt",
			"+++ /dev/null",
			"@@ -1,1 +0,0 @@",
			"-A",
		)

		out, err := tt.ApplyUnifiedDiff(t.Context(), ApplyUnifiedDiffArgs{DiffText: diff})
		mustNoErr(t, err)
		if out.Status != ApplyUnifiedDiffStatusConflict {
			t.Fatalf("delete conflict status mismatch: %s", out.Status)
		}
		if got := readFileString(t, path); got != "A\nB\n" {
			t.Fatalf("delete conflict must not modify file: got %q", got)
		}
	})

	t.Run("explicit_file_target_by_fileKey", func(t *testing.T) {
		path := writeTextFile(t, dir, "actual.txt", "A\nB\n")
		diff := makePatchText(
			"diff --git a/ghost/actual.txt b/ghost/actual.txt",
			"@@ -1,2 +1,2 @@",
			" A",
			"-B",
			"+X",
		)

		out, err := tt.ApplyUnifiedDiff(t.Context(), ApplyUnifiedDiffArgs{
			DiffText: diff,
			FileTargets: []ApplyUnifiedDiffFileTarget{{
				FileKey:    "file-1",
				TargetPath: "actual.txt",
			}},
		})
		mustNoErr(t, err)
		if out.Status != ApplyUnifiedDiffStatusApplied {
			t.Fatalf("explicit target status mismatch: %s", out.Status)
		}
		if got := readFileString(t, path); got != "A\nX\n" {
			t.Fatalf("explicit target content mismatch: got %q", got)
		}
		if len(out.FileTargets) != 1 || out.FileTargets[0].TargetPath != "actual.txt" ||
			out.FileTargets[0].FileKey != "file-1" {
			t.Fatalf("explicit target file targets mismatch: %#v", out.FileTargets)
		}
	})

	t.Run("missing_target_without_candidates_returns_needs_info", func(t *testing.T) {
		diff := makePatchText(
			"diff --git a/ghost/missing.txt b/ghost/missing.txt",
			"@@ -1,2 +1,2 @@",
			" A",
			"-B",
			"+X",
		)

		out, err := tt.ApplyUnifiedDiff(t.Context(), ApplyUnifiedDiffArgs{DiffText: diff})
		mustNoErr(t, err)
		if out.Status != ApplyUnifiedDiffStatusNeedsInfo {
			t.Fatalf("missing target status mismatch: %s", out.Status)
		}
		if len(out.Files) != 1 || out.Files[0].Status != ApplyUnifiedDiffStatusNeedsInfo {
			t.Fatalf("missing target file status mismatch: %#v", out.Files)
		}
	})

	t.Run("single_candidate_fallback_resolves_when_diff_path_is_unknown", func(t *testing.T) {
		path := writeTextFile(t, dir, "fallback.txt", "A\nB\n")
		diff := makePatchText(
			"diff --git a/ghost/dir/unmatched.txt b/ghost/dir/unmatched.txt",
			"@@ -1,2 +1,2 @@",
			" A",
			"-B",
			"+X",
		)

		out, err := tt.ApplyUnifiedDiff(t.Context(), ApplyUnifiedDiffArgs{
			DiffText:       diff,
			CandidatePaths: []string{"fallback.txt"},
		})
		mustNoErr(t, err)
		if out.Status != ApplyUnifiedDiffStatusApplied {
			t.Fatalf("single-candidate fallback status mismatch: %s", out.Status)
		}
		if got := readFileString(t, path); got != "A\nX\n" {
			t.Fatalf("single-candidate fallback content mismatch: got %q", got)
		}
		if len(out.Files) != 1 || out.Files[0].TargetPath != "fallback.txt" {
			t.Fatalf("single-candidate fallback target mismatch: %#v", out.Files)
		}
	})

	t.Run("candidate_paths_resolve_by_basename", func(t *testing.T) {
		path := writeTextFile(t, dir, "nested/target.txt", "A\nB\n")
		diff := makePatchText(
			"diff --git a/ghost/dir/target.txt b/ghost/dir/target.txt",
			"@@ -1,2 +1,2 @@",
			" A",
			"-B",
			"+X",
		)

		out, err := tt.ApplyUnifiedDiff(t.Context(), ApplyUnifiedDiffArgs{
			DiffText:       diff,
			CandidatePaths: []string{"nested/target.txt"},
		})
		mustNoErr(t, err)
		if out.Status != ApplyUnifiedDiffStatusApplied {
			t.Fatalf("candidate basename status mismatch: %s", out.Status)
		}
		if got := readFileString(t, path); got != "A\nX\n" {
			t.Fatalf("candidate basename content mismatch: got %q", got)
		}
		if len(out.Files) != 1 || out.Files[0].TargetPath != "nested/target.txt" {
			t.Fatalf("candidate basename target mismatch: %#v", out.Files)
		}
	})

	t.Run("ambiguous_candidate_paths_return_needs_info", func(t *testing.T) {
		writeTextFile(t, dir, "one/dup.txt", "A\nB\n")
		writeTextFile(t, dir, "two/dup.txt", "A\nB\n")
		diff := makePatchText(
			"diff --git a/ghost/dir/dup.txt b/ghost/dir/dup.txt",
			"@@ -1,2 +1,2 @@",
			" A",
			"-B",
			"+X",
		)

		out, err := tt.ApplyUnifiedDiff(t.Context(), ApplyUnifiedDiffArgs{
			DiffText:       diff,
			CandidatePaths: []string{"one/dup.txt", "two/dup.txt"},
		})
		mustNoErr(t, err)
		if out.Status != ApplyUnifiedDiffStatusNeedsInfo {
			t.Fatalf("ambiguous candidates status mismatch: %s", out.Status)
		}
		if len(out.Files) != 1 || out.Files[0].Status != ApplyUnifiedDiffStatusNeedsInfo {
			t.Fatalf("ambiguous candidates file status mismatch: %#v", out.Files)
		}
	})

	t.Run("duplicate_file_sections_for_same_path_are_coalesced", func(t *testing.T) {
		path := writeTextFile(t, dir, "duplicate-sections.txt", "A\nB\nC\nD\n")
		diff := makePatchText(
			"diff --git a/duplicate-sections.txt b/duplicate-sections.txt",
			"@@ -1,2 +1,2 @@",
			" A",
			"-B",
			"+X",
			"diff --git a/duplicate-sections.txt b/duplicate-sections.txt",
			"@@ -3,2 +3,2 @@",
			" C",
			"-D",
			"+Y",
		)

		out, err := tt.ApplyUnifiedDiff(t.Context(), ApplyUnifiedDiffArgs{DiffText: diff})
		mustNoErr(t, err)
		if out.Status != ApplyUnifiedDiffStatusApplied {
			t.Fatalf("duplicate section status mismatch: %s files=%#v", out.Status, out.Files)
		}
		if len(out.Files) != 1 || out.Files[0].Hunks != 2 {
			t.Fatalf("duplicate sections should be coalesced into one two-hunk file: %#v", out.Files)
		}
		if got := readFileString(t, path); got != "A\nX\nC\nY\n" {
			t.Fatalf("duplicate section content mismatch: got %q", got)
		}
	})

	t.Run("duplicate_mutating_targets_conflict", func(t *testing.T) {
		path := writeTextFile(t, dir, "shared.txt", "A\nB\nC\n")
		diff := makePatchText(
			"diff --git a/one.txt b/one.txt",
			"@@ -1,3 +1,3 @@",
			" A",
			"-B",
			"+X",
			" C",
			"diff --git a/two.txt b/two.txt",
			"@@ -1,3 +1,3 @@",
			" A",
			"-B",
			"+Y",
			" C",
		)

		out, err := tt.ApplyUnifiedDiff(t.Context(), ApplyUnifiedDiffArgs{
			DiffText: diff,
			FileTargets: []ApplyUnifiedDiffFileTarget{
				{FileKey: "file-1", TargetPath: "shared.txt"},
				{FileKey: "file-2", TargetPath: "shared.txt"},
			},
		})
		mustNoErr(t, err)
		if out.Status != ApplyUnifiedDiffStatusConflict {
			t.Fatalf("duplicate target status mismatch: %s", out.Status)
		}
		if got := readFileString(t, path); got != "A\nB\nC\n" {
			t.Fatalf("duplicate target conflict must not modify file: got %q", got)
		}
		if len(out.Files) != 2 || out.Files[0].Status != ApplyUnifiedDiffStatusConflict ||
			out.Files[1].Status != ApplyUnifiedDiffStatusConflict {
			t.Fatalf("duplicate target file statuses mismatch: %#v", out.Files)
		}
	})

	t.Run("binary_patch_is_reported_conflict", func(t *testing.T) {
		diff := makePatchText(
			"diff --git a/bin.dat b/bin.dat",
			"Binary files a/bin.dat and b/bin.dat differ",
		)

		out, err := tt.ApplyUnifiedDiff(t.Context(), ApplyUnifiedDiffArgs{DiffText: diff})
		mustNoErr(t, err)
		if out.Status != ApplyUnifiedDiffStatusConflict {
			t.Fatalf("binary patch status mismatch: %s files=%#v", out.Status, out.Files)
		}
		if len(out.Files) != 1 || out.Files[0].Status != ApplyUnifiedDiffStatusConflict {
			t.Fatalf("binary patch file status mismatch: %#v", out.Files)
		}
	})

	t.Run("rename_and_copy_patches_are_rejected", func(t *testing.T) {
		diff := makePatchText(
			"diff --git a/old.txt b/new.txt",
			"rename from old.txt",
			"rename to new.txt",
			"@@ -1,1 +1,1 @@",
			"-A",
			"+B",
			"diff --git a/src.txt b/copy.txt",
			"copy from src.txt",
			"copy to copy.txt",
			"@@ -1,1 +1,1 @@",
			"-C",
			"+D",
		)

		out, err := tt.ApplyUnifiedDiff(t.Context(), ApplyUnifiedDiffArgs{DiffText: diff})
		mustNoErr(t, err)
		if out.Status != ApplyUnifiedDiffStatusConflict {
			t.Fatalf("rename/copy status mismatch: %s", out.Status)
		}
		if len(out.Files) != 2 || out.Files[0].Status != ApplyUnifiedDiffStatusConflict ||
			out.Files[1].Status != ApplyUnifiedDiffStatusConflict {
			t.Fatalf("rename/copy file statuses mismatch: %#v", out.Files)
		}
	})

	t.Run("unrecognized_text_returns_needs_info", func(t *testing.T) {
		out, err := tt.ApplyUnifiedDiff(t.Context(), ApplyUnifiedDiffArgs{DiffText: "this is not a patch\n"})
		mustNoErr(t, err)
		if out.Status != ApplyUnifiedDiffStatusNeedsInfo || len(out.Diagnostics) == 0 {
			t.Fatalf("unrecognized text status/diagnostics mismatch: status=%s diags=%v", out.Status, out.Diagnostics)
		}
	})

	t.Run("metadata_only_single_file_returns_already_applied_noop", func(t *testing.T) {
		diff := makePatchText(
			"diff --git a/empty.txt b/empty.txt",
			"--- a/empty.txt",
			"+++ b/empty.txt",
		)

		out, err := tt.ApplyUnifiedDiff(t.Context(), ApplyUnifiedDiffArgs{DiffText: diff})
		mustNoErr(t, err)
		if out.Status != ApplyUnifiedDiffStatusAlreadyApplied ||
			len(out.Files) != 1 ||
			out.Files[0].Status != ApplyUnifiedDiffStatusAlreadyApplied {
			t.Fatalf("metadata-only single-file mismatch: status=%s files=%#v", out.Status, out.Files)
		}
	})

	t.Run("metadata_only_file_in_multi_file_diff_is_reported_already_applied", func(t *testing.T) {
		path := writeTextFile(t, dir, "multi.txt", "A\nB\n")
		diff := makePatchText(
			"diff --git a/empty.txt b/empty.txt",
			"--- a/empty.txt",
			"+++ b/empty.txt",
			"diff --git a/multi.txt b/multi.txt",
			"@@ -1,2 +1,2 @@",
			" A",
			"-B",
			"+X",
		)

		out, err := tt.ApplyUnifiedDiff(t.Context(), ApplyUnifiedDiffArgs{DiffText: diff})
		mustNoErr(t, err)
		if out.Status != ApplyUnifiedDiffStatusApplied || len(out.Files) != 2 {
			t.Fatalf("metadata-only multi-file mismatch: status=%s files=%#v", out.Status, out.Files)
		}
		if out.Files[0].Status != ApplyUnifiedDiffStatusAlreadyApplied ||
			out.Files[1].Status != ApplyUnifiedDiffStatusApplied {
			t.Fatalf("metadata-only multi-file file statuses mismatch: %#v", out.Files)
		}
		if got := readFileString(t, path); got != "A\nX\n" {
			t.Fatalf("metadata-only multi-file content mismatch: got %q", got)
		}
	})
}
