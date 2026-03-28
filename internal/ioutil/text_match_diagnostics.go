package ioutil

import (
	"encoding/json"
	"fmt"
)

type MaybeStartLineDiagnostic struct {
	RequestedLine     int   `json:"requestedLine"`
	Tolerance         int   `json:"tolerance"`
	ClosestDistance   int   `json:"closestDistance"`
	ClosestStartLines []int `json:"closestStartLines,omitempty"`
	Applied           bool  `json:"applied"`
}

type blockMatchCandidateDiagnostic struct {
	StartLine   int      `json:"startLine"`
	EndLine     int      `json:"endLine"`
	BeforeLines []string `json:"beforeLines,omitempty"`
	MatchLines  []string `json:"matchLines"`
	AfterLines  []string `json:"afterLines,omitempty"`
}

type blockMatchDiagnostic struct {
	CandidateStartLines         []int                           `json:"candidateStartLines,omitempty"`
	AdditionalCandidatesOmitted int                             `json:"additionalCandidatesOmitted,omitempty"`
	SampleCandidates            []blockMatchCandidateDiagnostic `json:"sampleCandidates,omitempty"`
	MaybeStartLine              *MaybeStartLineDiagnostic       `json:"maybeStartLine,omitempty"`
}

func OneBasedLineNumbers(idxs []int) []int {
	if len(idxs) == 0 {
		return nil
	}
	out := make([]int, len(idxs))
	for i, idx := range idxs {
		out[i] = idx + 1
	}
	return out
}

// NarrowMatchIndicesByMaybeStartLine applies a soft start-line hint.
//
// Behavior:
//   - if maybeStartLine is nil or invalid, no narrowing is applied
//   - if there is a unique closest candidate within tolerance, returns only that candidate
//   - otherwise returns the original candidates and emits diagnostics explaining the closest set
func NarrowMatchIndicesByMaybeStartLine(
	idxs []int,
	maybeStartLine *int,
	tolerance int,
) ([]int, *MaybeStartLineDiagnostic) {
	if len(idxs) == 0 || maybeStartLine == nil || *maybeStartLine <= 0 {
		return idxs, nil
	}
	if tolerance < 0 {
		tolerance = 0
	}

	hint := *maybeStartLine
	minDist := -1
	closest := make([]int, 0, 1)

	for _, idx := range idxs {
		dist := idx + 1 - hint
		if dist < 0 {
			dist = -dist
		}
		if minDist == -1 || dist < minDist {
			minDist = dist
			closest = closest[:0]
			closest = append(closest, idx)
			continue
		}
		if dist == minDist {
			closest = append(closest, idx)
		}
	}

	diag := &MaybeStartLineDiagnostic{
		RequestedLine:     hint,
		Tolerance:         tolerance,
		ClosestDistance:   minDist,
		ClosestStartLines: OneBasedLineNumbers(closest),
	}

	if len(closest) == 1 && minDist <= tolerance {
		diag.Applied = true
		return []int{closest[0]}, diag
	}

	return idxs, diag
}

func BuildBlockMatchDiagnosticJSON(
	lines []string,
	matchIdxs []int,
	matchWidth int,
	hint *MaybeStartLineDiagnostic,
	maxCandidates int,
	contextLines int,
) string {
	diag := blockMatchDiagnostic{
		CandidateStartLines: OneBasedLineNumbers(matchIdxs),
		MaybeStartLine:      hint,
	}

	if maxCandidates < 0 {
		maxCandidates = 0
	}
	if contextLines < 0 {
		contextLines = 0
	}
	if matchWidth < 1 {
		matchWidth = 1
	}

	limit := len(matchIdxs)
	if maxCandidates > 0 && limit > maxCandidates {
		limit = maxCandidates
		diag.AdditionalCandidatesOmitted = len(matchIdxs) - maxCandidates
	}

	if limit > 0 && len(lines) > 0 {
		diag.SampleCandidates = make([]blockMatchCandidateDiagnostic, 0, limit)
		for _, idx := range matchIdxs[:limit] {
			if idx < 0 || idx >= len(lines) {
				continue
			}

			matchEndExclusive := min(idx+matchWidth, len(lines))

			beforeStart := max(idx-contextLines, 0)

			afterEnd := min(matchEndExclusive+contextLines, len(lines))

			diag.SampleCandidates = append(diag.SampleCandidates, blockMatchCandidateDiagnostic{
				StartLine:   idx + 1,
				EndLine:     matchEndExclusive,
				BeforeLines: cloneStringSlice(lines[beforeStart:idx]),
				MatchLines:  cloneStringSlice(lines[idx:matchEndExclusive]),
				AfterLines:  cloneStringSlice(lines[matchEndExclusive:afterEnd]),
			})
		}
	}

	b, err := json.Marshal(diag)
	if err != nil {
		return fmt.Sprintf("candidateStartLines=%v", OneBasedLineNumbers(matchIdxs))
	}
	return string(b)
}

func cloneStringSlice(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	out := make([]string, len(in))
	copy(out, in)
	return out
}
