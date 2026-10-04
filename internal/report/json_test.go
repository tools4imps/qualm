package report

import (
	"bytes"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/tools4imps/qualm/internal/check"
	"github.com/tools4imps/qualm/internal/config"
	"github.com/tools4imps/qualm/internal/jev"
)

func renderJSON(t *testing.T, r check.Result) string {
	t.Helper()
	var b bytes.Buffer
	if err := JSON(&b, r); err != nil {
		t.Fatal(err)
	}
	return b.String()
}

// Contract: report/R6
func TestJSONEveryField(t *testing.T) {
	r := check.Result{
		Base: "abc123<&>",
		Files: []check.File{
			{
				Path: "lib/a.rb", Status: "judged", Bytes: 1234,
				Answers: map[string]jev.Answer{
					"push_back": {Value: 0.61},
					"direction": {Value: 0.99, Choice: "harder", Probabilities: map[string]float64{"harder": 0.99, "same": 0.01, "easier": 0}},
				},
				Failed: []string{"push_back"},
				Where:  map[string][2]int{"grew_a_big_unit": {412, 540}},
			},
			{Path: "lib/k.rb", Status: "kept", Reason: "fine", Bytes: 5},
			{Path: "docs/x.md", Status: "skipped", Reason: "prose or data"},
		},
		StaleKeeps: []config.Keep{{Path: "lib/old.rb", Change: "deadbeef", Reason: "r", Date: "2026-10-04"}},
		Usage:      check.Usage{Requests: 3, InputTokens: 4200, Cost: 0.00018},
	}
	out := renderJSON(t, r)
	if !strings.HasSuffix(out, "}\n") || !strings.Contains(out, "\n  \"passed\": false,\n") {
		t.Errorf("not indented with two spaces and a final newline:\n%s", out)
	}
	if !strings.Contains(out, "abc123<&>") {
		t.Errorf("HTML characters were escaped:\n%s", out)
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatal(err)
	}
	var want map[string]any
	if err := json.Unmarshal([]byte(`{
	  "passed": false, "base": "abc123<&>", "dry_run": false,
	  "files": [
	    {"path": "lib/a.rb", "status": "judged", "bytes": 1234,
	     "answers": {"push_back": {"value": 0.61}, "direction": {"value": 0.99, "choice": "harder", "probabilities": {"harder": 0.99, "same": 0.01, "easier": 0}}},
	     "failed": ["push_back"], "where": {"grew_a_big_unit": [412, 540]}},
	    {"path": "lib/k.rb", "status": "kept", "reason": "fine", "bytes": 5},
	    {"path": "docs/x.md", "status": "skipped", "reason": "prose or data"}
	  ],
	  "stale_keeps": [{"path": "lib/old.rb", "change": "deadbeef", "reason": "r", "date": "2026-10-04"}],
	  "usage": {"requests": 3, "input_tokens": 4200, "cost": 0.00018}
	}`), &want); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("decoded JSON differs\n got %v\nwant %v", got, want)
	}
}

// Contract: report/R6
func TestJSONEmptyResult(t *testing.T) {
	want := `{
  "passed": true,
  "base": "",
  "dry_run": false,
  "files": [],
  "stale_keeps": [],
  "usage": {
    "requests": 0,
    "input_tokens": 0,
    "cost": 0
  }
}
`
	expect(t, renderJSON(t, check.Result{}), want)
}

// Contract: report/R6
func TestJSONLeavesOutEmptyFields(t *testing.T) {
	r := check.Result{Files: []check.File{{Path: "a.rb", Status: "judged"}}, DryRun: true}
	out := renderJSON(t, r)
	for _, key := range []string{"reason", "bytes", "answers", "failed", "where"} {
		if strings.Contains(out, `"`+key+`"`) {
			t.Errorf("%q should be left out:\n%s", key, out)
		}
	}
	if !strings.Contains(out, `"dry_run": true`) {
		t.Errorf("dry_run missing:\n%s", out)
	}
}
