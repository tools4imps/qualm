// Package report turns a finished run into the text a person or a coding agent reads, and into JSON.
package report

import (
	"cmp"
	"fmt"
	"io"
	"maps"
	"slices"
	"strconv"
	"strings"

	"github.com/tools4imps/qualm/internal/check"
	"github.com/tools4imps/qualm/internal/jev"
	"github.com/tools4imps/qualm/internal/questions"
)

// Text writes the report a person or a coding agent reads.
func Text(w io.Writer, r check.Result) {
	judged := filter(r.Files, check.StatusJudged)
	var failing []check.File
	for _, f := range judged {
		if len(f.Failed) > 0 {
			failing = append(failing, f)
		}
	}
	fmt.Fprintln(w, headline(r.DryRun, len(judged), len(failing)))
	writeSkipped(w, filter(r.Files, check.StatusSkipped))
	switch {
	case len(judged) == 0:
	case r.DryRun:
		// A dry run sends nothing, so there is no verdict to report and no keeps to show.
		writeDryRun(w, judged)
		return
	case len(failing) > 0:
		writeFail(w, r.Questions, failing)
	}
	writeKeeps(w, r)
	writeWarnings(w, r.Warnings)
}

// headline is the report's first line: what the run found, or that it had nothing to look at.
func headline(dryRun bool, judged, failing int) string {
	switch {
	case judged == 0:
		return "qualm: nothing to judge."
	case dryRun:
		return "qualm: dry run, nothing sent."
	case failing == 0:
		return fmt.Sprintf("qualm: %s, no qualms.", count(judged, "changed file"))
	}
	return fmt.Sprintf("qualm: %d of %s drew a qualm.", failing, count(judged, "changed file"))
}

// writeSkipped counts the skipped files by reason, the reason that skipped most first. Without
// it a rule that skips every file reads the same as a change with nothing in it.
func writeSkipped(w io.Writer, skipped []check.File) {
	if len(skipped) == 0 {
		return
	}
	counts := map[string]int{}
	for _, f := range skipped {
		counts[f.Reason]++
	}
	reasons := slices.SortedFunc(maps.Keys(counts), func(a, b string) int {
		return cmp.Or(cmp.Compare(counts[b], counts[a]), cmp.Compare(a, b))
	})
	for i, why := range reasons {
		reasons[i] = fmt.Sprintf("%d %s", counts[why], shown(why))
	}
	fmt.Fprintf(w, "Skipped %s: %s.\n", count(len(skipped), "file"), strings.Join(reasons, ", "))
}

func writeFail(w io.Writer, qs []questions.Question, failing []check.File) {
	width := 0
	for _, f := range failing {
		width = max(width, len(shown(f.Path)))
	}
	for _, f := range failing {
		fmt.Fprintln(w)
		writeBlock(w, qs, f, width)
	}
	writeAdvice(w, qs, failing)
}

// writeBlock prints one failing file: the gate values, what the choices say and the diagnoses.
func writeBlock(w io.Writer, qs []questions.Question, f check.File, width int) {
	var gated []string
	for _, id := range f.Failed {
		gated = append(gated, fmt.Sprintf("%s %.2f", spaced(id), f.Answers[id].Value))
	}
	fmt.Fprintf(w, "%-*s  %s\n", width, shown(f.Path), strings.Join(gated, ", "))
	for _, q := range qs {
		if line := choice(q, f.Answers[q.ID]); line != "" {
			fmt.Fprintln(w, "  "+line)
		}
	}
	writeDiagnoses(w, qs, f)
}

// choice is how the answer to a choice question reads in a file's block, and "" when there is
// nothing to say. A question that describes the change, as the built-in direction does, is known
// by its options and says nothing of a change that is the same. Any other is named, since its
// option alone could mean anything.
func choice(q questions.Question, a jev.Answer) string {
	switch {
	case a.Choice == "": // only the answer to a choice question holds a choice
		return ""
	case q.Role != questions.RoleDescribes:
		return fmt.Sprintf("%s %s %.2f", spaced(q.ID), a.Choice, a.Value)
	case a.Choice == "same":
		return ""
	}
	return fmt.Sprintf("%s %.2f", a.Choice, a.Value)
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
		paths = append(paths, shellWord(f.Path))
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

// shown is a path or a reason as the report prints it. Both come from the repository, where
// anyone can name a file or write a keep. One that holds a line break or any other character a
// terminal doesn't print as itself is quoted with that character escaped, so it can't pass for a
// line of the report.
func shown(s string) string {
	if strings.ContainsFunc(s, func(r rune) bool { return !strconv.IsPrint(r) }) {
		return strconv.Quote(s)
	}
	return s
}

// shellWord is a path as a shell reads it back, for a command the reader is told to run. A path
// of nothing but plain characters stands as it is and any other goes in single quotes, where a
// shell reads nothing but the closing quote. One that shown would escape goes in $'...', the
// quoting that understands those escapes, to stay on one line.
func shellWord(path string) string {
	plain := func(r rune) bool {
		return 'a' <= r && r <= 'z' || 'A' <= r && r <= 'Z' || '0' <= r && r <= '9' || strings.ContainsRune("_./-", r)
	}
	switch quoted := shown(path); {
	case quoted != path:
		return "$'" + strings.ReplaceAll(quoted[1:len(quoted)-1], "'", `\'`) + "'"
	case strings.ContainsFunc(path, func(r rune) bool { return !plain(r) }):
		return "'" + strings.ReplaceAll(path, "'", `'\''`) + "'"
	}
	return path
}

func spaced(id string) string { return strings.ReplaceAll(id, "_", " ") }

// count is a number of things with the noun in the singular or the plural.
func count(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	return fmt.Sprintf("%d %ss", n, noun)
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
	if kept := filter(r.Files, check.StatusKept); len(kept) > 0 {
		fmt.Fprintln(w, "\nKept")
		for _, f := range kept {
			fmt.Fprintf(w, "  %s: %s\n", shown(f.Path), shown(f.Reason))
		}
	}
	if len(r.StaleKeeps) > 0 {
		fmt.Fprintln(w, "\nStale keeps (qualm keep clears them)")
		for _, k := range r.StaleKeeps {
			fmt.Fprintf(w, "  %s: %s\n", shown(k.Path), shown(k.Reason))
		}
	}
}

// writeWarnings ends the report with what went wrong without changing the verdict.
func writeWarnings(w io.Writer, warnings []string) {
	if len(warnings) == 0 {
		return
	}
	fmt.Fprintln(w, "\nWarnings")
	for _, warning := range warnings {
		// A warning can quote a path, such as the cache directory's.
		fmt.Fprintf(w, "  %s\n", shown(warning))
	}
}

// questionTokens is what the questions add to each file's request, by estimate.
const questionTokens = 1000

// writeDryRun lists what a real run would send, with an estimate of its size and cost.
func writeDryRun(w io.Writer, judged []check.File) {
	pathWidth, byteWidth := 0, 0
	for _, f := range judged {
		pathWidth = max(pathWidth, len(shown(f.Path)))
		byteWidth = max(byteWidth, len(strconv.Itoa(f.Bytes)))
	}
	fmt.Fprintln(w)
	total := 0
	for _, f := range judged {
		tokens := jev.TokensIn(f.Bytes) + questionTokens
		total += tokens
		fmt.Fprintf(w, "  %-*s   %*d bytes   about %d tokens\n", pathWidth, shown(f.Path), byteWidth, f.Bytes, tokens)
	}
	fmt.Fprintf(w, "\n%s, about %d tokens, about $%.4f.\n", count(len(judged), "file"), total, jev.CostOf(total))
}
