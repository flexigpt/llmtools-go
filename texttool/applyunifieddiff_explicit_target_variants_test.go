package texttool

import "testing"

func TestApplyUnifiedDiffExplicitTargetPathVariants(t *testing.T) {
	file := parsedPatchFile{FileKey: "file-1", OldPath: "old.txt", NewPath: "new.txt"}

	tests := []struct {
		name   string
		target ApplyUnifiedDiffFileTarget
	}{
		{
			name:   "new_path_only_matches_new_path",
			target: ApplyUnifiedDiffFileTarget{NewPath: "new.txt", TargetPath: "actual.txt"},
		},
		{
			name:   "old_path_only_matches_old_path",
			target: ApplyUnifiedDiffFileTarget{OldPath: "old.txt", TargetPath: "actual.txt"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok, err := findExplicitTarget(file, []ApplyUnifiedDiffFileTarget{tt.target})
			mustNoErr(t, err)
			if !ok || got.TargetPath != "actual.txt" {
				t.Fatalf("explicit path variant match failed: %#v ok=%v", got, ok)
			}
		})
	}
}
