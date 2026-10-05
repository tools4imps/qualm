# CLI

The command line is how CI and coding agents meet qualm, so a typo there must never pass for a clean run.

## Obligations

- **L1** `qualm` and `qualm PATH...` run the check. `qualm keep PATH... --reason TEXT` records keeps.
- **L2** It exits 0 when the gate passes, 1 when the gate fails and 2 when qualm couldn't run.
- **L3** An unknown flag, an unknown format, a threshold outside 0 to 1, or a number that isn't finite exits 2 with the usage on stderr.
- **L4** A `--base` that resolves to no commit exits 2.
- **L5** A run that needs Jev and has no key exits 2 naming `OPENROUTER_API_KEY`. A dry run needs no key.
- **L6** `--version` prints the version and `--help` prints the usage. Both exit 0.
- **L7** The report goes to stdout and every error goes to stderr.
- **L8** A mistake in `qualm.json` exits 2 with its message.
- **L9** `--format json` prints the JSON report.
- **L10** Flags may come before or after the paths.
- **L11** A flag that only applies to the check, given with `keep`, exits 2.

```covers
internal/cli
```
