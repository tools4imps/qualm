package check

import (
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
	return config.Keep{Path: path, Change: ChangeHash(r.diff(path)), Reason: reason, Date: "2026-10-01"}
}

// keepOptions are the options the keep command runs with: no client, since keeping asks nothing.
func (r *repo) keepOptions() Options {
	return Options{Dir: r.dir, Now: func() time.Time { return fixedNow }}
}

// saved is the config as it stands on disk.
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

	got, err := Keep(r.keepOptions(), []string{"lib/b.rb", "./lib/a.rb"}, "reviewed with Sam")

	if err != nil {
		t.Fatalf("Keep: %v", err)
	}
	want := []config.Keep{
		{Path: "lib/a.rb", Change: ChangeHash(r.diff("lib/a.rb")), Reason: "reviewed with Sam", Date: "2026-10-04"},
		{Path: "lib/b.rb", Change: ChangeHash(r.diff("lib/b.rb")), Reason: "reviewed with Sam", Date: "2026-10-04"},
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
	earlier := config.Keep{Path: "b.go", Change: ChangeHash("b.go as it was"), Reason: "kept b before", Date: "2026-01-01"}
	current := r.keepOf("a.go", "kept a before")
	gone := config.Keep{Path: "gone.go", Change: ChangeHash("gone"), Reason: "kept gone", Date: "2026-01-01"}
	before := config.Config{Model: "typesafe/jev-2", Skip: []string{"db/*"}, Keeps: []config.Keep{live, earlier, gone, current}}
	if err := before.Save(r.dir); err != nil {
		t.Fatal(err)
	}

	got, err := Keep(r.keepOptions(), []string{"b.go", "a.go", "./a.go"}, "kept again")

	if err != nil {
		t.Fatalf("Keep: %v", err)
	}
	want := []config.Keep{
		{Path: "a.go", Change: ChangeHash(r.diff("a.go")), Reason: "kept again", Date: "2026-10-04"},
		{Path: "b.go", Change: ChangeHash(r.diff("b.go")), Reason: "kept again", Date: "2026-10-04"},
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
	r := newRepo(t, map[string]string{"a.go": numbered(5)})
	r.write("a.go", edited(5, 3))
	before := time.Now().Format("2006-01-02")

	got, err := Keep(Options{Dir: r.dir}, []string{"a.go"}, "fine")

	after := time.Now().Format("2006-01-02")
	if err != nil || len(got) != 1 || (got[0].Date != before && got[0].Date != after) {
		t.Errorf("Keep returned %v, %v: want one keep dated %s", got, err, after)
	}
}

// Contract: gate/G4
// Contract: gate/G6
func TestRunHonoursAKeepThatKeepRecorded(t *testing.T) {
	t.Parallel()
	r := newRepo(t, map[string]string{"a.go": numbered(5)})
	r.write("a.go", edited(5, 3))
	f := newFake(t, says(map[string]float64{"push_back": 0.95}))
	o := r.options(f)
	if res := mustRun(t, o); res.Passed() {
		t.Fatal("the run passed before the keep, want it failing")
	}

	if _, err := Keep(o, []string{"a.go"}, "it is right as it stands"); err != nil {
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
				got, err := Keep(r.keepOptions(), tc.paths, tc.reason)

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
func TestChangeHashIsTheSHA256OfTheDiffInLowercaseHex(t *testing.T) {
	t.Parallel()
	for diff, want := range map[string]string{
		"":    "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
		"abc": "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad",
	} {
		if got := ChangeHash(diff); got != want {
			t.Errorf("ChangeHash(%q) = %s, want %s", diff, got, want)
		}
	}
}
