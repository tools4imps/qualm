// Package gitdiff finds what changed between a base commit and the working tree and turns each
// change into the normalised diff that qualm sends to Jev, caches by and binds keeps to.
package gitdiff

import (
	"bytes"
	"cmp"
	"errors"
	"fmt"
	"os/exec"
	"slices"
	"strings"
)

// Change is one changed file between the base and the working tree.
type Change struct {
	Path   string // the path in the working tree
	Status string // "added", "modified" or "renamed"
	Diff   string // the normalised diff, empty for a binary file
	Binary bool
	Marked bool // git attributes mark it linguist-generated or linguist-vendored
}

// defaultBranches is the order in which an empty ref looks for the branch work is measured from.
var defaultBranches = []string{"origin/HEAD", "origin/main", "origin/master", "main", "master"}

// run executes git in dir and returns stdout. Every call carries -C and quotepath=off so the
// result does not depend on the caller's working directory or configuration. okExit lists exit
// codes besides 0 that count as success.
func run(dir string, stdin string, okExit []int, args ...string) (string, error) {
	full := append([]string{"-C", dir, "-c", "core.quotepath=off"}, args...)
	cmd := exec.Command("git", full...)
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if stdin != "" {
		cmd.Stdin = strings.NewReader(stdin)
	}
	if err := cmd.Run(); err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) && slices.Contains(okExit, ee.ExitCode()) {
			return out.String(), nil
		}
		msg := strings.TrimSpace(errb.String())
		if msg == "" {
			msg = err.Error()
		}
		return "", fmt.Errorf("git %s: %s", args[0], msg)
	}
	return out.String(), nil
}

// resolve turns ref into a commit id, or returns "" when it names no commit. --end-of-options
// keeps a ref that looks like an option from being read as one.
func resolve(dir, ref string) string {
	out, err := run(dir, "", nil, "rev-parse", "--verify", "--quiet", "--end-of-options", ref+"^{commit}")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(out)
}

// Root returns the top of the repository that holds dir. qualm.json lives there, and every path
// qualm reports is relative to it.
func Root(dir string) (string, error) {
	out, err := run(dir, "", nil, "rev-parse", "--show-toplevel")
	if err != nil {
		return "", fmt.Errorf("%s is not a git repository", dir)
	}
	return strings.TrimSpace(out), nil
}

// Base returns the commit to compare the working tree with: the merge base of ref and HEAD.
// An empty ref means the default branch.
func Base(dir, ref string) (string, error) {
	var commit string
	if ref != "" {
		if commit = resolve(dir, ref); commit == "" {
			return "", fmt.Errorf("base %q does not name a commit", ref)
		}
	} else {
		for _, name := range defaultBranches {
			if commit = resolve(dir, name); commit != "" {
				break
			}
		}
		if commit == "" {
			return "", fmt.Errorf("no default branch found, tried %s; pass --base", strings.Join(defaultBranches, ", "))
		}
	}
	out, err := run(dir, "", nil, "merge-base", commit, "HEAD")
	if err != nil {
		return "", fmt.Errorf("no merge base between %s and HEAD", commit)
	}
	return strings.TrimSpace(out), nil
}

// statuses names the kinds of change that are listed, by the letter git gives them. A change of
// type, such as a file that became a symlink, counts as modified.
var statuses = map[byte]string{'A': "added", 'M': "modified", 'T': "modified", 'R': "renamed"}

// Changes lists the added, modified, renamed and untracked files between base and the working
// tree of dir, narrowed to paths when any are given, sorted by path.
func Changes(dir, base string, paths []string) ([]Change, error) {
	spec := append([]string{"--"}, paths...)

	// --relative keeps names relative to dir, which is how the path arguments are read too.
	listed, err := run(dir, "", nil, append([]string{"diff", "--relative", "--name-status", "-M", "-z", base}, spec...)...)
	if err != nil {
		return nil, err
	}
	var changes []Change
	toks := strings.Split(listed, "\x00")
	for i := 0; i < len(toks) && toks[i] != ""; {
		// A rename lists the old path and then the new one. -M finds renames and never copies, so
		// every other entry has one path.
		code, n := toks[i][0], 1
		if code == 'R' {
			n = 2
		}
		if i+n >= len(toks) {
			return nil, errors.New("git diff: malformed name-status output")
		}
		if status, ok := statuses[code]; ok {
			// A rename is asked for by both paths, so that git pairs them again.
			raw, err := run(dir, "", nil, append([]string{"diff", "--relative", "-U8", "-M", base, "--"}, toks[i+1:i+1+n]...)...)
			if err != nil {
				return nil, err
			}
			changes = append(changes, build(toks[i+n], status, raw))
		}
		i += 1 + n
	}

	untracked, err := run(dir, "", nil, append([]string{"ls-files", "--others", "--exclude-standard", "-z"}, spec...)...)
	if err != nil {
		return nil, err
	}

	for _, p := range strings.Split(untracked, "\x00") {
		if p == "" {
			continue
		}
		// --no-index exits 1 when the files differ, which is the case we are asking about.
		raw, err := run(dir, "", []int{1}, "diff", "--no-index", "-U8", "--", "/dev/null", p)
		if err != nil {
			return nil, err
		}
		changes = append(changes, build(p, "added", raw))
	}

	slices.SortFunc(changes, func(a, b Change) int { return cmp.Compare(a.Path, b.Path) })
	if err := mark(dir, changes); err != nil {
		return nil, err
	}
	return changes, nil
}

// build normalises a raw diff and notes whether git called the file binary.
func build(path, status, raw string) Change {
	c := Change{Path: path, Status: status, Diff: Normalise(raw)}
	if c.Diff == "" {
		for _, l := range strings.Split(raw, "\n") {
			if strings.HasPrefix(l, "Binary files ") {
				c.Binary = true
				break
			}
		}
	}
	return c
}

// mark sets Marked on the changes whose attributes call them generated or vendored. The names go
// in on stdin so a long list cannot overrun the command line.
func mark(dir string, changes []Change) error {
	if len(changes) == 0 {
		return nil
	}
	var in strings.Builder
	for _, c := range changes {
		in.WriteString(c.Path)
		in.WriteByte(0)
	}
	out, err := run(dir, in.String(), nil, "check-attr", "--stdin", "-z", "linguist-generated", "linguist-vendored")
	if err != nil {
		return err
	}
	marked := map[string]bool{}
	toks := strings.Split(out, "\x00")
	for i := 0; i+2 < len(toks); i += 3 {
		if v := toks[i+2]; v == "set" || v == "true" {
			marked[toks[i]] = true
		}
	}
	for i := range changes {
		changes[i].Marked = marked[changes[i].Path]
	}
	return nil
}
