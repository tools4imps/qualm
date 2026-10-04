// Package contractcheck holds qualm to its Contract: every obligation in contract/ has to be named
// by at least one test, and no test may name an obligation the Contract doesn't have.
package contractcheck

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

var (
	// An obligation is a list item that opens with its id in bold, such as "- **K1** ...".
	obligation = regexp.MustCompile(`(?m)^- \*\*([A-Z]\d+)\*\* `)
	// A test names an obligation in a comment, such as "Contract: skip/K1" after two slashes.
	named = regexp.MustCompile(`// Contract: ([a-z]+)/([A-Z]\d+)`)
)

func TestEveryObligationIsNamedByATest(t *testing.T) {
	root := filepath.Join("..", "..")
	covered := map[string]bool{}
	files, err := filepath.Glob(filepath.Join(root, "contract", "*", "README.md"))
	if err != nil || len(files) == 0 {
		t.Fatalf("no Contract found under %s: %v", filepath.Join(root, "contract"), err)
	}
	for _, file := range files {
		text, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		primitive := filepath.Base(filepath.Dir(file))
		for _, m := range obligation.FindAllStringSubmatch(string(text), -1) {
			covered[primitive+"/"+m[1]] = false
		}
	}

	var unknown []string
	err = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() && (d.Name() == ".git" || d.Name() == "spike" || d.Name() == "dist") {
			return filepath.SkipDir
		}
		if d.IsDir() || !strings.HasSuffix(path, "_test.go") {
			return nil
		}
		text, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for _, m := range named.FindAllStringSubmatch(string(text), -1) {
			id := m[1] + "/" + m[2]
			if _, ok := covered[id]; ok {
				covered[id] = true
			} else {
				unknown = append(unknown, id+" in "+filepath.ToSlash(path))
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	var missing []string
	for id, ok := range covered {
		if !ok {
			missing = append(missing, id)
		}
	}
	sort.Strings(missing)
	sort.Strings(unknown)
	t.Logf("contract coverage: %d of %d obligations have a test", len(covered)-len(missing), len(covered))
	if len(missing) > 0 {
		t.Errorf("no test names these obligations: %s", strings.Join(missing, ", "))
	}
	if len(unknown) > 0 {
		t.Errorf("tests name obligations the Contract doesn't have: %s", strings.Join(unknown, "; "))
	}
}
