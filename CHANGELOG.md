# Changelog

## 0.1.1

qualm is on RubyGems. `gem install qualm` installs the Go binary for your platform, with a `qualm` command that runs it. Nothing about the tool itself changed.

- A gem per platform: macOS, Linux and Windows, on Intel and ARM.
- Releases are driven by the version in `internal/cli/version.go`. Landing a new number on `main` publishes the gems through RubyGems trusted publishing, then cuts the tag and the GitHub release from the same build.

## 0.1.0

First release. `qualm` fails a pull request when an experienced reviewer would ask for the change to be simplified, and tells the coding agent what to fix.

- One request per changed file to Jev, TypeSafe's System One decision model, with the file's diff and ten questions. The run fails when `push_back` reaches 0.6.
- `qualm keep` records that a person accepted a change, bound to the lines it adds and removes.
- Text and JSON reports, with the lines each diagnosis points at and the command to keep a change.
- A cache keyed by the exact request, a budget of one dollar a run by default, and `--dry-run`.
- qualm ships with its own Contract (89 obligations, each named by a test), a mutation record, and a clean run on its own change.
