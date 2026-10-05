package gitdiff

import (
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// TestMain cuts the tests off from the developer's git configuration and home. The code under
// test runs git itself, so scrubbing the environment of the test's own git commands would not be
// enough.
func TestMain(m *testing.M) {
	home, err := os.MkdirTemp("", "qualm-gitdiff-")
	if err != nil {
		panic(err)
	}
	for name, value := range map[string]string{
		"HOME":              home,
		"XDG_CONFIG_HOME":   filepath.Join(home, "config"),
		"GIT_CONFIG_GLOBAL": os.DevNull,
		"GIT_CONFIG_SYSTEM": os.DevNull,
	} {
		os.Setenv(name, value)
	}
	// A test run from a git hook inherits these, and they would point every git command at the
	// hook's repository.
	for _, name := range []string{"GIT_DIR", "GIT_WORK_TREE", "GIT_INDEX_FILE"} {
		os.Unsetenv(name)
	}
	code := m.Run()
	os.RemoveAll(home)
	os.Exit(code)
}

// git runs git in dir and returns its trimmed output.
func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput()
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

// readAll lists every change between base and the working tree and reads its diff, by path.
func readAll(t *testing.T, dir, base string) map[string]Diff {
	t.Helper()
	changes, err := Changes(dir, base, nil)
	if err != nil {
		t.Fatal(err)
	}
	m := map[string]Diff{}
	for _, c := range changes {
		d, err := Read(dir, base, c)
		if err != nil {
			t.Fatalf("Read(%s): %v", c.Path, err)
		}
		m[c.Path] = d
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
	for path, d := range readAll(t, dir, base) {
		if !strings.HasPrefix(d.Text, "--- ") {
			t.Errorf("%s: diff does not start at its --- line: %q", path, d.Text)
		}
	}
}

// Contract: diff/D4
func TestChangesListPathsSortedAndLeaveOutDeletes(t *testing.T) {
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
	if d := readAll(t, dir, base)["sub/moved.go"].Text; !strings.HasPrefix(d, "--- a/c.go\n+++ b/sub/moved.go\n") {
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
	for path, d := range readAll(t, dir, base) {
		if d.Text == "" {
			t.Errorf("%q has no diff", path)
		}
	}
}

// Contract: diff/D5
func TestChangesNarrowedByPaths(t *testing.T) {
	dir, base := branchRepo(t)
	write(t, dir, "pkg/one.go", "package one\n")
	write(t, dir, "pkg/deep/two.go", "package two\n")
	write(t, dir, "other/three.go", "package three\n")
	write(t, dir, "pkg2/four.go", "package four\n")
	write(t, dir, "a.go", strings.Replace(lines(30), "line", "LINE", 1))
	git(t, dir, "add", "pkg/one.go", "other/three.go")

	cases := []struct {
		narrow []string
		want   []string
	}{
		// A directory selects what is under it, and not a neighbour whose name starts the same.
		{[]string{"pkg"}, []string{"pkg/deep/two.go", "pkg/one.go"}},
		{[]string{"pkg/"}, []string{"pkg/deep/two.go", "pkg/one.go"}},
		{[]string{"a.go"}, []string{"a.go"}},
		{[]string{"./a.go"}, []string{"a.go"}},
		{[]string{"a.go", "other"}, []string{"a.go", "other/three.go"}},
		{[]string{"pkg/deep/two.go"}, []string{"pkg/deep/two.go"}},
		{[]string{"pkg", "pkg/one.go"}, []string{"pkg/deep/two.go", "pkg/one.go"}},
		{[]string{"."}, []string{"a.go", "other/three.go", "pkg/deep/two.go", "pkg/one.go", "pkg2/four.go"}},
		// These are in the working tree or in the base, and hold no change.
		{[]string{"b.go"}, nil},
		{[]string{"pkg/deep", "c.go"}, []string{"pkg/deep/two.go"}},
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

	got := readAll(t, dir, base)
	if len(got) != 4 {
		t.Fatalf("got %d changes, want 4: %v", len(got), got)
	}
	for path, d := range got {
		if !strings.HasPrefix(d.Text, "--- ") {
			t.Errorf("%s: diff starts %q", path, d.Text[:min(20, len(d.Text))])
		}
		for _, l := range strings.Split(d.Text, "\n") {
			for _, bad := range []string{"diff --git", "index ", "old mode", "new mode", "new file mode",
				"similarity index", "rename from", "rename to"} {
				if strings.HasPrefix(l, bad) {
					t.Errorf("%s: diff keeps %q line %q", path, bad, l)
				}
			}
		}
	}
}

func TestNormaliseDropsEverythingBeforeFirstMinusLine(t *testing.T) {
	raw := "diff --git a/x b/x\nindex 1..2 100644\n--- a/x\n+++ b/x\n@@ -1 +1 @@\n-a\n+b\n"
	want := "--- a/x\n+++ b/x\n@@ -1 +1 @@\n-a\n+b\n"
	if got := normalise(raw); got != want {
		t.Errorf("normalise = %q, want %q", got, want)
	}
	if got := normalise("diff --git a/x b/x\nsimilarity index 100%\n"); got != "" {
		t.Errorf("normalise of a diff with no hunks = %q, want empty", got)
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

	committed, cb := setup()
	edit(committed)
	commit(t, committed, "edit")
	staged, sb := setup()
	edit(staged)
	git(t, staged, "add", "-A")
	unstaged, ub := setup()
	edit(unstaged)

	a, b, c := readAll(t, committed, cb), readAll(t, staged, sb), readAll(t, unstaged, ub)
	if len(a) != 2 || a["a.go"].Text == "" || a["fresh.go"].Text == "" {
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
	dir, _ := branchRepo(t)
	write(t, dir, "img.png", "\x89PNG\x00\x01\x02old")
	base := commit(t, dir, "add binary")
	write(t, dir, "img.png", "\x89PNG\x00\x01\x02new!")
	write(t, dir, "fresh.bin", "\x00\x00\x00\x01")
	write(t, dir, "text.go", "package t\n")

	m := readAll(t, dir, base)
	for _, p := range []string{"img.png", "fresh.bin"} {
		if !m[p].Binary || m[p].Text != "" {
			t.Errorf("%s: Binary=%v Text=%q, want binary with no diff", p, m[p].Binary, m[p].Text)
		}
	}
	if m["text.go"].Binary || m["text.go"].Text == "" {
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

// fakeGit puts a git on the path that prints what the test says, so that output real git never
// produces can be read: names for the listing, attrs for the marks and patch for any file's diff.
// Backslash escapes in the three are expanded.
func fakeGit(t *testing.T, names, attrs, patch string) {
	t.Helper()
	dir := t.TempDir()
	script := `#!/bin/sh
case "$*" in
*--name-status*) printf '%b' "$FAKE_NAMES" ;;
*check-attr*) cat >/dev/null; printf '%b' "$FAKE_ATTRS" ;;
*ls-files*) ;;
*-U8*) printf '%b' "$FAKE_PATCH" ;;
esac
`
	if err := os.WriteFile(filepath.Join(dir, "git"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("FAKE_NAMES", names)
	t.Setenv("FAKE_ATTRS", attrs)
	t.Setenv("FAKE_PATCH", patch)
}

// Contract: diff/D4
func TestChangesReadOutputWithNoTrailingSeparator(t *testing.T) {
	fakeGit(t, `M\0x`, `x\0`, ``)
	got, err := Changes(t.TempDir(), "base", nil)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(paths(got), []string{"x"}) || got[0].Marked {
		t.Errorf("changes = %+v", got)
	}
}

// Contract: diff/D4
func TestChangesRejectAStatusWithNoPath(t *testing.T) {
	fakeGit(t, `M`, ``, ``)
	_, err := Changes(t.TempDir(), "base", nil)
	if err == nil || !strings.Contains(err.Error(), "malformed") {
		t.Errorf("err = %v", err)
	}
}

// Contract: diff/D12
func TestGitFailureCarriesItsMessageOrItsExitStatus(t *testing.T) {
	dir := newRepo(t)
	_, err := run(dir, "", nil, "rev-parse", "--verify", "nosuchref")
	if err == nil || !strings.HasPrefix(err.Error(), "git rev-parse: fatal:") {
		t.Errorf("err = %v, want git's own message", err)
	}

	// git diff --quiet says nothing when it fails, so the exit status stands in for the message.
	write(t, dir, "a.go", "one\n")
	git(t, dir, "add", "a.go")
	git(t, dir, "commit", "-q", "-m", "a")
	write(t, dir, "a.go", "two\n")
	_, err = run(dir, "", nil, "diff", "--quiet")
	if err == nil || err.Error() != "git diff: exit status 1" {
		t.Errorf("err = %v", err)
	}
}

// spyGit puts a git on the path that notes each call's arguments and then runs the real git. It
// returns a function that lists the calls made so far.
func spyGit(t *testing.T) func() []string {
	t.Helper()
	real, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	log := filepath.Join(dir, "calls")
	script := "#!/bin/sh\nprintf '%s\\n' \"$*\" >> \"$SPY_LOG\"\nexec \"$SPY_GIT\" \"$@\"\n"
	if err := os.WriteFile(filepath.Join(dir, "git"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("SPY_LOG", log)
	t.Setenv("SPY_GIT", real)
	return func() []string {
		data, err := os.ReadFile(log)
		if err != nil && !os.IsNotExist(err) {
			t.Fatal(err)
		}
		return strings.Split(strings.TrimSpace(string(data)), "\n")
	}
}

// diffReads picks out the calls that read a file's diff, which are the diff calls that don't ask
// for names alone.
func diffReads(calls []string) []string {
	var out []string
	for _, c := range calls {
		if strings.Contains(c, " diff ") && !strings.Contains(c, "--name-status") {
			out = append(out, c)
		}
	}
	return out
}

// Contract: diff/D17
func TestListingReadsNoDiffAndAReadReadsOne(t *testing.T) {
	dir, base := branchRepo(t)
	write(t, dir, "a.go", strings.Replace(lines(30), "line", "LINE", 1))
	git(t, dir, "mv", "b.go", "b2.go")
	write(t, dir, "untracked.go", "package u\n")
	calls := spyGit(t)

	changes, err := Changes(dir, base, nil)

	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"a.go", "b2.go", "untracked.go"}; !reflect.DeepEqual(paths(changes), want) {
		t.Fatalf("paths = %v, want %v", paths(changes), want)
	}
	if got := diffReads(calls()); len(got) != 0 {
		t.Errorf("listing three changes read %d diffs, want none:\n%s", len(got), strings.Join(got, "\n"))
	}

	// Each read is one call for that file alone, and a rename is asked for by both of its names.
	for i, tail := range []string{" -- a.go", " -- b.go b2.go", " -- /dev/null untracked.go"} {
		d, err := Read(dir, base, changes[i])
		if err != nil || !strings.HasPrefix(d.Text, "--- ") && tail != " -- b.go b2.go" {
			t.Errorf("Read(%s) = %+v, %v", changes[i].Path, d, err)
		}
		if got := diffReads(calls()); len(got) != i+1 || !strings.HasSuffix(got[i], tail) {
			t.Errorf("after reading %s the diff calls are:\n%s\nwant %d, the last ending %q", changes[i].Path, strings.Join(got, "\n"), i+1, tail)
		}
	}
}

// spaced returns n numbered lines with every fifth one blank, so a diff of it has blank lines in
// its context.
func spaced(n int) string {
	var b strings.Builder
	for i := 1; i <= n; i++ {
		if i%5 != 0 {
			b.WriteString("row ")
			b.WriteString(string(rune('a' + i%26)))
		}
		b.WriteString("\n")
	}
	return b.String()
}

// settingsRepo is a repository whose changes each read differently under some git setting.
// algo.rb is the pair of texts from Myers's paper, which the histogram algorithm diffs another
// way. gap.rb has two edits thirty lines apart, near enough for a wider hunk context to join,
// among blank lines. slide.rb gains three lines that git can show starting at either of two
// places, which its indent heuristic decides. fresh.rb is untracked, so its diff comes from
// another git command. sub is a submodule, which git can print as a log. back.rb was staged with
// a change and then put back as it was, which git lists as changed when it is told not to look.
func settingsRepo(t *testing.T) (dir, base string) {
	t.Helper()
	dir = newRepo(t)
	write(t, dir, "algo.rb", "A\nB\nC\nA\nB\nB\nA\n")
	write(t, dir, "gap.rb", spaced(60))
	write(t, dir, "back.rb", "as it was\n")
	write(t, dir, "slide.rb", "1\n2\na\n\nb\n3\n4\n")
	base = commit(t, dir, "base")
	git(t, dir, "checkout", "-q", "-b", "feature")
	write(t, dir, "algo.rb", "C\nB\nA\nB\nA\nC\n")
	gap := strings.Replace(spaced(60), "row k\n", "ROW K\n", 1)
	write(t, dir, "gap.rb", strings.Replace(gap, "row o\n", "ROW O\n", 1))
	write(t, dir, "fresh.rb", "def fresh\n\n  1\nend\n")
	write(t, dir, "slide.rb", "1\n2\na\n\nb\na\n\nb\n3\n4\n")
	// An empty directory with an entry in the index is a submodule nobody has checked out.
	if err := os.Mkdir(filepath.Join(dir, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	git(t, dir, "update-index", "--add", "--cacheinfo", "160000,"+base+",sub")
	write(t, dir, "back.rb", "otherwise\n")
	git(t, dir, "add", "back.rb")
	write(t, dir, "back.rb", "as it was\n")
	return dir, base
}

// Contract: diff/D13
func TestDiffIsTheSameWhateverTheUsersGitSettings(t *testing.T) {
	dir, base := settingsRepo(t)
	want := readAll(t, dir, base)
	for _, path := range []string{"algo.rb", "gap.rb", "slide.rb", "fresh.rb", "sub"} {
		if !strings.HasPrefix(want[path].Text, "--- ") {
			t.Fatalf("%s has no diff to compare with: %+v", path, want[path])
		}
	}
	if len(want) != 5 {
		t.Fatalf("listed %d changes, want 5 with back.rb left out: %v", len(want), want)
	}
	if _, hunks := Hunks(want["gap.rb"].Text); len(hunks) != 2 {
		t.Fatalf("gap.rb has %d hunks, want 2 for a wider context to join", len(hunks))
	}
	same := func(t *testing.T) {
		t.Helper()
		got := readAll(t, dir, base)
		for path := range want {
			if got[path] != want[path] {
				t.Errorf("%s reads differently:\n%s\nwant:\n%s", path, got[path].Text, want[path].Text)
			}
		}
		if len(got) != len(want) {
			t.Errorf("listed %d changes, want %d", len(got), len(want))
		}
	}

	// The test process reads no global configuration, so the settings go in the repository's own.
	for _, setting := range []string{
		"color.ui=always",
		"color.diff=always",
		"diff.external=/bin/echo",
		"diff.algorithm=histogram",
		"diff.mnemonicPrefix=true",
		"diff.noprefix=true",
		"diff.interHunkContext=20",
		"diff.indentHeuristic=false",
		"diff.suppressBlankEmpty=true",
		"diff.submodule=log",
		"diff.autoRefreshIndex=false",
	} {
		t.Run(setting, func(t *testing.T) {
			key, value, _ := strings.Cut(setting, "=")
			git(t, dir, "config", key, value)
			defer git(t, dir, "config", "--unset", key)
			same(t)
		})
	}
	t.Run("GIT_EXTERNAL_DIFF", func(t *testing.T) {
		t.Setenv("GIT_EXTERNAL_DIFF", "/bin/echo")
		same(t)
	})
	// git reads extra diff options from this variable, and they win over the command line.
	t.Run("GIT_DIFF_OPTS", func(t *testing.T) {
		t.Setenv("GIT_DIFF_OPTS", "--unified=0")
		same(t)
	})
	t.Run("a text conversion", func(t *testing.T) {
		attributes := filepath.Join(dir, ".git", "info", "attributes")
		if err := os.WriteFile(attributes, []byte("*.rb diff=shout\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		defer os.Remove(attributes)
		git(t, dir, "config", "diff.shout.textconv", "tr a-z A-Z <")
		defer git(t, dir, "config", "--unset", "diff.shout.textconv")
		same(t)
	})
}

// Contract: diff/D15
func TestADiffThatCantBeReadIsAnError(t *testing.T) {
	cases := map[string]string{
		"nothing at all":              ``,
		"a header and no hunks":       `diff --git a/x b/x\nindex 1111111..2222222 100644\n`,
		"what a diff tool printed":    `x /tmp/old 1111111 100644 /tmp/new 2222222 100644\n`,
		"a diff in colour":            `\033[1mdiff --git a/x b/x\033[m\n\033[1m--- a/x\033[m\n\033[1m+++ b/x\033[m\n\033[36m@@ -1 +1 @@\033[m\n\033[31m-a\033[m\n\033[32m+b\033[m\n`,
		"a new file that isn't empty": `diff --git a/x b/x\nnew file mode 100644\nindex 0000000..e69de29\n`,
		"one mode line":               `diff --git a/x b/x\nnew mode 100755\n`,
		"a rename with an edit lost":  `diff --git a/w b/x\nsimilarity index 97%\nrename from w\nrename to x\n`,
	}
	for name, patch := range cases {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			write(t, dir, "x", "content\n")
			fakeGit(t, `M\0x\0`, ``, patch)
			changes, err := Changes(dir, "base", nil)
			if err != nil || len(changes) != 1 {
				t.Fatalf("Changes = %+v, %v", changes, err)
			}

			d, err := Read(dir, "base", changes[0])

			if err == nil || !strings.HasSuffix(err.Error(), " x") || d != (Diff{}) {
				t.Errorf("Read = %+v, %v: want no diff and an error naming x", d, err)
			}
		})
	}
}

// Contract: diff/D15
func TestADiffWithNoHunksIsReadWhenGitSaysWhyItHasNone(t *testing.T) {
	cases := map[string]struct {
		patch string
		want  Diff
	}{
		"a change of mode": {`diff --git a/x b/x\nold mode 100644\nnew mode 100755\n`, Diff{NoContent: true}},
		"a pure rename":    {`diff --git a/w b/x\nsimilarity index 100%\nrename from w\nrename to x\n`, Diff{NoContent: true}},
		"a binary file":    {`diff --git a/x b/x\nindex 1111111..2222222 100644\nBinary files a/x and b/x differ\n`, Diff{Binary: true}},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			write(t, dir, "x", "content\n")
			fakeGit(t, `M\0x\0`, ``, tc.patch)
			changes, err := Changes(dir, "base", nil)
			if err != nil || len(changes) != 1 {
				t.Fatalf("Changes = %+v, %v", changes, err)
			}

			d, err := Read(dir, "base", changes[0])

			if err != nil || d != tc.want {
				t.Errorf("Read = %+v, %v: want %+v", d, err, tc.want)
			}
		})
	}
}

// Contract: diff/D15
func TestOnlyAModeARenameOrAnEmptyNewFileHasNoContent(t *testing.T) {
	dir, base := branchRepo(t)
	if err := os.Chmod(filepath.Join(dir, "a.go"), 0o755); err != nil {
		t.Fatal(err)
	}
	git(t, dir, "mv", "b.go", "moved.go")
	write(t, dir, "empty.go", "")
	git(t, dir, "add", "empty.go")
	write(t, dir, "untracked-empty.go", "")
	write(t, dir, "c.go", strings.Replace(lines(30), "line", "LINE", 1))

	got := readAll(t, dir, base)

	for _, path := range []string{"a.go", "moved.go", "empty.go", "untracked-empty.go"} {
		if got[path] != (Diff{NoContent: true}) {
			t.Errorf("%s = %+v, want no content and nothing else", path, got[path])
		}
	}
	if d := got["c.go"]; d.NoContent || d.Binary || d.Text == "" {
		t.Errorf("c.go = %+v, want a diff", d)
	}
	if len(got) != 5 {
		t.Errorf("read %d changes, want 5", len(got))
	}
}

// Contract: diff/D14
func TestAFileNameIsNeverReadAsAPattern(t *testing.T) {
	dir := newRepo(t)
	odd := []string{":evil.rb", ":(bogus)x.rb", "lib/*.rb", "lib/a.rb", "lib/b.rb"}
	for _, name := range odd {
		write(t, dir, name, "first in "+name+"\n")
	}
	// A pattern in the attributes still matches, and marks the files it names and no other.
	write(t, dir, ".gitattributes", "lib/a.rb linguist-generated\n")
	base := commit(t, dir, "base")
	git(t, dir, "checkout", "-q", "-b", "feature")
	for _, name := range odd {
		write(t, dir, name, "second in "+name+"\n")
	}

	changes, err := Changes(dir, base, nil)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{":(bogus)x.rb", ":evil.rb", "lib/*.rb", "lib/a.rb", "lib/b.rb"}; !reflect.DeepEqual(paths(changes), want) {
		t.Fatalf("paths = %q, want %q", paths(changes), want)
	}
	for _, c := range changes {
		d, err := Read(dir, base, c)
		if err != nil {
			t.Errorf("Read(%s): %v", c.Path, err)
			continue
		}
		want := "--- a/" + c.Path + "\n+++ b/" + c.Path + "\n@@ -1 +1 @@\n-first in " + c.Path + "\n+second in " + c.Path + "\n"
		if d.Text != want {
			t.Errorf("%s reads:\n%s\nwant its own diff and nobody else's:\n%s", c.Path, d.Text, want)
		}
		if c.Marked != (c.Path == "lib/a.rb") {
			t.Errorf("%s Marked = %v", c.Path, c.Marked)
		}
	}
}

// Contract: diff/D5
func TestNarrowingToARenamedFileStillSeesItAsARename(t *testing.T) {
	dir, base := branchRepo(t)
	if err := os.MkdirAll(filepath.Join(dir, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	git(t, dir, "mv", "c.go", "sub/moved.go")
	write(t, dir, "sub/moved.go", strings.Replace(lines(30), "line", "LINE", 3))
	want := readAll(t, dir, base)["sub/moved.go"]
	if !strings.HasPrefix(want.Text, "--- a/c.go\n+++ b/sub/moved.go\n") {
		t.Fatalf("the full listing reads no rename: %q", want.Text)
	}

	for _, narrow := range []string{"sub/moved.go", "sub"} {
		changes, err := Changes(dir, base, []string{narrow})
		if err != nil || !reflect.DeepEqual(paths(changes), []string{"sub/moved.go"}) {
			t.Fatalf("Changes narrowed to %s = %v, %v", narrow, paths(changes), err)
		}
		got, err := Read(dir, base, changes[0])
		if err != nil || got != want {
			t.Errorf("narrowed to %s the diff is:\n%s\nwant the same as the full listing's:\n%s", narrow, got.Text, want.Text)
		}
	}
}

// Contract: diff/D16
func TestAPathThatIsNeitherInTheWorkingTreeNorInTheBaseIsAnError(t *testing.T) {
	dir, base := branchRepo(t)
	write(t, dir, "a.go", strings.Replace(lines(30), "line", "LINE", 1))
	write(t, dir, "lib/new.go", "package lib\n")
	git(t, dir, "rm", "-q", "b.go")
	write(t, dir, ".gitignore", "ignored.go\n")
	write(t, dir, "ignored.go", "package ignored\n")

	for _, narrow := range [][]string{{"nosuch.go"}, {"lib/nosuch.go"}, {"a.go", "lib/nosuch.go"}, {"lib/new.go/deeper"}} {
		changes, err := Changes(dir, base, narrow)
		missing := narrow[len(narrow)-1]
		if err == nil || err.Error() != "no such path: "+missing || changes != nil {
			t.Errorf("Changes narrowed to %v = %v, %v: want no changes and an error naming %s", narrow, paths(changes), err, missing)
		}
	}

	// A file that was deleted is in the base, and an ignored one is in the working tree.
	for _, narrow := range []string{"b.go", "ignored.go", "c.go", "lib"} {
		if _, err := Changes(dir, base, []string{narrow}); err != nil {
			t.Errorf("Changes narrowed to %s: %v, want no error for a path that exists", narrow, err)
		}
	}
}

// Contract: diff/D6
func TestAFileThatBecomesASymlinkKeepsNoGitHeaderLine(t *testing.T) {
	dir, base := branchRepo(t)
	if err := os.Remove(filepath.Join(dir, "a.go")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("b.go", filepath.Join(dir, "a.go")); err != nil {
		t.Skipf("no symlinks here: %v", err)
	}

	got := readAll(t, dir, base)["a.go"].Text

	// Git prints a change of type as the old file's removal and then the link's arrival.
	removed := "--- a/a.go\n+++ /dev/null\n@@ -1,30 +0,0 @@\n-" + strings.ReplaceAll(strings.TrimSuffix(lines(30), "\n"), "\n", "\n-") + "\n"
	added := "--- /dev/null\n+++ b/a.go\n@@ -0,0 +1 @@\n+b.go\n\\ No newline at end of file\n"
	if got != removed+added {
		t.Errorf("the diff is:\n%s\nwant:\n%s", got, removed+added)
	}
}

// Contract: diff/D6
func TestNormaliseDropsGitsHeaderLinesWhereverTheyAre(t *testing.T) {
	hunk := "@@ -1 +1 @@\n-a\n+b\n index stays, being a line of the file\n"
	raw := "diff --git a/x b/x\nold mode 100644\nnew mode 100755\nindex 1..2\n--- a/x\n+++ b/x\n" + hunk
	want := "--- a/x\n+++ b/x\n" + hunk
	for _, header := range []string{
		"diff --git a/y b/y", "index 3..4 100644", "old mode 100644", "new mode 100755", "new file mode 120000",
		"deleted file mode 100644", "similarity index 90%", "dissimilarity index 60%", "rename from x", "rename to y",
		"copy from x", "copy to y",
	} {
		raw += header + "\n"
	}
	raw += "--- a/y\n+++ b/y\n" + hunk
	want += "--- a/y\n+++ b/y\n" + hunk
	if got := normalise(raw); got != want {
		t.Errorf("normalise = %q, want %q", got, want)
	}
}

// Contract: diff/D3
// Contract: diff/D15
func TestAnUntrackedDirectoryThatGitListsByNameIsNoChange(t *testing.T) {
	dir, base := branchRepo(t)
	// Git lists a repository nested in this one, and a link to a directory, as it lists a file,
	// and has no diff to print for either.
	write(t, dir, "nested/inner.go", "package inner\n")
	git(t, filepath.Join(dir, "nested"), "init", "-q")
	write(t, dir, "pkg/x.go", "package pkg\n")
	if err := os.Symlink("pkg", filepath.Join(dir, "linked")); err != nil {
		t.Skipf("no symlinks here: %v", err)
	}
	if err := os.Symlink("a.go", filepath.Join(dir, "alias.go")); err != nil {
		t.Fatal(err)
	}

	got := readAll(t, dir, base)

	if len(got) != 2 || got["pkg/x.go"].Text == "" || !strings.HasSuffix(got["alias.go"].Text, "\n+a.go\n\\ No newline at end of file\n") {
		t.Errorf("read %+v, want pkg/x.go and the link to a file, which reads as the name it points at", got)
	}
}

// Contract: diff/D15
func TestReadRecognisesAReasonThatStartsTheOutput(t *testing.T) {
	fakeGit(t, ``, ``, `Binary files a/x and b/x differ\n`)
	d, err := Read(t.TempDir(), "base", Change{Path: "x"})
	if err != nil || !d.Binary || d.Text != "" {
		t.Errorf("Read = %+v, %v", d, err)
	}
}
