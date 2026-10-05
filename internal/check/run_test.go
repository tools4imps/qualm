package check

import (
	"context"
	"fmt"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/tools4imps/qualm/internal/config"
	"github.com/tools4imps/qualm/internal/jev"
	"github.com/tools4imps/qualm/internal/questions"
)

// manyChanges is a repository with n changed files, named so that the order they are written in
// is not the order they sort in.
func manyChanges(t *testing.T, n int) (*repo, []string) {
	t.Helper()
	base := map[string]string{}
	var sorted []string
	for i := 0; i < n; i++ {
		name := fmt.Sprintf("%c.go", 'a'+i)
		sorted = append(sorted, name)
		base[name] = numbered(5)
	}
	r := newRepo(t, base)
	for i := n - 1; i >= 0; i-- {
		r.write(sorted[i], edited(5, 3))
	}
	return r, sorted
}

func paths(res Result) []string {
	var out []string
	for _, f := range res.Files {
		out = append(out, f.Path)
	}
	return out
}

// Contract: judge/U7
func TestRunJudgesAtMostJobsFilesAtOnceAndListsThemSorted(t *testing.T) {
	t.Parallel()
	r, sorted := manyChanges(t, 6)
	f := newFake(t, nil)
	f.holdUntil(2, 20*time.Millisecond)
	o := r.options(f)
	o.Jobs = 2

	res := mustRun(t, o)

	if got := f.peakInFlight(); got != 2 {
		t.Errorf("%d requests were in flight at once, want 2: never more than Jobs, and not one at a time", got)
	}
	if f.count() != 6 {
		t.Errorf("made %d requests, want 6", f.count())
	}
	if !reflect.DeepEqual(paths(res), sorted) {
		t.Errorf("files = %v, want them sorted: %v", paths(res), sorted)
	}
	for _, file := range res.Files {
		if file.Answers["push_back"].Value != 0.1 {
			t.Errorf("%s has no answers", file.Path)
		}
	}
}

// Contract: judge/U7
func TestRunJudgesEightFilesAtOnceWhenJobsIsNotGiven(t *testing.T) {
	t.Parallel()
	r, _ := manyChanges(t, 10)
	f := newFake(t, nil)
	f.holdUntil(8, 20*time.Millisecond)

	mustRun(t, r.options(f))

	if got := f.peakInFlight(); got != 8 {
		t.Errorf("%d requests were in flight at once, want 8", got)
	}
}

// says answers every request with the given noul and score values.
func says(values map[string]float64) func(call) answer {
	return func(call) answer { return answer{values: values} }
}

// file finds one file in a result.
func file(t *testing.T, res Result, path string) File {
	t.Helper()
	for _, f := range res.Files {
		if f.Path == path {
			return f
		}
	}
	t.Fatalf("the result has no file %s: %+v", path, res.Files)
	return File{}
}

func ids(qs []questions.Question) []string {
	var out []string
	for _, q := range qs {
		out = append(out, q.ID)
	}
	return out
}

// Contract: gate/G1
func TestRunFailsWhenTheGateReachesItsThresholdOnAnyFile(t *testing.T) {
	t.Parallel()
	r := newRepo(t, map[string]string{"a.go": numbered(5), "b.go": numbered(5)})
	r.write("a.go", edited(5, 3))
	r.write("b.go", edited(5, 3))
	f := newFake(t, func(c call) answer {
		if strings.Contains(c.change(), "b.go") {
			return answer{values: map[string]float64{"push_back": 0.7}}
		}
		return answer{values: map[string]float64{"push_back": 0.3}}
	})

	res := mustRun(t, r.options(f))

	if res.Passed() {
		t.Error("the run passed with push_back at 0.7 on b.go")
	}
	if got := file(t, res, "a.go").Failed; len(got) != 0 {
		t.Errorf("a.go failed %v at 0.3, want nothing", got)
	}
	if got := file(t, res, "b.go").Failed; !reflect.DeepEqual(got, []string{"push_back"}) {
		t.Errorf("b.go failed %v, want push_back", got)
	}
	if want := r.git("rev-parse", "main"); res.Base != want {
		t.Errorf("base = %q, want main's commit %q", res.Base, want)
	}
	if !reflect.DeepEqual(ids(res.Questions), builtinIDs) {
		t.Errorf("questions = %v, want the built-in set in order", ids(res.Questions))
	}
}

// Contract: gate/G1
func TestRunPassesWhenNoGateReachesItsThreshold(t *testing.T) {
	t.Parallel()
	r := newRepo(t, map[string]string{"a.go": numbered(5), "b.go": numbered(5)})
	r.write("a.go", edited(5, 3))
	r.write("b.go", edited(5, 3))
	// A describing question and a diagnosis sit well over 0.6. Neither gates.
	f := newFake(t, says(map[string]float64{"push_back": 0.3, "simplified": 0.95, "added_copies": 0.95}))

	res := mustRun(t, r.options(f))

	if !res.Passed() {
		t.Errorf("the run failed with push_back at 0.3: %+v", res.Files)
	}
	for _, f := range res.Files {
		if len(f.Failed) != 0 {
			t.Errorf("%s failed %v, want nothing", f.Path, f.Failed)
		}
	}
}

// Contract: gate/G1
func TestRunFailsWhenTheGateIsExactlyAtItsThreshold(t *testing.T) {
	t.Parallel()
	r := oneChange(t)
	f := newFake(t, says(map[string]float64{"push_back": 0.6}))

	res := mustRun(t, r.options(f))

	if got := file(t, res, "a.go").Failed; !reflect.DeepEqual(got, []string{"push_back"}) {
		t.Errorf("failed %v at exactly 0.6, want push_back", got)
	}
}

// Contract: gate/G1
func TestRunListsEveryGateThatFiredAgainstItsOwnThresholdInQuestionOrder(t *testing.T) {
	t.Parallel()
	config := `{"questions": [
		{"id": "added_flag", "type": "noul", "instructions": "Did the change add a flag?", "gates": true, "threshold": 0.8},
		{"id": "hard_to_trace", "type": "score", "instructions": "How hard is it to trace?",
		 "criteria": ["easy", "middling", "hard"], "gates": true, "threshold": 0.4}]}`
	cases := []struct {
		name   string
		values map[string]float64
		want   []string
	}{
		{"all three", map[string]float64{"push_back": 0.9, "added_flag": 0.9, "hard_to_trace": 0.5}, []string{"push_back", "added_flag", "hard_to_trace"}},
		{"only the config's noul", map[string]float64{"push_back": 0.2, "added_flag": 0.9, "hard_to_trace": 0}, []string{"added_flag"}},
		{"over 0.6 but under its own 0.8", map[string]float64{"push_back": 0.2, "added_flag": 0.7, "hard_to_trace": 0}, nil},
		{"only the score", map[string]float64{"push_back": 0.2, "added_flag": 0.2, "hard_to_trace": 0.5}, []string{"hard_to_trace"}},
	}
	// Shared: each case has its own fake Jev and its own cache.
	r := newRepo(t, map[string]string{"a.go": numbered(5), "qualm.json": config})
	r.write("a.go", edited(5, 3))
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFake(t, says(tc.values))

			res := mustRun(t, r.options(f))

			if got := file(t, res, "a.go").Failed; !reflect.DeepEqual(got, tc.want) {
				t.Errorf("failed %v, want %v", got, tc.want)
			}
			if res.Passed() != (tc.want == nil) {
				t.Errorf("passed = %v with %v failing", res.Passed(), tc.want)
			}
		})
	}
}

// Contract: gate/G2
func TestRunThresholdReplacesTheGatesThresholdForTheRun(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name      string
		config    string
		threshold float64
		values    map[string]float64
		want      []string
		wantGate  float64
	}{
		{"none given keeps the default", `{}`, 0, map[string]float64{"push_back": 0.4}, nil, 0.6},
		{"given lowers the default", `{}`, 0.3, map[string]float64{"push_back": 0.4}, []string{"push_back"}, 0.3},
		{"none given keeps the config's", `{"gate": {"threshold": 0.3}}`, 0, map[string]float64{"push_back": 0.4}, []string{"push_back"}, 0.3},
		{"given replaces the config's", `{"gate": {"threshold": 0.3}}`, 0.9, map[string]float64{"push_back": 0.4}, nil, 0.9},
		{"given goes to the gate the config names", `{"gate": {"question": "simplified", "threshold": 0.5}}`, 0.2,
			map[string]float64{"push_back": 0.99, "simplified": 0.3}, []string{"simplified"}, 0.2},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := newRepo(t, map[string]string{"a.go": numbered(5), "qualm.json": tc.config})
			r.write("a.go", edited(5, 3))
			f := newFake(t, says(tc.values))
			o := r.options(f)
			o.Threshold = tc.threshold

			res := mustRun(t, o)

			if got := file(t, res, "a.go").Failed; !reflect.DeepEqual(got, tc.want) {
				t.Errorf("failed %v, want %v", got, tc.want)
			}
			for _, q := range res.Questions {
				if q.Gates && q.Threshold != tc.wantGate {
					t.Errorf("the result reports %s gating at %v, want %v", q.ID, q.Threshold, tc.wantGate)
				}
			}
		})
	}
}

// skips is a repository with one change of every kind that is skipped, and lib/a.rb, which is
// judged. It returns the reason each skipped path should carry.
func skips(t *testing.T) (*repo, map[string]string) {
	t.Helper()
	r := newRepo(t, map[string]string{
		".gitattributes": "gen/** linguist-generated\next/** linguist-vendored=true\n",
		"qualm.json":     `{"skip": ["db/schema.rb", "**/*.snap.go"]}`,
		"tool.sh":        "echo hi\n",
	})
	binary := "\x00\x01\x02 not text \x00"
	r.write("lib/a.rb", "puts 1\n")
	reasons := map[string]string{}
	for path, why := range map[string][2]string{
		"README.md":         {"# hi\n", "prose or data"},
		"vendor/dep/dep.go": {"package dep\n", "vendored or built"},
		"go.sum":            {"sum\n", "lockfile or generated"},
		"lib/a_test.rb":     {"assert true\n", "test"},
		"db/schema.rb":      {"create_table\n", "skip rule db/schema.rb"},
		"ui/view.snap.go":   {"package ui\n", "skip rule **/*.snap.go"},
		"img/logo.dat":      {binary, "binary"},
		"gen/api.go":        {"package gen\n", "marked generated or vendored"},
		"ext/lib.go":        {"package ext\n", "marked generated or vendored"},
		// A rule is looked at before the content, and the content before the mark.
		"vendor/blob.go": {binary, "vendored or built"},
		"gen/blob.go":    {binary, "binary"},
	} {
		r.write(path, why[0])
		reasons[path] = why[1]
	}
	// A change of mode alone leaves git with no lines to show.
	if err := os.Chmod(r.path("tool.sh"), 0o755); err != nil {
		t.Fatal(err)
	}
	reasons["tool.sh"] = "no content change"
	return r, reasons
}

// Contract: gate/G3
func TestRunSkipsWhatARuleTheContentOrAMarkRulesOutAndSendsNoneOfIt(t *testing.T) {
	t.Parallel()
	r, reasons := skips(t)
	f := newFake(t, nil)

	res := mustRun(t, r.options(f))

	if len(res.Files) != len(reasons)+1 {
		t.Errorf("the result lists %v, want the %d skipped files and lib/a.rb", paths(res), len(reasons))
	}
	for path, why := range reasons {
		got := file(t, res, path)
		if got.Status != "skipped" || got.Reason != why {
			t.Errorf("%s is %s (%q), want skipped (%q)", path, got.Status, got.Reason, why)
		}
		if got.Answers != nil || len(got.Failed) != 0 {
			t.Errorf("%s has answers %v, want none for a skipped file", path, got.Answers)
		}
	}
	if got := file(t, res, "lib/a.rb"); got.Status != "judged" || got.Reason != "" {
		t.Errorf("lib/a.rb is %s (%q), want judged", got.Status, got.Reason)
	}
	if f.count() != 1 || f.received()[0].change() != r.diff("lib/a.rb") {
		t.Errorf("made %d requests, want one for lib/a.rb alone", f.count())
	}
	if !res.Passed() {
		t.Error("the run failed, want skipped files to leave it passing")
	}
}

// Contract: gate/G3
func TestRunReportsTheSizeOfEveryDiffItHas(t *testing.T) {
	t.Parallel()
	r, _ := skips(t)
	f := newFake(t, nil)

	res := mustRun(t, r.options(f))

	for _, path := range []string{"lib/a.rb", "README.md", "vendor/dep/dep.go", "gen/api.go"} {
		if got, want := file(t, res, path).Bytes, len(r.diff(path)); got != want || want == 0 {
			t.Errorf("%s is %d bytes, want the %d of its diff", path, got, want)
		}
	}
	for _, path := range []string{"img/logo.dat", "tool.sh"} {
		if got := file(t, res, path).Bytes; got != 0 {
			t.Errorf("%s is %d bytes, want 0 for a change with no diff", path, got)
		}
	}
}

// Contract: gate/G3
func TestRunJudgesTestsWhenAskedTo(t *testing.T) {
	t.Parallel()
	r, _ := skips(t)
	f := newFake(t, nil)
	o := r.options(f)
	o.IncludeTests = true

	res := mustRun(t, o)

	if got := file(t, res, "lib/a_test.rb"); got.Status != "judged" || got.Reason != "" {
		t.Errorf("lib/a_test.rb is %s (%q), want judged", got.Status, got.Reason)
	}
	if f.count() != 2 {
		t.Errorf("made %d requests, want one each for lib/a.rb and lib/a_test.rb", f.count())
	}
}

// Contract: gate/G4
func TestRunKeepsAFileWhoseExactChangeAKeepNamesAndJudgesItOnceItChanges(t *testing.T) {
	t.Parallel()
	r := newRepo(t, map[string]string{"a.go": numbered(30)})
	r.write("a.go", edited(30, 15))
	keep := r.keepOf("a.go", "the long function is the point")
	r.keeps(keep)
	// Jev would fail the file, so only the keep can let it through.
	f := newFake(t, says(map[string]float64{"push_back": 0.95}))
	o := r.options(f)

	res := mustRun(t, o)

	got := file(t, res, "a.go")
	if got.Status != "kept" || got.Reason != "the long function is the point" {
		t.Errorf("a.go is %s (%q), want kept with the keep's reason", got.Status, got.Reason)
	}
	if got.Bytes != len(r.diff("a.go")) || got.Answers != nil || len(got.Failed) != 0 {
		t.Errorf("a.go = %+v, want its size and no answers", got)
	}
	if f.count() != 0 || !res.Passed() || len(res.StaleKeeps) != 0 {
		t.Errorf("made %d requests, passed %v, stale %v: want none, a pass and none", f.count(), res.Passed(), res.StaleKeeps)
	}

	r.write("a.go", edited(30, 15, 16))
	res = mustRun(t, o)

	got = file(t, res, "a.go")
	if got.Status != "judged" || got.Reason != "" || !reflect.DeepEqual(got.Failed, []string{"push_back"}) {
		t.Errorf("after another edit a.go is %s (%q) failing %v, want judged and failing push_back", got.Status, got.Reason, got.Failed)
	}
	if f.count() != 1 {
		t.Errorf("made %d requests after the edit, want 1", f.count())
	}
	if !reflect.DeepEqual(res.StaleKeeps, []config.Keep{keep}) {
		t.Errorf("stale keeps = %v, want the keep the edit left behind", res.StaleKeeps)
	}
}

// Contract: gate/G4
func TestRunDoesNotKeepAFileOnAKeepForAnotherPath(t *testing.T) {
	t.Parallel()
	r := oneChange(t)
	misfiled := r.keepOf("a.go", "fine")
	misfiled.Path = "b.go"
	r.keeps(misfiled)
	f := newFake(t, nil)

	res := mustRun(t, r.options(f))

	if got := file(t, res, "a.go"); got.Status != "judged" || f.count() != 1 {
		t.Errorf("a.go is %s after %d requests, want it judged: the keep names b.go", got.Status, f.count())
	}
	if !reflect.DeepEqual(res.StaleKeeps, []config.Keep{misfiled}) {
		t.Errorf("stale keeps = %v, want the keep for b.go", res.StaleKeeps)
	}
}

// Contract: gate/G4
func TestRunSkipsBeforeItKeeps(t *testing.T) {
	t.Parallel()
	r := newRepo(t, nil)
	r.write("lib/a_test.rb", "assert true\n")
	r.keeps(r.keepOf("lib/a_test.rb", "fine"))
	f := newFake(t, nil)

	res := mustRun(t, r.options(f))

	if got := file(t, res, "lib/a_test.rb"); got.Status != "skipped" || got.Reason != "test" {
		t.Errorf("lib/a_test.rb is %s (%q), want skipped as a test", got.Status, got.Reason)
	}
	if len(res.StaleKeeps) != 0 {
		t.Errorf("stale keeps = %v, want none: the keep still matches the change", res.StaleKeeps)
	}
}

// staleRepo holds one keep that still matches its change and three that don't: b.go has been
// edited since it was kept, c.go no longer differs from main, and gone.go isn't there at all.
func staleRepo(t *testing.T) (r *repo, live config.Keep, stale []config.Keep) {
	t.Helper()
	r = newRepo(t, map[string]string{"a.go": numbered(5), "b.go": numbered(5), "c.go": numbered(5)})
	r.write("a.go", edited(5, 3))
	r.write("b.go", edited(5, 3))
	r.write("c.go", edited(5, 3))
	live = r.keepOf("a.go", "kept a")
	stale = []config.Keep{
		r.keepOf("b.go", "kept b"),
		r.keepOf("c.go", "kept c"),
		{Path: "gone.go", Change: ChangeHash("a diff that was"), Reason: "kept gone", Date: "2026-09-01"},
	}
	r.write("b.go", edited(5, 3, 4))
	r.write("c.go", numbered(5))
	r.keeps(stale[0], live, stale[1], stale[2])
	return r, live, stale
}

// Contract: gate/G5
func TestRunReportsKeepsThatMatchNoCurrentChangeAndStillPasses(t *testing.T) {
	t.Parallel()
	r, _, stale := staleRepo(t)
	f := newFake(t, nil)

	res := mustRun(t, r.options(f))

	if !reflect.DeepEqual(res.StaleKeeps, stale) {
		t.Errorf("stale keeps = %v, want %v", res.StaleKeeps, stale)
	}
	if !res.Passed() {
		t.Error("the run failed, want stale keeps to leave it passing")
	}
	if a, b := file(t, res, "a.go"), file(t, res, "b.go"); a.Status != "kept" || b.Status != "judged" {
		t.Errorf("a.go is %s and b.go is %s, want kept and judged", a.Status, b.Status)
	}
}

// Contract: gate/G5
func TestRunNarrowedToPathsListsOnlyThoseAndCallsNoKeepStale(t *testing.T) {
	t.Parallel()
	r, _, _ := staleRepo(t)
	f := newFake(t, nil)
	o := r.options(f)
	o.Paths = []string{"b.go"}

	res := mustRun(t, o)

	if !reflect.DeepEqual(paths(res), []string{"b.go"}) {
		t.Errorf("files = %v, want b.go alone", paths(res))
	}
	if len(res.StaleKeeps) != 0 {
		t.Errorf("stale keeps = %v, want none from a run that can't see every change", res.StaleKeeps)
	}
}

// Contract: gate/G8
func TestDryRunSendsNothingAndListsEveryFileWithItsStatusAndSize(t *testing.T) {
	t.Parallel()
	r, live, stale := staleRepo(t)
	r.write("README.md", "# hi\n")
	f := newFake(t, nil)
	for name, client := range map[string]*jev.Client{"with no client": nil, "with a client": f.client} {
		t.Run(name, func(t *testing.T) {
			o := Options{Dir: r.dir, DryRun: true, CacheDir: t.TempDir(), Client: client}

			res, err := Run(context.Background(), o)

			if err != nil {
				t.Fatalf("Run: %v", err)
			}
			want := []File{
				{Path: "README.md", Status: "skipped", Reason: "prose or data", Bytes: len(r.diff("README.md"))},
				{Path: "a.go", Status: "kept", Reason: live.Reason, Bytes: len(r.diff("a.go"))},
				{Path: "b.go", Status: "judged", Bytes: len(r.diff("b.go"))},
				{Path: "qualm.json", Status: "skipped", Reason: "prose or data", Bytes: len(r.diff("qualm.json"))},
			}
			if !reflect.DeepEqual(res.Files, want) {
				t.Errorf("files = %+v, want %+v", res.Files, want)
			}
			if !res.DryRun || res.Usage != (Usage{}) || f.count() != 0 {
				t.Errorf("dry run %v, usage %+v, %d requests: want a dry run that spent and sent nothing", res.DryRun, res.Usage, f.count())
			}
			if res.Base != r.git("rev-parse", "main") || len(res.Questions) != 10 || !reflect.DeepEqual(res.StaleKeeps, stale) {
				t.Errorf("base %q, %d questions, stale %v: want what a full run reports", res.Base, len(res.Questions), res.StaleKeeps)
			}
			if got := entries(t, o.CacheDir); len(got) != 0 {
				t.Errorf("a dry run wrote %d cache entries", len(got))
			}
		})
	}
}

// Contract: gate/G10
func TestRunPassesWithNothingToJudgeAndNeedsNoClient(t *testing.T) {
	t.Parallel()
	cases := map[string]func(r *repo){
		"no changes":         func(r *repo) {},
		"only skipped files": func(r *repo) { r.write("README.md", "# hi\n"); r.write("lib/a_test.rb", "assert true\n") },
		"only kept files": func(r *repo) {
			r.write("a.go", edited(5, 3))
			r.keeps(r.keepOf("a.go", "fine"))
		},
	}
	for name, change := range cases {
		t.Run(name, func(t *testing.T) {
			r := newRepo(t, map[string]string{"a.go": numbered(5)})
			change(r)

			res, err := Run(context.Background(), Options{Dir: r.dir, CacheDir: t.TempDir()})

			if err != nil {
				t.Fatalf("Run: %v", err)
			}
			if !res.Passed() || res.DryRun || res.Usage != (Usage{}) {
				t.Errorf("passed %v, dry run %v, usage %+v: want a pass that cost nothing", res.Passed(), res.DryRun, res.Usage)
			}
			for _, f := range res.Files {
				if f.Status == "judged" {
					t.Errorf("%s was judged, want nothing judged", f.Path)
				}
			}
			if res.Base != r.git("rev-parse", "main") || len(res.Questions) != 10 {
				t.Errorf("base %q and %d questions, want them reported all the same", res.Base, len(res.Questions))
			}
		})
	}
}

// refused runs a check that has to end in an error, and holds it to reporting no result.
func refused(t *testing.T, o Options, want string) {
	t.Helper()
	res, err := Run(context.Background(), o)
	if err == nil || !strings.Contains(err.Error(), want) {
		t.Errorf("err = %v, want one that mentions %q", err, want)
	}
	if !reflect.DeepEqual(res, Result{}) {
		t.Errorf("result = %+v, want none beside an error", res)
	}
}

// Contract: gate/G9
func TestRunNeedsAClientWhenThereIsSomethingToJudge(t *testing.T) {
	t.Parallel()
	r := oneChange(t)

	refused(t, Options{Dir: r.dir, CacheDir: t.TempDir()}, "client")
}

// Contract: gate/G9
func TestRunStopsAtJevsFirstErrorAndSendsNothingMore(t *testing.T) {
	t.Parallel()
	r, _ := manyChanges(t, 3)
	f := newFake(t, func(call) answer { return answer{status: 400} })
	o := r.options(f)
	o.Jobs = 1

	refused(t, o, "400")

	if f.count() != 1 {
		t.Errorf("made %d requests, want the first error to stop the other two files", f.count())
	}
}

// Contract: gate/G9
func TestRunStopsOnAnErrorFromAnyWorker(t *testing.T) {
	t.Parallel()
	r, _ := manyChanges(t, 6)
	f := newFake(t, func(c call) answer {
		if strings.Contains(c.change(), "d.go") {
			return answer{status: 400}
		}
		return answer{}
	})
	o := r.options(f)
	o.Jobs = 4

	refused(t, o, "400")
}

// Contract: gate/G9
func TestRunStopsWhenTheBudgetIsSpent(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name         string
		budget, cost float64
		want         int
	}{
		{"a budget of three cents and requests of two", 0.03, 0.02, 2},
		{"no budget given, which is a dollar, and requests of fifty cents", 0, 0.5, 2},
		{"a budget of two dollars and requests of fifty cents", 2, 0.5, 4},
	}
	r, _ := manyChanges(t, 5)
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFake(t, func(call) answer { return answer{cost: tc.cost} })
			o := r.options(f)
			o.Jobs, o.Budget = 1, tc.budget

			refused(t, o, "budget")

			if f.count() != tc.want {
				t.Errorf("made %d requests, want %d before the budget ran out", f.count(), tc.want)
			}
		})
	}
}

// Contract: gate/G9
func TestRunStopsWhenAskingAgainFails(t *testing.T) {
	t.Parallel()
	cases := map[string]func(call) answer{
		"a close call's second request": func(c call) answer {
			if c.Nth == 2 {
				return answer{status: 400}
			}
			return answer{values: map[string]float64{"push_back": 0.6}}
		},
		"a hunk's request": func(c call) answer {
			if !whole(c) {
				return answer{status: 400}
			}
			return answer{values: map[string]float64{"push_back": 0.9, "added_copies": 0.9}}
		},
	}
	r := threeHunks(t)
	for name, reply := range cases {
		t.Run(name, func(t *testing.T) {
			f := newFake(t, reply)
			o := r.options(f)
			o.Jobs = 1 // one hunk at a time, so the count of requests is exact

			refused(t, o, "400")

			if f.count() != 2 {
				t.Errorf("made %d requests, want the run to stop at the second", f.count())
			}
		})
	}
}

// Contract: gate/G9
func TestRunStopsBeforeAskingWhenTheRepositoryOrTheConfigIsWrong(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		config string
		base   string
		want   string
	}{
		{"an unknown key in qualm.json", `{"modle": "x"}`, "", "qualm.json"},
		{"a drop of a question that isn't there", `{"drop": ["nope"]}`, "", "nope"},
		{"a base that names no commit", `{}`, "no-such-ref", "no-such-ref"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := newRepo(t, map[string]string{"a.go": numbered(5), "qualm.json": tc.config})
			r.write("a.go", edited(5, 3))
			f := newFake(t, nil)
			o := r.options(f)
			o.Base = tc.base

			refused(t, o, tc.want)

			if f.count() != 0 {
				t.Errorf("made %d requests, want none", f.count())
			}
		})
	}
}

// Contract: gate/G9
func TestRunSaysAMistakeInTheConfigIsInQualmJSON(t *testing.T) {
	t.Parallel()
	cases := map[string]string{
		"a key qualm doesn't know":              `{"modle": "x"}`,
		"a drop of a question that isn't there": `{"drop": ["nope"]}`,
		"a gate on a question that isn't there": `{"gate": {"question": "nope"}}`,
		"a question with no instructions":       `{"questions": [{"id": "mine", "type": "noul"}]}`,
		"no question left to gate":              `{"drop": ["push_back"]}`,
	}
	// Shared: the config is read from the working tree, so each case writes its own in turn.
	r := oneChange(t)
	f := newFake(t, nil)
	for name, config := range cases {
		t.Run(name, func(t *testing.T) {
			r.write("qualm.json", config)

			_, err := Run(context.Background(), r.options(f))

			if err == nil || !strings.HasPrefix(err.Error(), "qualm.json: ") || strings.Count(err.Error(), "qualm.json") != 1 {
				t.Errorf("err = %v, want one that starts with the file's name, said once", err)
			}
		})
	}
}
