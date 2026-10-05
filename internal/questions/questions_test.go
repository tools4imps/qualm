package questions

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func ids(qs []Question) []string {
	out := make([]string, len(qs))
	for i, q := range qs {
		out[i] = q.ID
	}
	return out
}

func find(t *testing.T, qs []Question, id string) Question {
	t.Helper()
	for _, q := range qs {
		if q.ID == id {
			return q
		}
	}
	t.Fatalf("no question %q in %v", id, ids(qs))
	return Question{}
}

func gates(qs []Question) []string {
	var out []string
	for _, q := range qs {
		if q.Gates {
			out = append(out, q.ID)
		}
	}
	return out
}

func mustResolve(t *testing.T, gateID string, th float64, drop []string, extra []Question) []Question {
	t.Helper()
	qs, err := Resolve(gateID, th, drop, extra)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	return qs
}

func mustFail(t *testing.T, want string, gateID string, th float64, drop []string, extra []Question) {
	t.Helper()
	_, err := Resolve(gateID, th, drop, extra)
	if err == nil {
		t.Fatalf("Resolve succeeded, want an error containing %q", want)
	}
	if !strings.Contains(err.Error(), want) {
		t.Fatalf("error %q does not contain %q", err, want)
	}
}

// Contract: questions/Q1
func TestTenBuiltInQuestionsAndOnlyPushBackGates(t *testing.T) {
	want := []string{"push_back", "direction", "simplified", "comments_only", "new_behaviour",
		"grew_a_big_unit", "added_copies", "added_impossible_guards", "added_placeholders", "added_unused_flexibility"}
	qs := Builtin()
	if !reflect.DeepEqual(ids(qs), want) {
		t.Fatalf("ids = %v, want %v", ids(qs), want)
	}
	if g := gates(qs); !reflect.DeepEqual(g, []string{"push_back"}) {
		t.Fatalf("gating = %v", g)
	}
	if pb := find(t, qs, "push_back"); pb.Threshold != 0.6 || pb.Role != "gate" {
		t.Fatalf("push_back = %+v", pb)
	}
	qs[0].Instructions = "changed"
	qs[1].Criteria = nil
	again := Builtin()
	if again[0].Instructions == "changed" || again[1].Criteria == nil {
		t.Fatal("Builtin shares state between calls")
	}
}

// Contract: questions/Q2
func TestWireCarriesGuardAfterInstructions(t *testing.T) {
	q := Question{ID: "x", Type: "noul", Role: "diagnosis", Instructions: "Did it?", Gates: true, Threshold: 0.7}
	w := q.Wire()
	if w["type"] != "noul" || w["instructions"] != "Did it? "+Guard {
		t.Fatalf("wire = %v", w)
	}
	if _, ok := w["criteria"]; ok {
		t.Fatal("criteria sent when there are none")
	}
	if len(w) != 2 {
		t.Fatalf("wire has extra keys: %v", w)
	}
	c := Question{ID: "c", Type: "choice", Instructions: "Which?", Criteria: json.RawMessage(`{"a":"A","b":"B"}`)}
	cw := c.Wire()
	b, err := json.Marshal(cw["criteria"])
	if err != nil || string(b) != `{"a":"A","b":"B"}` {
		t.Fatalf("criteria = %s, %v", b, err)
	}
	for _, q := range Builtin() {
		if !strings.HasSuffix(q.Wire()["instructions"].(string), " "+Guard) {
			t.Fatalf("%s lacks the guard", q.ID)
		}
	}
}

// Contract: questions/Q3
func TestExtraQuestionReplacesInPlaceOrIsAppended(t *testing.T) {
	extra := []Question{
		{ID: "zeta", Type: "noul", Instructions: "Z?"},
		{ID: "simplified", Type: "noul", Instructions: "Mine?"},
		{ID: "alpha", Type: "noul", Instructions: "A?", Gates: true, Threshold: 0.8},
	}
	qs := mustResolve(t, "", 0, nil, extra)
	got := ids(qs)
	if len(got) != 12 || got[2] != "simplified" || got[10] != "zeta" || got[11] != "alpha" {
		t.Fatalf("ids = %v", got)
	}
	if s := find(t, qs, "simplified"); s.Instructions != "Mine?" || s.Role != "diagnosis" {
		t.Fatalf("simplified = %+v", s)
	}
	if a := find(t, qs, "alpha"); a.Role != "gate" {
		t.Fatalf("alpha role = %q", a.Role)
	}
	if z := find(t, qs, "zeta"); z.Role != "diagnosis" {
		t.Fatalf("zeta role = %q", z.Role)
	}

	// The first built-in can be replaced too.
	qs = mustResolve(t, "", 0, nil, []Question{{ID: "push_back", Type: "noul", Instructions: "Mine?", Gates: true, Threshold: 0.5}})
	if len(qs) != 10 || qs[0].ID != "push_back" || qs[0].Instructions != "Mine?" || qs[0].Threshold != 0.5 {
		t.Fatalf("first = %+v of %d", qs[0], len(qs))
	}
}

// Contract: questions/Q4
func TestDropRemovesByIDAndRejectsUnknown(t *testing.T) {
	qs := mustResolve(t, "", 0, []string{"added_copies", "direction"}, nil)
	if len(qs) != 8 {
		t.Fatalf("got %v", ids(qs))
	}
	for _, q := range qs {
		if q.ID == "added_copies" || q.ID == "direction" {
			t.Fatalf("%s survived", q.ID)
		}
	}
	mustFail(t, "nonesuch", "", 0, []string{"nonesuch"}, nil)
}

// Contract: questions/Q5
func TestGateQuestionAndThresholdCanChange(t *testing.T) {
	qs := mustResolve(t, "", 0.9, nil, nil)
	if pb := find(t, qs, "push_back"); pb.Threshold != 0.9 || !pb.Gates {
		t.Fatalf("push_back = %+v", pb)
	}

	qs = mustResolve(t, "added_copies", 0, nil, nil)
	g := find(t, qs, "added_copies")
	if !g.Gates || g.Role != "gate" || g.Threshold != defaultThreshold {
		t.Fatalf("added_copies = %+v", g)
	}
	pb := find(t, qs, "push_back")
	if pb.Gates || pb.Role != "diagnosis" {
		t.Fatalf("push_back = %+v", pb)
	}
	if got := gates(qs); !reflect.DeepEqual(got, []string{"added_copies"}) {
		t.Fatalf("gating = %v", got)
	}

	qs = mustResolve(t, "added_copies", 0.75, nil, nil)
	if g := find(t, qs, "added_copies"); g.Threshold != 0.75 {
		t.Fatalf("threshold = %v", g.Threshold)
	}

	// A question from the config can be the gate, and it can be the choice of a built-in too.
	qs = mustResolve(t, "mine", 0, nil, []Question{{ID: "mine", Type: "score", Instructions: "?", Criteria: json.RawMessage(`["a","b"]`)}})
	if got := gates(qs); !reflect.DeepEqual(got, []string{"mine"}) {
		t.Fatalf("gating = %v", got)
	}

	// The gate may sit first in the list once the questions before it are dropped.
	qs = mustResolve(t, "simplified", 0, []string{"push_back", "direction"}, nil)
	if got := gates(qs); !reflect.DeepEqual(got, []string{"simplified"}) || qs[0].ID != "simplified" {
		t.Fatalf("gating = %v", got)
	}

	mustFail(t, "nonesuch", "nonesuch", 0, nil, nil)
	mustFail(t, "added_copies", "added_copies", 0, []string{"added_copies"}, nil)
	// A choice cannot gate, so naming one is caught by Q6's rule.
	mustFail(t, "direction", "direction", 0, nil, nil)
}

// Contract: questions/Q6
func TestEveryQuestionIsValidated(t *testing.T) {
	arr := func(s string) json.RawMessage { return json.RawMessage(s) }
	cases := []struct {
		name string
		q    Question
		want string
	}{
		{"empty id", Question{Type: "noul", Instructions: "?"}, "id"},
		{"empty instructions", Question{ID: "q", Type: "noul"}, "q"},
		{"unknown type", Question{ID: "q", Type: "yesno", Instructions: "?"}, "q"},
		{"empty type", Question{ID: "q", Instructions: "?"}, "q"},
		{"score without criteria", Question{ID: "q", Type: "score", Instructions: "?"}, "q"},
		{"score with one level", Question{ID: "q", Type: "score", Instructions: "?", Criteria: arr(`["a"]`)}, "q"},
		{"score with eleven levels", Question{ID: "q", Type: "score", Instructions: "?", Criteria: arr(`["1","2","3","4","5","6","7","8","9","10","11"]`)}, "q"},
		{"score with an object", Question{ID: "q", Type: "score", Instructions: "?", Criteria: arr(`{"a":"x","b":"y"}`)}, "q"},
		{"score with non-strings", Question{ID: "q", Type: "score", Instructions: "?", Criteria: arr(`["a",2]`)}, "q"},
		{"choice with one option", Question{ID: "q", Type: "choice", Instructions: "?", Criteria: arr(`{"a":"x"}`)}, "q"},
		{"choice with an array", Question{ID: "q", Type: "choice", Instructions: "?", Criteria: arr(`["a","b"]`)}, "q"},
		{"choice without criteria", Question{ID: "q", Type: "choice", Instructions: "?"}, "q"},
		{"gating choice", Question{ID: "q", Type: "choice", Instructions: "?", Criteria: arr(`{"a":"x","b":"y"}`), Gates: true, Threshold: 0.5}, "q"},
		{"gate with no threshold", Question{ID: "q", Type: "noul", Instructions: "?", Gates: true}, "q"},
		{"gate above one", Question{ID: "q", Type: "noul", Instructions: "?", Gates: true, Threshold: 1.1}, "q"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			mustFail(t, c.want, "", 0, nil, []Question{c.q})
		})
	}

	ok := []Question{
		{ID: "s2", Type: "score", Instructions: "?", Criteria: arr(`["a","b"]`)},
		{ID: "s10", Type: "score", Instructions: "?", Criteria: arr(`["1","2","3","4","5","6","7","8","9","10"]`), Gates: true, Threshold: 1},
		{ID: "c2", Type: "choice", Instructions: "?", Criteria: arr(`{"a":"x","b":"y"}`)},
	}
	mustResolve(t, "", 0, nil, ok)

	mustFail(t, "dup", "", 0, nil, []Question{
		{ID: "dup", Type: "noul", Instructions: "1?"},
		{ID: "dup", Type: "noul", Instructions: "2?"},
	})
	// The check covers a replacement of a built-in too.
	mustFail(t, "push_back", "", 0, nil, []Question{{ID: "push_back", Type: "noul", Instructions: "?", Gates: true}})
}

// Contract: questions/Q7
func TestResolveFailsWithNoGate(t *testing.T) {
	mustFail(t, "gate", "", 0, []string{"push_back"}, nil)
	mustResolve(t, "", 0, []string{"push_back"}, []Question{{ID: "mine", Type: "noul", Instructions: "?", Gates: true, Threshold: 0.5}})
}

// Contract: questions/Q8
func TestChoiceOptionsKeepWrittenOrder(t *testing.T) {
	q := Question{ID: "c", Type: "choice", Instructions: "?",
		Criteria: json.RawMessage(`{"zebra":"z","apple":"a","mango":"m","banana":"b"}`)}
	want := []string{"zebra", "apple", "mango", "banana"}
	if got := q.Options(); !reflect.DeepEqual(got, want) {
		t.Fatalf("options = %v", got)
	}
	if got := find(t, Builtin(), "direction").Options(); !reflect.DeepEqual(got, []string{"harder", "same", "easier"}) {
		t.Fatalf("direction options = %v", got)
	}
	if got := (Question{Type: "noul"}).Options(); len(got) != 0 {
		t.Fatalf("noul options = %v", got)
	}
}

func TestLevelsCountsScoreCriteria(t *testing.T) {
	q := Question{Type: "score", Criteria: json.RawMessage(`["a","b","c"]`)}
	if q.Levels() != 3 {
		t.Fatalf("levels = %d", q.Levels())
	}
	if (Question{Type: "noul"}).Levels() != 0 {
		t.Fatal("noul has levels")
	}
}
