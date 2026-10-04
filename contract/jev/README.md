# Jev

Jev is the only thing qualm talks to over the network. This is where money is spent and where a secret could leak.

## Obligations

- **J1** A request is a `POST` to the endpoint with `Authorization: Bearer <key>` and a JSON body of model, state and questions.
- **J2** A missing key fails before any request is made, and the error names `OPENROUTER_API_KEY`.
- **J3** A `noul` answer is its probability. A `score` answer is its level divided by the top level. A `choice` answer is the chosen option with every option's probability.
- **J4** A reply that lacks an asked question, or holds a value outside 0 to 1, is an error.
- **J5** Statuses 429, 500, 502, 503, 504 and 529 are retried, up to four attempts in all, waiting 1, 2 then 4 seconds. Any other failing status fails at once.
- **J6** An error never contains the key or the body of the reply.
- **J7** The reply's `usage.cost` is the cost. When it is absent, the cost is the input tokens at $0.042 per million.
- **J8** The cache key is the SHA-256 of the request's JSON. The same request always gives the same key, and a change to the model, the state or the questions gives another.
- **J9** The cache returns what was put under a key, misses on an unknown key or a corrupt entry, and writes through a temporary file.
- **J10** The budget refuses a request once the spend has reached the limit, and it counts requests, tokens and cost correctly from several goroutines at once.

```covers
internal/jev
```
