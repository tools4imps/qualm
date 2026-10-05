// Package report turns a finished run into the text a person or a coding agent reads, and into JSON.
package report

import (
	"cmp"
	"fmt"
	"io"
	"slices"
	"strconv"
	"strings"

	"github.com/tools4imps/qualm/internal/check"
	"github.com/tools4imps/qualm/internal/jev"
	"github.com/tools4imps/qualm/internal/questions"
)

// Text writes the report a person or a coding agent reads.
func Text(w io.Writer, r check.Result) {
	judged := filter(r.Files, "judged")
	var failing []check.File
	for _, f := range judged {
		if len(f.Failed) > 0 {
			failing = append(failing, f)
		}
	}
	switch {
	case len(judged) == 0:
		fmt.Fprintln(w, "qualm: nothing to judge.")
	case r.DryRun:
		// A dry run sends nothing, so there is no verdict to report and no keeps to show.
		writeDryRun(w, judged)
		return
	case len(failing) == 0:
		fmt.Fprintf(w, "qualm: %s, no qualms.\n", changed(len(judged)))
	default:
		writeFail(w, r, failing, len(judged))
	}
	writeKeeps(w, r)
}

func writeFail(w io.Writer, r check.Result, failing []check.File, judged int) {
	fmt.Fprintf(w, "qualm: %d of %s drew a qualm.\n", len(failing), changed(judged))
	width := 0
	for _, f := range failing {
		width = max(width, len(f.Path))
	}
	for _, f := range failing {
		fmt.Fprintln(w)
		writeBlock(w, r.Questions, f, width)
	}
	writeAdvice(w, r.Questions, failing)
}

// writeBlock prints one failing file: the gate values, the direction and the diagnoses.
func writeBlock(w io.Writer, qs []questions.Question, f check.File, width int) {
	var gated []string
	for _, id := range f.Failed {
		gated = append(gated, fmt.Sprintf("%s %.2f", spaced(id), f.Answers[id].Value))
	}
	fmt.Fprintf(w, "%-*s  %s\n", width, f.Path, strings.Join(gated, ", "))
	for _, q := range qs {
		// Only the answer to a choice question holds a choice.
		if a := f.Answers[q.ID]; a.Choice != "" && a.Choice != "same" {
			fmt.Fprintf(w, "  %s %.2f\n", a.Choice, a.Value)
		}
	}
	writeDiagnoses(w, qs, f)
}

// writeDiagnoses prints the diagnoses that fired on the file, strongest first. The sort is stable
// so ties keep the order the questions were asked in.
func writeDiagnoses(w io.Writer, qs []questions.Question, f check.File) {
	fired := f.Fired(qs)
	slices.SortStableFunc(fired, func(a, b questions.Question) int {
		return cmp.Compare(f.Answers[b.ID].Value, f.Answers[a.ID].Value)
	})
	texts := make([]string, len(fired))
	width := 0
	for i, q := range fired {
		texts[i] = fmt.Sprintf("%s %.2f", spaced(q.ID), f.Answers[q.ID].Value)
		width = max(width, len(texts[i]))
	}
	for i, q := range fired {
		line := "  " + texts[i]
		if span, ok := f.Where[q.ID]; ok {
			line = fmt.Sprintf("%-*s  %s", width+2, line, lines(span))
		}
		fmt.Fprintln(w, line)
	}
}

func lines(span [2]int) string {
	if span[0] == span[1] {
		return fmt.Sprintf("line %d", span[0])
	}
	return fmt.Sprintf("lines %d-%d", span[0], span[1])
}

// writeAdvice tells the reader what to do next, with the questions that fired on any failing file.
func writeAdvice(w io.Writer, qs []questions.Question, failing []check.File) {
	fmt.Fprintln(w, "\nWhat to do")
	if len(failing) == 1 {
		fmt.Fprintln(w, "  A reviewer would likely ask for this change to be simplified before it merges.")
	} else {
		fmt.Fprintln(w, "  A reviewer would likely ask for these changes to be simplified before they merge.")
	}
	var paths []string
	fired := map[string]bool{}
	for _, f := range failing {
		paths = append(paths, f.Path)
		for _, q := range f.Fired(qs) {
			fired[q.ID] = true
		}
	}
	if len(fired) == 0 {
		fmt.Fprintln(w, "  Simplify the change, then run qualm again.")
	} else {
		fmt.Fprintln(w, "  The questions that fired:")
		for _, q := range qs {
			if fired[q.ID] {
				fmt.Fprintf(w, "    %s: %s\n", q.ID, q.Instructions)
			}
		}
		fmt.Fprintln(w, "  Rework the change so they no longer apply, then run qualm again.")
	}
	fmt.Fprintln(w, "  A qualm is an opinion. If the change is right as it stands, a person can keep it:")
	fmt.Fprintf(w, "    qualm keep %s --reason \"...\"\n", strings.Join(paths, " "))
}

func spaced(id string) string { return strings.ReplaceAll(id, "_", " ") }

func changed(n int) string {
	if n == 1 {
		return "1 changed file"
	}
	return fmt.Sprintf("%d changed files", n)
}

func filter(files []check.File, status string) []check.File {
	var out []check.File
	for _, f := range files {
		if f.Status == status {
			out = append(out, f)
		}
	}
	return out
}

// writeKeeps prints the Kept and Stale keeps sections, each only when it has entries.
func writeKeeps(w io.Writer, r check.Result) {
	if kept := filter(r.Files, "kept"); len(kept) > 0 {
		fmt.Fprintln(w, "\nKept")
		for _, f := range kept {
			fmt.Fprintf(w, "  %s: %s\n", f.Path, f.Reason)
		}
	}
	if len(r.StaleKeeps) > 0 {
		fmt.Fprintln(w, "\nStale keeps (qualm keep clears them)")
		for _, k := range r.StaleKeeps {
			fmt.Fprintf(w, "  %s: %s\n", k.Path, k.Reason)
		}
	}
}

// questionTokens is what the questions add to each file's request, by estimate.
const questionTokens = 1000

// writeDryRun lists what a real run would send, with an estimate of its size and cost.
func writeDryRun(w io.Writer, judged []check.File) {
	pathWidth, byteWidth := 0, 0
	for _, f := range judged {
		pathWidth = max(pathWidth, len(f.Path))
		byteWidth = max(byteWidth, len(strconv.Itoa(f.Bytes)))
	}
	fmt.Fprintln(w, "qualm: dry run, nothing sent.")
	fmt.Fprintln(w)
	total := 0
	for _, f := range judged {
		tokens := f.Bytes/3 + questionTokens
		total += tokens
		fmt.Fprintf(w, "  %-*s   %*d bytes   about %d tokens\n", pathWidth, f.Path, byteWidth, f.Bytes, tokens)
	}
	noun := "files"
	if len(judged) == 1 {
		noun = "file"
	}
	cost := float64(total) * jev.PricePerMillion / 1e6
	fmt.Fprintf(w, "\n%d %s, about %d tokens, about $%.4f.\n", len(judged), noun, total, cost)
}
