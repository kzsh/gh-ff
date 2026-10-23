---
name: gh-ff
description: "Format reference for gh-ff YAML files that describe GitHub operations (PR reviews, comments, issues, any gh CLI call). Use when asked to prepare GitHub operations as a file: write the file per this reference and hand it to the user, who reviews and executes it with gh-ff."
---

# gh-ff file formats

You write the file; the user reviews it and executes it (`gh-ff submit
<file>` for a spec, `gh-ff run <file>` for an ops file). The file is the
deliverable — write it whether or not gh-ff is available to you, and
never execute GitHub operations yourself in its place.

Two formats, both self-contained YAML with markdown in block scalars:

- **Spec** — task-shaped documents like `review`. Prefer this where a
  document type exists for the task.
- **Ops** — direct mapping onto `gh` CLI invocations; `cmd: api` reaches
  any REST or GraphQL endpoint, so every GitHub operation is expressible.
  Use when no spec document type fits.

## Spec: review

Describes a whole PR review — summary, verdict, and all inline comments —
submitted as one atomic request:

```yaml
review:
  pr: 123
  repo: owner/repo        # optional; defaults to the current repo
  event: request_changes  # approve | request_changes | comment; omit to leave pending
  summary: |
    Two blocking issues, details inline.
  comments:
    - file: src/api.js
      line: 42
      body: |
        This endpoint is **unauthenticated**.

        Add the auth middleware before merging.
    - file: src/auth.js
      line: 15
      start_line: 12      # optional: comment spans 12-15
      side: RIGHT         # optional: LEFT | RIGHT
      body: Use bcrypt, not md5.
```

Bodies are markdown. `line` numbers refer to positions in the PR diff's
file versions, and the file must appear in the diff.

Validation (the file is rejected before anything runs):

- top-level key must be `review`; unknown keys anywhere are errors
- `pr`: required, integer > 0
- `event`: optional; exactly `approve`, `request_changes`, or `comment`
  (case-insensitive); omitted leaves the review pending
- a non-empty `summary` or at least one comment is required, except with
  `event: approve`
- each comment: `file`, `line` (integer > 0), and non-empty `body`
  required; `start_line` and `side` are optional and unvalidated locally
  (GitHub rejects `start_line` >= `line` and `side` outside LEFT/RIGHT)
- `summary` and bodies are whitespace-trimmed before submission

### Resubmission: `id` fields

After a successful submission, gh-ff writes the server-assigned ids back
into the file: `id:` on the review and on each comment. A file carrying
`review.id` updates the existing review on the next submission instead of
creating a duplicate — never the reverse.

When editing a file that already has ids:

- preserve every `id:` field; removing the review id causes a duplicate
  review, removing a comment id is an error
- only bodies are updatable: the summary and comment `body` text. The
  verdict (`event`) of a submitted review cannot be changed and is
  ignored; `file`/`line` anchors are fixed at creation
- do not add new comments to a review with an id — the file is rejected
  (updates cannot create); put new comments in a new review file
- never invent an id; a nonexistent id makes GitHub reject the update

## Ops format

```yaml
version: 1          # required, must be 1
ops:                # executed in order
  - cmd: pr review  # gh subcommand path, space-separated. Required.
    args: ["123"]   # positional arguments, optional
    flags:          # optional; flag names without dashes
      event: REQUEST_CHANGES
```

Flag value forms:

| YAML value            | Emitted as                                  |
|-----------------------|---------------------------------------------|
| string / number / bool| `--name=value`                              |
| list                  | `--name=v1 --name=v2` (repeated)            |
| `null` (empty value)  | `--name` (bare flag)                        |
| `{content: "..."}`    | `--name=<path>` of a file written from content |

Single-character names get one dash: `R: owner/repo` → `-R=owner/repo`.
Flags are emitted in sorted name order.

Validation (the file is rejected before anything runs):

- `version`: required, must be exactly `1`
- only `version`, `ops`, `cmd`, `args`, `flags` exist; unknown keys are
  errors
- each op: `cmd` required and non-empty; `args` must be strings
- flag values are limited to the table above: list items must be scalars
  (no nested lists or mappings), and a mapping value must be exactly
  `{content: <string>}` — any extra key is an error
- flag names are not checked against gh; a misspelled flag fails at
  execution time, not load time

## Keep files self-contained

Never reference external files the user would have to create. Where gh
expects a file path (`body-file`, `input`, ...), embed the content:

```yaml
flags:
  body-file:
    content: |
      ## Review
      Rate limiting is missing on the new endpoints.
```

gh-ff materializes the content to a temp file and substitutes the path.

## Ops examples

Inline line comment via the REST API:

```yaml
version: 1
ops:
  - cmd: api
    args: ["repos/{owner}/{repo}/pulls/123/comments"]
    flags:
      method: POST
      f:
        - "body=This endpoint is unauthenticated."
        - "path=src/api.js"
        - "commit_id=HEAD_SHA_HERE"
      F:
        - "line=42"
```

`gh api` notes: `{owner}/{repo}` placeholders resolve from the current
repo; `-f` sends string fields, `-F` typed fields (numbers, booleans);
`input` with `{content: ...}` sends a raw JSON body for nested payloads.

## Rules

- One file per user request; name it after the task (`review-pr-123.yaml`).
- Prefer spec documents over ops; within ops, prefer dedicated gh
  subcommands (`issue create`, `pr comment`) over raw `api` where one
  exists — fewer parameters to get wrong.
- Ops run in order and execution stops at the first failure; order
  dependent operations accordingly.
- Do not invent flags: use flags the gh subcommand actually accepts
  (`gh <cmd> --help`).
