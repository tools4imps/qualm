package report

import (
	"encoding/json"
	"io"

	"github.com/tools4imps/qualm/internal/check"
	"github.com/tools4imps/qualm/internal/jev"
)

type jsonReport struct {
	Passed     bool       `json:"passed"`
	Base       string     `json:"base"`
	DryRun     bool       `json:"dry_run"`
	Files      []jsonFile `json:"files"`
	StaleKeeps []jsonKeep `json:"stale_keeps"`
	Usage      jsonUsage  `json:"usage"`
}

type jsonFile struct {
	Path    string                `json:"path"`
	Status  string                `json:"status"`
	Reason  string                `json:"reason,omitempty"`
	Bytes   int                   `json:"bytes,omitempty"`
	Answers map[string]jev.Answer `json:"answers,omitempty"`
	Failed  []string              `json:"failed,omitempty"`
	Where   map[string][2]int     `json:"where,omitempty"`
}

type jsonKeep struct {
	Path   string `json:"path"`
	Change string `json:"change"`
	Reason string `json:"reason"`
	Date   string `json:"date"`
}

type jsonUsage struct {
	Requests    int     `json:"requests"`
	InputTokens int     `json:"input_tokens"`
	Cost        float64 `json:"cost"`
}

// JSON writes the same result as one JSON object.
func JSON(w io.Writer, r check.Result) error {
	// Slices start non-nil so an empty list is written as [] and not null.
	out := jsonReport{
		Passed:     r.Passed(),
		Base:       r.Base,
		DryRun:     r.DryRun,
		Files:      make([]jsonFile, 0, len(r.Files)),
		StaleKeeps: make([]jsonKeep, 0, len(r.StaleKeeps)),
		Usage:      jsonUsage{r.Usage.Requests, r.Usage.InputTokens, r.Usage.Cost},
	}
	for _, f := range r.Files {
		out.Files = append(out.Files, jsonFile(f))
	}
	for _, k := range r.StaleKeeps {
		out.StaleKeeps = append(out.StaleKeeps, jsonKeep(k))
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	return enc.Encode(out)
}
