package check

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tools4imps/qualm/internal/gitdiff"
	"github.com/tools4imps/qualm/internal/jev"
)

// TestMain cuts the tests off from the developer's git configuration, home and cache directory.
// The code under test runs git and looks up the user cache directory itself, so scrubbing the
// environment of the test's own git commands would not be enough.
func TestMain(m *testing.M) {
	home, err := os.MkdirTemp("", "qualm-check-")
	if err != nil {
		panic(err)
	}
	for name, value := range map[string]string{
		"HOME":              home,
		"XDG_CONFIG_HOME":   filepath.Join(home, "config"),
		"XDG_CACHE_HOME":    filepath.Join(home, "cache"),
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

var fixedNow = time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC)

// repo is a temporary git repository on a feature branch cut from main.
type repo struct {
	t   *testing.T
	dir string
}

// newRepo commits files to main and checks out a feature branch, so whatever a test writes
// afterwards is a change against the base.
func newRepo(t *testing.T, files map[string]string) *repo {
	t.Helper()
	r := &repo{t: t, dir: t.TempDir()}
	r.git("init", "-q", "-b", "main")
	r.git("config", "user.name", "Test")
	r.git("config", "user.email", "test@example.com")
	r.git("config", "commit.gpgsign", "false")
	for name, content := range files {
		r.write(name, content)
	}
	r.commit("base")
	r.git("checkout", "-q", "-b", "feature")
	return r
}

func (r *repo) git(args ...string) string {
	r.t.Helper()
	out, err := exec.Command("git", append([]string{"-C", r.dir}, args...)...).CombinedOutput()
	if err != nil {
		r.t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

func (r *repo) commit(msg string) {
	r.t.Helper()
	r.git("add", "-A")
	r.git("commit", "-q", "--allow-empty", "-m", msg)
}

func (r *repo) path(name string) string { return filepath.Join(r.dir, filepath.FromSlash(name)) }

func (r *repo) write(name, content string) {
	r.t.Helper()
	if err := os.MkdirAll(filepath.Dir(r.path(name)), 0o755); err != nil {
		r.t.Fatal(err)
	}
	if err := os.WriteFile(r.path(name), []byte(content), 0o644); err != nil {
		r.t.Fatal(err)
	}
}

// diff is the normalised diff of one changed path against main, as gitdiff computes it.
func (r *repo) diff(path string) string {
	r.t.Helper()
	base, err := gitdiff.Base(r.dir, "main")
	if err != nil {
		r.t.Fatal(err)
	}
	changes, err := gitdiff.Changes(r.dir, base, nil)
	if err != nil {
		r.t.Fatal(err)
	}
	for _, c := range changes {
		if c.Path == path {
			return c.Diff
		}
	}
	r.t.Fatalf("%s is not a change against main", path)
	return ""
}

// options are the options every test starts from: this repository, the fake Jev, a cache of the
// test's own and a fixed clock.
func (r *repo) options(f *fakeJev) Options {
	return Options{
		Dir:      r.dir,
		CacheDir: r.t.TempDir(),
		Client:   f.client,
		Now:      func() time.Time { return fixedNow },
	}
}

func mustRun(t *testing.T, o Options) Result {
	t.Helper()
	res, err := Run(context.Background(), o)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	return res
}

// numbered returns n lines, "line 1" to "line n".
func numbered(n int) string {
	var b strings.Builder
	for i := 1; i <= n; i++ {
		fmt.Fprintf(&b, "line %d\n", i)
	}
	return b.String()
}

// edited returns numbered(n) with each of the given lines replaced by "changed <line>".
func edited(n int, lines ...int) string {
	text := numbered(n)
	for _, l := range lines {
		text = strings.Replace(text, fmt.Sprintf("line %d\n", l), fmt.Sprintf("changed %d\n", l), 1)
	}
	return text
}

// call is one request the fake Jev received.
type call struct {
	Model     string                    `json:"model"`
	State     map[string]any            `json:"state"`
	Questions map[string]map[string]any `json:"questions"`
	Body      string                    `json:"-"`
	Nth       int                       `json:"-"` // how many times this exact request has arrived, counting this one
}

func (c call) change() string {
	s, _ := c.State["change"].(string)
	return s
}

func (c call) ids() []string {
	var ids []string
	for id := range c.Questions {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

// answer is what a test tells the fake Jev to say to one request.
type answer struct {
	status  int                           // 0 for 200
	values  map[string]float64            // noul and score values from 0 to 1 by id, 0.1 when absent
	choices map[string]map[string]float64 // a choice's probabilities by id, mostly "same" when absent
	cost    float64                       // 0 for a tenth of a cent
}

// fakeJev stands in for Jev. It records every request and answers from a function the test gives.
type fakeJev struct {
	t      *testing.T
	client *jev.Client

	mu       sync.Mutex
	reply    func(call) answer
	together int
	linger   time.Duration
	calls    []call
	seen     map[string]int
	inFlight int
	peak     int
}

// newFake starts a fake Jev. A nil reply answers every request with the defaults.
func newFake(t *testing.T, reply func(call) answer) *fakeJev {
	t.Helper()
	f := &fakeJev{t: t, reply: reply, seen: map[string]int{}}
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	f.client = &jev.Client{Endpoint: srv.URL, Key: "test-key", Sleep: func(time.Duration) {}}
	return f
}

// say changes how the fake answers from here on.
func (f *fakeJev) say(reply func(call) answer) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.reply = reply
}

// holdUntil makes each request wait until n are in flight at once, then linger. Waiting makes a
// test of the limit independent of timing, and lingering gives a request over the limit time to
// show up.
func (f *fakeJev) holdUntil(n int, linger time.Duration) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.together, f.linger = n, linger
}

func (f *fakeJev) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

func (f *fakeJev) received() []call {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]call(nil), f.calls...)
}

// callWith returns the only request whose state.change is change.
func (f *fakeJev) callWith(change string) call {
	f.t.Helper()
	var found []call
	for _, c := range f.received() {
		if c.change() == change {
			found = append(found, c)
		}
	}
	if len(found) != 1 {
		f.t.Fatalf("%d requests carried the change, want 1:\n%s", len(found), change)
	}
	return found[0]
}

func (f *fakeJev) peakInFlight() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.peak
}

func (f *fakeJev) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	c := call{Body: string(body)}
	if err := json.Unmarshal(body, &c); err != nil {
		f.t.Errorf("the fake Jev could not read a request: %v", err)
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	reply := f.enter(&c)
	defer f.leave()
	f.hold()
	a := answer{}
	if reply != nil {
		a = reply(c)
	}
	if a.status != 0 && a.status != http.StatusOK {
		w.WriteHeader(a.status)
		return
	}
	json.NewEncoder(w).Encode(rawReply(c, a))
}

// enter records the request and counts it in flight.
func (f *fakeJev) enter(c *call) func(call) answer {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.seen[c.Body]++
	c.Nth = f.seen[c.Body]
	f.calls = append(f.calls, *c)
	f.inFlight++
	if f.inFlight > f.peak {
		f.peak = f.inFlight
	}
	return f.reply
}

func (f *fakeJev) leave() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.inFlight--
}

// hold gives up waiting after two seconds, so a run that never reaches the number in flight fails
// its assertion and doesn't hang.
func (f *fakeJev) hold() {
	f.mu.Lock()
	together, linger := f.together, f.linger
	f.mu.Unlock()
	deadline := time.Now().Add(2 * time.Second)
	for f.peakInFlight() < together && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	time.Sleep(linger)
}

// rawReply builds Jev's reply format for the questions a request asked.
func rawReply(c call, a answer) map[string]any {
	answers := map[string]any{}
	for id, q := range c.Questions {
		value, ok := a.values[id]
		if !ok {
			value = 0.1
		}
		switch q["type"] {
		case "choice":
			probs := a.choices[id]
			if probs == nil {
				probs = map[string]float64{"harder": 0.05, "same": 0.9, "easier": 0.05}
			}
			answers[id] = map[string]any{"type": "choice", "choice": likeliest(probs), "probabilities": probs}
		case "score":
			levels, _ := q["criteria"].([]any)
			answers[id] = map[string]any{"type": "score", "score": value * float64(len(levels)-1)}
		default:
			answers[id] = map[string]any{"type": "noul", "noul": value}
		}
	}
	cost := a.cost
	if cost == 0 {
		cost = 0.001
	}
	return map[string]any{
		"answers": answers,
		"usage":   map[string]any{"input_tokens": 100, "output_tokens": 10, "cost": cost},
	}
}

func likeliest(probs map[string]float64) string {
	best := ""
	for name, p := range probs {
		if best == "" || p > probs[best] || (p == probs[best] && name < best) {
			best = name
		}
	}
	return best
}
