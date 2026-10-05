# Mutation testing

Gremlins, run package by package with `--timeout-coefficient 15 --workers 4`.

| Package | Killed | Lived | Not covered |
| --- | --- | --- | --- |
| atomicfile | 5 | 1 | 0 |
| skip | 20 | 0 | 0 |
| questions | 45 | 0 | 1 |
| config | 11 | 0 | 0 |
| jev | 47 | 0 | 3 |
| report | 40 | 0 | 9 |
| gitdiff | 89 | 5 | 1 |
| check | 94 | 0 | 0 |
| cli | 30 | 1 | 12 |
| **Total** | **381** | **7** | **26** |

Every survivor in the table is explained below. The "not covered" rows fall into two groups. Two are lines that cannot run in a test. The other 24 are `case` clauses of a `switch`, which Gremlins' coverage map never attributes to a test even though tests run them. For those, each mutation was applied by hand in a scratch copy of the repository and the package's tests failed every time, so they are killed in fact. One of them, the upper bound of `--budget` at `cli.go:154`, is killed by a test added in this pass.

## Mutants no test can kill

### internal/atomicfile/atomicfile.go

- **Line 20, negation on `err == nil` after `tmp.Close()`.** The mutant keeps a write error only when it is nil, which needs `Write` to fail on a temporary file just created, or `Close` to fail on a regular file. Neither happens without a full disk or a broken operating system.

### internal/questions/questions.go

- **Line 54, arithmetic on `"questions: builtin.json: " + err.Error()`.** This is the panic message for an embedded file that fails to decode. The embedded file is checked by the tests, so the line cannot run, and the mutation only changes a string join.

### internal/jev/client.go

- **Line 66, arithmetic on `90 * time.Second`.** The timeout of the shared HTTP client is 90 seconds. Seeing the difference between `*` and `/` would need a test that waits more than a minute for a stalled server.

### internal/cli/cli.go

- **Line 123, boundary on `len(args) > 0`.** With `>=` the loop body also runs for an empty list, and there `Parse` does nothing and `fs.Args()` is empty, so the loop breaks at once with the same settings.

### internal/gitdiff/diff.go

- **Line 62, boundary on `i >= 0` for `strings.Index(rest, "\n@@ ")`.** `rest` always begins with `@@ `, either because `lineStart` cut it there or because the previous hunk ended just before a `\n@@ `, so the index is never 0 and `>=` and `>` agree.
- **Line 78, boundary on `i < 0` for `strings.Index(line, " +")`.** `line` is the first line of a hunk that begins with `@@ `, so the index of ` +` is at least 2 and never 0. Only a hand-built input to the unexported `newRange` that starts with a space and a plus could tell the two apart.
- **Line 94, boundary on `len(diff) <= limit`.** A diff exactly as long as the limit that goes through the splitting code comes out as one piece equal to the diff, which is what the early return gives.
- **Line 111, boundary on `len(header)+len(h.Text) > limit`.** A hunk that fills the limit exactly is either appended whole or handed to `splitHunk`, which finds no line that overflows and returns the same single piece. In both paths an open piece is flushed first.
- **Line 144, boundary on `n > 0` after the loop in `splitHunk`.** `n` is 0 at that point only when the hunk has no lines under its `@@` line, which git never prints. See the note below.

## Mutants Gremlins calls not covered that the tests do kill

Each was applied by hand in a scratch copy, and the package's tests failed.

- **internal/jev/client.go**, lines 104 and 109: negation of `err != nil` and `status == http.StatusOK`.
- **internal/report/text.go**, lines 30, 35, 45, 49, 107, 109, 111 and 197: the `switch` clauses of the report's layout, headline, choice wording and shell quoting, including the boundary and negation at line 35.
- **internal/gitdiff/gitdiff.go**, line 243: negation of `text != ""` in `Read`.
- **internal/cli/cli.go**, lines 148 to 154: the format, threshold, jobs and budget checks in `validate`, with the boundary and negation of each comparison.

## Worth a look

- **`splitHunk` drops a hunk that has nothing under its `@@` line.** If such a hunk is too large to fit with the header, `splitHunk` returns no pieces, so the hunk vanishes from what is sent. Git does not print hunks like that, so this only matters for a hand-built diff. It is also why the mutant on `diff.go` line 144 lives, since asserting the dropping would pin a behaviour that looks accidental.
- **`gitdiff.tracked` accepts a trailing empty path.** The malformed check `i+n >= len(toks)` counts the empty token after the final NUL as a token, so `M\0` (a status with the NUL but no path) passes the check and produces a change with the path `""`. Real git never prints that.
