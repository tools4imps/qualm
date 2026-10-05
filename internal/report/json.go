package report

import (
	"encoding/json"
	"io"

	"github.com/tools4imps/qualm/internal/check"
	"github.com/tools4imps/qualm/internal/config"
)

// JSON writes the same result as one JSON object. The result's own tags give the object its
// shape, and the report adds whether the run passed.
func JSON(w io.Writer, r check.Result) error {
	// An empty list is written as [] and not null.
	if r.Files == nil {
		r.Files = []check.File{}
	}
	if r.StaleKeeps == nil {
		r.StaleKeeps = []config.Keep{}
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	return enc.Encode(struct {
		Passed bool `json:"passed"`
		check.Result
	}{r.Passed(), r})
}
