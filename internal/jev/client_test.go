package jev

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tools4imps/qualm/internal/questions"
)

const testKey = "sk-test-key-0123456789"

// The three question types, built by hand. The score has four levels, so its top level is 3.
var (
	qNoul   = questions.Question{ID: "push_back", Type: "noul", Instructions: "x"}
	qScore  = questions.Question{ID: "hard_to_trace", Type: "score", Instructions: "x", Criteria: json.RawMessage(`["a","b","c","d"]`)}
	qChoice = questions.Question{ID: "direction", Type: "choice", Instructions: "x", Criteria: json.RawMessage(`{"harder":"h","same":"s","easier":"e"}`)}
	allQs   = []questions.Question{qNoul, qScore, qChoice}
)

const goodReply = `{"answers":{
 "push_back":{"type":"noul","noul":0.22},
 "hard_to_trace":{"type":"score","score":1.5,"probabilities":{"0":0.7,"1":0.3}},
 "direction":{"type":"choice","choice":"same","probabilities":{"harder":0.05,"same":0.9,"easier":0.05}},
 "extra":{"type":"noul","noul":7}},
 "usage":{"input_tokens":407,"output_tokens":39,"cost":0.000017094}}`

func testRequest() Request {
	return Request{
		Model:     "typesafe/jev-1.13",
		State:     map[string]any{"language": "Go", "change": "diff"},
		Questions: map[string]any{"push_back": map[string]any{"type": "noul"}},
	}
}

// fake serves canned responses in order, repeating the last, and counts what it saw.
type fake struct {
	mu       sync.Mutex
	statuses []int
	body     string
	hits     int
	last     *http.Request
	lastBody []byte
}

func (f *fake) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.last = r
	f.lastBody, _ = io.ReadAll(r.Body)
	i := f.hits
	if i >= len(f.statuses) {
		i = len(f.statuses) - 1
	}
	f.hits++
	status := f.statuses[i]
	w.WriteHeader(status)
	if status == 200 {
		io.WriteString(w, f.body)
	}
}

// start returns a client pointed at a fake server, with a Sleep that records its waits.
func start(t *testing.T, f *fake) (*Client, *[]time.Duration) {
	t.Helper()
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	var waits []time.Duration
	c := &Client{Endpoint: srv.URL, Key: testKey, Sleep: func(d time.Duration) { waits = append(waits, d) }}
	return c, &waits
}

// Contract: jev/J1
func TestRequestShape(t *testing.T) {
	f := &fake{statuses: []int{200}, body: goodReply}
	c, _ := start(t, f)
	if _, err := c.Ask(context.Background(), testRequest(), allQs); err != nil {
		t.Fatal(err)
	}
	if f.last.Method != http.MethodPost {
		t.Errorf("method = %s", f.last.Method)
	}
	if got := f.last.Header.Get("Authorization"); got != "Bearer "+testKey {
		t.Errorf("Authorization = %q", got)
	}
	if got := f.last.Header.Get("Content-Type"); got != "application/json" {
		t.Errorf("Content-Type = %q", got)
	}
	var body map[string]any
	if err := json.Unmarshal(f.lastBody, &body); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"model", "state", "questions"} {
		if _, ok := body[k]; !ok {
			t.Errorf("body lacks %q", k)
		}
	}
}

// Contract: jev/J2
func TestMissingKeyFailsBeforeAnyRequest(t *testing.T) {
	f := &fake{statuses: []int{200}, body: goodReply}
	c, _ := start(t, f)
	c.Key = ""
	_, err := c.Ask(context.Background(), testRequest(), allQs)
	if err == nil || !strings.Contains(err.Error(), "OPENROUTER_API_KEY") {
		t.Fatalf("err = %v", err)
	}
	if f.hits != 0 {
		t.Errorf("server saw %d requests", f.hits)
	}
}

// Contract: jev/J3
func TestAnswersAreReadByType(t *testing.T) {
	f := &fake{statuses: []int{200}, body: goodReply}
	c, _ := start(t, f)
	r, err := c.Ask(context.Background(), testRequest(), allQs)
	if err != nil {
		t.Fatal(err)
	}
	if got := r.Answers["push_back"].Value; got != 0.22 {
		t.Errorf("noul = %v", got)
	}
	if got := r.Answers["hard_to_trace"].Value; got != 0.5 {
		t.Errorf("score = %v, want 1.5/3", got)
	}
	ch := r.Answers["direction"]
	if ch.Choice != "same" || ch.Value != 0.9 || len(ch.Probabilities) != 3 || ch.Probabilities["harder"] != 0.05 {
		t.Errorf("choice = %+v", ch)
	}
}

// Contract: jev/J4
func TestBadRepliesAreErrors(t *testing.T) {
	cases := map[string]struct {
		body string
		want string
	}{
		"missing":      {`{"answers":{"push_back":{"noul":0.1}}}`, "hard_to_trace"},
		"noul high":    {`{"answers":{"push_back":{"noul":1.2}}}`, "push_back"},
		"noul low":     {`{"answers":{"push_back":{"noul":-0.1}}}`, "push_back"},
		"noul absent":  {`{"answers":{"push_back":{}}}`, "push_back"},
		"score high":   {`{"answers":{"push_back":{"noul":0.1},"hard_to_trace":{"score":3.5}}}`, "hard_to_trace"},
		"score low":    {`{"answers":{"push_back":{"noul":0.1},"hard_to_trace":{"score":-1}}}`, "hard_to_trace"},
		"choice prob":  {`{"answers":{"push_back":{"noul":0.1},"hard_to_trace":{"score":1},"direction":{"choice":"same","probabilities":{"same":1.5}}}}`, "direction"},
		"choice unset": {`{"answers":{"push_back":{"noul":0.1},"hard_to_trace":{"score":1},"direction":{"choice":"other","probabilities":{"same":1}}}}`, "direction"},
		"not json":     {`<html>`, "not the JSON"},
	}
	qs := allQs
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			f := &fake{statuses: []int{200}, body: tc.body}
			c, _ := start(t, f)
			asked := qs
			if name == "noul high" || name == "noul low" || name == "noul absent" {
				asked = []questions.Question{qNoul}
			}
			_, err := c.Ask(context.Background(), testRequest(), asked)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("err = %v, want mention of %q", err, tc.want)
			}
		})
	}
}

// Contract: jev/J4
func TestValuesAtTheEndsOfTheUnitIntervalAreAccepted(t *testing.T) {
	cases := map[string]struct {
		body string
		want float64
	}{
		"noul zero":    {`{"answers":{"push_back":{"noul":0}}}`, 0},
		"noul one":     {`{"answers":{"push_back":{"noul":1}}}`, 1},
		"score bottom": {`{"answers":{"push_back":{"noul":0.1},"hard_to_trace":{"score":0}}}`, 0},
		"score top":    {`{"answers":{"push_back":{"noul":0.1},"hard_to_trace":{"score":3}}}`, 1},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			f := &fake{statuses: []int{200}, body: tc.body}
			c, _ := start(t, f)
			asked := []questions.Question{qNoul}
			id := "push_back"
			if strings.HasPrefix(name, "score") {
				asked = []questions.Question{qNoul, qScore}
				id = "hard_to_trace"
			}
			r, err := c.Ask(context.Background(), testRequest(), asked)
			if err != nil {
				t.Fatal(err)
			}
			if got := r.Answers[id].Value; got != tc.want {
				t.Errorf("value = %v, want %v", got, tc.want)
			}
		})
	}

	f := &fake{statuses: []int{200}, body: `{"answers":{"push_back":{"noul":0.1},"hard_to_trace":{"score":1},
 "direction":{"choice":"same","probabilities":{"harder":0,"same":1,"easier":0}}}}`}
	c, _ := start(t, f)
	r, err := c.Ask(context.Background(), testRequest(), allQs)
	if err != nil {
		t.Fatal(err)
	}
	if got := r.Answers["direction"]; got.Value != 1 || got.Probabilities["harder"] != 0 {
		t.Errorf("choice = %+v", got)
	}
}

// Contract: jev/J4
func TestReplySizeLimitIsExact(t *testing.T) {
	const head = `{"answers":{"push_back":{"noul":0.1}},"pad":"`
	const tail = `"}`
	fill := func(total int) string {
		return head + strings.Repeat("x", total-len(head)-len(tail)) + tail
	}

	f := &fake{statuses: []int{200}, body: fill(maxReplySize)}
	c, _ := start(t, f)
	if _, err := c.Ask(context.Background(), testRequest(), []questions.Question{qNoul}); err != nil {
		t.Fatalf("a reply of exactly the limit: %v", err)
	}

	f = &fake{statuses: []int{200}, body: fill(maxReplySize + 1)}
	c, _ = start(t, f)
	_, err := c.Ask(context.Background(), testRequest(), []questions.Question{qNoul})
	if err == nil || !strings.Contains(err.Error(), "too large") {
		t.Fatalf("a reply one byte over the limit: %v", err)
	}
}

// Contract: jev/J4
func TestAScoreQuestionWithOneLevelIsAnError(t *testing.T) {
	one := questions.Question{ID: "flat", Type: "score", Instructions: "x", Criteria: json.RawMessage(`["only"]`)}
	// Whatever the score, there is no top level to divide it by.
	for _, score := range []string{"-1", "0", "0.5", "1"} {
		body := `{"answers":{"flat":{"score":` + score + `}}}`

		_, err := parseReply([]byte(body), []questions.Question{one}, 0)

		if err == nil || !strings.Contains(err.Error(), "flat") {
			t.Errorf("score %s: err = %v, want one naming the question", score, err)
		}
	}
}

// Contract: jev/J4
func TestAQuestionOfAnUnknownTypeIsAnError(t *testing.T) {
	odd := questions.Question{ID: "odd", Type: "ranking", Instructions: "x"}

	_, err := parseReply([]byte(`{"answers":{"odd":{"noul":0.5}}}`), []questions.Question{odd}, 0)

	if err == nil || !strings.Contains(err.Error(), "ranking") {
		t.Errorf("err = %v, want one naming the type", err)
	}
}

// Contract: jev/J4
func TestExtraAnswersAreIgnored(t *testing.T) {
	f := &fake{statuses: []int{200}, body: goodReply}
	c, _ := start(t, f)
	r, err := c.Ask(context.Background(), testRequest(), []questions.Question{qNoul})
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Answers) != 1 {
		t.Errorf("answers = %v", r.Answers)
	}
}

// Contract: jev/J5
func TestRetriesTransientStatuses(t *testing.T) {
	for _, status := range []int{429, 500, 502, 503, 504, 529} {
		f := &fake{statuses: []int{status}}
		c, waits := start(t, f)
		_, err := c.Ask(context.Background(), testRequest(), allQs)
		if err == nil {
			t.Fatalf("%d: want an error", status)
		}
		if f.hits != 4 {
			t.Errorf("%d: %d requests, want 4", status, f.hits)
		}
		want := []time.Duration{time.Second, 2 * time.Second, 4 * time.Second}
		if len(*waits) != 3 || (*waits)[0] != want[0] || (*waits)[1] != want[1] || (*waits)[2] != want[2] {
			t.Errorf("%d: waits = %v", status, *waits)
		}
	}
}

// Contract: jev/J5
func TestRetrySucceedsLater(t *testing.T) {
	f := &fake{statuses: []int{503, 429, 200}, body: goodReply}
	c, waits := start(t, f)
	if _, err := c.Ask(context.Background(), testRequest(), allQs); err != nil {
		t.Fatal(err)
	}
	if f.hits != 3 || len(*waits) != 2 {
		t.Errorf("hits = %d, waits = %v", f.hits, *waits)
	}
}

// Contract: jev/J5
func TestOtherFailuresAreNotRetried(t *testing.T) {
	for _, status := range []int{400, 401, 403, 404, 418} {
		f := &fake{statuses: []int{status}}
		c, waits := start(t, f)
		if _, err := c.Ask(context.Background(), testRequest(), allQs); err == nil {
			t.Fatalf("%d: want an error", status)
		}
		if f.hits != 1 || len(*waits) != 0 {
			t.Errorf("%d: hits = %d, waits = %v", status, f.hits, *waits)
		}
	}
}

// Contract: jev/J5
// Contract: jev/J6
func TestTransportErrorsAreRetried(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	url := srv.URL
	srv.Close()
	var waits []time.Duration
	c := &Client{Endpoint: url, Key: testKey, Sleep: func(d time.Duration) { waits = append(waits, d) }}
	_, err := c.Ask(context.Background(), testRequest(), allQs)
	// The message is fixed: what the connection said, and the address it was refused at, stay out.
	if err == nil || err.Error() != "jev failed after 4 attempts: jev: the request could not be completed" {
		t.Fatalf("err = %v", err)
	}
	if len(waits) != 3 {
		t.Errorf("waits = %v", waits)
	}
}

// Contract: jev/J6
func TestATimeoutSaysItTimedOutAndNothingMore(t *testing.T) {
	old := httpClient
	httpClient = &http.Client{Timeout: 30 * time.Millisecond}
	defer func() { httpClient = old }()
	cases := map[string]func(w http.ResponseWriter){
		"before any reply":         func(http.ResponseWriter) {},
		"in the middle of a reply": func(w http.ResponseWriter) { w.WriteHeader(200); w.(http.Flusher).Flush() },
	}
	for name, begin := range cases {
		t.Run(name, func(t *testing.T) {
			done := make(chan struct{})
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				begin(w)
				<-done
			}))
			defer srv.Close()
			defer close(done) // lets the handlers go, so the server can close
			c := &Client{Endpoint: srv.URL, Key: testKey, Sleep: func(time.Duration) {}}

			_, err := c.Ask(context.Background(), testRequest(), allQs)

			if err == nil || err.Error() != "jev failed after 4 attempts: jev: the request timed out" {
				t.Errorf("err = %v", err)
			}
		})
	}
}

// Contract: jev/J5
func TestCancelledContextStopsRetries(t *testing.T) {
	f := &fake{statuses: []int{503}}
	c, _ := start(t, f)
	ctx, cancel := context.WithCancel(context.Background())
	c.Sleep = func(time.Duration) { cancel() }
	_, err := c.Ask(ctx, testRequest(), allQs)
	if err == nil {
		t.Fatal("want an error")
	}
	if f.hits != 1 {
		t.Errorf("%d requests after cancel, want 1", f.hits)
	}
}

// Contract: jev/J6
func TestErrorsHoldNeitherKeyNorBody(t *testing.T) {
	const secret = "hunter2-the-secret-body"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(401)
		io.WriteString(w, secret+" "+r.Header.Get("Authorization"))
	}))
	defer srv.Close()
	c := &Client{Endpoint: srv.URL, Key: testKey, Sleep: func(time.Duration) {}}
	_, err := c.Ask(context.Background(), testRequest(), allQs)
	if err == nil {
		t.Fatal("want an error")
	}
	msg := err.Error()
	if strings.Contains(msg, secret) || strings.Contains(msg, testKey) {
		t.Errorf("error leaks: %s", msg)
	}
	if !strings.Contains(msg, "401") || !strings.Contains(msg, "refused") {
		t.Errorf("error = %s", msg)
	}
}

// Contract: jev/J6
func TestOtherErrorsHoldNeitherKeyNorBody(t *testing.T) {
	const secret = "hunter2-the-secret-body"
	for _, status := range []int{403, 400, 503} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(status)
			io.WriteString(w, secret+" "+r.Header.Get("Authorization"))
		}))
		c := &Client{Endpoint: srv.URL, Key: testKey, Sleep: func(time.Duration) {}}
		_, err := c.Ask(context.Background(), testRequest(), allQs)
		srv.Close()
		if err == nil {
			t.Fatalf("%d: want an error", status)
		}
		msg := err.Error()
		if strings.Contains(msg, secret) || strings.Contains(msg, testKey) {
			t.Errorf("%d: error leaks: %s", status, msg)
		}
		if !strings.Contains(msg, strconv.Itoa(status)) {
			t.Errorf("%d: error names no status: %s", status, msg)
		}
		if status == 403 && !strings.Contains(msg, "refused") {
			t.Errorf("403: %s", msg)
		}
	}
}

// Contract: jev/J6
func TestMalformedReplyErrorHoldsNoBody(t *testing.T) {
	const secret = "hunter2-the-secret-body"
	f := &fake{statuses: []int{200}, body: secret + testKey}
	c, _ := start(t, f)
	_, err := c.Ask(context.Background(), testRequest(), allQs)
	if err == nil || strings.Contains(err.Error(), secret) || strings.Contains(err.Error(), testKey) {
		t.Errorf("err = %v", err)
	}
}

// Contract: jev/J7
func TestCostFromUsageOrFromTokens(t *testing.T) {
	// A reply that says nothing of its size is taken to have read a token for every three bytes sent.
	body, err := json.Marshal(testRequest())
	if err != nil || len(body) < 90 {
		t.Fatalf("the request is %d bytes, %v", len(body), err)
	}
	guess := len(body) / 3
	cases := map[string]struct {
		usage  string
		tokens int
		cost   float64
	}{
		"reported":         {`{"input_tokens":1000,"cost":0.5}`, 1000, 0.5},
		"absent":           {`{"input_tokens":1000000}`, 1000000, 0.042},
		"zero":             {`{"input_tokens":2000000,"cost":0}`, 2000000, 0.084},
		"a cost alone":     {`{"cost":0.5}`, 0, 0.5},
		"no usage":         {`null`, guess, CostOf(guess)},
		"an empty usage":   {`{}`, guess, CostOf(guess)},
		"zeros for both":   {`{"input_tokens":0,"cost":0}`, guess, CostOf(guess)},
		"a null cost only": {`{"cost":null}`, guess, CostOf(guess)},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			f := &fake{statuses: []int{200}, body: `{"answers":{"push_back":{"noul":0.1}},"usage":` + tc.usage + `}`}
			c, _ := start(t, f)
			r, err := c.Ask(context.Background(), testRequest(), []questions.Question{qNoul})
			if err != nil {
				t.Fatal(err)
			}
			if d := r.Cost - tc.cost; d > 1e-12 || d < -1e-12 || tc.cost == 0 {
				t.Errorf("cost = %v, want %v", r.Cost, tc.cost)
			}
			if r.InputTokens != tc.tokens {
				t.Errorf("input tokens = %d, want %d", r.InputTokens, tc.tokens)
			}
		})
	}
	f := &fake{statuses: []int{200}, body: goodReply}
	c, _ := start(t, f)
	r, _ := c.Ask(context.Background(), testRequest(), allQs)
	if r.InputTokens != 407 {
		t.Errorf("input tokens = %d", r.InputTokens)
	}
}

// Contract: jev/J8
func TestKeyIsStableAndSensitive(t *testing.T) {
	a := Request{Model: "m", State: map[string]any{}, Questions: map[string]any{}}
	b := Request{Model: "m", State: map[string]any{}, Questions: map[string]any{}}
	a.State["x"], a.State["y"], a.State["z"] = 1, 2, 3
	b.State["z"], b.State["y"], b.State["x"] = 3, 2, 1
	a.Questions["p"], a.Questions["q"] = "1", "2"
	b.Questions["q"], b.Questions["p"] = "2", "1"
	k := Key(a)
	if k != Key(b) {
		t.Error("insertion order changed the key")
	}
	if len(k) != 64 || k != strings.ToLower(k) {
		t.Errorf("key = %q", k)
	}

	changed := []func(*Request){
		func(r *Request) { r.Model = "other" },
		func(r *Request) { r.State["y"] = 99 },
		func(r *Request) { r.State["new"] = 1 },
		func(r *Request) { r.Questions["q"] = "changed" },
		func(r *Request) { r.Questions["extra"] = "1" },
	}
	for i, mutate := range changed {
		c := Request{Model: "m", State: map[string]any{"x": 1, "y": 2, "z": 3}, Questions: map[string]any{"p": "1", "q": "2"}}
		mutate(&c)
		if Key(c) == k {
			t.Errorf("change %d left the key the same", i)
		}
	}
}
