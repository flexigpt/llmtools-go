package integration

import (
	"path/filepath"
	"testing"

	"github.com/flexigpt/llmtools-go/fstool"
	"github.com/flexigpt/llmtools-go/texttool"
)

// Read/modify loop + disambiguated replacements + insert/delete.

func TestE2E_Text_ReadModifyLoop(t *testing.T) {
	base := t.TempDir()
	h := newHarness(t, base)

	docRel := "doc.md"
	docAbs := filepath.Join(base, docRel)

	// 1) Create a file via writefile (end-to-end).
	initial := "" +
		"# Title\n" +
		"Intro line\n" +
		"\n" +
		"## Section A\n" +
		"<!-- A START -->\n" +
		"TODO: old\n" +
		"<!-- A END -->\n" +
		"\n" +
		"## Section B\n" +
		"<!-- B START -->\n" +
		"TODO: old\n" +
		"<!-- B END -->\n"

	_ = callJSON[fstool.WriteFileOut](t, h.r, "writefile", fstool.WriteFileArgs{
		Path:          docRel,
		Encoding:      "text",
		Content:       initial,
		Overwrite:     false,
		CreateParents: false,
	})

	// 2) Read a bounded range (marker-to-marker) to show how to constrain reads.
	rng := callJSON[texttool.ReadTextRangeOut](t, h.r, "readtextrange", texttool.ReadTextRangeArgs{
		Path:            docRel,
		StartMatchLines: []string{"<!-- B START -->"},
		EndMatchLines:   []string{"<!-- B END -->"},
	})
	if rng.LinesReturned == 0 {
		t.Fatalf("expected some lines in range, got: %s", debugJSON(t, rng))
	}

	// 3) Find occurrences (substring) with context.
	found := callJSON[texttool.FindTextOut](t, h.r, "findtext", texttool.FindTextArgs{
		Path:         docRel,
		QueryType:    "substring",
		Query:        "TODO:",
		ContextLines: 1,
		MaxMatches:   10,
	})
	if found.MatchesReturned < 2 {
		t.Fatalf("expected 2 TODO matches, got: %s", debugJSON(t, found))
	}

	// 4) Replace only the TODO in Section B using beforeLines/afterLines disambiguation.
	one := 1
	_ = callJSON[texttool.ReplaceTextOut](t, h.r, "replacetext", texttool.ReplaceTextArgs{
		Path:          docRel,
		TextAbove:     stringPtr("<!-- B START -->"),
		OldText:       stringPtr("TODO: old"),
		TextBelow:     stringPtr("<!-- B END -->"),
		NewText:       stringPtr("TODO: new"),
		ExpectedCount: &one,
	})

	// 5) Insert after a uniquely-matched anchor.
	_ = callJSON[texttool.InsertTextOut](t, h.r, "inserttext", texttool.InsertTextArgs{
		Path:      docRel,
		Position:  "between",
		TextAbove: stringPtr("<!-- A END -->\n\n"),
		TextBelow: stringPtr("## Section B"),
		Text:      stringPtr("\nInserted after A"),
	})

	// 6) Delete a line block (exact match).
	_ = callJSON[texttool.DeleteTextLinesOut](t, h.r, "deletetextlines", texttool.DeleteTextLinesArgs{
		Path:              docRel,
		MatchLines:        []string{"Intro line"},
		ExpectedDeletions: 1,
	})

	// 7) Verify final content via readfile.
	out := callRaw(t, h.r, "readfile", fstool.ReadFileArgs{
		Path:     docRel,
		Encoding: "text",
	})
	got := requireSingleTextOutput(t, out)

	want := "" +
		"# Title\n" +
		"\n" +
		"## Section A\n" +
		"<!-- A START -->\n" +
		"TODO: old\n" +
		"<!-- A END -->\n" +
		"\n" +
		"Inserted after A\n" +
		"\n" +
		"## Section B\n" +
		"<!-- B START -->\n" +
		"TODO: new\n" +
		"<!-- B END -->\n"

	if got != want {
		t.Fatalf("final doc mismatch\npath=%s\n--- got ---\n%s\n--- want ---\n%s", docAbs, got, want)
	}
}
