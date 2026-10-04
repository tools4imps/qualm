package gitdiff

import (
	"fmt"
	"strings"
	"testing"
)

const hdr = "--- a/f.go\n+++ b/f.go\n"

// Contract: diff/D10
func TestHunksCarryNewFileLineRanges(t *testing.T) {
	diff := hdr +
		"@@ -3,4 +3,5 @@ func x() {\n ctx\n-gone\n+new1\n+new2\n ctx\n ctx\n" +
		"@@ -20 +21 @@\n-old\n+new\n" +
		"@@ -30,2 +32,0 @@\n-del1\n-del2\n" +
		"@@ -40,0 +41,3 @@\n+a\n+b\n+c\n\\ No newline at end of file\n"
	header, hunks := Hunks(diff)
	if header != hdr {
		t.Errorf("header = %q, want %q", header, hdr)
	}
	want := []struct{ start, end int }{{3, 7}, {21, 21}, {32, 32}, {41, 43}}
	if len(hunks) != len(want) {
		t.Fatalf("got %d hunks, want %d", len(hunks), len(want))
	}
	var joined string
	for i, h := range hunks {
		if h.Start != want[i].start || h.End != want[i].end {
			t.Errorf("hunk %d range = %d-%d, want %d-%d", i, h.Start, h.End, want[i].start, want[i].end)
		}
		if !strings.HasPrefix(h.Text, "@@ ") || !strings.HasSuffix(h.Text, "\n") {
			t.Errorf("hunk %d text malformed: %q", i, h.Text)
		}
		joined += h.Text
	}
	if header+joined != diff {
		t.Errorf("header and hunks do not rebuild the diff")
	}
}

func TestHunksOfEmptyDiff(t *testing.T) {
	if h, hs := Hunks(""); h != "" || len(hs) != 0 {
		t.Errorf("Hunks(\"\") = %q, %v", h, hs)
	}
}

// bigHunk makes a hunk of n added lines.
func bigHunk(start, n int) string {
	var b strings.Builder
	fmt.Fprintf(&b, "@@ -%d,0 +%d,%d @@\n", start, start, n)
	for i := 0; i < n; i++ {
		fmt.Fprintf(&b, "+row %d of hunk at %d\n", i, start)
	}
	return b.String()
}

// bodyLines returns every line of the hunks of diff that is not an "@@" line.
func bodyLines(t *testing.T, diff string) []string {
	t.Helper()
	_, hunks := Hunks(diff)
	var out []string
	for _, h := range hunks {
		for _, l := range strings.SplitAfter(h.Text, "\n")[1:] {
			if l != "" {
				out = append(out, l)
			}
		}
	}
	return out
}

// Contract: diff/D11
func TestSplitKeepsHunksWholeAndRepeatsHeader(t *testing.T) {
	h1, h2, h3 := bigHunk(1, 5), bigHunk(100, 5), bigHunk(200, 5)
	diff := hdr + h1 + h2 + h3
	limit := len(hdr) + len(h1) + len(h2) // two hunks fit, three do not
	pieces := Split(diff, limit)
	if len(pieces) != 2 {
		t.Fatalf("got %d pieces, want 2", len(pieces))
	}
	if pieces[0] != hdr+h1+h2 || pieces[1] != hdr+h3 {
		t.Errorf("pieces do not hold whole hunks in order: %q", pieces)
	}
	for i, p := range pieces {
		if len(p) > limit {
			t.Errorf("piece %d is %d bytes, over the limit %d", i, len(p), limit)
		}
	}
}

// Contract: diff/D11
func TestSplitUnderLimitIsOnePiece(t *testing.T) {
	diff := hdr + bigHunk(1, 3)
	if got := Split(diff, 10000); len(got) != 1 || got[0] != diff {
		t.Errorf("Split = %q, want the diff untouched", got)
	}
}

// Contract: diff/D11
func TestSplitCutsAnOversizedHunkBetweenLines(t *testing.T) {
	small, big := bigHunk(1, 2), bigHunk(50, 40)
	diff := hdr + small + big
	limit := len(hdr) + 300
	pieces := Split(diff, limit)
	if len(pieces) < 3 {
		t.Fatalf("got %d pieces, want the big hunk cut into several", len(pieces))
	}
	bigAt := strings.SplitN(big, "\n", 2)[0] + "\n"
	for i, p := range pieces {
		if !strings.HasPrefix(p, hdr) {
			t.Errorf("piece %d does not start with the header", i)
		}
		if len(p) > limit {
			t.Errorf("piece %d is %d bytes, over the limit %d", i, len(p), limit)
		}
		if !strings.HasSuffix(p, "\n") {
			t.Errorf("piece %d was cut inside a line", i)
		}
		if i > 0 && !strings.HasPrefix(p, hdr+bigAt) {
			t.Errorf("piece %d of the big hunk does not repeat its @@ line: %q", i, p[:60])
		}
	}
	var got []string
	for _, p := range pieces {
		got = append(got, bodyLines(t, p)...)
	}
	want := bodyLines(t, diff)
	if strings.Join(got, "") != strings.Join(want, "") || len(got) != len(want) {
		t.Errorf("body lines across pieces differ from the original: got %d lines, want %d", len(got), len(want))
	}
}

// Contract: diff/D11
func TestSplitEmitsALineLongerThanTheLimitOnItsOwn(t *testing.T) {
	long := "+" + strings.Repeat("y", 500) + "\n"
	diff := hdr + "@@ -0,0 +1,2 @@\n" + long + "+short\n"
	pieces := Split(diff, len(hdr)+100)
	if len(pieces) != 2 {
		t.Fatalf("got %d pieces, want 2", len(pieces))
	}
	if !strings.Contains(pieces[0], long) || !strings.Contains(pieces[1], "+short\n") {
		t.Errorf("lines lost or cut: %q", pieces)
	}
}
