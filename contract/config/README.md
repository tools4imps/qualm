# Config

`qualm.json` holds a team's settings and its keeps. A typo in it must never pass for a clean run.

## Obligations

- **C1** A repository without `qualm.json` runs on the defaults.
- **C2** An unknown key anywhere in the file is an error, and so is malformed JSON.
- **C3** A gate threshold outside 0 to 1 is an error.
- **C4** Saving writes two-space indentation, a fixed key order and a final newline. Loading what was saved gives the same config.
- **C5** A save never leaves a half-written file: it writes a temporary file and renames it.

```covers
internal/config
```
