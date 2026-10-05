# Gate

The gate turns answers into a pass or a fail. It must never pass a change it couldn't judge, and a person's decision to keep a change must bind to exactly that change.

## Obligations

- **G1** The run fails when any gating question reaches its threshold on any judged file. Otherwise it passes.
- **G2** A threshold given for the run replaces the default gate's threshold.
- **G3** A file matching a skip rule, a binary file and a file git marks generated or vendored are reported as skipped with the reason, and are never sent.
- **G4** A keep whose hash equals the file's current change hash marks the file kept. It isn't sent and it can't fail the run.
- **G5** A keep that matches no current change is reported as stale and doesn't fail the run.
- **G6** Keeping records the path, the change hash, the reason and the date for each path. It replaces an earlier keep for the same path and drops stale keeps.
- **G7** Keeping refuses an empty reason and a path with no change against the base, and writes nothing when it refuses.
- **G8** A dry run sends nothing and reports each file's diff size.
- **G9** An error from Jev or from the budget stops the run with that error. The run reports no result.
- **G10** With nothing to judge, the run passes.
- **G11** A mistake in a config question or in the gate setting is reported as a mistake in `qualm.json`.

```covers
internal/check
```
