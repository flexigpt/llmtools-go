package ioutil

import (
	"fmt"
	"strings"
)

// NewlineKind describes the newline convention detected in a file.
type NewlineKind string

const (
	NewlineLF   NewlineKind = "lf"
	NewlineCRLF NewlineKind = "crlf"
)

func (n NewlineKind) sep() string {
	if n == NewlineCRLF {
		return "\r\n"
	}
	return "\n"
}

func NormalizeLineBlockInput(in []string) []string {
	if in == nil {
		return nil
	}

	out := make([]string, 0, len(in))
	for _, s := range in {
		s = strings.ReplaceAll(s, "\r\n", "\n")
		s = strings.ReplaceAll(s, "\r", "\n")
		s = strings.TrimRight(s, "\n")

		parts := strings.Split(s, "\n")
		out = append(out, parts...)
	}
	return out
}

func RequireSingleTrimmedBlockMatch(lines, block []string, name string) (int, error) {
	idxs := FindTrimmedBlockMatches(lines, block)
	if len(idxs) == 0 {
		return 0, fmt.Errorf(
			"no match found for %s; suggestion=copy a longer distinctive block from the file or read nearby lines first",
			name,
		)
	}
	if len(idxs) > 1 {
		return 0, fmt.Errorf(
			"ambiguous match for %s: found %d occurrences. diagnostics=%s suggestion=copy a longer distinctive block from the file",
			name,
			len(idxs),
			BuildBlockMatchDiagnosticJSON(lines, idxs, len(block), nil, 5, 1),
		)
	}
	return idxs[0], nil
}

func RequireSingleMatch(idxs []int, name string) (int, error) {
	if len(idxs) == 0 {
		return 0, fmt.Errorf("no match found for %s", name)
	}
	if len(idxs) > 1 {
		return 0, fmt.Errorf(
			"ambiguous match for %s: found %d occurrences at start lines %v; provide a more specific match",
			name,
			len(idxs),
			OneBasedLineNumbers(idxs),
		)
	}
	return idxs[0], nil
}

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

		if len(tBefore) > 0 {
			if i-len(tBefore) < 0 {
				continue
			}
			if !IsBlockEqualsAt(tLines, tBefore, i-len(tBefore)) {
				continue
			}
		}

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

func EnsureNonOverlappingFixedWidth(matchIdxs []int, width int) error {
	if len(matchIdxs) <= 1 || width <= 0 {
		return nil
	}
	for i := 0; i < len(matchIdxs)-1; i++ {
		if matchIdxs[i]+width > matchIdxs[i+1] {
			return fmt.Errorf(
				"overlapping matches detected at start lines %d and %d; provide tighter surrounding context to disambiguate",
				matchIdxs[i]+1,
				matchIdxs[i+1]+1,
			)
		}
	}
	return nil
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
	if start < 0 {
		return false
	}
	if len(needle) == 0 {
		return true
	}
	if start+len(needle) > len(haystack) {
		return false
	}
	for j := range needle {
		if haystack[start+j] != needle[j] {
			return false
		}
	}
	return true
}
