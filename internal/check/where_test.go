package check

import (
	"fmt"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/tools4imps/qualm/internal/gitdiff"
)

// threeHunks is a repository where a.go changed on lines 10, 50 and 90 of 100. With eight lines of
// context that is three hunks, covering lines 2 to 18, 42 to 58 and 82 to 98.
func threeHunks(t *testing.T) *repo {
	t.Helper()
	r := newRepo(t, map[string]string{"a.go": numbered(100)})
	r.write("a.go", edited(100, 10, 50, 90))
	return r
}

// whole reports whether a request asks about a whole file and not one hunk of it. Only a request
// for the whole file carries the gate.
func whole(c call) bool {
	_, ok := c.Questions["push_back"]
	return ok
}

// Contract: judge/U6
func TestRunAsksEachFiredDiagnosisHunkByHunkAndReportsTheStrongest(t *testing.T) {
	t.Parallel()
	r := threeHunks(t)
	// added_placeholders is just under 0.5 and simplified is no diagnosis, so neither is asked about
	// again. added_copies ties between the first and last hunks, and the earlier one wins.
	f := newFake(t, func(c call) answer {
		switch {
		case whole(c):
			return answer{values: map[string]float64{
				"push_back": 0.9, "grew_a_big_unit": 0.8, "added_copies": 0.5, "added_placeholders": 0.49, "simplified": 0.95,
			}}
		case strings.Contains(c.change(), "changed 10"):
			return answer{values: map[string]float64{"grew_a_big_unit": 0.2, "added_copies": 0.6}}
		case strings.Contains(c.change(), "changed 50"):
			return answer{values: map[string]float64{"grew_a_big_unit": 0.9, "added_copies": 0.1}}
		}
		return answer{values: map[string]float64{"grew_a_big_unit": 0.4, "added_copies": 0.6}}
	})
	o := r.options(f)

	res := mustRun(t, o)

	want := map[string][2]int{"grew_a_big_unit": {42, 58}, "added_copies": {2, 18}}
	if got := file(t, res, "a.go").Where; !reflect.DeepEqual(got, want) {
		t.Errorf("where = %v, want %v", got, want)
	}
	header, hunks := gitdiff.Hunks(r.diff("a.go"))
	if f.count() != 4 || len(hunks) != 3 {
		t.Fatalf("made %d requests for a diff of %d hunks, want 4 for 3", f.count(), len(hunks))
	}
	for _, h := range hunks {
		c := f.callWith(header + h.Text)
		if !reflect.DeepEqual(c.ids(), []string{"added_copies", "grew_a_big_unit"}) {
			t.Errorf("a hunk was asked %v, want the two diagnoses that fired", c.ids())
		}
		if c.State["language"] != "Go" || c.State["format"] != formatNote {
			t.Errorf("a hunk's state = %v, want the language and the format note", c.State)
		}
	}

	again := mustRun(t, o)

	if f.count() != 4 {
		t.Errorf("the second run made %d requests, want the hunks replayed from the cache", f.count()-4)
	}
	if got := file(t, again, "a.go").Where; !reflect.DeepEqual(got, want) {
		t.Errorf("the replayed where = %v, want %v", got, want)
	}
}

// Contract: judge/U6
func TestRunAsksAtMostJobsHunksAtOnceAcrossFailingFiles(t *testing.T) {
	t.Parallel()
	r := newRepo(t, map[string]string{"a.go": numbered(100), "b.go": numbered(100)})
	r.write("a.go", edited(100, 20, 80))
	r.write("b.go", edited(100, 20, 80))
	f := newFake(t, nil)
	var wholes atomic.Int32
	f.say(func(c call) answer {
		if !whole(c) {
			return answer{}
		}
		// Both files are past the hold once the second is answered, so only the hunks that follow
		// wait for company.
		if wholes.Add(1) == 2 {
			f.holdUntil(3, 20*time.Millisecond)
		}
		return answer{values: map[string]float64{"push_back": 0.9, "added_copies": 0.9}}
	})
	o := r.options(f)
	o.Jobs = 3

	mustRun(t, o)

	if got := f.peakInFlight(); got != 3 {
		t.Errorf("%d requests for hunks were in flight at once, want 3: never more than Jobs, and the hunks of two files not one file at a time", got)
	}
	if f.count() != 6 {
		t.Errorf("made %d requests, want 2 for the files and 4 for their hunks", f.count())
	}
}

// Contract: judge/U6
func TestRunReportsTheOnlyHunkWithoutAskingAgain(t *testing.T) {
	t.Parallel()
	r := newRepo(t, map[string]string{"a.go": numbered(30)})
	r.write("a.go", edited(30, 15))
	f := newFake(t, says(map[string]float64{"push_back": 0.9, "grew_a_big_unit": 0.8, "added_copies": 0.5, "added_placeholders": 0.2}))

	res := mustRun(t, r.options(f))

	want := map[string][2]int{"grew_a_big_unit": {7, 23}, "added_copies": {7, 23}}
	if got := file(t, res, "a.go").Where; !reflect.DeepEqual(got, want) {
		t.Errorf("where = %v, want %v", got, want)
	}
	if f.count() != 1 {
		t.Errorf("made %d requests for a diff of one hunk, want 1", f.count())
	}
}

// Contract: judge/U6
func TestRunLooksForNothingWhenNoDiagnosisFiredOrTheFilePassed(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		values map[string]float64
	}{
		{"a failing file with no diagnosis at 0.5", map[string]float64{"push_back": 0.9, "grew_a_big_unit": 0.49, "simplified": 0.9}},
		{"a passing file with a diagnosis over 0.5", map[string]float64{"push_back": 0.2, "grew_a_big_unit": 0.9}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := threeHunks(t)
			f := newFake(t, says(tc.values))

			res := mustRun(t, r.options(f))

			if got := file(t, res, "a.go").Where; len(got) != 0 {
				t.Errorf("where = %v, want nothing", got)
			}
			if f.count() != 1 {
				t.Errorf("made %d requests, want 1", f.count())
			}
		})
	}
}

// Contract: judge/U6
func TestRunAsksAHunkOverThePieceLimitInPieces(t *testing.T) {
	t.Parallel()
	// One small hunk near the top, and 700 long lines added at the bottom in a hunk of some 70,000
	// bytes.
	var added strings.Builder
	for i := 1; i <= 700; i++ {
		fmt.Fprintf(&added, "added %04d %s\n", i, strings.Repeat("x", 90))
	}
	r := newRepo(t, map[string]string{"a.go": numbered(100)})
	r.write("a.go", edited(100, 10)+added.String())
	// The diagnosis is strongest in the last lines of the big hunk, which only its second piece
	// holds, and the small hunk beats the first piece.
	f := newFake(t, func(c call) answer {
		switch {
		case whole(c):
			return answer{values: map[string]float64{"push_back": 0.9, "added_copies": 0.9}}
		case strings.Contains(c.change(), "added 0700"):
			return answer{values: map[string]float64{"added_copies": 0.8}}
		case strings.Contains(c.change(), "changed 10"):
			return answer{values: map[string]float64{"added_copies": 0.3}}
		}
		return answer{values: map[string]float64{"added_copies": 0.1}}
	})

	res := mustRun(t, r.options(f))

	_, hunks := gitdiff.Hunks(r.diff("a.go"))
	if len(hunks) != 2 || len(hunks[1].Text) <= 60000 {
		t.Fatalf("the diff has %d hunks, want 2 with the second over the limit", len(hunks))
	}
	want := map[string][2]int{"added_copies": {hunks[1].Start, hunks[1].End}}
	if got := file(t, res, "a.go").Where; !reflect.DeepEqual(got, want) {
		t.Errorf("where = %v, want the big hunk: %v", got, want)
	}
	hunkRequests := 0
	for _, c := range f.received() {
		if len(c.change()) > 60000 {
			t.Errorf("a request carried %d bytes of diff, want at most 60000", len(c.change()))
		}
		if !whole(c) {
			hunkRequests++
		}
	}
	if hunkRequests != 3 {
		t.Errorf("made %d requests for the hunks, want 1 for the small one and 2 for the big one's pieces", hunkRequests)
	}
}

// Contract: judge/U6
func TestRunNeverLooksForWhereAChoicePoints(t *testing.T) {
	t.Parallel()
	r := threeHunks(t)
	r.write("qualm.json", `{"questions": [{"id": "mood", "type": "choice", "instructions": "What mood?",
		"criteria": {"grim": "It is grim.", "sunny": "It is sunny."}}]}`)
	f := newFake(t, func(c call) answer {
		return answer{
			values:  map[string]float64{"push_back": 0.9},
			choices: map[string]map[string]float64{"mood": {"grim": 0.9, "sunny": 0.1}},
		}
	})

	res := mustRun(t, r.options(f))

	got := file(t, res, "a.go")
	if got.Answers["mood"].Choice != "grim" || len(got.Failed) != 1 {
		t.Fatalf("a.go = %+v, want it failing with a grim mood", got)
	}
	if f.count() != 1 || len(got.Where) != 0 || len(got.Fired(res.Questions)) != 0 {
		t.Errorf("made %d requests, where %v, fired %v: want a choice at 0.9 to count as no diagnosis", f.count(), got.Where, ids(got.Fired(res.Questions)))
	}
}

// Contract: gate/G12
func TestRunKeepsItsVerdictWhenFindingWhereFails(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		budget float64
		reply  func(call) answer
		want   string // what the warning has to mention
		asked  int
	}{
		{"Jev fails on a hunk", 0, func(c call) answer {
			if !whole(c) {
				return answer{status: 400}
			}
			return answer{values: map[string]float64{"push_back": 0.9, "added_copies": 0.9}}
		}, "HTTP 400", 3},
		{"the budget runs out among the hunks", 0.002, says(map[string]float64{"push_back": 0.9, "added_copies": 0.9}), "budget reached", 2},
	}
	// a.go has three hunks to ask about and b.go has one, which needs no request.
	r := threeHunks(t)
	r.write("b.go", "package b\n")
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFake(t, tc.reply)
			o := r.options(f)
			o.Jobs, o.Budget = 1, tc.budget // one request at a time, so the count is exact

			res := mustRun(t, o)

			a, b := file(t, res, "a.go"), file(t, res, "b.go")
			if res.Passed() || len(a.Failed) != 1 || len(b.Failed) != 1 || a.Answers["added_copies"].Value != 0.9 {
				t.Errorf("passed %v with a.go failing %v and b.go failing %v, want the verdict to stand", res.Passed(), a.Failed, b.Failed)
			}
			if len(a.Where) != 0 || !reflect.DeepEqual(b.Where, map[string][2]int{"added_copies": {1, 1}}) {
				t.Errorf("where = %v and %v, want none for a.go and the only hunk of b.go", a.Where, b.Where)
			}
			prefix := "couldn't find where the diagnoses point: "
			if len(res.Warnings) != 1 || !strings.HasPrefix(res.Warnings[0], prefix) || !strings.Contains(res.Warnings[0], tc.want) {
				t.Errorf("warnings = %q, want one that starts %q and mentions %q", res.Warnings, prefix, tc.want)
			}
			if f.count() != tc.asked || res.Usage.Requests != 2 {
				t.Errorf("made %d requests and counted %d, want %d made with no hunk asked after the failure, and 2 counted", f.count(), res.Usage.Requests, tc.asked)
			}
		})
	}
}
