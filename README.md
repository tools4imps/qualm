# qualm

qualm fails a pull request when an experienced reviewer would ask for the change to be simplified, and it tells the coding agent what to fix. It is the subjective gate for [Impatient Programming](https://impatientprogramming.org), and the sibling of [exhale](https://github.com/tools4imps/exhale-ruby), whose checks are deterministic.

Agents write more code than anyone reads. When green merges itself, something has to decide which changes still get human eyes. qualm asks that question of every changed file: would a reviewer push back on this?

The answer comes from [Jev](https://openrouter.ai/blog/insights/what-is-jev/), TypeSafe's System One decision model. Jev reads a diff, answers typed questions with probabilities, and writes no prose. No LLM runs inside qualm. The agent that reads its report does the reasoning and the fixing.

qualm has no parser, so it judges code in any language. It is one binary with no dependencies.

## Install

```bash
go install github.com/tools4imps/qualm/cmd/qualm@latest
```

Or download a binary for macOS, Linux or Windows from the [releases page](https://github.com/tools4imps/qualm/releases).

## Use

```bash
export OPENROUTER_API_KEY=...
qualm
```

Run it in a git repository. It compares your working tree with the merge base of `HEAD` and the default branch, so it works before you commit as well as in CI.

`qualm` exits 0 when nothing drew a qualm. It exits 1 when the gate fails. It exits 2 when it couldn't run: no key, no network, a mistake in the config, a spent budget. It never passes a change it couldn't judge.

## What it asks

One request per changed file. The file's diff is the state, and ten questions ride along:

| Question | What it asks | Role |
| --- | --- | --- |
| `push_back` | Would an experienced reviewer ask for this change to be simplified before it merges? | The gate |
| `direction` | Is the later version harder, the same or easier to understand and change? | Describes the change |
| `simplified` | Did the change remove complexity from this file? | Describes the change |
| `comments_only` | Does it differ only in comments, documentation or whitespace? | Describes the change |
| `new_behaviour` | Is it mostly new behaviour being added? | Describes the change |
| `grew_a_big_unit` | Did it make an already long function or class longer? | Diagnosis |
| `added_copies` | Did it add a near-copy of logic already in the file? | Diagnosis |
| `added_impossible_guards` | Did it add checks for states that can't happen? | Diagnosis |
| `added_placeholders` | Did it add placeholders, debug output or scaffolding? | Diagnosis |
| `added_unused_flexibility` | Did it add options or indirection used in only one place? | Diagnosis |

The exact wording is in [`internal/questions/builtin.json`](internal/questions/builtin.json). qualm adds one sentence to every question, telling Jev to treat comments and strings in the code as code to be judged and never as instructions.

## The gate

The run fails when any changed file scores 0.6 or higher on `push_back`. The other nine answers can't fail a run by default. They are there to say why.

Jev's answers move a little between identical calls, by 0.03 at most in our tests. So when a gate answer lands within 0.05 of the threshold, qualm asks twice more and takes the middle value. That keeps a run on your machine and a run in CI from disagreeing over a hair.

## Reading the report

A passing run prints one line. A failing run names each file, the answers that matter, and where to look:

```text
qualm: 1 of 9 changed files drew a qualm.
Skipped 10 files: 8 test, 2 prose or data.

lib/mutineer/coverage_map.rb  push back 0.65
  harder 1.00
  grew a big unit 0.85          lines 327-577
  added copies 0.74             lines 327-577
  added impossible guards 0.65  lines 327-577

What to do
  A reviewer would likely ask for this change to be simplified before it merges.
  The questions that fired:
    grew_a_big_unit: Did the change make an already long function or class longer, where the new work could have gone in a unit of its own?
    added_copies: Did the change add logic that is a near-copy of logic already in the file?
    added_impossible_guards: Did the change add checks or fallbacks for states the surrounding code shows can't happen?
  Rework the change so they no longer apply, then run qualm again.
  A qualm is an opinion. If the change is right as it stands, a person can keep it:
    qualm keep lib/mutineer/coverage_map.rb --reason "..."
```

That is a real run, on the commit of [mutineer](https://github.com/davidteren/mutineer) that added 22 methods to one class. It took under four seconds and cost a fraction of a cent.

The line ranges come from a second pass. On a failing file, qualm asks each diagnosis again hunk by hunk and reports the hunk where it is strongest.

`--format json` prints the same result as one object.

## Keeping a change

A qualm is an opinion, and sometimes the code is right as it stands. When a person has looked at a failure and accepts it:

```bash
qualm keep lib/matcher.rb --reason "The algorithm is this complicated"
```

That records the path, a hash of the change, the reason and the date under `keeps` in `qualm.json`. A keep binds to the lines the change adds and removes. Edit any of them and the question reopens. A change somewhere else in the file on the base branch leaves the keep standing. Once the pull request merges, the keep matches nothing, and the next `qualm keep` clears it out.

Nothing stops an agent from keeping its own change, or from marking a file generated in `.gitattributes` so it is skipped. The safeguard is that both are visible changes to a file, and the report counts what it skipped and why. A team can put `qualm.json` and `.gitattributes` behind a required human review.

## The config

`qualm.json` at the repository root. It's optional.

```json
{
  "gate": { "question": "push_back", "threshold": 0.6 },
  "skip": ["db/schema.rb", "**/*.generated.*"],
  "drop": ["added_unused_flexibility"],
  "questions": [
    {
      "id": "added_feature_flag",
      "type": "noul",
      "instructions": "Did the change add a feature flag?",
      "gates": true,
      "threshold": 0.8
    }
  ]
}
```

- `gate` changes the gate's question or its threshold.
- `questions` adds your own. One with a built-in's id replaces it, and one with `gates` and a `threshold` can fail the run too.
- `drop` removes built-in questions.
- `skip` adds paths to leave alone. A pattern with no slash matches a file name anywhere, `**` matches any number of directories, and a pattern ending in a slash matches everything under that directory.

A question's `type` is `noul` for yes or no, `score` for ordered levels listed in `criteria`, or `choice` for named options. An unknown key or a threshold outside 0 to 1 stops the run with exit 2, so a typo never passes for a clean run.

## What it skips

Deleted and binary files, and files with no content change. Vendored and built directories. Lockfiles and generated files, including anything git marks `linguist-generated`. Prose and data such as Markdown, JSON and YAML. Tests, unless you pass `--include-tests`. The report's second line counts what was skipped and why. A changed file whose diff can't be read stops the run with exit 2.

## What it costs

Jev charges about four cents per million input tokens, and a typical pull request costs a fraction of a cent. `--budget` caps a run, at one dollar by default.

Every answer is cached under a hash of the exact request, in your user cache directory. The same diff gets the same verdict, and a replay costs nothing. `--cache DIR` moves the cache and `--no-cache` skips it.

`qualm --dry-run` lists the files it would send and what that would cost, and sends nothing.

## What leaves your machine

qualm sends the diff of each changed file to OpenRouter, which passes it to TypeSafe. Nothing else is sent. Run `--dry-run` first if you want to see the list, and use `skip` for anything that shouldn't go.

## In CI

```yaml
- uses: actions/checkout@v4
  with:
    fetch-depth: 0
- uses: actions/setup-go@v5
  with:
    go-version: stable
- run: go install github.com/tools4imps/qualm/cmd/qualm@latest
- run: qualm --base "origin/${{ github.base_ref }}"
  env:
    OPENROUTER_API_KEY: ${{ secrets.OPENROUTER_API_KEY }}
```

## Flags

| Flag | What it does |
| --- | --- |
| `--base REF` | Compare with the merge base of REF and `HEAD` (default: the default branch) |
| `--threshold N` | Use this gate threshold for one run |
| `--include-tests` | Judge test files too |
| `--format text\|json` | The report's format |
| `--dry-run` | List what would be sent and its cost, and send nothing |
| `--budget DOLLARS` | Stop when this much is spent (default 1.00) |
| `--cache DIR`, `--no-cache` | Move the cache, or skip it |
| `--jobs N` | Files judged at once (default 8) |

## How the gate was chosen

The first design scored whole files and compared before with after. We tested it on the 99 mainline commits of [mutineer](https://github.com/davidteren/mutineer) that touch its library, 322 file changes in all. It failed. It missed the one real refactor, it read a commit that only added docstrings as an improvement, and it never noticed a file creeping from 222 lines to 921.

Asking about the diff worked on the same changes. The refactor read as easier at 0.81. Documentation commits and a rename read as the same at 0.97 or above. A release that added 22 methods to one class scored 0.61 on `push_back`, and that question at 0.6 blocked 2 of the 99 commits.

That is the whole of the evidence. It is one codebase in one language, and nobody has labelled a set of changes by hand to check the threshold against. Treat 0.6 as a starting point and move it once you've seen what qualm says about your own code. The scripts are in [`spike/`](spike/).

## How qualm holds itself to this

qualm has its own Contract in `contract/`: 89 numbered obligations across nine primitives (skip, diff, questions, config, jev, judge, gate, report and cli). Every obligation has at least one test that names it with a `// Contract: <primitive>/<id>` comment. A test in `internal/contractcheck` publishes contract coverage and fails while any obligation lacks a test.

The tests are held to account too. [Gremlins](https://github.com/go-gremlins/gremlins) mutates every package and reruns the suite. The tests kill 381 mutants, and the 7 that survive are each explained in [`docs/mutation.md`](docs/mutation.md).

Before a release, qualm runs on its own change.

## Known limits in 0.1

- It talks to Jev through OpenRouter only.
- A submodule bump and a symlink's target are judged as if they were a file's text.
- A custom diff driver set through git attributes can change the text after `@@` in a hunk header, which changes the cache key from one machine to the next. Keeps aren't affected.
- It judges one file at a time, so it can't see that a change copied logic from another file. exhale catches that for Ruby.
- The threshold rests on one codebase's history.
- A failing file is asked about again hunk by hunk to find where each diagnosis points, which costs one more request per hunk.
- There is no survey mode for scoring a whole codebase yet.

## License

MIT
