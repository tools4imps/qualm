# Questions

The questions are the product. A team can add its own, and a mistake in one must stop the run instead of quietly asking something else.

## Obligations

- **Q1** Ten questions are built in: `push_back`, `direction`, `simplified`, `comments_only`, `new_behaviour`, `grew_a_big_unit`, `added_copies`, `added_impossible_guards`, `added_placeholders` and `added_unused_flexibility`. `push_back` is the only one that gates, at 0.6.
- **Q2** Every question sent carries the guard sentence after its instructions.
- **Q3** A config question with a built-in's id replaces it. Any other is added after the built-ins.
- **Q4** `drop` removes built-in questions by id. Dropping an id that doesn't exist is an error.
- **Q5** The gate's question and threshold can be changed. Naming a question that doesn't exist is an error.
- **Q6** A question needs an id, instructions and a known type. A score needs 2 to 10 levels and a choice needs at least 2 options. A gating question must be `noul` or `score` with a threshold above 0 and at most 1. Two config questions with one id are an error.
- **Q7** With no gating question left, resolving fails.
- **Q8** A choice question's options keep the order they were written in.

```covers
internal/questions
```
