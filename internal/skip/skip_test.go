package skip

import "testing"

type reasonCase struct {
	name  string
	rules Rules
	path  string
	want  string
}

func runReasons(t *testing.T, cases []reasonCase) {
	t.Helper()
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := c.rules.Reason(c.path); got != c.want {
				t.Errorf("Reason(%q) = %q, want %q", c.path, got, c.want)
			}
		})
	}
}

// Contract: skip/K1
func TestSkippedDirectories(t *testing.T) {
	const built = "vendored or built"
	var cases []reasonCase
	for _, dir := range []string{"vendor", "node_modules", "dist", "build", "target", "third_party", ".git"} {
		cases = append(cases,
			reasonCase{dir + " at the top", Rules{}, dir + "/main.go", built},
			reasonCase{dir + " nested deep", Rules{}, "a/b/c/" + dir + "/d/e.go", built},
		)
	}
	cases = append(cases,
		reasonCase{"a file named like a skipped directory", Rules{}, "src/vendor", ""},
		reasonCase{"a file whose name contains a skipped word", Rules{}, "src/vendored.go", ""},
		reasonCase{"another file with a skipped word", Rules{}, "src/latest.go", ""},
		reasonCase{"a directory that merely starts with a skipped word", Rules{}, "builds/main.go", ""},
		reasonCase{"a directory that merely ends with a skipped word", Rules{}, "rebuild/main.go", ""},
		reasonCase{"a leading dot-slash is ignored", Rules{}, "./vendor/x.go", built},
	)
	runReasons(t, cases)
}

// Contract: skip/K2
func TestLockfilesAndGeneratedFiles(t *testing.T) {
	const gen = "lockfile or generated"
	var cases []reasonCase
	for _, p := range []string{
		"Gemfile.lock", "yarn.lock", "package-lock.json", "go.sum",
		"app.min.js", "app.min.css", "app.js.map", "api.pb.go",
		"schema_generated.go", "schema_generated.ts", "client.generated.ts", "client.generated.go",
	} {
		cases = append(cases,
			reasonCase{p + " at the top", Rules{}, p, gen},
			reasonCase{p + " nested", Rules{}, "web/static/" + p, gen},
		)
	}
	cases = append(cases,
		reasonCase{"package-lock.json is generated, not just data", Rules{}, "package-lock.json", gen},
		reasonCase{"lock as a word in a name", Rules{}, "src/lockbox.go", ""},
		reasonCase{"a .lock that is not the extension", Rules{}, "src/clock.go", ""},
		reasonCase{"min inside a name", Rules{}, "src/admin.js", ""},
		reasonCase{"generated without the separator", Rules{}, "src/generated.go", ""},
		reasonCase{"pb.go lookalike", Rules{}, "src/pb.go", ""},
		reasonCase{"a skipped directory wins over a generated name", Rules{}, "vendor/api.pb.go", "vendored or built"},
	)
	runReasons(t, cases)
}

// Contract: skip/K3
func TestProseAndDataFiles(t *testing.T) {
	const prose = "prose or data"
	var cases []reasonCase
	for _, ext := range []string{"md", "txt", "rst", "json", "yaml", "yml", "toml", "xml", "csv", "svg", "html"} {
		cases = append(cases,
			reasonCase{"." + ext + " at the top", Rules{}, "file." + ext, prose},
			reasonCase{"." + ext + " nested", Rules{}, "docs/deep/file." + ext, prose},
		)
	}
	cases = append(cases,
		reasonCase{"a bare name that looks like an extension", Rules{}, "md", ""},
		reasonCase{"an extension that only starts with a data one", Rules{}, "src/page.htmlx", ""},
		reasonCase{"a longer extension that ends in a data one", Rules{}, "src/app.tsyml", ""},
		reasonCase{"an extension in the middle of the name", Rules{}, "src/json.go", ""},
		reasonCase{"source in a docs directory is still judged", Rules{}, "docs/build.rb", ""},
		reasonCase{"the lockfile reason wins for package-lock.json", Rules{}, "package-lock.json", "lockfile or generated"},
	)
	runReasons(t, cases)
}

// Contract: skip/K4
func TestTestsAreSkippedUnlessIncluded(t *testing.T) {
	skipped := []string{
		"foo_test.go", "pkg/foo_test.go", "foo_test.rb", "foo_spec.rb", "spec/models/foo_spec.rb",
		"foo.test.ts", "web/foo.test.js", "foo.spec.ts", "web/foo.spec.js",
		"test_foo.py", "pkg/test_foo.py",
		"test/helper.rb", "tests/helper.py", "spec/helper.rb", "__tests__/foo.js",
		"a/b/c/test/helper.rb", "a/b/c/tests/x.go", "a/b/spec/support/x.rb", "a/__tests__/deep/foo.js",
	}
	judged := []string{
		"testing.go", "contest.go", "latest.go", "foo_testing.go", "test.go", "attest_foo.py",
		"src/testdata_loader.go", "src/tests.go", "src/specs.rb", "src/footest.go", "foo.testing.ts",
		"src/tester/main.go", "src/specimen/main.go", "latest/main.go", "src/test_helpers/x.go",
	}

	var cases []reasonCase
	for _, p := range skipped {
		cases = append(cases,
			reasonCase{p + " skipped by default", Rules{}, p, "test"},
			reasonCase{p + " judged when tests are included", Rules{IncludeTests: true}, p, ""},
		)
	}
	for _, p := range judged {
		cases = append(cases,
			reasonCase{p + " is not a test", Rules{}, p, ""},
			reasonCase{p + " is not a test with tests included", Rules{IncludeTests: true}, p, ""},
		)
	}
	cases = append(cases,
		reasonCase{"a skipped directory is not undone by including tests", Rules{IncludeTests: true}, "vendor/foo_test.go", "vendored or built"},
		reasonCase{"prose in a test directory stays prose", Rules{IncludeTests: true}, "test/notes.md", "prose or data"},
		reasonCase{"prose is named before tests", Rules{}, "test/notes.md", "prose or data"},
	)
	runReasons(t, cases)
}

// Contract: skip/K5
func TestMatch(t *testing.T) {
	cases := []struct {
		pattern, path string
		want          bool
	}{
		// No slash: the file name, in any directory.
		{"*.go", "main.go", true},
		{"*.go", "a/b/c/main.go", true},
		{"main.go", "a/main.go", true},
		{"main.go", "a/main.go.bak", false},
		{"gen", "gen/main.go", false},
		{"*.go", "a.go/readme", false},
		{"?.go", "a.go", true},
		{"?.go", "ab.go", false},
		{"*", "a/b/c.go", true},
		// A slash: the whole path.
		{"src/*.go", "src/main.go", true},
		{"src/*.go", "src/deep/main.go", false},
		{"src/*.go", "lib/src/main.go", false},
		{"src/*.go", "other/main.go", false},
		{"src/main.go", "src/main.go", true},
		{"src/main.go", "lib/main.go", false},
		{"src/*/main.go", "src/a/main.go", true},
		{"src/*/main.go", "src/a/b/main.go", false},
		{"s?c/main.go", "src/main.go", true},
		// A trailing slash names a directory and everything under it.
		{"docs/", "docs/guide/intro.go", true},
		{"docs/", "docs/a.go", true},
		{"docs/", "lib/docs/a.go", false},
		{"docs/", "docs", false},
		{"**/fixtures/", "a/b/fixtures/x/y.go", true},
		// "*" and "?" stay inside one segment.
		{"a*c/x.go", "a/b/c/x.go", false},
		{"a?c/x.go", "a/c/x.go", false},
		// "**" matches any number of directories, including none.
		{"src/**/main.go", "src/main.go", true},
		{"src/**/main.go", "src/a/main.go", true},
		{"src/**/main.go", "src/a/b/c/main.go", true},
		{"src/**/main.go", "lib/a/main.go", false},
		{"src/**/main.go", "x/src/main.go", false},
		{"**/main.go", "main.go", true},
		{"**/main.go", "a/b/main.go", true},
		{"**/main.go", "a/b/other.go", false},
		{"src/**", "src/a.go", true},
		{"src/**", "src/a/b/c.go", true},
		{"src/**", "lib/a.go", false},
		{"**/gen/**/*.go", "gen/a.go", true},
		{"**/gen/**/*.go", "x/gen/y/z/a.go", true},
		{"**/gen/**/*.go", "x/gen/a.txt", false},
		{"a/**/**/b", "a/b", true},
		{"a/**/b", "a/b/c", false},
		// Edges.
		{"", "main.go", false},
		{"[", "main.go", false},
		{"*.go", "", false},
	}
	for _, c := range cases {
		if got := Match(c.pattern, c.path); got != c.want {
			t.Errorf("Match(%q, %q) = %v, want %v", c.pattern, c.path, got, c.want)
		}
	}
}

// Contract: skip/K5
func TestExtraPatternsSkip(t *testing.T) {
	r := Rules{Extra: []string{"*.gen.go", "internal/legacy/**", "docs/*.go"}}
	runReasons(t, []reasonCase{
		{"name pattern, top", r, "a.gen.go", "skip rule *.gen.go"},
		{"name pattern, nested", r, "x/y/a.gen.go", "skip rule *.gen.go"},
		{"path pattern, directly under", r, "internal/legacy/a.go", "skip rule internal/legacy/**"},
		{"path pattern, deep", r, "internal/legacy/a/b/c.go", "skip rule internal/legacy/**"},
		{"path pattern anchored at the root", r, "pkg/internal/legacy/a.go", ""},
		{"path pattern does not match a sibling directory", r, "internal/modern/a.go", ""},
		{"slash pattern does not reach into subdirectories", r, "docs/deep/a.go", ""},
		{"slash pattern matches its own directory", r, "docs/a.go", "skip rule docs/*.go"},
		{"unmatched path is judged", r, "main.go", ""},
		{"extra patterns can skip tests-included files", Rules{Extra: []string{"*_test.go"}, IncludeTests: true}, "a_test.go", "skip rule *_test.go"},
	})
}

// Contract: skip/K6
func TestEverySkipHasItsReason(t *testing.T) {
	extra := Rules{Extra: []string{"*.go", "docs/**"}}
	runReasons(t, []reasonCase{
		{"vendored", Rules{}, "vendor/x.go", "vendored or built"},
		{"lockfile", Rules{}, "go.sum", "lockfile or generated"},
		{"prose", Rules{}, "README.md", "prose or data"},
		{"test", Rules{}, "a_test.go", "test"},
		{"extra rule names its pattern", extra, "main.go", "skip rule *.go"},
		{"extra rule names the first pattern that matched", Rules{Extra: []string{"*.rb", "*.go", "a*"}}, "a.go", "skip rule *.go"},
		{"default rules apply before extra ones", extra, "vendor/x.go", "vendored or built"},
		{"generated beats extra", extra, "x.pb.go", "lockfile or generated"},
		{"prose beats extra", extra, "docs/a.md", "prose or data"},
		{"test beats extra", extra, "a_test.go", "test"},
		{"a path no rule matches is judged", Rules{Extra: []string{"*.rb"}}, "main.go", ""},
		{"a path is judged with no rules at all", Rules{}, "cmd/qualm/main.go", ""},
		{"an empty path is judged", Rules{}, "", ""},
		{"a nil Extra and tests included still judge source", Rules{IncludeTests: true}, "lib/a.rb", ""},
	})
}
