package gitdiff

import (
	"strconv"
	"strings"
)

// Hunk is one "@@" section of a diff.
type Hunk struct {
	Text       string // the "@@" line and the lines under it, ending in a newline
	Start, End int    // the first and last line it covers in the new file
}

// normalise drops every line before the first line that starts with "--- ". Those lines carry
// blob hashes and modes that differ between machines. A diff with no such line, which is what git
// prints for a binary file or a pure rename, normalises to "".
func normalise(raw string) string {
	if i := lineStart(raw, "--- "); i >= 0 {
		return raw[i:]
	}
	return ""
}

// lineStart is where the first line of s that starts with prefix begins, or -1 when none does.
func lineStart(s, prefix string) int {
	if strings.HasPrefix(s, prefix) {
		return 0
	}
	if i := strings.Index(s, "\n"+prefix); i >= 0 {
		return i + 1
	}
	return -1
}

// Hunks splits a normalised diff into its header, which is the "---" and "+++" lines, and its hunks.
func Hunks(diff string) (header string, hunks []Hunk) {
	cut := lineStart(diff, "@@ ")
	if cut < 0 {
		return diff, nil
	}
	header, rest := diff[:cut], diff[cut:]
	for rest != "" {
		// A body line always starts with a space, "+", "-" or "\", so "\n@@ " can only be a new hunk.
		end := len(rest)
		if i := strings.Index(rest, "\n@@ "); i >= 0 {
			end = i + 1
		}
		text := rest[:end]
		rest = rest[end:]
		start, last := newRange(text)
		hunks = append(hunks, Hunk{Text: text, Start: start, End: last})
	}
	return header, hunks
}

// newRange reads the "+c,d" part of a hunk's "@@ -a,b +c,d @@" line. A missing count is 1. A hunk
// that adds nothing covers the single line where the deletion sits.
func newRange(text string) (start, end int) {
	line, _, _ := strings.Cut(text, "\n")
	i := strings.Index(line, " +")
	if i < 0 {
		return 0, 0
	}
	spec, _, _ := strings.Cut(line[i+2:], " ")
	first, count, hasCount := strings.Cut(spec, ",")
	start, _ = strconv.Atoi(first)
	n := 1
	if hasCount {
		n, _ = strconv.Atoi(count)
	}
	return start, start + max(n, 1) - 1
}

// Split cuts a normalised diff into pieces of at most limit bytes where it can. Each piece starts
// with the header. Hunks stay whole unless one is larger than the limit on its own.
func Split(diff string, limit int) []string {
	if len(diff) <= limit {
		return []string{diff}
	}
	header, hunks := Hunks(diff)
	if len(hunks) == 0 {
		return []string{diff}
	}
	var pieces []string
	cur := header
	open := false // cur holds at least one hunk
	flush := func() {
		if open {
			pieces = append(pieces, cur)
		}
		cur, open = header, false
	}
	for _, h := range hunks {
		if len(header)+len(h.Text) > limit {
			flush()
			pieces = append(pieces, splitHunk(header, h.Text, limit)...)
			continue
		}
		if open && len(cur)+len(h.Text) > limit {
			flush()
		}
		cur += h.Text
		open = true
	}
	flush()
	return pieces
}

// splitHunk cuts one hunk between lines. Every piece repeats the header and the hunk's "@@" line so
// it reads as a hunk on its own. A line longer than the room left goes in a piece by itself.
func splitHunk(header, text string, limit int) []string {
	ls := strings.SplitAfter(text, "\n")
	if ls[len(ls)-1] == "" {
		ls = ls[:len(ls)-1]
	}
	prefix := header + ls[0]
	var pieces []string
	cur, n := prefix, 0
	for _, l := range ls[1:] {
		if n > 0 && len(cur)+len(l) > limit {
			pieces = append(pieces, cur)
			cur, n = prefix, 0
		}
		cur += l
		n++
	}
	if n > 0 {
		pieces = append(pieces, cur)
	}
	return pieces
}
