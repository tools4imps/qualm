# Skip

qualm judges code that people wrote. Anything else it sends to Jev is money spent on an answer nobody needs, so the skip rules decide before any request is made.

## Obligations

- **K1** A path under `vendor/`, `node_modules/`, `dist/`, `build/`, `target/`, `third_party/` or `.git/`, at any depth, is skipped.
- **K2** Lockfiles and generated files are skipped: `*.lock`, `package-lock.json`, `go.sum`, `*.min.js`, `*.min.css`, `*.map`, `*.pb.go`, `*_generated.*` and `*.generated.*`.
- **K3** Prose and data files are skipped: `.md`, `.txt`, `.rst`, `.json`, `.yaml`, `.yml`, `.toml`, `.xml`, `.csv`, `.svg` and `.html`.
- **K4** Tests are skipped by name (`*_test.go`, `*_test.rb`, `*_spec.rb`, `*.test.*`, `*.spec.*`, `test_*.py`) and by directory (`test/`, `tests/`, `spec/`, `__tests__/` at any depth), unless tests are included.
- **K5** A config pattern with no slash matches the file name in any directory. One with a slash matches the whole path. `**` matches any number of directories, including none. A pattern ending in a slash matches everything under that directory.
- **K6** Every skip comes with its reason, which is the first rule that matches in the order above, with config patterns last. A path no rule matches is judged.

```covers
internal/skip
```
