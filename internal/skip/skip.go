// Package skip decides which changed paths qualm leaves alone, so that no request is spent on
// code nobody wrote or nobody needs judged.
package skip

import (
	"path"
	"slices"
	"strings"
)

// Rules decides which changed paths qualm judges.
type Rules struct {
	Extra        []string // patterns from qualm.json
	IncludeTests bool
}

var generatedNames = []string{
	"*.lock", "package-lock.json", "go.sum", "*.min.js", "*.min.css", "*.map", "*.pb.go",
	"*_generated.*", "*.generated.*",
}

var proseNames = []string{
	"*.md", "*.txt", "*.rst", "*.json", "*.yaml", "*.yml", "*.toml", "*.xml", "*.csv", "*.svg", "*.html",
}

var testNames = []string{"*_test.go", "*_test.rb", "*_spec.rb", "*.test.*", "*.spec.*", "test_*.py"}

var testDirs = []string{"test", "tests", "spec", "__tests__"}

var builtDirs = []string{"vendor", "node_modules", "dist", "build", "target", "third_party", ".git"}

// Reason says why a path is skipped, or returns "" when the path is judged.
// The reasons are "vendored or built", "lockfile or generated", "prose or data", "test" and
// "skip rule <pattern>".
func (r Rules) Reason(path string) string {
	segments := split(path)
	if len(segments) == 0 {
		return ""
	}
	dirs, name := segments[:len(segments)-1], segments[len(segments)-1]
	switch {
	case under(dirs, builtDirs):
		return "vendored or built"
	case matchesAny(generatedNames, name):
		return "lockfile or generated"
	case matchesAny(proseNames, name):
		return "prose or data"
	case !r.IncludeTests && (under(dirs, testDirs) || matchesAny(testNames, name)):
		return "test"
	}
	for _, pattern := range r.Extra {
		if Match(pattern, path) {
			return "skip rule " + pattern
		}
	}
	return ""
}

// Match reports whether a pattern matches a slash-separated path. A pattern with no slash matches
// the file name in any directory. "**" matches any number of directories, including none. "*" and
// "?" match inside one path segment.
func Match(pattern, path string) bool {
	segments := split(path)
	if len(segments) == 0 {
		return false
	}
	if !strings.Contains(pattern, "/") {
		return matchSegment(pattern, segments[len(segments)-1])
	}
	// "docs/" is how people write a directory, and they mean everything under it. The "*" asks
	// for at least one segment below, so a file that happens to be named "docs" isn't caught.
	if strings.HasSuffix(pattern, "/") {
		pattern += "**/*"
	}
	return matchSegments(split(pattern), segments)
}

// matchSegments walks the pattern and path together. "**" tries every possible number of
// directories, so it can match none.
func matchSegments(pattern, path []string) bool {
	if len(pattern) == 0 {
		return len(path) == 0
	}
	if pattern[0] == "**" {
		for skip := 0; skip <= len(path); skip++ {
			if matchSegments(pattern[1:], path[skip:]) {
				return true
			}
		}
		return false
	}
	return len(path) > 0 && matchSegment(pattern[0], path[0]) && matchSegments(pattern[1:], path[1:])
}

// matchSegment is path.Match on one segment. A malformed pattern matches nothing rather than
// failing, since patterns come from a hand-edited config.
func matchSegment(pattern, name string) bool {
	ok, err := path.Match(pattern, name)
	return err == nil && ok
}

// under reports whether any of a path's directories, at any depth, has one of the names.
func under(dirs, names []string) bool {
	return slices.ContainsFunc(dirs, func(dir string) bool { return slices.Contains(names, dir) })
}

// matchesAny tests a file name against a list of globs.
func matchesAny(globs []string, name string) bool {
	return slices.ContainsFunc(globs, func(glob string) bool { return matchSegment(glob, name) })
}

// split cleans a path into segments. Git prints paths without a leading "./", but callers may not.
func split(path string) []string {
	return slices.DeleteFunc(strings.Split(path, "/"), func(s string) bool { return s == "" || s == "." })
}
