// Package check runs qualm's check: it finds the changed files, asks Jev about each one, applies
// the gate, and records keeps.
package check

import (
	"slices"
	"time"

	"github.com/tools4imps/qualm/internal/config"
	"github.com/tools4imps/qualm/internal/jev"
	"github.com/tools4imps/qualm/internal/questions"
)

// Options is everything a run needs from the command line and its surroundings.
type Options struct {
	Dir          string // the repository
	Base         string // --base, empty for the default branch
	Paths        []string
	IncludeTests bool
	Threshold    float64 // --threshold, 0 when not given
	Jobs         int     // requests in flight at once, 8 when 0
	DryRun       bool
	NoCache      bool
	CacheDir     string  // empty for the user cache directory
	Budget       float64 // dollars, 1 when 0
	Client       *jev.Client
	Now          func() time.Time // nil for time.Now
}

// File is what happened to one changed file. The tags are its shape in the JSON report.
type File struct {
	Path    string                `json:"path"`
	Status  string                `json:"status"`            // StatusJudged, StatusKept or StatusSkipped
	Reason  string                `json:"reason,omitempty"`  // why it was kept or skipped
	Bytes   int                   `json:"bytes,omitempty"`   // the size of its normalised diff
	Answers map[string]jev.Answer `json:"answers,omitempty"` // by question id
	Failed  []string              `json:"failed,omitempty"`  // the gating questions that reached their threshold
	Where   map[string][2]int     `json:"where,omitempty"`   // a diagnosis id to the first and last line it points at in the new file
}

// firedAt is the value from which a diagnosis counts as having fired.
const firedAt = 0.5

// Fired lists the diagnoses among qs that the file answered at firedAt or above, in question
// order. They are what a report shows under a failing file and what the file is searched for.
func (f File) Fired(qs []questions.Question) []questions.Question {
	var fired []questions.Question
	for _, q := range qs {
		if q.Role == questions.RoleDiagnosis && f.Answers[q.ID].Value >= firedAt {
			fired = append(fired, q)
		}
	}
	return fired
}

// Usage is what the run cost.
type Usage struct {
	Requests    int     `json:"requests"`
	InputTokens int     `json:"input_tokens"`
	Cost        float64 `json:"cost"`
}

// Result is a finished run. The tags and the field order are its shape in the JSON report.
type Result struct {
	Base       string               `json:"base"`    // the commit the working tree was compared with
	DryRun     bool                 `json:"dry_run"` // nothing was sent
	Questions  []questions.Question `json:"-"`       // the resolved set, in the order asked
	Files      []File               `json:"files"`   // sorted by path
	StaleKeeps []config.Keep        `json:"stale_keeps"`
	Usage      Usage                `json:"usage"`
}

// Passed reports whether no judged file reached a gate.
func (r Result) Passed() bool {
	return !slices.ContainsFunc(r.Files, func(f File) bool { return len(f.Failed) > 0 })
}
