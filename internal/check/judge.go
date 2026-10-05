package check

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"slices"

	"github.com/tools4imps/qualm/internal/config"
	"github.com/tools4imps/qualm/internal/gitdiff"
	"github.com/tools4imps/qualm/internal/jev"
	"github.com/tools4imps/qualm/internal/questions"
)

// FormatNote tells Jev how to read the change it is sent.
const FormatNote = "A unified diff of one file. Lines starting with - are the earlier version, lines starting with + are the later version."

// pieceLimit is the most bytes of diff sent in one request.
const pieceLimit = 60000

// defaultBudget is what a run may spend, in dollars, when no budget is given.
const defaultBudget = 1

// defaultJobs is how many requests are in flight at once when the options don't say.
const defaultJobs = 8

// closeMargin is how near its threshold a gating value has to be to count as a close call.
const closeMargin = 0.05

// slack keeps floating point error from deciding whether a value sits on the margin: 0.65 - 0.6
// comes out a hair over 0.05.
const slack = 1e-9

// askedWhenClose is how many times a close call is asked in all. It is odd so there is a median.
const askedWhenClose = 3

// languages names the language of a file by its extension. A file whose extension isn't here is
// sent with no language rather than a guess.
var languages = map[string]string{
	".rb": "Ruby", ".erb": "ERB", ".go": "Go",
	".js": "JavaScript", ".mjs": "JavaScript", ".cjs": "JavaScript", ".jsx": "JavaScript",
	".ts": "TypeScript", ".tsx": "TypeScript", ".py": "Python", ".rs": "Rust", ".java": "Java",
	".kt": "Kotlin", ".swift": "Swift", ".cs": "C#", ".php": "PHP", ".ex": "Elixir", ".exs": "Elixir",
	".c": "C", ".h": "C", ".cc": "C++", ".cpp": "C++", ".hpp": "C++", ".sh": "shell", ".sql": "SQL",
	".scala": "Scala",
}

// judge asks Jev about one run's files. Workers share it, and nothing in it changes after
// newJudge except the budget, which locks itself.
type judge struct {
	client *jev.Client
	cache  jev.Cache // with no directory when the cache is off: it then never hits and stores nothing
	budget *jev.Budget
	model  string
	qs     []questions.Question
	jobs   int
}

// newJudge gathers what asking needs from the options and the config.
func newJudge(o Options, cfg config.Config, qs []questions.Question) (*judge, error) {
	if o.Client == nil {
		return nil, errors.New("no client to reach Jev with")
	}
	j := &judge{
		client: o.Client,
		budget: jev.NewBudget(cmp.Or(o.Budget, defaultBudget)),
		model:  cmp.Or(cfg.Model, config.DefaultModel),
		qs:     qs,
		jobs:   o.Jobs,
	}
	if j.jobs < 1 {
		j.jobs = defaultJobs
	}
	if !o.NoCache {
		dir, err := cacheDir(o.CacheDir)
		if err != nil {
			return nil, err
		}
		j.cache = jev.Cache{Dir: dir}
	}
	return j, nil
}

// cacheDir is where settled answers are stored: dir when one is given, and otherwise qualm's own
// folder in the user cache directory.
func cacheDir(dir string) (string, error) {
	if dir != "" {
		return dir, nil
	}
	user, err := os.UserCacheDir()
	if err != nil {
		return "", fmt.Errorf("no cache directory: %w; pass --cache or --no-cache", err)
	}
	return filepath.Join(user, "qualm"), nil
}

// request builds what is sent for a change, or for a part of one, to a file at path.
func (j *judge) request(path, change string, qs []questions.Question) jev.Request {
	state := map[string]any{"format": FormatNote, "change": change}
	if language, ok := languages[filepath.Ext(path)]; ok {
		state["language"] = language
	}
	wire := make(map[string]any, len(qs))
	for _, q := range qs {
		wire[q.ID] = q.Wire()
	}
	return jev.Request{Model: j.model, State: state, Questions: wire}
}

// file judges one changed file: it asks every question about the change, piece by piece when the
// change is large, and notes the gates the answers failed.
func (j *judge) file(ctx context.Context, f *File, c gitdiff.Change) error {
	var replies []jev.Reply
	for _, piece := range gitdiff.Split(c.Diff, pieceLimit) {
		reply, err := j.ask(ctx, j.request(c.Path, piece, j.qs), j.qs)
		if err != nil {
			return err
		}
		replies = append(replies, reply)
	}
	f.Answers = combine(j.qs, replies)
	f.Failed = failed(j.qs, f.Answers)
	return nil
}

// ask answers a request from the cache when it can, and from Jev when it can't.
func (j *judge) ask(ctx context.Context, req jev.Request, asked []questions.Question) (jev.Reply, error) {
	key := jev.Key(req)
	if reply, ok := j.cache.Get(key); ok {
		return reply, nil
	}
	reply, err := j.settled(ctx, req, asked)
	if err != nil {
		return jev.Reply{}, err
	}
	// A failed write costs a request next time and changes no answer, so the run carries on. With
	// the cache off every write fails, which is how nothing gets stored.
	_ = j.cache.Put(key, reply)
	return reply, nil
}

// settled asks Jev and steadies a close call. Jev's answers move a little between identical
// requests, so a gate near its threshold could pass on one machine and fail on another. Asking
// twice more and taking the median of the three makes that unlikely. The extra requests skip the
// cache, which holds only settled answers. Answers that weren't close stay as first given.
func (j *judge) settled(ctx context.Context, req jev.Request, asked []questions.Question) (jev.Reply, error) {
	first, err := j.fresh(ctx, req, asked)
	if err != nil {
		return jev.Reply{}, err
	}
	unsure := closeCalls(asked, first)
	if len(unsure) == 0 {
		return first, nil
	}
	replies := []jev.Reply{first}
	for len(replies) < askedWhenClose {
		again, err := j.fresh(ctx, req, asked)
		if err != nil {
			return jev.Reply{}, err
		}
		replies = append(replies, again)
	}
	for _, id := range unsure {
		answer := first.Answers[id]
		answer.Value = median(replies, id)
		first.Answers[id] = answer
	}
	return first, nil
}

// closeCalls lists the gating questions among those asked whose value is within the margin of
// their threshold.
func closeCalls(asked []questions.Question, reply jev.Reply) []string {
	var ids []string
	for _, q := range asked {
		if q.Gates && math.Abs(reply.Answers[q.ID].Value-q.Threshold) <= closeMargin+slack {
			ids = append(ids, q.ID)
		}
	}
	return ids
}

// median is the middle of one question's values across an odd number of replies.
func median(replies []jev.Reply, id string) float64 {
	values := make([]float64, len(replies))
	for i, r := range replies {
		values[i] = r.Answers[id].Value
	}
	slices.Sort(values)
	return values[len(values)/2]
}

// fresh asks Jev, within the budget, and counts what the reply cost.
func (j *judge) fresh(ctx context.Context, req jev.Request, asked []questions.Question) (jev.Reply, error) {
	if err := j.budget.Allow(); err != nil {
		return jev.Reply{}, err
	}
	reply, err := j.client.Ask(ctx, req, asked)
	if err != nil {
		return jev.Reply{}, err
	}
	j.budget.Spend(reply)
	return reply, nil
}

// usage is what the run has spent so far.
func (j *judge) usage() Usage {
	requests, tokens, cost := j.budget.Spent()
	return Usage{Requests: requests, InputTokens: tokens, Cost: cost}
}

// combine folds the replies to a change's pieces into one answer per question. A problem shows in
// the piece that holds it, so the strongest piece speaks for the file. A tie keeps the earlier piece.
func combine(qs []questions.Question, replies []jev.Reply) map[string]jev.Answer {
	out := make(map[string]jev.Answer, len(qs))
	for _, q := range qs {
		best := replies[0].Answers[q.ID]
		for _, r := range replies[1:] {
			if a := r.Answers[q.ID]; strength(q, a) > strength(q, best) {
				best = a
			}
		}
		out[q.ID] = best
	}
	return out
}

// strength is what pieces are compared by: the value, or for a choice the probability of its
// first-listed option, since a choice's own value belongs to whichever option won.
func strength(q questions.Question, a jev.Answer) float64 {
	if q.Type == "choice" {
		return a.Probabilities[q.Options()[0]]
	}
	return a.Value
}
