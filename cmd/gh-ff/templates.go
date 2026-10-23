package main

// Templates are valid documents, not just skeletons: each parses,
// validates, and dry-runs as written (pinned by TestTemplates).
var templates = map[string]string{
	"review": `# gh-ff review spec. Preview: gh-ff submit -n <file>
review:
  pr: 1                      # required: pull request number, > 0
  # repo: owner/name         # optional: defaults to the repo of the current directory
  # event: comment           # optional: approve | request_changes | comment; omit = pending review
  summary: |                 # a summary or a comment is required, except to approve
    Overall feedback, markdown.
  comments:
    - file: path/in/repo.go  # required: file must appear in the PR diff
      line: 1                # required: diff line number, > 0
      # start_line: 1        # optional: first line of a multi-line range
      # side: RIGHT          # optional: LEFT (old version) | RIGHT (new version)
      body: |                # required: markdown
        Comment text.
`,
	"ops": `# gh-ff layer-1 ops file. Preview: gh-ff run -n <file>
version: 1                   # required: must be 1
ops:                         # executed in order; stops at first failure
  - cmd: api                 # required: gh subcommand path, space-separated ("pr comment", "api")
    args: ["repos/{owner}/{repo}/issues"]
    flags:                   # flag names without dashes; one dash if single-char
      method: POST           # scalar          -> --method=POST
      f:                     # list            -> repeated: -f=title=Example
        - "title=Example"
      # paginate:            # null            -> --paginate (bare flag)
      # input:               # {content: ...}  -> written to a temp file, path substituted
      #   content: |
      #     {"title": "Example"}
`,
}
