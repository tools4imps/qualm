package atomicfile

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func names(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, e := range entries {
		out = append(out, e.Name())
	}
	return out
}

func TestWriteCreatesTheFileWithTheModeAndTheData(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "out.json")

	if err := Write(path, []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(path)
	if err != nil || string(data) != "hello" {
		t.Errorf("read %q, %v, want hello", data, err)
	}
	if info, _ := os.Stat(path); runtime.GOOS != "windows" && info.Mode().Perm() != 0o644 {
		t.Errorf("mode = %v, want 0644", info.Mode().Perm())
	}
}

func TestWriteReplacesAWholeFileWithAShorterOne(t *testing.T) {
	path := filepath.Join(t.TempDir(), "out")
	if err := Write(path, []byte("a much longer first version"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := Write(path, []byte("short"), 0o644); err != nil {
		t.Fatal(err)
	}

	if data, _ := os.ReadFile(path); string(data) != "short" {
		t.Errorf("file holds %q, want short", data)
	}
}

// Contract: config/C5
// Contract: jev/J9
func TestWriteLeavesNoTemporaryFileBehind(t *testing.T) {
	dir := t.TempDir()
	if err := Write(filepath.Join(dir, "out"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := names(t, dir); len(got) != 1 || got[0] != "out" {
		t.Errorf("directory holds %v, want only out", got)
	}

	// A directory where the file should go makes the rename fail once the temporary file exists.
	if err := os.MkdirAll(filepath.Join(dir, "blocked", "child"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := Write(filepath.Join(dir, "blocked"), []byte("x"), 0o644); err == nil {
		t.Fatal("want an error when the target is a directory")
	}
	if got := names(t, dir); len(got) != 2 {
		t.Errorf("directory holds %v after a failed write, want only out and blocked", got)
	}

	if err := Write(filepath.Join(dir, "missing", "out"), []byte("x"), 0o644); err == nil {
		t.Error("want an error when the directory is missing")
	}
}
