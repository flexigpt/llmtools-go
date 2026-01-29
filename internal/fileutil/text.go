package fileutil

import (
	"fmt"
	"strings"
)

// NormalizeLineBlockInput makes tool line-block arguments more forgiving.
//
// Behavior:
//   - Treats embedded CRLF/CR/LF in items as line breaks (splits into multiple lines).
//   - Trims trailing newline characters from each item to avoid accidental extra empty lines.
//   - Preserves intentional empty lines ("" remains a single empty line).
//
// This helps when callers (especially LLMs) accidentally include newline characters in JSON strings.
func NormalizeLineBlockInput(in []string) []string {
	if in == nil {
		return nil
	}

	out := make([]string, 0, len(in))
	for _, s := range in {
		s = strings.ReplaceAll(s, "\r\n", "\n")
		s = strings.ReplaceAll(s, "\r", "\n")
		// Common accidental case: "line\n" as an item. We treat it as "line".
		s = strings.TrimRight(s, "\n")

		parts := strings.Split(s, "\n")
		out = append(out, parts...)
	}
	return out
}

// FindTrimmedBlockMatches returns all start indices i where `block` matches `lines`
// when comparing strings.TrimSpace(line) line-by-line.
//
// Returns indices in ascending order.
func FindTrimmedBlockMatches(lines, block []string) []int {
	if len(block) == 0 {
		return nil
	}

	tLines := GetTrimmedLines(lines)
	tBlock := GetTrimmedLines(block)

	var idxs []int
	for i := 0; i+len(tBlock) <= len(tLines); i++ {
		if IsBlockEqualsAt(tLines, tBlock, i) {
			idxs = append(idxs, i)
		}
	}
	return idxs
}

// FindTrimmedAdjacentBlockMatches finds indices i where:
//
//	(before matches immediately before i, if provided) AND
//	(match matches at i) AND
//	(after matches immediately after the match block, if provided)
//
// Comparison is done on trimmed lines.
func FindTrimmedAdjacentBlockMatches(lines, before, match, after []string) []int {
	if len(match) == 0 {
		return nil
	}

	tLines := GetTrimmedLines(lines)
	tBefore := GetTrimmedLines(before)
	tMatch := GetTrimmedLines(match)
	tAfter := GetTrimmedLines(after)

	var idxs []int
	for i := 0; i+len(tMatch) <= len(tLines); i++ {
		if !IsBlockEqualsAt(tLines, tMatch, i) {
			continue
		}

		// Before must be immediately adjacent.
		if len(tBefore) > 0 {
			if i-len(tBefore) < 0 {
				continue
			}
			if !IsBlockEqualsAt(tLines, tBefore, i-len(tBefore)) {
				continue
			}
		}

		// After must be immediately adjacent.
		if len(tAfter) > 0 {
			afterStart := i + len(tMatch)
			if afterStart+len(tAfter) > len(tLines) {
				continue
			}
			if !IsBlockEqualsAt(tLines, tAfter, afterStart) {
				continue
			}
		}

		idxs = append(idxs, i)
	}

	return idxs
}

// EnsureNonOverlappingFixedWidth ensures matches do not overlap.
// Matches must be sorted ascending (as produced by Find* helpers).
func EnsureNonOverlappingFixedWidth(matchIdxs []int, width int) error {
	if len(matchIdxs) <= 1 || width <= 0 {
		return nil
	}
	for i := 0; i < len(matchIdxs)-1; i++ {
		if matchIdxs[i]+width > matchIdxs[i+1] {
			return fmt.Errorf(
				"overlapping matches detected at line indices %d and %d; provide tighter beforeLines/afterLines to disambiguate",
				matchIdxs[i],
				matchIdxs[i+1],
			)
		}
	}
	return nil
}

// RequireSingleMatch enforces that idxs contains exactly one match index.
// This is useful for “anchor must be unique” tool semantics.
func RequireSingleMatch(idxs []int, name string) (int, error) {
	if len(idxs) == 0 {
		return 0, fmt.Errorf("no match found for %s", name)
	}
	if len(idxs) > 1 {
		return 0, fmt.Errorf(
			"ambiguous match for %s: found %d occurrences; provide a more specific match",
			name,
			len(idxs),
		)
	}
	return idxs[0], nil
}

// RequireSingleTrimmedBlockMatch finds trimmed-equal block matches and requires exactly one.
func RequireSingleTrimmedBlockMatch(lines, block []string, name string) (int, error) {
	return RequireSingleMatch(FindTrimmedBlockMatches(lines, block), name)
}

func GetTrimmedLines(lines []string) []string {
	if len(lines) == 0 {
		return nil
	}
	out := make([]string, len(lines))
	for i := range lines {
		out[i] = strings.TrimSpace(lines[i])
	}
	return out
}

func IsBlockEqualsAt(haystack, needle []string, start int) bool {
	for j := range needle {
		if haystack[start+j] != needle[j] {
			return false
		}
	}
	return true
}
