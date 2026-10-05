package cli

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/tools4imps/qualm/internal/config"
)

// TestMain cuts the tests off from the developer's git configuration and home. The code under
// test runs git itself, so the whole test process has to be cut off and not only the git commands
// the tests run.
func TestMain(m *testing.M) {
	home, err := os.MkdirTemp("", "qualm-cli-")
	if err != nil {
		panic(err)
	}
	for name, value := range map[string]string{
		"HOME":              home,
		"XDG_CONFIG_HOME":   filepath.Join(home, "config"),
		"GIT_CONFIG_GLOBAL": os.DevNull,
		"GIT_CONFIG_SYSTEM": os.DevNull,
	} {
		os.Setenv(name, value)
	}
	// A test run from a git hook inherits these, and they would point every git command at the
	// hook's repository.
	for _, name := range []string{"GIT_DIR", "GIT_WORK_TREE", "GIT_INDEX_FILE"} {
		os.Unsetenv(name)
	}
	code := m.Run()
	os.RemoveAll(home)
	os.Exit(code)
}

// fakeJev answers every question in a request in Jev's raw reply format.
type fakeJev struct {
	*httptest.Server
	mu       sync.Mutex
	pushBack float64
	status   int // 0 for 200
	requests atomic.Int32
}

func newFakeJev(t *testing.T, pushBack float64) *fakeJev {
	t.Helper()
	f := &fakeJev{pushBack: pushBack}
	f.Server = httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.Close)
	return f
}

func (f *fakeJev) serve(w http.ResponseWriter, r *http.Request) {
	f.requests.Add(1)
	f.mu.Lock()
	status, push := f.status, f.pushBack
	f.mu.Unlock()
	if status != 0 {
		http.Error(w, "no", status)
		return
	}
	body, _ := io.ReadAll(r.Body)
	var req struct {
		Questions map[string]struct {
			Type string `json:"type"`
		} `json:"questions"`
	}
	_ = json.Unmarshal(body, &req)
	answers := map[string]any{}
	for id, q := range req.Questions {
		switch q.Type {
		case "noul":
			v := 0.1
			if id == "push_back" {
				v = push
			}
			answers[id] = map[string]any{"type": "noul", "noul": v}
		case "score":
			answers[id] = map[string]any{"type": "score", "score": 0.0, "probabilities": map[string]float64{"0": 1}}
		default:
			answers[id] = map[string]any{"type": "choice", "choice": "same",
				"probabilities": map[string]float64{"harder": 0.1, "same": 0.8, "easier": 0.1}}
		}
	}
	_ = json.NewEncoder(w).Encode(map[string]any{
		"answers": answers,
		"usage":   map[string]any{"input_tokens": 100, "output_tokens": 5, "cost": 0.0001},
	})
}

func (f *fakeJev) count() int { return int(f.requests.Load()) }

func git(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
}

// newRepo builds a repository on main with one commit, then a feature branch where lib/a.go
// is changed.
func newRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	git(t, dir, "init", "-q", "-b", "main")
	git(t, dir, "config", "user.name", "Test")
	git(t, dir, "config", "user.email", "test@example.com")
	git(t, dir, "config", "commit.gpgsign", "false")
	write(t, dir, "lib/a.go", "package lib\n\nfunc A() int { return 1 }\n")
	git(t, dir, "add", ".")
	git(t, dir, "commit", "-q", "-m", "first")
	git(t, dir, "checkout", "-q", "-b", "feature")
	write(t, dir, "lib/a.go", "package lib\n\nfunc A() int {\n\tif true {\n\t\treturn 2\n\t}\n\treturn 3\n}\n")
	return dir
}

func write(t *testing.T, dir, name, content string) {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

type outcome struct {
	code           int
	stdout, stderr string
}

type world struct {
	dir  string
	fake *fakeJev
	key  string
}

func newWorld(t *testing.T, pushBack float64) *world {
	return &world{dir: newRepo(t), fake: newFakeJev(t, pushBack), key: "test-key"}
}

// run calls Run in the world with a private cache unless the arguments name one.
func (w *world) run(t *testing.T, args ...string) outcome {
	t.Helper()
	return w.runIn(t, w.dir, args...)
}

func (w *world) runIn(t *testing.T, dir string, args ...string) outcome {
	t.Helper()
	var out, errb bytes.Buffer
	getenv := func(k string) string {
		if k == "OPENROUTER_API_KEY" {
			return w.key
		}
		return ""
	}
	full := append([]string{"--cache", t.TempDir()}, args...)
	for _, a := range args {
		if a == "--cache" {
			full = args
		}
	}
	code := Run(full, Env{Dir: dir, Getenv: getenv, Stdout: &out, Stderr: &errb, Endpoint: w.fake.URL})
	return outcome{code, out.String(), errb.String()}
}

// A cache shared between runs of one test, for the keep case where "no request" must be proved.
func (w *world) runCached(t *testing.T, cache string, args ...string) outcome {
	t.Helper()
	return w.run(t, append([]string{"--cache", cache}, args...)...)
}

func TestVersionIsExported(t *testing.T) {
	if Version != "0.1.0" {
		t.Fatalf("Version = %q", Version)
	}
}

// Contract: cli/L1
func TestKeepRecordsAndALaterCheckSkipsTheFile(t *testing.T) {
	w := newWorld(t, 0.9)
	cache := t.TempDir()
	o := w.runCached(t, cache, "keep", "lib/a.go", "--reason", "it is fine")
	if o.code != 0 || !strings.Contains(o.stdout, "qualm: kept lib/a.go") {
		t.Fatalf("keep: %+v", o)
	}
	cfg, err := config.Load(w.dir)
	if err != nil || len(cfg.Keeps) != 1 {
		t.Fatalf("config %+v err %v", cfg, err)
	}
	o = w.runCached(t, cache)
	if o.code != 0 || w.fake.count() != 0 {
		t.Fatalf("check after keep: %+v requests %d", o, w.fake.count())
	}
	if !strings.Contains(o.stdout, "lib/a.go") || !strings.Contains(o.stdout, "it is fine") {
		t.Errorf("kept file not listed:\n%s", o.stdout)
	}
}

// Contract: cli/L1
func TestCheckWithAPathJudgesThatPath(t *testing.T) {
	w := newWorld(t, 0.9)
	o := w.run(t, "lib/a.go")
	if o.code != 1 || !strings.Contains(o.stdout, "lib/a.go") {
		t.Fatalf("%+v", o)
	}
}

// Contract: cli/L1
func TestPathsAreRelativeToTheDirectoryRunFrom(t *testing.T) {
	w := newWorld(t, 0.9)
	o := w.runIn(t, filepath.Join(w.dir, "lib"), "a.go")
	if o.code != 1 || !strings.Contains(o.stdout, "lib/a.go") {
		t.Fatalf("%+v", o)
	}
	if o := w.runIn(t, filepath.Join(w.dir, "lib"), "../../elsewhere.go"); o.code != 2 {
		t.Fatalf("outside the repository: %+v", o)
	}
}

// Contract: cli/L2
func TestExitCodeFollowsTheGate(t *testing.T) {
	w := newWorld(t, 0.2)
	if o := w.run(t); o.code != 0 || !strings.Contains(o.stdout, "no qualms") {
		t.Fatalf("pass: %+v", o)
	}
	w = newWorld(t, 0.9)
	if o := w.run(t); o.code != 1 || !strings.Contains(o.stdout, "lib/a.go") {
		t.Fatalf("fail: %+v", o)
	}
	w = newWorld(t, 0.2)
	w.fake.status = http.StatusBadRequest
	if o := w.run(t); o.code != 2 {
		t.Fatalf("could not run: %+v", o)
	}
}

// Contract: cli/L3
func TestBadFlagsExitTwoWithUsage(t *testing.T) {
	cases := map[string][]string{
		"unknown flag":       {"--bogus"},
		"missing value":      {"--base"},
		"bad format":         {"--format", "xml"},
		"threshold zero":     {"--threshold", "0"},
		"threshold over 1":   {"--threshold", "1.5"},
		"threshold negative": {"--threshold", "-0.2"},
		"jobs zero":          {"--jobs", "0"},
		"budget zero":        {"--budget", "0"},
		"reason w/o keep":    {"--reason", "x"},
	}
	for name, args := range cases {
		t.Run(name, func(t *testing.T) {
			w := newWorld(t, 0.2)
			o := w.run(t, args...)
			if o.code != 2 || o.stdout != "" {
				t.Fatalf("%+v", o)
			}
			if !strings.HasPrefix(o.stderr, "qualm: ") || !strings.Contains(o.stderr, "usage: qualm") {
				t.Errorf("stderr:\n%s", o.stderr)
			}
			if w.fake.count() != 0 {
				t.Error("a request was made")
			}
		})
	}
}

// Contract: cli/L4
func TestUnknownBaseExitsTwo(t *testing.T) {
	w := newWorld(t, 0.2)
	o := w.run(t, "--base", "nosuchbranch")
	if o.code != 2 || o.stderr == "" || o.stdout != "" {
		t.Fatalf("%+v", o)
	}
}

// Contract: cli/L5
func TestMissingKeyExitsTwoButADryRunNeedsNone(t *testing.T) {
	w := newWorld(t, 0.2)
	w.key = ""
	o := w.run(t)
	if o.code != 2 || !strings.Contains(o.stderr, "OPENROUTER_API_KEY") {
		t.Fatalf("%+v", o)
	}
	o = w.run(t, "--dry-run")
	if o.code != 0 || w.fake.count() != 0 {
		t.Fatalf("dry run: %+v requests %d", o, w.fake.count())
	}
}

// Contract: cli/L6
func TestVersionAndHelpExitZeroAndWinOverTheRest(t *testing.T) {
	w := newWorld(t, 0.2)
	o := w.run(t, "--bogus", "--version")
	if o.code != 0 || o.stdout != "qualm 0.1.0\n" || o.stderr != "" {
		t.Fatalf("version: %+v", o)
	}
	for _, h := range []string{"--help", "-h"} {
		o = w.run(t, "--threshold", "9", h)
		if o.code != 0 || o.stderr != "" || o.stdout != usage {
			t.Fatalf("%s: %+v", h, o)
		}
	}
	if n := strings.Count(usage, "\n"); n > 28 {
		t.Errorf("usage is %d lines", n)
	}
}

// Contract: cli/L7
func TestReportsGoToStdoutAndErrorsToStderr(t *testing.T) {
	for _, push := range []float64{0.2, 0.9} {
		w := newWorld(t, push)
		if o := w.run(t); o.stderr != "" || o.stdout == "" {
			t.Errorf("push %v: %+v", push, o)
		}
	}
	w := newWorld(t, 0.2)
	w.fake.status = http.StatusBadRequest
	if o := w.run(t); o.stdout != "" || o.stderr == "" {
		t.Errorf("error: %+v", o)
	}
}

// Contract: cli/L8
func TestConfigMistakeExitsTwo(t *testing.T) {
	for name, config := range map[string]string{
		"a key qualm doesn't know":              `{"bogus": 1}`,
		"a drop of a question that isn't there": `{"drop": ["bogus"]}`,
	} {
		t.Run(name, func(t *testing.T) {
			w := newWorld(t, 0.2)
			write(t, w.dir, "qualm.json", config)
			o := w.run(t)
			if o.code != 2 || !strings.HasPrefix(o.stderr, "qualm: qualm.json: ") || o.stdout != "" {
				t.Fatalf("%+v", o)
			}
		})
	}
}

// Contract: cli/L9
func TestJSONFormat(t *testing.T) {
	w := newWorld(t, 0.2)
	o := w.run(t, "--format", "json")
	var got map[string]any
	if err := json.Unmarshal([]byte(o.stdout), &got); err != nil {
		t.Fatalf("%v\n%s", err, o.stdout)
	}
	if got["passed"] != true || o.code != 0 {
		t.Fatalf("%+v", o)
	}
}

// Contract: cli/L10
func TestFlagsMayComeBeforeOrAfterPaths(t *testing.T) {
	w := newWorld(t, 0.2)
	a := w.run(t, "--dry-run", "lib/a.go")
	b := w.run(t, "lib/a.go", "--dry-run")
	if a.code != 0 || a != b {
		t.Fatalf("check:\n%+v\n%+v", a, b)
	}
	k1 := w.run(t, "keep", "lib/a.go", "--reason", "x")
	k2 := w.run(t, "keep", "--reason", "x", "lib/a.go")
	if k1.code != 0 || k1 != k2 {
		t.Fatalf("keep:\n%+v\n%+v", k1, k2)
	}
}

// Contract: cli/L10
func TestEverythingAfterDoubleDashIsAPathEvenWhenItStartsWithADash(t *testing.T) {
	w := newWorld(t, 0.2)
	write(t, w.dir, "-odd.go", "package lib\n")

	without := w.run(t, "--dry-run", "-odd.go")
	with := w.run(t, "--dry-run", "--", "-odd.go")

	if without.code != 2 {
		t.Errorf("without --, -odd.go should be read as a flag: %+v", without)
	}
	if with.code != 0 || !strings.Contains(with.stdout, "-odd.go") || strings.Contains(with.stdout, "lib/a.go") {
		t.Errorf("with --, want only -odd.go listed: %+v", with)
	}
}

// Contract: cli/L10
func TestKeepIsRecognisedOnlyAsTheFirstPath(t *testing.T) {
	w := newWorld(t, 0.2)
	o := w.run(t, "lib/a.go", "keep", "--reason", "x")
	if o.code != 2 {
		t.Fatalf("keep after a path should be a stray --reason: %+v", o)
	}
}

func TestKeepNeedsPathsAndReason(t *testing.T) {
	w := newWorld(t, 0.2)
	cases := []struct {
		args []string
		want string // what check.Keep says
	}{
		{[]string{"keep", "--reason", "x"}, "qualm: a keep needs at least one path\n"},
		{[]string{"keep", "lib/a.go"}, "qualm: a keep needs a reason\n"},
	}
	for _, tc := range cases {
		if o := w.run(t, tc.args...); o.code != 2 || o.stdout != "" || o.stderr != tc.want {
			t.Errorf("%v: %+v, want exit 2, empty stdout and stderr %q", tc.args, o, tc.want)
		}
	}
}

func TestOutsideARepositoryExitsTwo(t *testing.T) {
	t.Setenv("GIT_CEILING_DIRECTORIES", filepath.Dir(t.TempDir()))
	w := &world{dir: t.TempDir(), fake: newFakeJev(t, 0.2), key: "k"}
	if o := w.run(t); o.code != 2 || o.stderr == "" {
		t.Fatalf("%+v", o)
	}
}
