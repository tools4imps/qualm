// Package cli is qualm's command line: flags, the two commands and the exit codes.
package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"github.com/tools4imps/qualm/internal/check"
	"github.com/tools4imps/qualm/internal/gitdiff"
	"github.com/tools4imps/qualm/internal/jev"
	"github.com/tools4imps/qualm/internal/report"
)

// Version is the release this source builds.
const Version = "0.1.0"

// Env is the world outside the program, passed in so tests can stand in for it.
type Env struct {
	Dir      string
	Getenv   func(string) string
	Stdout   io.Writer
	Stderr   io.Writer
	Endpoint string // empty for jev.Endpoint
}

const usage = `usage: qualm [flags] [PATH...]
       qualm keep PATH... --reason TEXT

Commands:
  (none)  judge the changed files against the base and gate on Jev's answers
  keep    record that a file's current change is accepted, with a reason

Flags:
  --base REF         compare with the merge base of REF (default: the default branch)
  --threshold N      gate at N, above 0 and at most 1 (default: each question's own)
  --include-tests    judge test files too
  --format text|json report format (default text)
  --cache DIR        keep settled answers in DIR
  --no-cache         neither read nor write the cache
  --budget DOLLARS   stop asking once this much is spent (default 1.00)
  --jobs N           files judged at once (default 8)
  --dry-run          list what would be sent and make no request
  --reason TEXT      why a change is kept (keep only)
  --version, --help  print the version or this text

Exit codes: 0 the gate passed, 1 the gate failed, 2 qualm couldn't run
`

// settings is what the command line asked for.
type settings struct {
	base, format, cache, reason string
	threshold, budget           float64
	jobs                        int
	includeTests, noCache       bool
	dryRun                      bool
	positional                  []string
	given                       map[string]bool
}

// Run runs qualm with the given arguments and returns its exit code.
func Run(args []string, env Env) int {
	if wantsInfo(args, env) {
		return 0
	}
	s, err := parse(args)
	if err == nil {
		err = s.validate()
	}
	if err != nil {
		fmt.Fprintf(env.Stderr, "qualm: %v\n%s", err, usage)
		return 2
	}
	return s.execute(env)
}

// wantsInfo answers --version and --help before anything else on the line can object.
func wantsInfo(args []string, env Env) bool {
	for _, a := range args {
		if a == "--" {
			return false
		}
		switch a {
		case "--version", "-version":
			fmt.Fprintf(env.Stdout, "qualm %s\n", Version)
			return true
		case "--help", "-help", "-h":
			fmt.Fprint(env.Stdout, usage)
			return true
		}
	}
	return false
}

func newFlagSet(s *settings) *flag.FlagSet {
	fs := flag.NewFlagSet("qualm", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.StringVar(&s.base, "base", "", "")
	fs.StringVar(&s.format, "format", "text", "")
	fs.StringVar(&s.cache, "cache", "", "")
	fs.StringVar(&s.reason, "reason", "", "")
	fs.Float64Var(&s.threshold, "threshold", 0, "")
	fs.Float64Var(&s.budget, "budget", 0, "")
	fs.IntVar(&s.jobs, "jobs", 0, "")
	fs.BoolVar(&s.includeTests, "include-tests", false, "")
	fs.BoolVar(&s.noCache, "no-cache", false, "")
	fs.BoolVar(&s.dryRun, "dry-run", false, "")
	return fs
}

// parse reads flags and paths in any order. The flag package stops at the first path, so it is
// called again on whatever follows each one.
func parse(args []string) (*settings, error) {
	s := &settings{given: map[string]bool{}}
	fs := newFlagSet(s)
	for len(args) > 0 {
		if err := fs.Parse(args); err != nil {
			return nil, err
		}
		rest := fs.Args()
		if consumed := len(args) - len(rest); consumed > 0 && args[consumed-1] == "--" {
			s.positional = append(s.positional, rest...)
			break
		}
		if len(rest) == 0 {
			break
		}
		s.positional = append(s.positional, rest[0])
		args = rest[1:]
	}
	fs.Visit(func(f *flag.Flag) { s.given[f.Name] = true })
	return s, nil
}

func (s *settings) isKeep() bool { return len(s.positional) > 0 && s.positional[0] == "keep" }

func (s *settings) validate() error {
	switch {
	case s.format != "text" && s.format != "json":
		return fmt.Errorf("unknown format %q, want text or json", s.format)
	case s.given["threshold"] && (s.threshold <= 0 || s.threshold > 1):
		return fmt.Errorf("--threshold must be above 0 and at most 1")
	case s.given["jobs"] && s.jobs < 1:
		return fmt.Errorf("--jobs must be at least 1")
	case s.given["budget"] && s.budget <= 0:
		return fmt.Errorf("--budget must be above 0")
	case s.given["reason"] && !s.isKeep():
		return fmt.Errorf("--reason belongs to the keep command")
	}
	if s.isKeep() {
		if len(s.positional) < 2 {
			return errors.New("keep needs at least one PATH")
		}
		if strings.TrimSpace(s.reason) == "" {
			return errors.New("keep needs --reason TEXT")
		}
	}
	return nil
}

func (s *settings) execute(env Env) int {
	opts, err := s.options(env)
	if err != nil {
		return fail(env, err)
	}
	if s.isKeep() {
		return s.keep(env, opts)
	}
	return s.check(env, opts)
}

func (s *settings) check(env Env, opts check.Options) int {
	res, err := check.Run(context.Background(), opts)
	if err != nil {
		return fail(env, err)
	}
	if s.format == "json" {
		if err := report.JSON(env.Stdout, res); err != nil {
			return fail(env, err)
		}
	} else {
		report.Text(env.Stdout, res)
	}
	if res.DryRun || res.Passed() {
		return 0
	}
	return 1
}

func (s *settings) keep(env Env, opts check.Options) int {
	paths := opts.Paths
	if _, err := check.Keep(opts, paths, s.reason); err != nil {
		return fail(env, err)
	}
	for _, p := range paths {
		fmt.Fprintf(env.Stdout, "qualm: kept %s\n", p)
	}
	return 0
}

func fail(env Env, err error) int {
	fmt.Fprintf(env.Stderr, "qualm: %v\n", err)
	return 2
}

// options turns the settings into what check wants, resolving paths against the repository root.
func (s *settings) options(env Env) (check.Options, error) {
	root, err := gitdiff.Root(env.Dir)
	if err != nil {
		return check.Options{}, err
	}
	args := s.positional
	if s.isKeep() {
		args = args[1:]
	}
	paths, err := relativePaths(env.Dir, root, args)
	if err != nil {
		return check.Options{}, err
	}
	return check.Options{
		Dir:          root,
		Base:         s.base,
		Paths:        paths,
		IncludeTests: s.includeTests,
		Threshold:    s.threshold,
		Jobs:         s.jobs,
		DryRun:       s.dryRun,
		NoCache:      s.noCache,
		CacheDir:     s.cache,
		Budget:       s.budget,
		Client:       &jev.Client{Endpoint: env.Endpoint, Key: env.Getenv("OPENROUTER_API_KEY")},
	}, nil
}

// relativePaths makes each argument a slash-separated path under root. Both ends are resolved
// through symlinks because git reports real paths and a macOS temp directory is a symlink.
func relativePaths(dir, root string, args []string) ([]string, error) {
	realDir, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return nil, err
	}
	realRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, a := range args {
		abs := a
		if !filepath.IsAbs(a) {
			abs = filepath.Join(realDir, a)
		}
		rel, err := filepath.Rel(realRoot, filepath.Clean(abs))
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return nil, fmt.Errorf("%s is outside the repository", a)
		}
		out = append(out, filepath.ToSlash(rel))
	}
	return out, nil
}
