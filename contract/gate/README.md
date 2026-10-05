# Gate

The gate turns answers into a pass or a fail. It must never pass a change it couldn't judge, and a person's decision to keep a change must bind to exactly that change.

## Obligations

- **G1** The run fails when any gating question reaches its threshold on any judged file. Otherwise it passes.
- **G2** A threshold given for the run replaces the default gate's threshold.
- **G3** A file matching a skip rule, a binary file and a file git marks generated or vendored are reported as skipped with the reason, and are never sent.
- **G4** A keep binds to the lines a file's change adds and removes. While those are the same the file is kept: it isn't sent and it can't fail the run. A change elsewhere in the file on the base branch doesn't break the keep.
- **G5** A keep that matches no current change is reported as stale and doesn't fail the run. A keep for a file the run skips is not looked at.
- **G6** Keeping records the path, the change hash, the reason and the date for each path. It replaces an earlier keep for the same path and drops stale keeps.
- **G7** Keeping refuses an empty reason and a path with no change against the base, and writes nothing when it refuses.
- **G8** A dry run sends nothing and reports the diff size of each file it would judge.
- **G9** An error from Jev or from the budget while a file is being judged stops the run with that error. The run reports no result.
- **G10** With nothing to judge, the run passes.
- **G11** A mistake in a config question or in the gate setting is reported as a mistake in `qualm.json`.
- **G12** An error while finding where the diagnoses point leaves the verdict standing and is reported as a warning. So is a cache that can't be written.
- **G13** A file skipped by a rule or by a git attribute never has its diff read.

```covers
internal/check
```
