# The spike

Throwaway Ruby scripts from the design of qualm. They asked Jev draft questions about real code to find out what works as a gate.

- `run.rb` scores whole files. `history.rb` scores each file before and after every mainline commit of a repository. `compare.rb` asks about each change instead, as a diff and as a pair of whole versions.
- `questions.json` holds the file-level questions and `questions-change.json` the change-shaped ones that qualm ships with.
- `show.rb`, `history_show.rb` and `compare_show.rb` print the results.

What they found is summarised in `docs/superpowers/specs/2026-10-04-qualm-design.md`. The raw answers and the cache are left out of the repository. Rerunning everything costs about 15 cents.

To run one: `OPENROUTER_API_KEY=... ruby compare.rb path/to/repository`.
