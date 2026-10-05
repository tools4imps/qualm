// Package config reads and writes qualm.json, a team's settings and its keeps.
package config

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/tools4imps/qualm/internal/atomicfile"
	"github.com/tools4imps/qualm/internal/questions"
)

// File is the config's name at the repository root.
const File = "qualm.json"

// DefaultModel is the Jev model used when the config names none.
const DefaultModel = "typesafe/jev-1.13"

// Gate changes the default gate's question or threshold. A zero field means not set.
type Gate struct {
	Question  string  `json:"question,omitempty"`
	Threshold float64 `json:"threshold,omitempty"`
}

// Keep records that a person accepted one exact change to one file.
type Keep struct {
	Path   string `json:"path"`
	Change string `json:"change"` // the SHA-256 of the lines the change adds and removes, in hex
	Reason string `json:"reason"`
	Date   string `json:"date"` // YYYY-MM-DD
}

// Config is the whole of qualm.json. The field order is the order Save writes.
type Config struct {
	Model     string               `json:"model,omitempty"`
	Gate      *Gate                `json:"gate,omitempty"`
	Skip      []string             `json:"skip,omitempty"`
	Drop      []string             `json:"drop,omitempty"`
	Questions []questions.Question `json:"questions,omitempty"`
	Keeps     []Keep               `json:"keeps,omitempty"`
}

// Load reads qualm.json from dir. A missing file gives the zero Config. Anything the file holds
// that qualm does not know is an error, so a typo never passes for a clean run.
func Load(dir string) (Config, error) {
	data, err := os.ReadFile(filepath.Join(dir, File))
	if os.IsNotExist(err) {
		return Config{}, nil
	}
	if err != nil {
		return Config{}, fmt.Errorf("%s: %w", File, err)
	}
	var c Config
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&c); err != nil {
		return Config{}, fmt.Errorf("%s: %w", File, err)
	}
	// A second Decode must hit the end; anything else is content after the object.
	if _, err := dec.Token(); err != io.EOF {
		return Config{}, fmt.Errorf("%s: unexpected content after the closing brace", File)
	}
	if c.Gate != nil && (c.Gate.Threshold < 0 || c.Gate.Threshold > 1) {
		return Config{}, fmt.Errorf("%s: gate threshold %v is outside 0 to 1", File, c.Gate.Threshold)
	}
	// Save indents criteria along with everything else, so Load compacts them to give the same
	// bytes whichever way the file was laid out.
	for i, q := range c.Questions {
		if len(q.Criteria) == 0 {
			continue
		}
		var b bytes.Buffer
		json.Compact(&b, q.Criteria) // can't fail on bytes the decoder has just accepted
		c.Questions[i].Criteria = b.Bytes()
	}
	return c, nil
}

// Save writes qualm.json to dir, whole or not at all. A config is meant to be committed and
// shared, so it is world readable.
func (c Config) Save(dir string) error {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(c); err != nil {
		return fmt.Errorf("%s: %w", File, err)
	}
	if err := atomicfile.Write(filepath.Join(dir, File), buf.Bytes(), 0o644); err != nil {
		return fmt.Errorf("%s: %w", File, err)
	}
	return nil
}
