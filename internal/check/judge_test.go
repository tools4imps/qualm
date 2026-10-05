package check

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/tools4imps/qualm/internal/gitdiff"
	"github.com/tools4imps/qualm/internal/jev"
	"github.com/tools4imps/qualm/internal/questions"
)

const formatNote = "A unified diff of one file. Lines starting with - are the earlier version, lines starting with + are the later version."

var builtinIDs = []string{
	"push_back", "direction", "simplified", "comments_only", "new_behaviour", "grew_a_big_unit",
	"added_copies", "added_impossible_guards", "added_placeholders", "added_unused_flexibility",
}

// wired is a set of questions as they look once a request has been through JSON.
func wired(t *testing.T, qs []questions.Question) map[string]map[string]any {
	t.Helper()
	out := map[string]map[string]any{}
	for _, q := range qs {
		data, err := json.Marshal(q.Wire())
		if err != nil {
			t.Fatal(err)
		}
		var w map[string]any
		if err := json.Unmarshal(data, &w); err != nil {
			t.Fatal(err)
		}
		out[q.ID] = w
	}
	return out
}

// Contract: judge/U1
func TestRequestNamesTheLanguageOfEveryExtensionInTheTable(t *testing.T) {
	t.Parallel()
	table := map[string]string{
		".rb": "Ruby", ".erb": "ERB", ".go": "Go",
		".js": "JavaScript", ".mjs": "JavaScript", ".cjs": "JavaScript", ".jsx": "JavaScript",
		".ts": "TypeScript", ".tsx": "TypeScript", ".py": "Python", ".rs": "Rust", ".java": "Java",
		".kt": "Kotlin", ".swift": "Swift", ".cs": "C#", ".php": "PHP", ".ex": "Elixir", ".exs": "Elixir",
		".c": "C", ".h": "C", ".cc": "C++", ".cpp": "C++", ".hpp": "C++", ".sh": "shell", ".sql": "SQL",
		".scala": "Scala",
	}
	if len(languages) != len(table) {
		t.Errorf("the table holds %d extensions, want %d", len(languages), len(table))
	}
	j := &judge{}
	for ext, want := range table {
		if got := j.request("src/thing"+ext, "diff", nil).State["language"]; got != want {
			t.Errorf("language for %s = %v, want %s", ext, got, want)
		}
	}
	for _, path := range []string{"Makefile", "notes.xyz", "rb", "dir.rb/file"} {
		if got, ok := j.request(path, "diff", nil).State["language"]; ok {
			t.Errorf("language for %s = %v, want none", path, got)
		}
	}
}

// Contract: judge/U1
func TestRunAsksTheModelAndTheQuestionsTheConfigNames(t *testing.T) {
	t.Parallel()
	r := newRepo(t, map[string]string{
		"a.go": numbered(5),
		"qualm.json": `{"model": "typesafe/jev-2", "drop": ["added_placeholders"],
			"questions": [{"id": "added_flag", "type": "noul", "instructions": "Did the change add a flag?"}]}`,
	})
	r.write("a.go", edited(5, 3))
	f := newFake(t, nil)

	mustRun(t, r.options(f))

	c := f.callWith(r.diff("a.go"))
	if c.Model != "typesafe/jev-2" {
		t.Errorf("model = %q, want the config's typesafe/jev-2", c.Model)
	}
	if _, ok := c.Questions["added_placeholders"]; ok {
		t.Error("asked added_placeholders, which the config drops")
	}
	want := "Did the change add a flag? " + questions.Guard
	if got := c.Questions["added_flag"]["instructions"]; got != want {
		t.Errorf("added_flag instructions = %v, want %q", got, want)
	}
	if len(c.Questions) != 10 {
		t.Errorf("asked %v, want nine built-in questions and added_flag", c.ids())
	}
}

// Contract: judge/U1
func TestRunSendsEachFileOnceWithItsDiffTheNoteTheLanguageAndEveryQuestion(t *testing.T) {
	t.Parallel()
	r := newRepo(t, map[string]string{"lib/a.rb": numbered(30)})
	r.write("lib/a.rb", edited(30, 15))
	r.write("bin/tool", "#!/bin/sh\necho hi\n")
	f := newFake(t, nil)

	res := mustRun(t, r.options(f))

	if f.count() != 2 {
		t.Fatalf("made %d requests for 2 files, want 2", f.count())
	}
	ruby := f.callWith(r.diff("lib/a.rb"))
	wantState := map[string]any{"language": "Ruby", "format": formatNote, "change": r.diff("lib/a.rb")}
	if !reflect.DeepEqual(ruby.State, wantState) {
		t.Errorf("state for lib/a.rb = %v, want %v", ruby.State, wantState)
	}
	tool := f.callWith(r.diff("bin/tool"))
	wantState = map[string]any{"format": formatNote, "change": r.diff("bin/tool")}
	if !reflect.DeepEqual(tool.State, wantState) {
		t.Errorf("state for bin/tool = %v, want %v with no language", tool.State, wantState)
	}
	for _, c := range []call{ruby, tool} {
		if c.Model != "typesafe/jev-1.13" {
			t.Errorf("model = %q, want the default", c.Model)
		}
		if len(c.Questions) != len(builtinIDs) {
			t.Errorf("asked %v, want the ten built-in questions", c.ids())
		}
		if want := wired(t, questions.Builtin()); !reflect.DeepEqual(c.Questions, want) {
			t.Errorf("questions = %v, want %v", c.Questions, want)
		}
	}

	if len(res.Files) != 2 || res.Files[0].Path != "bin/tool" || res.Files[1].Path != "lib/a.rb" {
		t.Fatalf("files = %+v, want bin/tool then lib/a.rb", res.Files)
	}
	for _, file := range res.Files {
		if file.Status != "judged" {
			t.Errorf("%s status = %q, want judged", file.Path, file.Status)
		}
		for _, id := range builtinIDs {
			if _, ok := file.Answers[id]; !ok {
				t.Errorf("%s has no answer for %s", file.Path, id)
			}
		}
		if got := file.Answers["push_back"].Value; got != 0.1 {
			t.Errorf("%s push_back = %v, want the fake's 0.1", file.Path, got)
		}
		if got := file.Answers["direction"]; got.Choice != "same" || got.Value != 0.9 {
			t.Errorf("%s direction = %+v, want same at 0.9", file.Path, got)
		}
	}
}

// long returns n lines of over a hundred bytes each, so a diff of many hunks passes the size at
// which it is split. The given lines read "changed NNNN".
func long(n int, changed map[int]bool) string {
	var b strings.Builder
	for i := 1; i <= n; i++ {
		if changed[i] {
			fmt.Fprintf(&b, "changed %04d\n", i)
			continue
		}
		fmt.Fprintf(&b, "line %04d %s\n", i, strings.Repeat("x", 100))
	}
	return b.String()
}

// bigRepo changes every fortieth line of a 4000 line file, which gives big.go a diff of a hundred
// hunks that has to be asked in several pieces. It returns the pieces.
func bigRepo(t *testing.T, config string) (*repo, []string) {
	t.Helper()
	r := newRepo(t, map[string]string{"big.go": long(4000, nil), "qualm.json": config})
	every40th := map[int]bool{}
	for i := 40; i <= 4000; i += 40 {
		every40th[i] = true
	}
	r.write("big.go", long(4000, every40th))
	diff := r.diff("big.go")
	pieces := gitdiff.Split(diff, 60000)
	if len(diff) <= 60000 || len(pieces) < 3 {
		t.Fatalf("the diff is %d bytes in %d pieces, too small to test with", len(diff), len(pieces))
	}
	return r, pieces
}

// Contract: judge/U2
func TestRunKeepsTheEarlierPieceWhenAChoiceTies(t *testing.T) {
	t.Parallel()
	r, pieces := bigRepo(t, `{}`)
	// Every piece gives "harder" the same probability, so the first piece asked must stand.
	f := newFake(t, func(c call) answer {
		probs := map[string]float64{"harder": 0.2, "same": 0.1, "easier": 0.7}
		if c.change() == pieces[0] {
			probs = map[string]float64{"harder": 0.2, "same": 0.8, "easier": 0}
		}
		return answer{choices: map[string]map[string]float64{"direction": probs}}
	})

	res := mustRun(t, r.options(f))

	if f.count() != len(pieces) {
		t.Fatalf("made %d requests, want %d", f.count(), len(pieces))
	}
	if got := file(t, res, "big.go").Answers["direction"]; got.Choice != "same" || got.Value != 0.8 {
		t.Errorf("direction = %+v, want the first piece's same at 0.8", got)
	}
}

// Contract: judge/U2
func TestRunAsksALargeDiffInPiecesAndTakesTheHighestValueOfEach(t *testing.T) {
	t.Parallel()
	r, pieces := bigRepo(t, `{"questions": [{"id": "hard_to_trace", "type": "score",
		"instructions": "How hard is it to trace?", "criteria": ["easy", "middling", "hard"]}]}`)
	// The first piece is strongest on two questions and a middle piece on the gate. The middle
	// piece is also where "harder", the first option, is likeliest, though "same" wins there and
	// other pieces hold a choice with a higher probability.
	f := newFake(t, func(c call) answer {
		switch {
		case strings.Contains(c.change(), "changed 0040"):
			return answer{
				// The score stays under 0.5, where a diagnosis would be asked about hunk by hunk.
				values:  map[string]float64{"simplified": 0.7, "hard_to_trace": 0.4},
				choices: map[string]map[string]float64{"direction": {"harder": 0.3, "same": 0, "easier": 0.7}},
			}
		case strings.Contains(c.change(), "changed 2000"):
			return answer{
				values:  map[string]float64{"push_back": 0.9},
				choices: map[string]map[string]float64{"direction": {"harder": 0.4, "same": 0.6, "easier": 0}},
			}
		}
		return answer{}
	})

	res := mustRun(t, r.options(f))

	var sent []string
	for _, c := range f.received() {
		sent = append(sent, c.change())
		if len(c.change()) > 60000 {
			t.Errorf("a piece of %d bytes was sent, want at most 60000", len(c.change()))
		}
	}
	sort.Strings(sent)
	sort.Strings(pieces)
	if !reflect.DeepEqual(sent, pieces) {
		t.Fatalf("made %d requests, want one for each of the %d pieces", len(sent), len(pieces))
	}
	got := file(t, res, "big.go")
	for id, want := range map[string]float64{"push_back": 0.9, "simplified": 0.7, "hard_to_trace": 0.4, "comments_only": 0.1} {
		if got.Answers[id].Value != want {
			t.Errorf("%s = %v, want %v, the highest among the pieces", id, got.Answers[id].Value, want)
		}
	}
	wantDirection := jev.Answer{Value: 0.6, Choice: "same", Probabilities: map[string]float64{"harder": 0.4, "same": 0.6, "easier": 0}}
	if !reflect.DeepEqual(got.Answers["direction"], wantDirection) {
		t.Errorf("direction = %+v, want %+v from the piece where harder is likeliest", got.Answers["direction"], wantDirection)
	}
	if !reflect.DeepEqual(got.Failed, []string{"push_back"}) {
		t.Errorf("failed %v, want push_back from the piece that reached the gate", got.Failed)
	}
}

// entries reads every file in a cache directory, by name. A directory that doesn't exist has none.
func entries(t *testing.T, dir string) map[string]string {
	t.Helper()
	out := map[string]string{}
	list, err := os.ReadDir(dir)
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	for _, e := range list {
		data, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		out[e.Name()] = string(data)
	}
	return out
}

// oneChange is a repository with a single changed file, a.go.
func oneChange(t *testing.T) *repo {
	t.Helper()
	r := newRepo(t, map[string]string{"a.go": numbered(5)})
	r.write("a.go", edited(5, 3))
	return r
}

// Contract: judge/U3
func TestRunReplaysACachedAnswerWithoutARequest(t *testing.T) {
	t.Parallel()
	r := oneChange(t)
	f := newFake(t, says(map[string]float64{"push_back": 0.3}))
	o := r.options(f)

	first := mustRun(t, o)
	if want := (Usage{Requests: 1, InputTokens: 100, Cost: 0.001}); first.Usage != want {
		t.Errorf("the first run's usage = %+v, want %+v", first.Usage, want)
	}
	request := sha256.Sum256([]byte(f.received()[0].Body))
	sum := sha256.Sum256([]byte(hex.EncodeToString(request[:]) + "\n\"push_back\" 0.6"))
	if got := entries(t, o.CacheDir); len(got) != 1 || got[hex.EncodeToString(sum[:])+".json"] == "" {
		t.Errorf("the cache holds %v, want one entry named by the SHA-256 of the request's key and the gate's threshold", got)
	}

	// Jev would now say something else, so only a replay can give the first answer again.
	f.say(says(map[string]float64{"push_back": 0.9}))
	second := mustRun(t, o)

	if f.count() != 1 {
		t.Errorf("made %d requests over two runs, want 1", f.count())
	}
	if !reflect.DeepEqual(second.Files, first.Files) {
		t.Errorf("the replayed files = %+v, want %+v", second.Files, first.Files)
	}
	if second.Usage != (Usage{}) {
		t.Errorf("the replay's usage = %+v, want nothing spent", second.Usage)
	}
	if first.Warnings != nil || second.Warnings != nil {
		t.Errorf("warnings = %q and %q, want none", first.Warnings, second.Warnings)
	}
}

// Contract: judge/U3
func TestRunAsksAgainWhenTheAnswerWasSettledAgainstAnotherThreshold(t *testing.T) {
	t.Parallel()
	r := oneChange(t)
	f := newFake(t, inTurn(
		map[string]float64{"push_back": 0.68},
		map[string]float64{"push_back": 0.71},
		map[string]float64{"push_back": 0.72},
	))
	o := r.options(f)

	// At 0.6 the first answer is no close call, so it is cached as it came.
	first := mustRun(t, o)
	if got := file(t, first, "a.go"); got.Answers["push_back"].Value != 0.68 || f.count() != 1 {
		t.Fatalf("push_back = %v after %d requests, want 0.68 after 1", got.Answers["push_back"].Value, f.count())
	}

	// At 0.7 it would have been one, so the cached answer is not the one a cold cache gives.
	o.Threshold = 0.7
	second := mustRun(t, o)

	got := file(t, second, "a.go")
	if f.count() != 4 || got.Answers["push_back"].Value != 0.72 || !reflect.DeepEqual(got.Failed, []string{"push_back"}) {
		t.Errorf("at 0.7 push_back = %v, failing %v, after %d requests: want the median 0.72 and a fail after 3 more",
			got.Answers["push_back"].Value, got.Failed, f.count()-1)
	}

	// Each threshold keeps its own settled answer.
	for threshold, want := range map[float64]float64{0: 0.68, 0.6: 0.68, 0.7: 0.72} {
		o.Threshold = threshold
		if got := file(t, mustRun(t, o), "a.go").Answers["push_back"].Value; got != want || f.count() != 4 {
			t.Errorf("at %v push_back = %v after %d requests, want %v replayed", threshold, got, f.count(), want)
		}
	}
}

// Contract: judge/U3
func TestRunAsksAgainWhenACachedEntryDoesNotAnswerEveryQuestion(t *testing.T) {
	t.Parallel()
	cases := map[string]func(answers map[string]any){
		"no answers at all": func(answers map[string]any) { clear(answers) },
		"one answer gone":   func(answers map[string]any) { delete(answers, "added_copies") },
		"a value over 1":    func(answers map[string]any) { answers["push_back"] = map[string]any{"value": 1.5} },
		"a value under 0":   func(answers map[string]any) { answers["simplified"] = map[string]any{"value": -0.1} },
	}
	r := oneChange(t) // shared: each case has its own fake Jev and its own cache
	for name, damage := range cases {
		t.Run(name, func(t *testing.T) {
			f := newFake(t, says(map[string]float64{"push_back": 0.9}))
			o := r.options(f)
			first := mustRun(t, o)
			sound := entries(t, o.CacheDir)
			for name, data := range sound {
				var entry map[string]any
				if err := json.Unmarshal([]byte(data), &entry); err != nil {
					t.Fatal(err)
				}
				damage(entry["answers"].(map[string]any))
				damaged, err := json.Marshal(entry)
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(o.CacheDir, name), damaged, 0o600); err != nil {
					t.Fatal(err)
				}
			}

			second := mustRun(t, o)

			if f.count() != 2 || len(sound) != 1 {
				t.Errorf("made %d requests with %d cache entries, want the one entry asked for again", f.count(), len(sound))
			}
			if !reflect.DeepEqual(second.Files, first.Files) {
				t.Errorf("push_back = %v failing %v, want the fresh answer the first run got", second.Files[0].Answers["push_back"].Value, second.Files[0].Failed)
			}
			if got := entries(t, o.CacheDir); !reflect.DeepEqual(got, sound) {
				t.Errorf("the cache holds %v, want the damaged entry replaced by the fresh answer", got)
			}
		})
	}
}

// Contract: judge/U3
func TestRunWithTheCacheOffReadsNothingAndWritesNothing(t *testing.T) {
	t.Parallel()
	r := oneChange(t)
	f := newFake(t, says(map[string]float64{"push_back": 0.3}))
	o := r.options(f)
	mustRun(t, o)
	warm := entries(t, o.CacheDir)

	f.say(says(map[string]float64{"push_back": 0.9}))
	o.NoCache = true
	res := mustRun(t, o)

	if f.count() != 2 {
		t.Errorf("made %d requests, want a second one with the cache off", f.count())
	}
	if got := file(t, res, "a.go").Answers["push_back"].Value; got != 0.9 {
		t.Errorf("push_back = %v, want the fresh 0.9 and not the cached 0.3", got)
	}
	if got := entries(t, o.CacheDir); !reflect.DeepEqual(got, warm) {
		t.Error("the cache changed during a run with the cache off")
	}
	if res.Warnings != nil {
		t.Errorf("warnings = %q, want none: a cache that is off isn't one that failed", res.Warnings)
	}

	o.CacheDir = filepath.Join(t.TempDir(), "unused")
	mustRun(t, o)
	if got := entries(t, o.CacheDir); len(got) != 0 {
		t.Errorf("a run with the cache off wrote %d entries to an empty cache", len(got))
	}
}

// Contract: judge/U3
func TestRunCachesUnderTheUserCacheDirectoryWhenNoneIsGiven(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CACHE_HOME", filepath.Join(home, "cache"))
	userCache, err := os.UserCacheDir()
	if err != nil || !strings.HasPrefix(userCache, home) {
		t.Skipf("the user cache directory %q can't be moved on this system: %v", userCache, err)
	}
	r := oneChange(t)
	f := newFake(t, nil)
	o := r.options(f)
	o.CacheDir = ""

	mustRun(t, o)
	mustRun(t, o)

	if f.count() != 1 {
		t.Errorf("made %d requests over two runs, want 1", f.count())
	}
	if got := entries(t, filepath.Join(userCache, "qualm")); len(got) != 1 {
		t.Errorf("%s holds %d entries, want 1", filepath.Join(userCache, "qualm"), len(got))
	}
}

// Contract: judge/U3
func TestRunStopsWhenThereIsNoUserCacheDirectoryUnlessTheCacheIsOff(t *testing.T) {
	t.Setenv("HOME", "")
	t.Setenv("XDG_CACHE_HOME", "")
	if dir, err := os.UserCacheDir(); err == nil {
		t.Skipf("this system still finds a user cache directory, %q", dir)
	}
	r := oneChange(t)
	f := newFake(t, nil)
	o := r.options(f)
	o.CacheDir = ""

	res, err := Run(context.Background(), o)

	if err == nil || !strings.Contains(err.Error(), "cache") {
		t.Errorf("err = %v, want one about the cache directory", err)
	}
	if !reflect.DeepEqual(res, Result{}) || f.count() != 0 {
		t.Errorf("got a result and %d requests, want neither", f.count())
	}

	o.NoCache = true
	if _, err := Run(context.Background(), o); err != nil {
		t.Errorf("with the cache off: %v, want a run that needs no directory", err)
	}
}

// Contract: judge/U3
// Contract: gate/G12
func TestRunCarriesOnAndWarnsOnceWhenTheCacheCannotBeWritten(t *testing.T) {
	t.Parallel()
	r, _ := manyChanges(t, 3)
	f := newFake(t, says(map[string]float64{"push_back": 0.3}))
	o := r.options(f)
	// A file where the directory should be makes every write fail.
	o.CacheDir = filepath.Join(t.TempDir(), "cache")
	if err := os.WriteFile(o.CacheDir, []byte("in the way"), 0o644); err != nil {
		t.Fatal(err)
	}

	res := mustRun(t, o)

	if got := file(t, res, "a.go").Answers["push_back"].Value; got != 0.3 || f.count() != 3 || !res.Passed() {
		t.Errorf("push_back = %v after %d requests, passed %v: want 0.3 after 3 and a pass", got, f.count(), res.Passed())
	}
	want := []string{"the cache at " + o.CacheDir + " couldn't be written, so answers won't be reused"}
	if !reflect.DeepEqual(res.Warnings, want) {
		t.Errorf("warnings = %q, want one for the three failed writes: %q", res.Warnings, want)
	}
}

// inTurn answers the first arrival of a request with the first set of values, the second arrival
// with the second and so on, repeating the last.
func inTurn(values ...map[string]float64) func(call) answer {
	return func(c call) answer {
		i := c.Nth - 1
		if i >= len(values) {
			i = len(values) - 1
		}
		return answer{values: values[i]}
	}
}

// Contract: judge/U4
func TestRunSettlesACloseCallWithTheMedianOfThreeAndCachesIt(t *testing.T) {
	t.Parallel()
	r := oneChange(t)
	f := newFake(t, inTurn(
		map[string]float64{"push_back": 0.62, "simplified": 0.3},
		map[string]float64{"push_back": 0.58, "simplified": 0.4},
		map[string]float64{"push_back": 0.57, "simplified": 0.5},
	))
	o := r.options(f)

	first := mustRun(t, o)

	calls := f.received()
	if len(calls) != 3 || calls[1].Body != calls[0].Body || calls[2].Body != calls[0].Body {
		t.Fatalf("made %d requests, want the same request three times", len(calls))
	}
	got := file(t, first, "a.go")
	if got.Answers["push_back"].Value != 0.58 || len(got.Failed) != 0 {
		t.Errorf("push_back = %v, failed %v, want the median 0.58 and a pass", got.Answers["push_back"].Value, got.Failed)
	}
	if got.Answers["simplified"].Value != 0.3 {
		t.Errorf("simplified = %v, want the first reply's 0.3 since it doesn't gate", got.Answers["simplified"].Value)
	}
	if first.Usage.Requests != 3 {
		t.Errorf("usage counts %d requests, want all 3", first.Usage.Requests)
	}

	second := mustRun(t, o)

	if f.count() != 3 {
		t.Errorf("the second run made %d requests, want none", f.count()-3)
	}
	if got := file(t, second, "a.go").Answers["push_back"].Value; got != 0.58 {
		t.Errorf("the second run's push_back = %v, want the settled 0.58 from the cache", got)
	}
}

// Contract: judge/U4
func TestRunTakesTheMiddleOfThreeValuesWhereverItFalls(t *testing.T) {
	t.Parallel()
	cases := []struct {
		values   [3]float64
		want     float64
		wantFail bool
	}{
		{[3]float64{0.58, 0.62, 0.63}, 0.62, true},
		{[3]float64{0.6, 0.1, 0.9}, 0.6, true},
		{[3]float64{0.61, 0.2, 0.3}, 0.3, false},
		{[3]float64{0.59, 0.95, 0.9}, 0.9, true},
	}
	r := oneChange(t) // shared: each case has its own fake Jev and its own cache
	for _, tc := range cases {
		t.Run(fmt.Sprint(tc.values), func(t *testing.T) {
			f := newFake(t, inTurn(
				map[string]float64{"push_back": tc.values[0]},
				map[string]float64{"push_back": tc.values[1]},
				map[string]float64{"push_back": tc.values[2]},
			))

			res := mustRun(t, r.options(f))

			got := file(t, res, "a.go")
			if got.Answers["push_back"].Value != tc.want || (len(got.Failed) > 0) != tc.wantFail {
				t.Errorf("push_back = %v, failed %v, want %v and failing %v", got.Answers["push_back"].Value, got.Failed, tc.want, tc.wantFail)
			}
		})
	}
}

// Contract: judge/U4
func TestRunSettlesOnlyTheGateThatWasClose(t *testing.T) {
	t.Parallel()
	r := newRepo(t, map[string]string{"a.go": numbered(5), "qualm.json": `{"questions": [
		{"id": "added_flag", "type": "noul", "instructions": "Did the change add a flag?", "gates": true, "threshold": 0.8}]}`})
	r.write("a.go", edited(5, 3))
	// added_flag is close to its own 0.8. push_back starts far from its 0.6, so its later values
	// must not count.
	f := newFake(t, inTurn(
		map[string]float64{"push_back": 0.3, "added_flag": 0.78},
		map[string]float64{"push_back": 0.9, "added_flag": 0.84},
		map[string]float64{"push_back": 0.9, "added_flag": 0.81},
	))

	res := mustRun(t, r.options(f))

	got := file(t, res, "a.go")
	if f.count() != 3 || got.Answers["added_flag"].Value != 0.81 || got.Answers["push_back"].Value != 0.3 {
		t.Errorf("after %d requests added_flag = %v and push_back = %v, want 0.81 and 0.3 after 3",
			f.count(), got.Answers["added_flag"].Value, got.Answers["push_back"].Value)
	}
	if !reflect.DeepEqual(got.Failed, []string{"added_flag"}) {
		t.Errorf("failed %v, want added_flag alone", got.Failed)
	}
}

// Contract: judge/U4
// Contract: judge/U5
func TestRunAsksAgainOnlyWithinFiveHundredthsOfTheThreshold(t *testing.T) {
	t.Parallel()
	// The margin is closeMargin plus a slack for rounding. A threshold of 2^-50 and a value that
	// sits above it by exactly that sum make the distance equal the sum with no rounding at all.
	edge := float64(closeMargin + slack)
	tiny := math.Ldexp(1, -50)
	cases := []struct {
		name      string
		threshold float64
		values    map[string]float64
		want      int
	}{
		{"exactly the margin and its slack", tiny, map[string]float64{"push_back": edge + tiny}, 3},
		{"far above", 0, map[string]float64{"push_back": 0.9}, 1},
		{"just outside above", 0, map[string]float64{"push_back": 0.66}, 1},
		{"exactly five hundredths above", 0, map[string]float64{"push_back": 0.65}, 3},
		{"on the threshold", 0, map[string]float64{"push_back": 0.6}, 3},
		{"exactly five hundredths below", 0, map[string]float64{"push_back": 0.55}, 3},
		{"just outside below", 0, map[string]float64{"push_back": 0.54}, 1},
		{"far below", 0, map[string]float64{"push_back": 0.1}, 1},
		{"questions that don't gate sit on 0.6", 0, map[string]float64{"push_back": 0.1, "simplified": 0.6, "added_copies": 0.62}, 1},
		// A question that doesn't gate has a threshold of zero, which a low value is close to.
		{"questions that don't gate sit near zero", 0, map[string]float64{"push_back": 0.2, "simplified": 0.02, "added_copies": 0}, 1},
		{"near 0.6 when the threshold is 0.3", 0.3, map[string]float64{"push_back": 0.6}, 1},
		{"near a threshold of 0.3", 0.3, map[string]float64{"push_back": 0.33}, 3},
	}
	r := oneChange(t) // shared: each case has its own fake Jev and its own cache
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFake(t, says(tc.values))
			o := r.options(f)
			o.Threshold = tc.threshold

			mustRun(t, o)

			if f.count() != tc.want {
				t.Errorf("made %d requests, want %d", f.count(), tc.want)
			}
		})
	}
}

// Contract: judge/U4
func TestRunSettlesACloseCallWithTheCacheOffAndStoresNothing(t *testing.T) {
	t.Parallel()
	r := oneChange(t)
	f := newFake(t, says(map[string]float64{"push_back": 0.62}))
	o := r.options(f)
	o.NoCache = true

	mustRun(t, o)

	if f.count() != 3 {
		t.Errorf("made %d requests, want 3", f.count())
	}
	if got := entries(t, o.CacheDir); len(got) != 0 {
		t.Errorf("the cache holds %d entries, want none with the cache off", len(got))
	}
}
