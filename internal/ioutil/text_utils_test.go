package ioutil

import (
	"strings"
	"testing"
)

const (
	textUtilsTestMatchLine = "match"
	textUtilsTestAfterLine = "after"
)

func TestNormalizeLineBlockInput(t *testing.T) {
	tests := []struct {
		name string
		in   []string
		want []string
	}{
		{
			name: "nil stays nil",
			in:   nil,
			want: nil,
		},
		{
			name: "empty slice stays empty",
			in:   []string{},
			want: []string{},
		},
		{
			name: "splits embedded newlines and trims trailing newline",
			in:   []string{"a\nb\n", "c"},
			want: []string{"a", "b", "c"},
		},
		{
			name: "handles CRLF and CR",
			in:   []string{"a\r\nb\r\n", "c\rd"},
			want: []string{"a", "b", "c", "d"},
		},
		{
			name: "preserves intentional empty lines",
			in:   []string{"a\n\nb"},
			want: []string{"a", "", "b"},
		},
		{
			name: "empty string remains one empty line",
			in:   []string{""},
			want: []string{""},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := NormalizeLineBlockInput(tc.in)
			if !equalStringSlices(got, tc.want) {
				t.Fatalf("got=%#v want=%#v", got, tc.want)
			}
		})
	}
}

func TestFindTrimmedBlockMatches(t *testing.T) {
	tests := []struct {
		name  string
		lines []string
		block []string
		want  []int
	}{
		{
			name:  "empty block yields nil",
			lines: []string{"a"},
			block: nil,
			want:  nil,
		},
		{
			name:  "single match with whitespace differences",
			lines: []string{"  a  ", "b", "c"},
			block: []string{"a", " b "},
			want:  []int{0},
		},
		{
			name:  "multiple matches",
			lines: []string{" a ", "b", "x", "a", " b "},
			block: []string{"a", "b"},
			want:  []int{0, 3},
		},
		{
			name:  "no matches",
			lines: []string{"a", "b"},
			block: []string{"b", "a"},
			want:  nil,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := FindTrimmedBlockMatches(tc.lines, tc.block)
			if !equalIntSlices(got, tc.want) {
				t.Fatalf("got=%v want=%v", got, tc.want)
			}
		})
	}
}

func TestFindTrimmedAdjacentBlockMatches(t *testing.T) {
	lines := []string{
		"HEADER",
		" before ",
		textUtilsTestMatchLine,
		" " + textUtilsTestAfterLine + " ",
		textUtilsTestMatchLine,
		textUtilsTestAfterLine,
	}

	tests := []struct {
		name   string
		before []string
		match  []string
		after  []string
		want   []int
	}{
		{
			name:   "match only",
			match:  []string{textUtilsTestMatchLine},
			before: nil,
			after:  nil,
			want:   []int{2, 4},
		},
		{
			name:   "before+match+after must be adjacent",
			before: []string{"before"},
			match:  []string{textUtilsTestMatchLine},
			after:  []string{textUtilsTestAfterLine},
			want:   []int{2},
		},
		{
			name:   "before required but not present => none",
			before: []string{"nope"},
			match:  []string{textUtilsTestMatchLine},
			after:  []string{textUtilsTestAfterLine},
			want:   nil,
		},
		{
			name:   "empty match => nil",
			before: []string{"before"},
			match:  nil,
			after:  []string{textUtilsTestAfterLine},
			want:   nil,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := FindTrimmedAdjacentBlockMatches(lines, tc.before, tc.match, tc.after)
			if !equalIntSlices(got, tc.want) {
				t.Fatalf("got=%v want=%v", got, tc.want)
			}
		})
	}
}

func TestEnsureNonOverlappingFixedWidth(t *testing.T) {
	tests := []struct {
		name     string
		idxs     []int
		width    int
		wantErr  bool
		contains string
	}{
		{"len<=1 ok", []int{5}, 2, false, ""},
		{"width<=0 ok", []int{1, 2}, 0, false, ""},
		{"non-overlapping ok", []int{0, 3, 6}, 3, false, ""},
		{"overlapping errors", []int{0, 2}, 3, true, "overlapping matches"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := EnsureNonOverlappingFixedWidth(tc.idxs, tc.width)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error, got nil")
				}
				if tc.contains != "" && !strings.Contains(err.Error(), tc.contains) {
					t.Fatalf("error %q does not contain %q", err.Error(), tc.contains)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

func TestGetTrimmedLines(t *testing.T) {
	tests := []struct {
		name string
		in   []string
		want []string
	}{
		{"nil => nil", nil, nil},
		{"empty => nil", []string{}, nil},
		{"trims all", []string{" a ", "\tb\t", "\n"}, []string{"a", "b", ""}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := GetTrimmedLines(tc.in)
			if !equalStringSlices(got, tc.want) {
				t.Fatalf("got=%#v want=%#v", got, tc.want)
			}
		})
	}
}

func TestIsBlockEqualsAt(t *testing.T) {
	h := []string{"a", "b", "c", "d"}
	tests := []struct {
		name   string
		needle []string
		start  int
		want   bool
	}{
		{"match at 1", []string{"b", "c"}, 1, true},
		{"mismatch at 0", []string{"b"}, 0, false},
		{"match single", []string{"d"}, 3, true},
		{"out of range start returns false (no panic)", []string{"c"}, 99, false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := IsBlockEqualsAt(h, tc.needle, tc.start)
			if got != tc.want {
				t.Fatalf("got=%v want=%v", got, tc.want)
			}
		})
	}
}
