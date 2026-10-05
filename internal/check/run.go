package check

import (
	"context"
	"fmt"
	"slices"
	"sync"

	"github.com/tools4imps/qualm/internal/config"
	"github.com/tools4imps/qualm/internal/gitdiff"
	"github.com/tools4imps/qualm/internal/jev"
	"github.com/tools4imps/qualm/internal/questions"
	"github.com/tools4imps/qualm/internal/skip"
)

// The statuses a File can have.
const (
	statusJudged  = "judged"
	statusKept    = "kept"
	statusSkipped = "skipped"
)

// Run does the check and returns what happened.
func Run(ctx context.Context, o Options) (Result, error) {
	cfg, err := config.Load(o.Dir)
	if err != nil {
		return Result{}, err
	}
	qs, err := resolve(cfg, o.Threshold)
	if err != nil {
		return Result{}, err
	}
	base, changes, err := changesSince(o.Dir, o.Base, o.Paths)
	if err != nil {
		return Result{}, err
	}
	rules := skip.Rules{Extra: cfg.Skip, IncludeTests: o.IncludeTests}
	res := Result{Base: base, Questions: qs, Files: classify(changes, rules, cfg.Keeps), DryRun: o.DryRun}
	if len(o.Paths) == 0 {
		// A run narrowed to some paths can't see every change, so it can't call a keep stale.
		res.StaleKeeps = stale(cfg.Keeps, changes)
	}
	if o.DryRun || !anyJudged(res.Files) {
		return res, nil
	}
	j, err := newJudge(o, cfg, qs)
	if err != nil {
		return Result{}, err
	}
	if err := j.all(ctx, res.Files, changes); err != nil {
		return Result{}, err
	}
	res.Usage = j.usage()
	return res, nil
}

// resolve merges the config into the built-in questions. A threshold given for the run wins over
// the config's. A set that doesn't resolve is a mistake in the config, so the error names the file.
func resolve(cfg config.Config, threshold float64) ([]questions.Question, error) {
	var gate config.Gate
	if cfg.Gate != nil {
		gate = *cfg.Gate
	}
	if threshold > 0 {
		gate.Threshold = threshold
	}
	qs, err := questions.Resolve(gate.Question, gate.Threshold, cfg.Drop, cfg.Questions)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", config.File, err)
	}
	return qs, nil
}

// changesSince finds the base commit for ref and what has changed in dir since, narrowed to paths
// when any are given.
func changesSince(dir, ref string, paths []string) (base string, changes []gitdiff.Change, err error) {
	if base, err = gitdiff.Base(dir, ref); err != nil {
		return "", nil, err
	}
	if changes, err = gitdiff.Changes(dir, base, paths); err != nil {
		return "", nil, err
	}
	return base, changes, nil
}

// classify lists a file for every change, in the same order, with what is to happen to it.
func classify(changes []gitdiff.Change, rules skip.Rules, keeps []config.Keep) []File {
	files := make([]File, len(changes))
	for i, c := range changes {
		status, reason := statusOf(c, rules, keeps)
		files[i] = File{Path: c.Path, Status: status, Reason: reason, Bytes: len(c.Diff)}
	}
	return files
}

// statusOf decides whether a change is skipped, kept or judged, and why when it isn't judged.
func statusOf(c gitdiff.Change, rules skip.Rules, keeps []config.Keep) (status, reason string) {
	if why := skipReason(c, rules); why != "" {
		return statusSkipped, why
	}
	for _, k := range keeps {
		if binds(k, c) {
			return statusKept, k.Reason
		}
	}
	return statusJudged, ""
}

// skipReason says why a change is not worth a request, or returns "" when it is. The path is
// looked at first because it is the reason a person can do something about.
func skipReason(c gitdiff.Change, rules skip.Rules) string {
	switch why := rules.Reason(c.Path); {
	case why != "":
		return why
	case c.Binary:
		return "binary"
	case c.Marked:
		return "marked generated or vendored"
	case c.Diff == "":
		// A change of mode alone has no lines, and so nothing to judge.
		return "no content change"
	}
	return ""
}

// anyJudged reports whether any file is bound for Jev. A run with none needs no client, no cache
// and no key, so a change to prose alone passes anywhere.
func anyJudged(files []File) bool {
	return slices.ContainsFunc(files, func(f File) bool { return f.Status == statusJudged })
}

// all judges the files marked for it, each against the change at the same index, and then finds
// where to look in the ones that failed. The second pass waits for the first because a worker that
// stopped to wait for its file's hunks could leave no worker free to ask about them.
func (j *judge) all(ctx context.Context, files []File, changes []gitdiff.Change) error {
	err := j.each(ctx, len(files), func(ctx context.Context, i int) error {
		if files[i].Status != statusJudged {
			return nil
		}
		return j.file(ctx, &files[i], changes[i])
	})
	if err != nil {
		return err
	}
	return j.where(ctx, files, changes)
}

// each calls do for every index below n, jobs at a time, and returns the first error. That error
// cancels the context, so the calls still waiting send nothing. Everything a run asks of Jev goes
// through here, which is what holds the requests in flight to jobs.
func (j *judge) each(ctx context.Context, n int, do func(ctx context.Context, i int) error) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	var (
		workers sync.WaitGroup
		once    sync.Once
		first   error
	)
	next := make(chan int)
	for range min(j.jobs, n) {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for i := range next {
				if err := do(ctx, i); err != nil {
					once.Do(func() { first = err; cancel() })
				}
			}
		}()
	}
	for i := range n {
		next <- i
	}
	close(next)
	workers.Wait()
	return first
}

// failed lists the gating questions whose answer reached their threshold, in question order.
func failed(qs []questions.Question, answers map[string]jev.Answer) []string {
	var ids []string
	for _, q := range qs {
		if q.Gates && answers[q.ID].Value >= q.Threshold {
			ids = append(ids, q.ID)
		}
	}
	return ids
}
