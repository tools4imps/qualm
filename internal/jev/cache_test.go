package jev

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func sampleReply() Reply {
	return Reply{
		Answers: map[string]Answer{
			"a": {Value: 0.25},
			"b": {Value: 0.9, Choice: "same", Probabilities: map[string]float64{"same": 0.9, "harder": 0.1}},
		},
		InputTokens: 407,
		Cost:        0.000017,
	}
}

// Contract: jev/J9
func TestCacheRoundTrip(t *testing.T) {
	c := Cache{Dir: filepath.Join(t.TempDir(), "nested", "qualm")} // Put makes the directory
	want := sampleReply()
	if err := c.Put("abc", want); err != nil {
		t.Fatal(err)
	}
	got, ok := c.Get("abc")
	if !ok || !reflect.DeepEqual(got, want) {
		t.Errorf("got %+v, %v", got, ok)
	}
	entries, _ := os.ReadDir(c.Dir)
	if len(entries) != 1 || entries[0].Name() != "abc.json" {
		t.Errorf("directory holds %v, want only abc.json", entries)
	}
}

// Contract: jev/J9
func TestCacheMisses(t *testing.T) {
	dir := t.TempDir()
	c := Cache{Dir: dir}
	if _, ok := c.Get("unknown"); ok {
		t.Error("unknown key hit")
	}
	if _, ok := (Cache{Dir: filepath.Join(dir, "absent")}).Get("k"); ok {
		t.Error("missing directory hit")
	}
	if err := os.WriteFile(filepath.Join(dir, "bad.json"), []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, ok := c.Get("bad"); ok {
		t.Error("corrupt entry hit")
	}
	if err := os.WriteFile(filepath.Join(dir, "empty.json"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, ok := c.Get("empty"); ok {
		t.Error("empty entry hit")
	}
}

// Contract: jev/J9
func TestEmptyDirIsUnusable(t *testing.T) {
	c := Cache{}
	if _, ok := c.Get("k"); ok {
		t.Error("hit with no directory")
	}
	if err := c.Put("k", sampleReply()); err == nil {
		t.Error("Put with no directory succeeded")
	}
}

// Contract: jev/J9
func TestPutFailureLeavesNoTempFile(t *testing.T) {
	dir := t.TempDir()
	// A directory where the entry should go makes the rename fail after the temp file exists.
	if err := os.MkdirAll(filepath.Join(dir, "k.json", "child"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := (Cache{Dir: dir}).Put("k", sampleReply()); err == nil {
		t.Fatal("want an error")
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 {
		t.Errorf("directory holds %v, want only k.json", entries)
	}
}
