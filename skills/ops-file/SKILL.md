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
  any REST or GraphQL endpoint.
  Use when no spec document type fits.

## Spec: review

Describes a whole PR review — summary, verdict, and all inline comments.
gh-ff creates it pending, records its ids in the file, then publishes it
with the `event`:

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
- a non-empty `summary`, at least one comment, or an `event` is required;
  every event may be submitted without a summary
- each comment: `file`, `line` (integer > 0), and non-empty `body`
  required; `start_line` and `side` are optional and unvalidated locally
  (GitHub rejects `start_line` >= `line` and `side` outside LEFT/RIGHT)
- `summary` and bodies are whitespace-trimmed before submission

### Resubmission: `id` fields

On submission, gh-ff writes the server-assigned ids into the file
before publishing: `id:` on the review and on each comment. A file
carrying `review.id` updates the existing review on the next submission
instead of creating a duplicate — never the reverse. If publishing
failed, the review stays pending with its ids in the file, and the next
submission publishes it.

When editing a file that already has ids:

- preserve every `id:` field; removing the review id while comments keep
  theirs is an error, removing all ids creates a duplicate review, and
  removing a comment id is an error
- only bodies are updatable: the summary and comment `body` text.
  `event` applies only while the review is pending; a published
  review's verdict cannot be changed. `file`/`line` anchors are fixed at
  creation
- do not add new comments to a review with an id — the file is rejected
  (updates cannot create); put new comments in a new review file
- never invent an id; a nonexistent id makes GitHub reject the update
- deleting a comment from the file leaves it on GitHub untouched; an
  empty `summary` leaves the submitted summary unchanged

If a review was submitted but its ids are missing from the file, the user
recovers them with `gh-ff sync <file> [review-id]`; without an id, sync
finds the review by the authenticated user whose body equals `summary`.

### Existing discussion: `reference`

`gh-ff sync` also writes a top-level `reference` section beside
`review`: the PR's existing reviews, inline threads, and conversation
comments, from all authors including the user.

```yaml
reference:
  reviews:              # verdicts and summaries
    - id: 554
      author: alice
      state: CHANGES_REQUESTED
      body: Needs tests.
  threads:              # inline comments; root first, then replies
    - file: src/api.js
      line: 42
      outdated: true    # anchor gone from the current diff; line is from the old one
      comments:
        - id: 901
          author: me
          body: This endpoint is unauthenticated.
        - id: 950
          author: alice
          body: Fixed in a1b2c3.
  conversation:         # top-level PR comments
    - id: 1001
      author: bob
      body: LGTM once CI passes.
```

Read it for context: what others raised, how they replied to the user's
comments, which points are already settled. Treat it as read-only:

- never edit it; sync replaces the whole section, discarding edits
- never copy its entries into `review.comments`; that would resubmit
  someone else's comment as the user's, or fail on its foreign `id`
- do not write a `reference` section yourself

Review comments with no body and state `COMMENTED` are omitted from
`reviews`; GitHub creates one per inline comment, already in `threads`.

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

## Checking a file

These commands validate without touching GitHub; run them if gh-ff is
available:

- `gh-ff template <review|ops>` prints a valid starter file
- `gh-ff compile <spec.yaml>` prints the ops a spec lowers to; for a
  new review, the publish op carries a literal `REVIEW_ID` that submit
  fills in, so the output is not runnable as an ops file
- `gh-ff submit -n <spec.yaml>` and `gh-ff run -n <ops.yaml>` print the
  `gh` commands without executing them

The user may execute with `--read-only`, which aborts before the first
command unless every command is read-only: `gh api` with `method` GET,
HEAD, or OPTIONS, or with neither `method` nor a body flag (`f`, `F`,
`field`, `raw-field`, `input`); a GraphQL query; or a gh verb in `view`, `list`,
`diff`, `checks`, `status`, `search`, `browse`. Anything else counts as a
mutation.

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
