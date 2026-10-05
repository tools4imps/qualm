package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/tools4imps/qualm/internal/questions"
)

func write(t *testing.T, dir, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, File), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func loadErr(t *testing.T, body string) error {
	t.Helper()
	dir := t.TempDir()
	write(t, dir, body)
	_, err := Load(dir)
	if err == nil {
		t.Fatalf("Load accepted %s", body)
	}
	if !strings.HasPrefix(err.Error(), "qualm.json: ") {
		t.Fatalf("error %q does not start with the file name", err)
	}
	return err
}

// Contract: config/C1
func TestMissingFileIsTheZeroConfig(t *testing.T) {
	c, err := Load(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(c, Config{}) {
		t.Fatalf("config = %+v", c)
	}
}

// Contract: config/C2
func TestUnknownKeysAndBadJSONAreErrors(t *testing.T) {
	bad := map[string]string{
		"top level":     `{"modle": "x"}`,
		"in the gate":   `{"gate": {"questoin": "push_back"}}`,
		"in a question": `{"questions": [{"id": "a", "type": "noul", "instructions": "?", "gate": true}]}`,
		"in a keep":     `{"keeps": [{"path": "a", "change": "b", "reason": "c", "date": "d", "who": "e"}]}`,
		"malformed":     `{"model": `,
		"trailing":      `{"model": "x"} {"model": "y"}`,
		"trailing junk": `{} x`,
		"not an object": `[]`,
		"empty file":    ``,
	}
	for name, body := range bad {
		t.Run(name, func(t *testing.T) { loadErr(t, body) })
	}
	if err := loadErr(t, `{"modle": "x"}`); !strings.Contains(err.Error(), "modle") {
		t.Fatalf("error does not name the key: %v", err)
	}
}

// Contract: config/C3
func TestGateThresholdMustBeWithinZeroAndOne(t *testing.T) {
	for _, body := range []string{
		`{"gate": {"threshold": 1.5}}`,
		`{"gate": {"threshold": -0.1}}`,
	} {
		loadErr(t, body)
	}
	for _, body := range []string{
		`{"gate": {"threshold": 0}}`,
		`{"gate": {"threshold": 1}}`,
		`{"gate": {"question": "push_back", "threshold": 0.6}}`,
		`{}`,
	} {
		dir := t.TempDir()
		write(t, dir, body)
		if _, err := Load(dir); err != nil {
			t.Fatalf("%s: %v", body, err)
		}
	}
}

// Contract: config/C4
func TestSaveFormatAndRoundTrip(t *testing.T) {
	dir := t.TempDir()
	small := Config{Model: "m", Skip: []string{"a<b>&c"}}
	if err := small.Save(dir); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(dir, File))
	if err != nil {
		t.Fatal(err)
	}
	want := "{\n  \"model\": \"m\",\n  \"skip\": [\n    \"a<b>&c\"\n  ]\n}\n"
	if string(got) != want {
		t.Fatalf("bytes = %q, want %q", got, want)
	}

	full := Config{
		Model: DefaultModel,
		Gate:  &Gate{Question: "added_copies", Threshold: 0.7},
		Skip:  []string{"db/schema.rb", "**/*.generated.*"},
		Drop:  []string{"added_unused_flexibility"},
		Questions: []questions.Question{
			{ID: "flag", Type: "noul", Instructions: "Did the change add a flag?", Gates: true, Threshold: 0.8},
			{ID: "tone", Type: "choice", Role: "describes", Instructions: "Which?",
				Criteria: json.RawMessage(`{"zebra":"z","apple":"a"}`)},
		},
		Keeps: []Keep{
			{Path: "a.go", Change: "abc123", Reason: "needed", Date: "2026-10-04"},
			{Path: "b.go", Change: "def456", Reason: "also", Date: "2026-10-05"},
		},
	}
	if err := full.Save(dir); err != nil {
		t.Fatal(err)
	}
	back, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(back, full) {
		t.Fatalf("round trip changed the config:\n got %+v\nwant %+v", back, full)
	}
	raw, _ := os.ReadFile(filepath.Join(dir, File))
	if !strings.HasSuffix(string(raw), "}\n") || strings.HasSuffix(string(raw), "\n\n") {
		t.Fatalf("final newline wrong: %q", raw[len(raw)-3:])
	}
	order := []string{`"model"`, `"gate"`, `"skip"`, `"drop"`, `"questions"`, `"keeps"`}
	last := -1
	for _, k := range order {
		i := strings.Index(string(raw), "\n  "+k)
		if i <= last {
			t.Fatalf("key %s out of order in %s", k, raw)
		}
		last = i
	}
}

// Contract: config/C5
func TestSaveReplacesWholeAndLeavesNoTempFile(t *testing.T) {
	dir := t.TempDir()
	long := Config{Model: "a-very-long-model-name-that-takes-up-space", Skip: []string{"one", "two", "three"}}
	if err := long.Save(dir); err != nil {
		t.Fatal(err)
	}
	short := Config{Model: "s"}
	if err := short.Save(dir); err != nil {
		t.Fatal(err)
	}
	back, err := Load(dir)
	if err != nil {
		t.Fatalf("saved file does not parse: %v", err)
	}
	if !reflect.DeepEqual(back, short) {
		t.Fatalf("back = %+v", back)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != File {
		t.Fatalf("directory holds %v", entries)
	}
	if err := short.Save(filepath.Join(dir, "missing")); err == nil {
		t.Fatal("save into a missing directory succeeded")
	}
}
