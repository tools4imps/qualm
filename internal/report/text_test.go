package report

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/tools4imps/qualm/internal/check"
	"github.com/tools4imps/qualm/internal/config"
	"github.com/tools4imps/qualm/internal/jev"
	"github.com/tools4imps/qualm/internal/questions"
)

func render(r check.Result) string {
	var b strings.Builder
	Text(&b, r)
	return b.String()
}

func judged(path string) check.File { return check.File{Path: path, Status: "judged", Bytes: 10} }

func expect(t *testing.T, got, want string) {
	t.Helper()
	if got != want {
		t.Errorf("output differs\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}

// Contract: report/R1
func TestPassPlural(t *testing.T) {
	r := check.Result{Files: []check.File{judged("a.rb"), judged("b.rb")}}
	expect(t, render(r), "qualm: 2 changed files, no qualms.\n")
}

// Contract: report/R1
func TestPassSingular(t *testing.T) {
	r := check.Result{Files: []check.File{judged("a.rb"), {Path: "x.md", Status: "skipped", Reason: "prose or data"}}}
	expect(t, render(r), "qualm: 1 changed file, no qualms.\n")
}

// Contract: report/R2
func TestNothingToJudge(t *testing.T) {
	r := check.Result{Files: []check.File{{Path: "x.md", Status: "skipped"}}}
	expect(t, render(r), "qualm: nothing to judge.\n")
}

// Contract: report/R5
func TestNothingToJudgeWithKept(t *testing.T) {
	r := check.Result{Files: []check.File{{Path: "lib/matcher.rb", Status: "kept", Reason: "The algorithm is this complicated"}}}
	expect(t, render(r), "qualm: nothing to judge.\n\nKept\n  lib/matcher.rb: The algorithm is this complicated\n")
}

// Contract: report/R5
func TestPassWithKeptAndStale(t *testing.T) {
	r := check.Result{
		Files: []check.File{
			judged("a.rb"),
			{Path: "lib/matcher.rb", Status: "kept", Reason: "The algorithm is this complicated"},
		},
		StaleKeeps: []config.Keep{{Path: "lib/old.rb", Reason: "It was fine at the time"}},
	}
	want := "qualm: 1 changed file, no qualms.\n" +
		"\nKept\n  lib/matcher.rb: The algorithm is this complicated\n" +
		"\nStale keeps (qualm keep clears them)\n  lib/old.rb: It was fine at the time\n"
	expect(t, render(r), want)
}

// Contract: report/R5
func TestStaleOnly(t *testing.T) {
	r := check.Result{StaleKeeps: []config.Keep{{Path: "lib/old.rb", Reason: "It was fine at the time"}}}
	expect(t, render(r), "qualm: nothing to judge.\n\nStale keeps (qualm keep clears them)\n  lib/old.rb: It was fine at the time\n")
}

const (
	bigUnit = "Did the change make an already long function or class longer, where the new work could have gone in a unit of its own?"
	copies  = "Did the change add logic that is a near-copy of logic already in the file?"
)

func qs() []questions.Question {
	return []questions.Question{
		{ID: "push_back", Type: "noul", Role: "gate", Gates: true, Threshold: 0.6, Instructions: "Would a reviewer push back?"},
		{ID: "direction", Type: "choice", Role: "describes", Criteria: json.RawMessage(`{"harder":"h","same":"s","easier":"e"}`), Instructions: "How hard?"},
		{ID: "grew_a_big_unit", Type: "noul", Role: "diagnosis", Instructions: bigUnit},
		{ID: "added_copies", Type: "noul", Role: "diagnosis", Instructions: copies},
	}
}

func score(v float64) jev.Answer { return jev.Answer{Value: v} }

func direction(choice string, v float64) jev.Answer { return jev.Answer{Value: v, Choice: choice} }

func failing(path string, answers map[string]jev.Answer, where map[string][2]int) check.File {
	return check.File{Path: path, Status: "judged", Answers: answers, Failed: []string{"push_back"}, Where: where}
}

// Contract: report/R3
// Contract: report/R4
// Contract: report/R8
func TestFailOneFile(t *testing.T) {
	f := failing("lib/mutineer/coverage_map.rb", map[string]jev.Answer{
		"push_back":       score(0.61),
		"direction":       direction("harder", 0.99),
		"grew_a_big_unit": score(0.84),
		"added_copies":    score(0.75),
	}, map[string][2]int{"grew_a_big_unit": {412, 540}, "added_copies": {598, 655}})
	r := check.Result{Questions: qs(), Files: []check.File{f, judged("a.rb"), judged("b.rb"), judged("c.rb"), judged("d.rb"), judged("e.rb"), judged("f.rb")}}
	want := `qualm: 1 of 7 changed files drew a qualm.

lib/mutineer/coverage_map.rb  push back 0.61
  harder 0.99
  grew a big unit 0.84  lines 412-540
  added copies 0.75     lines 598-655

What to do
  A reviewer would likely ask for this change to be simplified before it merges.
  The questions that fired:
    grew_a_big_unit: ` + bigUnit + `
    added_copies: ` + copies + `
  Rework the change so they no longer apply, then run qualm again.
  A qualm is an opinion. If the change is right as it stands, a person can keep it:
    qualm keep lib/mutineer/coverage_map.rb --reason "..."
`
	expect(t, render(r), want)
}

// Contract: report/R3
// Contract: report/R4
func TestFailSeveralFiles(t *testing.T) {
	a := failing("lib/a.rb", map[string]jev.Answer{
		"push_back": score(0.7), "direction": direction("same", 0.9), "added_copies": score(0.55),
	}, nil)
	b := failing("lib/much/longer.rb", map[string]jev.Answer{
		"push_back": score(0.65), "direction": direction("easier", 0.8), "grew_a_big_unit": score(0.9), "added_copies": score(0.4),
	}, map[string][2]int{"grew_a_big_unit": {7, 7}})
	r := check.Result{Questions: qs(), Files: []check.File{a, b, judged("z.rb")}}
	want := `qualm: 2 of 3 changed files drew a qualm.

lib/a.rb            push back 0.70
  added copies 0.55

lib/much/longer.rb  push back 0.65
  easier 0.80
  grew a big unit 0.90  line 7

What to do
  A reviewer would likely ask for these changes to be simplified before they merge.
  The questions that fired:
    grew_a_big_unit: ` + bigUnit + `
    added_copies: ` + copies + `
  Rework the change so they no longer apply, then run qualm again.
  A qualm is an opinion. If the change is right as it stands, a person can keep it:
    qualm keep lib/a.rb lib/much/longer.rb --reason "..."
`
	expect(t, render(r), want)
}

// Contract: report/R3
func TestFailWithoutDiagnosis(t *testing.T) {
	f := failing("lib/a.rb", map[string]jev.Answer{
		"push_back": score(0.8), "direction": direction("same", 0.9), "grew_a_big_unit": score(0.49),
	}, nil)
	r := check.Result{Questions: qs(), Files: []check.File{f}}
	want := `qualm: 1 of 1 changed file drew a qualm.

lib/a.rb  push back 0.80

What to do
  A reviewer would likely ask for this change to be simplified before it merges.
  Simplify the change, then run qualm again.
  A qualm is an opinion. If the change is right as it stands, a person can keep it:
    qualm keep lib/a.rb --reason "..."
`
	expect(t, render(r), want)
}

// Contract: report/R3
func TestDiagnosisWithAndWithoutWhere(t *testing.T) {
	f := failing("a.rb", map[string]jev.Answer{
		"push_back": score(0.8), "grew_a_big_unit": score(0.6), "added_copies": score(0.9),
	}, map[string][2]int{"grew_a_big_unit": {3, 9}})
	r := check.Result{Questions: qs(), Files: []check.File{f}}
	got := render(r)
	want := "  added copies 0.90\n  grew a big unit 0.60  lines 3-9\n"
	if !strings.Contains(got, "\n"+want+"\n") {
		t.Errorf("block lines missing\n%s", got)
	}
}

// Contract: report/R3
func TestEqualDiagnosesKeepQuestionOrder(t *testing.T) {
	f := failing("a.rb", map[string]jev.Answer{
		"push_back": score(0.8), "added_copies": score(0.7), "grew_a_big_unit": score(0.7),
	}, nil)
	r := check.Result{Questions: qs(), Files: []check.File{f}}
	if !strings.Contains(render(r), "\n  grew a big unit 0.70\n  added copies 0.70\n\n") {
		t.Errorf("ties out of order\n%s", render(r))
	}
}

// Contract: report/R3
func TestFailThenKeeps(t *testing.T) {
	f := failing("a.rb", map[string]jev.Answer{"push_back": score(0.8)}, nil)
	r := check.Result{
		Questions:  qs(),
		Files:      []check.File{f, {Path: "k.rb", Status: "kept", Reason: "ok"}},
		StaleKeeps: []config.Keep{{Path: "o.rb", Reason: "old"}},
	}
	want := `qualm: 1 of 1 changed file drew a qualm.

a.rb  push back 0.80

What to do
  A reviewer would likely ask for this change to be simplified before it merges.
  Simplify the change, then run qualm again.
  A qualm is an opinion. If the change is right as it stands, a person can keep it:
    qualm keep a.rb --reason "..."

Kept
  k.rb: ok

Stale keeps (qualm keep clears them)
  o.rb: old
`
	expect(t, render(r), want)
}

// Contract: report/R3
func TestSeveralFailedQuestionsJoined(t *testing.T) {
	q := append(qs(), questions.Question{ID: "too_big", Type: "score", Role: "gate", Gates: true, Threshold: 0.5, Instructions: "x"})
	f := check.File{Path: "a.rb", Status: "judged", Failed: []string{"push_back", "too_big"},
		Answers: map[string]jev.Answer{"push_back": score(0.7), "too_big": score(0.5)}}
	got := render(check.Result{Questions: q, Files: []check.File{f}})
	if !strings.Contains(got, "\na.rb  push back 0.70, too big 0.50\n") {
		t.Errorf("failed questions not joined\n%s", got)
	}
}

// Contract: report/R7
func TestDryRunSeveralFiles(t *testing.T) {
	r := check.Result{DryRun: true, Files: []check.File{
		{Path: "lib/a.rb", Status: "judged", Bytes: 1234},
		{Path: "lib/long/name.rb", Status: "judged", Bytes: 300},
		{Path: "x.md", Status: "skipped"},
	}}
	want := `qualm: dry run, nothing sent.

  lib/a.rb           1234 bytes   about 1411 tokens
  lib/long/name.rb    300 bytes   about 1100 tokens

2 files, about 2511 tokens, about $0.0001.
`
	expect(t, render(r), want)
}

// Contract: report/R7
func TestDryRunOneFile(t *testing.T) {
	r := check.Result{DryRun: true, Files: []check.File{{Path: "a.rb", Status: "judged", Bytes: 3000}}}
	want := "qualm: dry run, nothing sent.\n\n  a.rb   3000 bytes   about 2000 tokens\n\n1 file, about 2000 tokens, about $0.0001.\n"
	expect(t, render(r), want)
}

// Contract: report/R7
func TestDryRunNothingToJudge(t *testing.T) {
	r := check.Result{DryRun: true, Files: []check.File{{Path: "x.md", Status: "skipped"}}}
	expect(t, render(r), "qualm: nothing to judge.\n")
}
