# gh-ff

Execute GitHub operations from reviewable YAML files. An agent (or you)
writes the file; you read it, then `gh-ff` runs it via the `gh` CLI. The
idea is to have deterministic execution of the specified operations after 
from review to execution. 

## Formats

Two layers, both self-contained YAML with markdown in block scalars:

- **Ops** (layer 1) — entries map directly onto `gh` invocations:
  `cmd`, `args`, `flags`. `cmd: api` reaches any REST/GraphQL endpoint.
  Executed in order, stops at first failure.
- **Spec** (layer 2) — task-shaped documents compiled to ops. Currently
  one type: `review`, a whole PR review (summary, verdict, inline
  comments) submitted as one atomic request.

Full format reference, including validation rules:
[skills/ops-file/SKILL.md](skills/ops-file/SKILL.md).

## Usage

```
gh-ff run [-n] [--read-only] <ops.yaml>     execute an ops file
gh-ff submit [-n] [--read-only] <spec.yaml> compile a spec and execute it
gh-ff compile <spec.yaml>       print the compiled ops YAML
gh-ff template <review|ops>     print a starter file
gh-ff sync <spec.yaml> [id]     recover server ids for a submitted review
```

`-n`/`--dry-run` prints the `gh` commands without executing.

`--read-only` audits every compiled command before the first one runs and
aborts, naming the offenders, if any would mutate. The audit is static and
fails closed: `gh api` is judged by its HTTP method (explicit `-X`/`--method`,
or the POST that `gh` implies when a body flag is present), `gh api graphql`
by whether its query document is a mutation, and every other `gh` command by
its trailing verb against a read-only list (`view`, `list`, `diff`, `checks`,
`status`, `search`, `browse`). Unrecognized commands count as mutations.

After `submit`, gh-ff writes the server-assigned review and comment ids
back into the spec file. Resubmitting a file with ids updates bodies in
place instead of creating a duplicate review.

## Layout

- `opsfile/` — layer-1 parsing, validation, argv construction
- `spec/` — layer-2 parsing, compilation, id write-back
- `cmd/gh-ff/` — CLI
- `skills/ops-file/` — format reference as an agent skill

## Build

```
just          # go build ./...
just test
just install  # go install ./cmd/gh-ff
```

Requires `gh` authenticated at runtime.
