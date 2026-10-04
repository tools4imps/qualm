package check

import (
	"context"
	"sync"

	"github.com/tools4imps/qualm/internal/config"
	"github.com/tools4imps/qualm/internal/gitdiff"
	"github.com/tools4imps/qualm/internal/jev"
	"github.com/tools4imps/qualm/internal/questions"
	"github.com/tools4imps/qualm/internal/skip"
)

// defaultJobs is how many files are judged at once when the options don't say.
const defaultJobs = 8

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
	if err := judgeAll(ctx, j, res.Files, changes, o.Jobs); err != nil {
		return Result{}, err
	}
	res.Usage = j.usage()
	return res, nil
}

// resolve merges the config into the built-in questions. A threshold given for the run wins over
// the config's.
func resolve(cfg config.Config, threshold float64) ([]questions.Question, error) {
	var gate config.Gate
	if cfg.Gate != nil {
		gate = *cfg.Gate
	}
	if threshold > 0 {
		gate.Threshold = threshold
	}
	return questions.Resolve(gate.Question, gate.Threshold, cfg.Drop, cfg.Questions)
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
	for _, f := range files {
		if f.Status == statusJudged {
			return true
		}
	}
	return false
}

// judgeAll judges the files marked for it, each against the change at the same index, jobs at a
// time. It returns the first error. That error cancels the context, so the files still waiting
// send nothing.
func judgeAll(ctx context.Context, j *judge, files []File, changes []gitdiff.Change, jobs int) error {
	if jobs < 1 {
		jobs = defaultJobs
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	var (
		workers sync.WaitGroup
		once    sync.Once
		first   error
	)
	next := make(chan int)
	for w := 0; w < jobs; w++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for i := range next {
				if err := j.file(ctx, &files[i], changes[i]); err != nil {
					once.Do(func() { first = err; cancel() })
				}
			}
		}()
	}
	for i := range files {
		if files[i].Status == statusJudged {
			next <- i
		}
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
