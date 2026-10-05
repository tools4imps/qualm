package check

import (
	"context"

	"github.com/tools4imps/qualm/internal/gitdiff"
	"github.com/tools4imps/qualm/internal/jev"
	"github.com/tools4imps/qualm/internal/questions"
)

// search is the hunt through one failing file's hunks for the lines its fired diagnoses point at.
type search struct {
	file    *File
	fired   []questions.Question
	header  string
	hunks   []gitdiff.Hunk
	replies []jev.Reply // one for each hunk
}

// where finds the lines of the new file that each fired diagnosis points at in every failing
// file, so the reader knows which part of a change to rework. Each hunk is asked about on its
// own, and a diagnosis points at the hunk where it is strongest.
func (j *judge) where(ctx context.Context, files []File, changes []gitdiff.Change) error {
	type ask struct {
		*search
		hunk int
	}
	var searches []*search
	var asks []ask
	for i := range files {
		fired := files[i].Fired(j.qs)
		if len(files[i].Failed) == 0 || len(fired) == 0 {
			continue
		}
		header, hunks := gitdiff.Hunks(changes[i].Diff)
		s := &search{&files[i], fired, header, hunks, make([]jev.Reply, len(hunks))}
		searches = append(searches, s)
		// One hunk is the only place to point, so asking about it again would buy nothing.
		if len(hunks) > 1 {
			for h := range hunks {
				asks = append(asks, ask{s, h})
			}
		}
	}
	err := j.each(ctx, len(asks), func(ctx context.Context, i int) (err error) {
		s, h := asks[i].search, asks[i].hunk
		s.replies[h], err = j.ask(ctx, j.request(s.file.Path, s.header+s.hunks[h].Text, s.fired), s.fired)
		return err
	})
	if err != nil {
		return err
	}
	for _, s := range searches {
		s.file.Where = s.strongest()
	}
	return nil
}

// strongest points each fired diagnosis at the hunk that answered it highest.
func (s *search) strongest() map[string][2]int {
	at := make(map[string][2]int, len(s.fired))
	for _, q := range s.fired {
		var top float64
		for h, hunk := range s.hunks {
			// Only a higher value moves the pointer, which leaves a tie with the earlier hunk.
			if v := s.replies[h].Answers[q.ID].Value; h == 0 || v > top {
				top, at[q.ID] = v, [2]int{hunk.Start, hunk.End}
			}
		}
	}
	return at
}
