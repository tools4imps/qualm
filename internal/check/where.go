package check

import (
	"context"

	"github.com/tools4imps/qualm/internal/gitdiff"
	"github.com/tools4imps/qualm/internal/jev"
	"github.com/tools4imps/qualm/internal/questions"
)

// firedAt is the value from which a diagnosis counts as having fired.
const firedAt = 0.5

// where finds the lines of the new file that each fired diagnosis points at, so the reader of a
// failing file knows which part of the change to rework.
func (j *judge) where(ctx context.Context, c gitdiff.Change, answers map[string]jev.Answer) (map[string][2]int, error) {
	fired := firedDiagnoses(j.qs, answers)
	if len(fired) == 0 {
		return nil, nil
	}
	header, hunks := gitdiff.Hunks(c.Diff)
	at := make(map[string][2]int, len(fired))
	if len(hunks) == 1 {
		// One hunk is the only place to point, so asking again would buy nothing.
		for _, q := range fired {
			at[q.ID] = [2]int{hunks[0].Start, hunks[0].End}
		}
		return at, nil
	}
	strongest := map[string]float64{}
	for _, h := range hunks {
		reply, err := j.ask(ctx, j.request(c.Path, header+h.Text, fired), fired)
		if err != nil {
			return nil, err
		}
		for _, q := range fired {
			v := reply.Answers[q.ID].Value
			// Only a higher value moves the pointer, which leaves a tie with the earlier hunk.
			if _, seen := at[q.ID]; !seen || v > strongest[q.ID] {
				strongest[q.ID], at[q.ID] = v, [2]int{h.Start, h.End}
			}
		}
	}
	return at, nil
}

// firedDiagnoses lists the diagnoses whose answer reached firedAt, in question order.
func firedDiagnoses(qs []questions.Question, answers map[string]jev.Answer) []questions.Question {
	var fired []questions.Question
	for _, q := range qs {
		if q.Role == "diagnosis" && answers[q.ID].Value >= firedAt {
			fired = append(fired, q)
		}
	}
	return fired
}
