# gh-ff

Execute GitHub operations from YAML files. An agent (or you) writes the
file; you read it, then `gh-ff` runs it via the `gh` CLI, executing only
the operations the file states, in file order.

## Formats

Two layers, both self-contained YAML with markdown in block scalars:

- **Ops** (layer 1) — entries map directly onto `gh` invocations:
  `cmd`, `args`, `flags`. `cmd: api` reaches any REST/GraphQL endpoint.
  Executed in order, stops at first failure.
- **Spec** (layer 2) — task-shaped documents compiled to ops. Currently
  one type: `review`, a whole PR review (summary, verdict, inline
  comments), created pending, then published once its ids are recorded.

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

`submit` creates a review with content in two steps: a pending review,
whose review and comment ids gh-ff writes into the spec file, then a
publish with the `event`. Every id is in the file before the review is
visible. If the create fails or its response is lost, gh-ff adopts your
pending review on the PR when its content matches the file; GitHub allows
one pending review per user per PR, so a repeated create cannot
duplicate. A review with an `event` needs no summary; a bodiless approval
with no comments is a single request.

Resubmitting a file with ids updates bodies in place, then publishes the
review if it is still pending.

`sync` also writes the PR's existing reviews, inline threads, and
conversation comments into a top-level `reference` section. `submit`
ignores it; each sync replaces it.

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
