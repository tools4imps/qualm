// Package gitdiff finds what changed between a base commit and the working tree and turns each
// change into the normalised diff that qualm sends to Jev, caches by and binds keeps to.
package gitdiff

import (
	"bytes"
	"cmp"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"slices"
	"strings"
)

// Change is one changed file between the base and the working tree, as far as the listing knows
// it. Its diff is read apart, by Read, so that a file nobody will judge costs no more than its name.
type Change struct {
	Path      string // the path in the working tree
	Marked    bool   // git attributes mark it linguist-generated or linguist-vendored
	from      string // the path it was renamed from, empty for any other change
	untracked bool
}

// Diff is what Read finds in one change: lines to judge, or one of the two reasons there are none.
type Diff struct {
	Text      string // the normalised diff
	Binary    bool   // git calls the file binary
	NoContent bool   // only the mode or the name changed, or the file is new and empty
}

// defaultBranches is the order in which an empty ref looks for the branch work is measured from.
var defaultBranches = []string{"origin/HEAD", "origin/main", "origin/master", "main", "master"}

// settings go before the subcommand of every call. A path is read as it is written and never as a
// pattern, since a file can be named ":x" or "*.rb". The rest pin what the user's configuration
// could otherwise change and no flag of git diff reaches. Without autoRefreshIndex git lists a
// file that was staged with a change and then put back, and has no diff to print for it.
var settings = []string{
	"--literal-pathspecs",
	"-c", "core.quotepath=off",
	"-c", "diff.suppressBlankEmpty=false",
	"-c", "diff.noprefix=false",
	"-c", "diff.mnemonicPrefix=false",
	"-c", "diff.autoRefreshIndex=true",
}

// pinned opens every diff call. Colour, an external diff tool, a text conversion, another
// algorithm, other prefixes or a submodule printed as a log would each make the same change read
// differently on the next machine, or not read as a diff at all. Read knows a new empty file by
// its whole id, which --full-index prints.
var pinned = []string{
	"diff", "--no-color", "--no-ext-diff", "--no-textconv", "--diff-algorithm=myers",
	"--src-prefix=a/", "--dst-prefix=b/", "--inter-hunk-context=0", "--indent-heuristic",
	"--submodule=short", "--full-index",
}

// diff runs git diff with the pinned options ahead of the given ones.
func diff(dir string, okExit []int, args ...string) (string, error) {
	return run(dir, "", okExit, slices.Concat(pinned, args)...)
}

// run executes git in dir and returns stdout. Every call carries -C and the settings, so the
// result does not depend on the caller's working directory or configuration. okExit lists exit
// codes besides 0 that count as success.
func run(dir string, stdin string, okExit []int, args ...string) (string, error) {
	cmd := exec.Command("git", slices.Concat([]string{"-C", dir}, settings, args)...)
	// git reads more diff options from this variable and they win over the command line, so it
	// is emptied for the child.
	cmd.Env = append(os.Environ(), "GIT_DIFF_OPTS=")
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

// listed holds the letters git gives the kinds of change that are listed: added, modified, renamed
// and changed in type, such as a file that became a symlink. Deleted files are left out.
const listed = "AMTR"

// Changes lists the added, modified, renamed and untracked files between base and the working
// tree of dir, narrowed to paths when any are given, sorted by path. It reads no diff.
func Changes(dir, base string, paths []string) ([]Change, error) {
	changes, err := tracked(dir, base)
	if err != nil {
		return nil, err
	}
	untracked, err := run(dir, "", nil, "ls-files", "--others", "--exclude-standard", "-z")
	if err != nil {
		return nil, err
	}
	for _, p := range strings.Split(untracked, "\x00") {
		// Git lists a repository nested in this one, and a link to a directory, by name as it does
		// a file. Neither has lines of its own, and git has no diff to print for one.
		if p != "" && !isDir(filepath.Join(dir, filepath.FromSlash(p))) {
			changes = append(changes, Change{Path: p, untracked: true})
		}
	}
	if changes, err = narrow(dir, base, changes, paths); err != nil {
		return nil, err
	}
	slices.SortFunc(changes, func(a, b Change) int { return cmp.Compare(a.Path, b.Path) })
	if err := mark(dir, changes); err != nil {
		return nil, err
	}
	return changes, nil
}

// tracked lists the changes git knows of between base and the working tree. It asks about the
// whole tree, because git shown one side of a rename alone would call it a new file.
func tracked(dir, base string) ([]Change, error) {
	// --relative keeps names relative to dir, which is how the path arguments are read too.
	names, err := diff(dir, nil, "--relative", "--name-status", "-M", "-z", base)
	if err != nil {
		return nil, err
	}
	var changes []Change
	toks := strings.Split(names, "\x00")
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
		if strings.IndexByte(listed, code) >= 0 {
			c := Change{Path: toks[i+n]}
			if code == 'R' {
				c.from = toks[i+1]
			}
			changes = append(changes, c)
		}
		i += 1 + n
	}
	return changes, nil
}

// narrow keeps the changes that the path arguments select, and all of them when there are no
// arguments. An argument that selects nothing is a mistake unless it names something that is
// there to be unchanged, so a mistyped path can't pass for a clean run.
func narrow(dir, base string, changes []Change, args []string) ([]Change, error) {
	if len(args) == 0 {
		return changes, nil
	}
	for _, arg := range args {
		if !slices.ContainsFunc(changes, func(c Change) bool { return selects(arg, c.Path) }) && !exists(dir, base, arg) {
			return nil, fmt.Errorf("no such path: %s", arg)
		}
	}
	return slices.DeleteFunc(changes, func(c Change) bool {
		return !slices.ContainsFunc(args, func(arg string) bool { return selects(arg, c.Path) })
	}), nil
}

// selects reports whether a path argument names the file or a directory above it.
func selects(arg, file string) bool {
	arg = path.Clean(arg)
	return arg == "." || arg == file || strings.HasPrefix(file, arg+"/")
}

// exists reports whether a path relative to dir is in the working tree or in the base commit.
func exists(dir, base, p string) bool {
	if _, err := os.Lstat(filepath.Join(dir, filepath.FromSlash(p))); err == nil {
		return true
	}
	// "./" makes git read the path from dir, as it reads every other path here.
	_, err := run(dir, "", nil, "cat-file", "-e", base+":./"+p)
	return err == nil
}

// Read reads the diff of one change that Changes listed for the same dir and base. A diff with no
// hunks is taken for what it says only when git gives the reason. Anything else is a diff that
// could not be read, and it is an error so that it never passes for a change with nothing in it.
func Read(dir, base string, c Change) (Diff, error) {
	raw, err := c.raw(dir, base)
	if err != nil {
		return Diff{}, err
	}
	has := func(prefix string) bool { return lineStart(raw, prefix) >= 0 }
	switch text := normalise(raw); {
	case text != "":
		return Diff{Text: text}, nil
	case has("Binary files "):
		return Diff{Binary: true}, nil
	case has("old mode ") && has("new mode "), // the mode alone changed
		has("similarity index 100%"), // a rename with no edit
		has("new file mode ") && addsEmpty(raw):
		return Diff{NoContent: true}, nil
	}
	return Diff{}, fmt.Errorf("git printed no diff that can be read for %s", c.Path)
}

// isDir reports whether file is a directory, or a link to one.
func isDir(file string) bool {
	info, err := os.Stat(file)
	return err == nil && info.IsDir()
}

// emptyBlobs are the ids git gives a file with nothing in it, under SHA-1 and under SHA-256.
var emptyBlobs = []string{
	"e69de29bb2d1d6434b8b29ae775ad8c2e48c5391",
	"473a0f4c3be8a93681a267e3b1e9a7dcda1185436fe141f7749120a303721813",
}

// addsEmpty reports whether git's index line gives the new side of a diff the id of an empty
// file. A new file's diff has no hunks only then. The id is asked of git and not of the working
// tree, which in a sparse checkout doesn't hold every file the branch added. The index line comes
// after the "diff --git" line, so it is looked for after a line break.
func addsEmpty(raw string) bool {
	_, rest, _ := strings.Cut(raw, "\nindex ")
	line, _, _ := strings.Cut(rest, "\n")
	_, id, _ := strings.Cut(line, "..")
	return slices.Contains(emptyBlobs, id)
}

// raw is the change's diff as git prints it.
func (c Change) raw(dir, base string) (string, error) {
	if c.untracked {
		// --no-index exits 1 when the files differ, which is the case we are asking about.
		return diff(dir, []int{1}, "--no-index", "-U8", "--", "/dev/null", c.Path)
	}
	args := []string{"--relative", "-U8", "-M", base, "--"}
	if c.from != "" {
		// A rename is asked for by both paths, so that git pairs them again.
		args = append(args, c.from)
	}
	return diff(dir, nil, append(args, c.Path)...)
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
