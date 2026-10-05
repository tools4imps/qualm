# Report

The report is read by a coding agent as often as by a person. It has to say what failed, where, and what to do about it, in words the agent can act on.

## Obligations

- **R1** A passing run prints one line with the number of files judged: `qualm: 7 changed files, no qualms.` One file reads `1 changed file`.
- **R2** With nothing to judge it prints `qualm: nothing to judge.`
- **R3** A failing run names each failing file with its gate value, the direction when it isn't `same`, and each diagnosis at 0.5 or above with its line range, strongest first.
- **R4** A failing run ends with what to do: the text of each diagnosis question that fired, without the guard sentence, and the `qualm keep` command for the failing paths.
- **R5** Kept files are listed with their reasons. Stale keeps are listed with a note that `qualm keep` clears them.
- **R6** The JSON report is one object holding `passed`, `dry_run`, `base`, `files`, `stale_keeps` and `usage`. Each file holds `path`, `status`, and, when present, `reason`, `bytes`, `answers`, `failed` and `where`.
- **R7** A dry run lists each file with its diff size and estimated tokens, and ends with the estimated total cost.
- **R8** In a file's lines a question is named by its id with the underscores as spaces. The list of questions that fired uses the id as written, so it can be found in the config.

```covers
internal/report
```
