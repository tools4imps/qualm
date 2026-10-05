package check

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/tools4imps/qualm/internal/config"
)

// keeps writes qualm.json to the working tree with these keeps and nothing else.
func (r *repo) keeps(keeps ...config.Keep) {
	r.t.Helper()
	if err := (config.Config{Keeps: keeps}).Save(r.dir); err != nil {
		r.t.Fatal(err)
	}
}

// keepOf is a keep for a path's change as it stands now.
func (r *repo) keepOf(path, reason string) config.Keep {
	r.t.Helper()
	return config.Keep{Path: path, Change: changeHash(r.diff(path)), Reason: reason, Date: "2026-10-01"}
}

// keepOptions are the options the keep command runs with: no client, since keeping asks nothing.
func (r *repo) keepOptions() Options {
	return Options{Dir: r.dir, Now: func() time.Time { return fixedNow }}
}

// saved is the config as it stands on disk.
// keepPaths calls Keep for the given paths, which Keep reads from the options.
func keepPaths(o Options, paths []string, reason string) ([]config.Keep, error) {
	o.Paths = paths
	return Keep(o, reason)
}

func (r *repo) saved() config.Config {
	r.t.Helper()
	cfg, err := config.Load(r.dir)
	if err != nil {
		r.t.Fatal(err)
	}
	return cfg
}

// Contract: gate/G6
func TestKeepRecordsThePathTheHashTheReasonAndTheDateOfEachPath(t *testing.T) {
	t.Parallel()
	r := newRepo(t, map[string]string{"lib/a.rb": numbered(5), "lib/b.rb": numbered(5)})
	r.write("lib/a.rb", edited(5, 2))
	r.write("lib/b.rb", edited(5, 4))

	got, err := keepPaths(r.keepOptions(), []string{"lib/b.rb", "./lib/a.rb"}, "reviewed with Sam")

	if err != nil {
		t.Fatalf("Keep: %v", err)
	}
	want := []config.Keep{
		{Path: "lib/a.rb", Change: changeHash(r.diff("lib/a.rb")), Reason: "reviewed with Sam", Date: "2026-10-04"},
		{Path: "lib/b.rb", Change: changeHash(r.diff("lib/b.rb")), Reason: "reviewed with Sam", Date: "2026-10-04"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Keep returned %v, want %v", got, want)
	}
	if saved := r.saved().Keeps; !reflect.DeepEqual(saved, want) {
		t.Errorf("qualm.json holds %v, want %v", saved, want)
	}
}

// Contract: gate/G6
func TestKeepReplacesAnEarlierKeepDropsStaleOnesAndLeavesTheRestOfTheConfig(t *testing.T) {
	t.Parallel()
	r := newRepo(t, map[string]string{"a.go": numbered(5), "b.go": numbered(5), "z.go": numbered(5)})
	for _, name := range []string{"a.go", "b.go", "z.go"} {
		r.write(name, edited(5, 3))
	}
	live := r.keepOf("z.go", "kept z")
	earlier := config.Keep{Path: "b.go", Change: "0b", Reason: "kept b before", Date: "2026-01-01"}
	current := r.keepOf("a.go", "kept a before")
	gone := config.Keep{Path: "gone.go", Change: "0d", Reason: "kept gone", Date: "2026-01-01"}
	before := config.Config{Model: "typesafe/jev-2", Skip: []string{"db/*"}, Keeps: []config.Keep{live, earlier, gone, current}}
	if err := before.Save(r.dir); err != nil {
		t.Fatal(err)
	}

	got, err := keepPaths(r.keepOptions(), []string{"b.go", "a.go", "./a.go"}, "kept again")

	if err != nil {
		t.Fatalf("Keep: %v", err)
	}
	want := []config.Keep{
		{Path: "a.go", Change: changeHash(r.diff("a.go")), Reason: "kept again", Date: "2026-10-04"},
		{Path: "b.go", Change: changeHash(r.diff("b.go")), Reason: "kept again", Date: "2026-10-04"},
		live,
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Keep returned %v, want %v", got, want)
	}
	after := before
	after.Keeps = want
	if saved := r.saved(); !reflect.DeepEqual(saved, after) {
		t.Errorf("qualm.json holds %+v, want %+v", saved, after)
	}
}

// Contract: gate/G6
func TestKeepDatesAKeepTodayWhenNoClockIsGiven(t *testing.T) {
	t.Parallel()
	r := oneChange(t)
	before := time.Now().Format("2006-01-02")

	got, err := keepPaths(Options{Dir: r.dir}, []string{"a.go"}, "fine")

	after := time.Now().Format("2006-01-02")
	if err != nil || len(got) != 1 || (got[0].Date != before && got[0].Date != after) {
		t.Errorf("Keep returned %v, %v: want one keep dated %s", got, err, after)
	}
}

// Contract: gate/G4
// Contract: gate/G6
func TestRunHonoursAKeepThatKeepRecorded(t *testing.T) {
	t.Parallel()
	r := oneChange(t)
	f := newFake(t, says(map[string]float64{"push_back": 0.95}))
	o := r.options(f)
	if res := mustRun(t, o); res.Passed() {
		t.Fatal("the run passed before the keep, want it failing")
	}

	if _, err := keepPaths(o, []string{"a.go"}, "it is right as it stands"); err != nil {
		t.Fatalf("Keep: %v", err)
	}
	res := mustRun(t, o)

	if got := file(t, res, "a.go"); got.Status != "kept" || got.Reason != "it is right as it stands" || !res.Passed() {
		t.Errorf("a.go is %s (%q) and the run passed %v, want kept and passing", got.Status, got.Reason, res.Passed())
	}
}

// Contract: gate/G7
func TestKeepRefusesAndWritesNothing(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		paths  []string
		reason string
		want   string // what the error has to mention
	}{
		{"no reason", []string{"a.go"}, "", "reason"},
		{"a blank reason", []string{"a.go"}, " \t\n", "reason"},
		{"no paths", nil, "fine", "path"},
		{"an unchanged file", []string{"same.go"}, "fine", "same.go"},
		{"a file that isn't there", []string{"nope.go"}, "fine", "nope.go"},
		{"a changed file with no diff", []string{"logo.dat"}, "fine", "logo.dat"},
		{"a good path beside a bad one", []string{"a.go", "same.go"}, "fine", "same.go"},
	}
	configs := map[string]string{
		"with no qualm.json":   "",
		"with a qualm.json":    "{\"model\":   \"typesafe/jev-2\",\n\"keeps\": []}",
		"with an earlier keep": `{"keeps": [{"path": "a.go", "change": "0f", "reason": "before", "date": "2026-01-01"}]}`,
	}
	for state, config := range configs {
		// A refusal leaves the repository as it was, so the cases can share one.
		r := newRepo(t, map[string]string{"a.go": numbered(5), "same.go": numbered(5)})
		r.write("a.go", edited(5, 3))
		r.write("logo.dat", "\x00\x01\x02")
		if config != "" {
			r.write("qualm.json", config)
		}
		for _, tc := range cases {
			t.Run(tc.name+" "+state, func(t *testing.T) {
				got, err := keepPaths(r.keepOptions(), tc.paths, tc.reason)

				if err == nil || !strings.Contains(err.Error(), tc.want) || got != nil {
					t.Errorf("Keep returned %v, %v: want no keeps and an error naming %q", got, err, tc.want)
				}
				data, err := os.ReadFile(r.path("qualm.json"))
				if config == "" && !os.IsNotExist(err) {
					t.Errorf("qualm.json was written: %q", data)
				}
				if config != "" && string(data) != config {
					t.Errorf("qualm.json changed to %q, want it byte for byte as it was", data)
				}
			})
		}
	}
}

// Contract: gate/G4
func TestChangeHashCoversTheLinesAddedAndRemovedAndNothingElse(t *testing.T) {
	t.Parallel()
	const diff = "--- a/x.sql\n+++ b/x.sql\n@@ -3,4 +3,4 @@ create\n one\n-two\n+2\n three\n@@ -40,2 +40,3 @@\n forty\n+more\n"
	sum := sha256.Sum256([]byte("-two\n+2\n+more\n"))
	want := hex.EncodeToString(sum[:])
	if got := changeHash(diff); got != want {
		t.Errorf("changeHash = %s, want %s, the SHA-256 of the three changed lines in lowercase hex", got, want)
	}
	same := map[string]string{
		"other line numbers":    strings.NewReplacer("-3,4 +3,4", "-9,4 +9,4", "-40,2 +40,3", "-46,2 +46,3").Replace(diff),
		"other lines around it": strings.NewReplacer(" one\n", " uno\n", " forty\n", " 40\n", " create\n", " insert\n").Replace(diff),
		"another name":          strings.ReplaceAll(diff, "x.sql", "y.sql"),
		"a note from git":       diff + "\\ No newline at end of file\n",
	}
	for name, other := range same {
		if changeHash(other) != want {
			t.Errorf("%s changed the hash, want it left out", name)
		}
	}
	differs := map[string]string{
		"a changed line edited":          strings.Replace(diff, "+2\n", "+two!\n", 1),
		"a changed line dropped":         strings.Replace(diff, "+more\n", "", 1),
		"the changed lines reordered":    strings.Replace(diff, "-two\n+2\n", "+2\n-two\n", 1),
		"a removed line that starts --":  strings.Replace(diff, "-two\n", "-two\n--- a comment in SQL\n", 1),
		"an added line that starts ++":   strings.Replace(diff, "+2\n", "+2\n+++ b\n", 1),
		"a line added to the last hunk":  diff + "+and more\n",
		"a line added with no line end":  diff + "+and more",
		"an added line become a removal": strings.Replace(diff, "+more\n", "-more\n", 1),
	}
	for name, other := range differs {
		if changeHash(other) == want {
			t.Errorf("%s left the hash the same", name)
		}
	}
}
