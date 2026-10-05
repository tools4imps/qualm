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
	answers []map[string]jev.Answer // one set for each hunk
}

// where finds the lines of the new file that each fired diagnosis points at in every failing
// file, so the reader knows which part of a change to rework. Each hunk is asked about on its
// own, and a diagnosis points at the hunk where it is strongest. The verdict is in by now, so a
// failure here is a warning and takes nothing from it.
func (j *judge) where(ctx context.Context, files []File, diffs []string) {
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
		header, hunks := gitdiff.Hunks(diffs[i])
		s := &search{&files[i], fired, header, hunks, make([]map[string]jev.Answer, len(hunks))}
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
		s.answers[h], err = j.answers(ctx, s.file.Path, s.header+s.hunks[h].Text, s.fired)
		return err
	})
	if err != nil {
		j.warn("couldn't find where the diagnoses point: %v", err)
	}
	for _, s := range searches {
		// A lone hunk was never asked about, so it stands whatever became of the requests.
		if err == nil || len(s.hunks) == 1 {
			s.file.Where = s.strongest()
		}
	}
}

// strongest points each fired diagnosis at the hunk that answered it highest.
func (s *search) strongest() map[string][2]int {
	at := make(map[string][2]int, len(s.fired))
	for _, q := range s.fired {
		var top float64
		for h, hunk := range s.hunks {
			// Only a higher value moves the pointer, which leaves a tie with the earlier hunk.
			if v := s.answers[h][q.ID].Value; h == 0 || v > top {
				top, at[q.ID] = v, [2]int{hunk.Start, hunk.End}
			}
		}
	}
	return at
}
