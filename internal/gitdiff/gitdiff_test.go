package gitdiff

import (
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// git runs git in dir and returns its trimmed output. The environment is scrubbed of the
// developer's own git configuration so a test never reads it.
func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

// newRepo makes an empty repository on branch main with a local identity.
func newRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	git(t, dir, "init", "-q", "-b", "main")
	git(t, dir, "config", "user.name", "Test")
	git(t, dir, "config", "user.email", "test@example.com")
	git(t, dir, "config", "commit.gpgsign", "false")
	return dir
}

func write(t *testing.T, dir, name, content string) {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// commit stages everything and commits it, returning the new commit's id.
func commit(t *testing.T, dir, msg string) string {
	t.Helper()
	git(t, dir, "add", "-A")
	git(t, dir, "commit", "-q", "-m", msg)
	return git(t, dir, "rev-parse", "HEAD")
}

// lines returns n numbered lines so an edit in the middle leaves context around it.
func lines(n int) string {
	var b strings.Builder
	for i := 1; i <= n; i++ {
		b.WriteString("line ")
		b.WriteString(strings.Repeat("x", i%3))
		b.WriteString(string(rune('a' + i%26)))
		b.WriteString("\n")
	}
	return b.String()
}

func byPath(t *testing.T, cs []Change) map[string]Change {
	t.Helper()
	m := map[string]Change{}
	for _, c := range cs {
		m[c.Path] = c
	}
	return m
}

func paths(cs []Change) []string {
	var out []string
	for _, c := range cs {
		out = append(out, c.Path)
	}
	return out
}

// Contract: diff/D1
func TestBaseIsMergeBaseOfRefAndHead(t *testing.T) {
	dir := newRepo(t)
	write(t, dir, "a.go", "one\n")
	c1 := commit(t, dir, "c1")
	git(t, dir, "checkout", "-q", "-b", "feature")
	write(t, dir, "b.go", "feature\n")
	commit(t, dir, "feature work")
	git(t, dir, "checkout", "-q", "main")
	write(t, dir, "c.go", "main moved\n")
	commit(t, dir, "main moves on")
	git(t, dir, "checkout", "-q", "feature")

	got, err := Base(dir, "main")
	if err != nil {
		t.Fatal(err)
	}
	if got != c1 {
		t.Errorf("Base = %s, want merge base %s", got, c1)
	}
}

// Contract: diff/D1
func TestBaseMayBeHeadItself(t *testing.T) {
	dir := newRepo(t)
	write(t, dir, "a.go", "one\n")
	head := commit(t, dir, "c1")
	got, err := Base(dir, "main")
	if err != nil {
		t.Fatal(err)
	}
	if got != head {
		t.Errorf("Base = %s, want HEAD %s", got, head)
	}
}

// addOrigin gives dir a bare origin, pushes the named branches to it and fetches.
func addOrigin(t *testing.T, dir string, branches ...string) {
	t.Helper()
	bare := t.TempDir()
	git(t, bare, "init", "-q", "--bare")
	git(t, dir, "remote", "add", "origin", bare)
	for _, b := range branches {
		git(t, dir, "push", "-q", "origin", b)
	}
	git(t, dir, "fetch", "-q", "origin")
}

// Contract: diff/D1
func TestDefaultBaseOrder(t *testing.T) {
	// Each case builds a repository where local main has moved one commit past the remote
	// branch, then branches feature from local main. The base then says which name won:
	// the remote's commit when a remote name resolved first, the local one otherwise.
	cases := []struct {
		name      string
		branch    string // the repository's main line
		remote    []string
		setHead   string // remote branch to point origin/HEAD at
		wantAfter bool   // true when the base should be the commit after the remote branch
	}{
		{"origin/HEAD first", "main", []string{"main", "trunk"}, "trunk", false},
		{"origin/main over main", "main", []string{"main"}, "", false},
		{"origin/master over master", "master", []string{"master"}, "", false},
		{"local main", "main", nil, "", true},
		{"master alone", "master", nil, "", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := newRepo(t)
			git(t, dir, "checkout", "-q", "-b", tc.branch)
			write(t, dir, "a.go", "one\n")
			first := commit(t, dir, "first")
			if len(tc.remote) > 0 {
				// A remote branch other than the main line is the first commit too.
				for _, b := range tc.remote {
					if b != tc.branch {
						git(t, dir, "branch", b)
					}
				}
				addOrigin(t, dir, tc.remote...)
				if tc.setHead != "" {
					git(t, dir, "remote", "set-head", "origin", tc.setHead)
				}
			}
			write(t, dir, "b.go", "two\n")
			second := commit(t, dir, "second")
			git(t, dir, "checkout", "-q", "-b", "feature")

			got, err := Base(dir, "")
			if err != nil {
				t.Fatal(err)
			}
			want := first
			if tc.wantAfter {
				want = second
			}
			if got != want {
				t.Errorf("Base = %s, want %s", got, want)
			}
		})
	}
}

// Contract: diff/D1
func TestDefaultBaseErrorsWhenNothingResolves(t *testing.T) {
	dir := newRepo(t)
	git(t, dir, "checkout", "-q", "-b", "trunk")
	write(t, dir, "a.go", "one\n")
	commit(t, dir, "first")
	if got, err := Base(dir, ""); err == nil {
		t.Errorf("Base = %s, want an error when no default branch exists", got)
	}
}

// Contract: diff/D2
func TestBaseRejectsRefsThatAreNotCommits(t *testing.T) {
	dir := newRepo(t)
	write(t, dir, "a.go", "one\n")
	commit(t, dir, "c1")
	tree := git(t, dir, "rev-parse", "HEAD^{tree}")
	for _, ref := range []string{"no-such-branch", "--output=" + filepath.Join(dir, "pwned"), "-h", tree} {
		if got, err := Base(dir, ref); err == nil {
			t.Errorf("Base(%q) = %s, want an error", ref, got)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "pwned")); err == nil {
		t.Error("an option-shaped ref was run as an option")
	}
}

// branchRepo returns a repository with a.go, b.go and c.go on main, now on branch feature.
func branchRepo(t *testing.T) (dir, base string) {
	t.Helper()
	dir = newRepo(t)
	write(t, dir, "a.go", lines(30))
	write(t, dir, "b.go", lines(30))
	write(t, dir, "c.go", lines(30))
	base = commit(t, dir, "base")
	git(t, dir, "checkout", "-q", "-b", "feature")
	return dir, base
}

// Contract: diff/D3
func TestChangesCountCommittedStagedUnstagedAndUntracked(t *testing.T) {
	dir, base := branchRepo(t)
	write(t, dir, "committed.go", "package a\n")
	write(t, dir, "a.go", strings.Replace(lines(30), "line", "LINE", 1))
	commit(t, dir, "committed")
	write(t, dir, "staged.go", "package b\n")
	git(t, dir, "add", "staged.go")
	write(t, dir, "b.go", strings.Replace(lines(30), "line", "LINE", 2))
	write(t, dir, "untracked.go", "package c\n")
	write(t, dir, ".gitignore", "ignored.go\nbuild/\n")
	write(t, dir, "ignored.go", "package d\n")
	write(t, dir, "build/out.go", "package e\n")

	got, err := Changes(dir, base, nil)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{".gitignore", "a.go", "b.go", "committed.go", "staged.go", "untracked.go"}
	if !reflect.DeepEqual(paths(got), want) {
		t.Errorf("paths = %v, want %v", paths(got), want)
	}
	for _, c := range got {
		if c.Diff == "" || !strings.HasPrefix(c.Diff, "--- ") {
			t.Errorf("%s: diff does not start at its --- line: %q", c.Path, c.Diff)
		}
	}
}

// Contract: diff/D4
func TestChangesListStatusesSortedAndLeaveOutDeletes(t *testing.T) {
	dir, _ := branchRepo(t)
	// b.go gets unrelated content so git pairs the rename with c.go and not with the deleted file.
	write(t, dir, "b.go", strings.Repeat("something else entirely\n", 30))
	base := commit(t, dir, "distinct b")
	write(t, dir, "z-new.go", "package z\n")
	write(t, dir, "a.go", strings.Replace(lines(30), "line", "LINE", 1))
	git(t, dir, "rm", "-q", "b.go")
	// A rename with a small edit, so git's rename detection still pairs the files.
	if err := os.MkdirAll(filepath.Join(dir, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	git(t, dir, "mv", "c.go", "sub/moved.go")
	write(t, dir, "sub/moved.go", strings.Replace(lines(30), "line", "LINE", 3))
	write(t, dir, "m-untracked.go", "package m\n")

	got, err := Changes(dir, base, nil)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"a.go", "m-untracked.go", "sub/moved.go", "z-new.go"}
	if !reflect.DeepEqual(paths(got), want) {
		t.Fatalf("paths = %v, want %v", paths(got), want)
	}
	m := byPath(t, got)
	for path, status := range map[string]string{
		"a.go": "modified", "m-untracked.go": "added", "sub/moved.go": "renamed", "z-new.go": "added",
	} {
		if m[path].Status != status {
			t.Errorf("%s status = %q, want %q", path, m[path].Status, status)
		}
	}
	if d := m["sub/moved.go"].Diff; !strings.HasPrefix(d, "--- a/c.go\n+++ b/sub/moved.go\n") {
		t.Errorf("renamed diff should show old and new paths, got %q", d)
	}
}

// Contract: diff/D4
func TestChangesKeepOddFileNames(t *testing.T) {
	dir, base := branchRepo(t)
	write(t, dir, "sp ace/café 日本.go", "package x\n")
	write(t, dir, "tab\there.go", "package y\n")
	git(t, dir, "add", "-A")
	write(t, dir, "untracked ü.go", "package z\n")

	got, err := Changes(dir, base, nil)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"sp ace/café 日本.go", "tab\there.go", "untracked ü.go"}
	if !reflect.DeepEqual(paths(got), want) {
		t.Errorf("paths = %q, want %q", paths(got), want)
	}
	for _, c := range got {
		if c.Diff == "" {
			t.Errorf("%q has no diff", c.Path)
		}
	}
}

// Contract: diff/D5
func TestChangesNarrowedByPaths(t *testing.T) {
	dir, base := branchRepo(t)
	write(t, dir, "pkg/one.go", "package one\n")
	write(t, dir, "pkg/deep/two.go", "package two\n")
	write(t, dir, "other/three.go", "package three\n")
	write(t, dir, "a.go", strings.Replace(lines(30), "line", "LINE", 1))
	git(t, dir, "add", "pkg/one.go", "other/three.go")

	cases := []struct {
		narrow []string
		want   []string
	}{
		{[]string{"pkg"}, []string{"pkg/deep/two.go", "pkg/one.go"}},
		{[]string{"a.go"}, []string{"a.go"}},
		{[]string{"a.go", "other"}, []string{"a.go", "other/three.go"}},
		{[]string{"pkg/deep/two.go"}, []string{"pkg/deep/two.go"}},
		{[]string{"nothing-here"}, nil},
	}
	for _, tc := range cases {
		got, err := Changes(dir, base, tc.narrow)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(paths(got), tc.want) {
			t.Errorf("paths %v: got %v, want %v", tc.narrow, paths(got), tc.want)
		}
	}
}

// Contract: diff/D6
func TestDiffHasNoGitHeaderLines(t *testing.T) {
	dir, base := branchRepo(t)
	write(t, dir, "a.go", strings.Replace(lines(30), "line", "LINE", 1))
	if err := os.Chmod(filepath.Join(dir, "a.go"), 0o755); err != nil {
		t.Fatal(err)
	}
	write(t, dir, "new.go", "package n\n")
	git(t, dir, "add", "new.go")
	git(t, dir, "mv", "b.go", "b2.go")
	write(t, dir, "b2.go", strings.Replace(lines(30), "line", "LINE", 2))
	write(t, dir, "untracked.go", "package u\n")

	got, err := Changes(dir, base, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 4 {
		t.Fatalf("got %d changes, want 4: %v", len(got), paths(got))
	}
	for _, c := range got {
		if !strings.HasPrefix(c.Diff, "--- ") {
			t.Errorf("%s: diff starts %q", c.Path, c.Diff[:min(20, len(c.Diff))])
		}
		for _, l := range strings.Split(c.Diff, "\n") {
			for _, bad := range []string{"diff --git", "index ", "old mode", "new mode", "new file mode",
				"similarity index", "rename from", "rename to"} {
				if strings.HasPrefix(l, bad) {
					t.Errorf("%s: diff keeps %q line %q", c.Path, bad, l)
				}
			}
		}
	}
}

func TestNormaliseDropsEverythingBeforeFirstMinusLine(t *testing.T) {
	raw := "diff --git a/x b/x\nindex 1..2 100644\n--- a/x\n+++ b/x\n@@ -1 +1 @@\n-a\n+b\n"
	want := "--- a/x\n+++ b/x\n@@ -1 +1 @@\n-a\n+b\n"
	if got := Normalise(raw); got != want {
		t.Errorf("Normalise = %q, want %q", got, want)
	}
	if got := Normalise("diff --git a/x b/x\nsimilarity index 100%\n"); got != "" {
		t.Errorf("Normalise of a diff with no hunks = %q, want empty", got)
	}
}

// Contract: diff/D7
func TestSameChangeGivesSameDiffHoweverItIsHeld(t *testing.T) {
	edit := func(dir string) {
		write(t, dir, "a.go", strings.Replace(lines(40), "line", "LINE", 2))
		write(t, dir, "fresh.go", "package fresh\n")
	}
	setup := func() (string, string) {
		dir := newRepo(t)
		write(t, dir, "a.go", lines(40))
		base := commit(t, dir, "base")
		git(t, dir, "checkout", "-q", "-b", "feature")
		return dir, base
	}
	diffs := func(dir, base string) map[string]string {
		cs, err := Changes(dir, base, nil)
		if err != nil {
			t.Fatal(err)
		}
		m := map[string]string{}
		for _, c := range cs {
			m[c.Path] = c.Diff
		}
		return m
	}

	committed, cb := setup()
	edit(committed)
	commit(t, committed, "edit")
	staged, sb := setup()
	edit(staged)
	git(t, staged, "add", "-A")
	unstaged, ub := setup()
	edit(unstaged)

	a, b, c := diffs(committed, cb), diffs(staged, sb), diffs(unstaged, ub)
	if len(a) != 2 || a["a.go"] == "" || a["fresh.go"] == "" {
		t.Fatalf("committed diffs look wrong: %v", a)
	}
	if !reflect.DeepEqual(a, b) {
		t.Errorf("committed and staged differ:\n%v\n%v", a, b)
	}
	if !reflect.DeepEqual(a, c) {
		t.Errorf("committed and unstaged differ:\n%v\n%v", a, c)
	}
}

// Contract: diff/D8
func TestBinaryFilesAreMarkedAndHaveNoDiff(t *testing.T) {
	dir, base := branchRepo(t)
	write(t, dir, "img.png", "\x89PNG\x00\x01\x02old")
	commit(t, dir, "add binary")
	base = git(t, dir, "rev-parse", "HEAD")
	write(t, dir, "img.png", "\x89PNG\x00\x01\x02new!")
	write(t, dir, "fresh.bin", "\x00\x00\x00\x01")
	write(t, dir, "text.go", "package t\n")

	got, err := Changes(dir, base, nil)
	if err != nil {
		t.Fatal(err)
	}
	m := byPath(t, got)
	for _, p := range []string{"img.png", "fresh.bin"} {
		if !m[p].Binary || m[p].Diff != "" {
			t.Errorf("%s: Binary=%v Diff=%q, want binary with no diff", p, m[p].Binary, m[p].Diff)
		}
	}
	if m["text.go"].Binary || m["text.go"].Diff == "" {
		t.Errorf("text.go wrongly treated as binary: %+v", m["text.go"])
	}
}

// Contract: diff/D9
func TestAttributesMarkGeneratedAndVendoredFiles(t *testing.T) {
	dir, base := branchRepo(t)
	write(t, dir, ".gitattributes",
		"gen.go linguist-generated\nvend/** linguist-vendored=true\nnot.go linguist-generated=false\nplain.go\n")
	write(t, dir, "gen.go", "package g\n")
	write(t, dir, "vend/lib.go", "package v\n")
	write(t, dir, "not.go", "package n\n")
	write(t, dir, "plain.go", "package p\n")
	git(t, dir, "add", "gen.go")

	got, err := Changes(dir, base, nil)
	if err != nil {
		t.Fatal(err)
	}
	m := byPath(t, got)
	want := map[string]bool{"gen.go": true, "vend/lib.go": true, "not.go": false, "plain.go": false}
	for p, marked := range want {
		if m[p].Marked != marked {
			t.Errorf("%s Marked = %v, want %v", p, m[p].Marked, marked)
		}
	}
}

// Contract: diff/D12
func TestRootIsFoundFromAnyDirectoryInside(t *testing.T) {
	dir := newRepo(t)
	write(t, dir, "lib/deep/a.go", "package a\n")
	commit(t, dir, "first")
	// macOS hands out temp directories through a symlink, and git reports the real path.
	want, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, from := range []string{dir, filepath.Join(dir, "lib"), filepath.Join(dir, "lib", "deep")} {
		got, err := Root(from)
		if err != nil {
			t.Fatalf("Root(%q): %v", from, err)
		}
		if got != want {
			t.Errorf("Root(%q) = %q, want %q", from, got, want)
		}
	}
}

// Contract: diff/D12
func TestRootOutsideARepositoryIsAnError(t *testing.T) {
	// GIT_CEILING_DIRECTORIES stops git from walking up into a repository that happens to hold
	// the temp directory.
	dir := t.TempDir()
	t.Setenv("GIT_CEILING_DIRECTORIES", filepath.Dir(dir))
	if got, err := Root(dir); err == nil {
		t.Errorf("Root outside a repository = %q, want an error", got)
	} else if !strings.Contains(err.Error(), "not a git repository") {
		t.Errorf("error %q should say it is not a git repository", err)
	}
}
