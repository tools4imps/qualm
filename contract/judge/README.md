# Judge

Judging is one file's trip to Jev and back: what is sent, how a large diff is handled, how a close call is settled, and where in the file a diagnosis points.

## Obligations

- **U1** One request per file carries the normalised diff as `state.change`, the format note, the language when the extension is known, and every resolved question.
- **U2** A diff over 60,000 bytes is asked in pieces. Each question takes its highest value among the pieces, and a choice takes the piece where its first option is most probable.
- **U3** A cached answer is replayed without a request, but only when it answers every question asked and was settled against the same gate thresholds. With the cache off, nothing is read and nothing is written.
- **U4** A gating value within 0.05 of its threshold is asked twice more without the cache. The median of the three counts, and it is what gets cached.
- **U5** A gating value further than 0.05 from its threshold is not asked again.
- **U6** For a failing file with several hunks, each diagnosis at 0.5 or above is asked hunk by hunk and reports the lines of the hunk where it is strongest. With one hunk, that hunk's lines are reported without a request. A hunk over the piece limit is asked in pieces like any other diff.
- **U7** Files are judged at most `Jobs` at a time, the hunk-by-hunk requests are held to the same bound, and the result lists the files sorted by path.

```covers
internal/check
```
