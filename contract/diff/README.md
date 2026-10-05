# Diff

The diff is what Jev sees, what the cache is keyed by and what a keep binds to. It has to be the same text for the same change on every machine.

## Obligations

- **D1** The base is the merge base of the given ref and `HEAD`. With no ref, the default branch is the first of `origin/HEAD`, `origin/main`, `origin/master`, `main` and `master` that resolves.
- **D2** A ref that resolves to no commit is an error, including one that looks like a git option.
- **D3** Changes are measured from the base to the working tree. Committed, staged, unstaged and untracked files all count, and files git ignores don't.
- **D4** Added, modified and renamed files are listed, sorted by path. Deleted files are not. A renamed file appears under its new path.
- **D5** Path arguments narrow the list to those files and directories. They narrow it after renames are paired, so a renamed file is still seen as a rename.
- **D6** A normalised diff starts at its `---` line. No `diff --git`, `index`, mode or rename line survives anywhere in it, including when a file changes type.
- **D7** The same change gives the same normalised diff whether it is committed, staged or sitting in the working tree.
- **D8** A binary file is marked binary and has no diff.
- **D9** A file that git attributes mark `linguist-generated` or `linguist-vendored` is marked.
- **D10** Each hunk carries the line range it covers in the new file.
- **D11** Splitting keeps hunks whole and repeats the `---` and `+++` lines on every piece. A hunk larger than the limit is split between lines. Read in order, the pieces hold every line of the original hunks.
- **D12** The repository's root is found from any directory inside it. Outside a repository it is an error that says so.
- **D13** The diff does not depend on the user's git settings or environment. Colour, an external diff tool, text conversion, the diff algorithm, the context and the path prefixes are all pinned.
- **D14** A file's name is never read as a pattern, whatever characters it holds.
- **D15** A changed file whose diff can't be read is an error, never a skip. Only a mode-only change, a pure rename or a new empty file has no content to judge.
- **D16** A path argument that exists neither in the working tree nor in the base is an error.
- **D17** Listing the changes reads no diff. A file's diff is read only when it is asked for.

```covers
internal/gitdiff
```
