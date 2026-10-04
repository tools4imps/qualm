// Package check runs qualm's check: it finds the changed files, asks Jev about each one, applies
// the gate, and records keeps.
package check

import (
	"time"

	"github.com/tools4imps/qualm/internal/config"
	"github.com/tools4imps/qualm/internal/jev"
	"github.com/tools4imps/qualm/internal/questions"
)

// Options is everything a run needs from the command line and its surroundings.
type Options struct {
	Dir          string  // the repository
	Base         string  // --base, empty for the default branch
	Paths        []string
	IncludeTests bool
	Threshold    float64 // --threshold, 0 when not given
	Jobs         int     // files judged at once, 8 when 0
	DryRun       bool
	NoCache      bool
	CacheDir     string  // empty for the user cache directory
	Budget       float64 // dollars, 1 when 0
	Client       *jev.Client
	Now          func() time.Time // nil for time.Now
}

// File is what happened to one changed file.
type File struct {
	Path    string
	Status  string                // "judged", "kept" or "skipped"
	Reason  string                // why it was kept or skipped
	Bytes   int                   // the size of its normalised diff
	Answers map[string]jev.Answer // by question id
	Failed  []string              // the gating questions that reached their threshold
	Where   map[string][2]int     // a diagnosis id to the first and last line it points at in the new file
}

// Usage is what the run cost.
type Usage struct {
	Requests    int
	InputTokens int
	Cost        float64
}

// Result is a finished run.
type Result struct {
	Base       string               // the commit the working tree was compared with
	Questions  []questions.Question // the resolved set, in the order asked
	Files      []File               // sorted by path
	StaleKeeps []config.Keep
	Usage      Usage
	DryRun     bool
}

// Passed reports whether no judged file reached a gate.
func (r Result) Passed() bool {
	for _, f := range r.Files {
		if len(f.Failed) > 0 {
			return false
		}
	}
	return true
}
