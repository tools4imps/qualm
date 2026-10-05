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

// The statuses a File can have, as the JSON report spells them.
const (
	StatusJudged  = "judged"
	StatusKept    = "kept"
	StatusSkipped = "skipped"
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
	files, diffs, err := classify(o.Dir, base, changes, rules, cfg.Keeps)
	if err != nil {
		return Result{}, err
	}
	res := Result{Base: base, Questions: qs, Files: files, DryRun: o.DryRun}
	if len(o.Paths) == 0 {
		// A run narrowed to some paths can't see every change, so it can't call a keep stale.
		res.StaleKeeps = stale(cfg.Keeps, files, diffs)
	}
	if o.DryRun || !anyJudged(res.Files) {
		return res, nil
	}
	j, err := newJudge(o, cfg, qs)
	if err != nil {
		return Result{}, err
	}
	if err := j.all(ctx, res.Files, diffs); err != nil {
		return Result{}, err
	}
	res.Usage, res.Warnings = j.usage(), j.warnings
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

// classify lists a file for every change, in the same order, with what is to happen to it, and
// beside it the diff of each change it had to read to decide. A skip rule or a mark settles a file
// from the listing alone, so the diff of such a file is never read.
func classify(dir, base string, changes []gitdiff.Change, rules skip.Rules, keeps []config.Keep) ([]File, []string, error) {
	files := make([]File, len(changes))
	diffs := make([]string, len(changes))
	for i, c := range changes {
		files[i].Path = c.Path
		if why := unreadReason(c, rules); why != "" {
			files[i].Status, files[i].Reason = StatusSkipped, why
			continue
		}
		d, err := gitdiff.Read(dir, base, c)
		if err != nil {
			return nil, nil, err
		}
		diffs[i], files[i].Bytes = d.Text, len(d.Text)
		files[i].Status, files[i].Reason = statusOf(c.Path, d, keeps)
	}
	return files, diffs, nil
}

// unreadReason says why a change is skipped on the strength of the listing alone, or returns ""
// when its diff has to be read. The path is looked at first because it is the reason a person can
// do something about.
func unreadReason(c gitdiff.Change, rules skip.Rules) string {
	if why := rules.Reason(c.Path); why != "" {
		return why
	}
	if c.Marked {
		return "marked generated or vendored"
	}
	return ""
}

// statusOf decides whether a change whose diff has been read is skipped, kept or judged, and why
// when it isn't judged.
func statusOf(path string, d gitdiff.Diff, keeps []config.Keep) (status, reason string) {
	switch {
	case d.Binary:
		return StatusSkipped, "binary"
	case d.NoContent:
		return StatusSkipped, "no content change"
	}
	hash := changeHash(d.Text)
	for _, k := range keeps {
		if k.Path == path && k.Change == hash {
			return StatusKept, k.Reason
		}
	}
	return StatusJudged, ""
}

// anyJudged reports whether any file is bound for Jev. A run with none needs no client, no cache
// and no key, so a change to prose alone passes anywhere.
func anyJudged(files []File) bool {
	return slices.ContainsFunc(files, func(f File) bool { return f.Status == StatusJudged })
}

// all judges the files marked for it, each on the diff at the same index, and then finds where to
// look in the ones that failed. The second pass waits for the first because a worker that stopped
// to wait for its file's hunks could leave no worker free to ask about them.
func (j *judge) all(ctx context.Context, files []File, diffs []string) error {
	err := j.each(ctx, len(files), func(ctx context.Context, i int) error {
		if files[i].Status != StatusJudged {
			return nil
		}
		return j.file(ctx, &files[i], diffs[i])
	})
	if err != nil {
		return err
	}
	j.where(ctx, files, diffs)
	return nil
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
