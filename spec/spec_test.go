package spec

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func load(t *testing.T, content string) *Doc {
	t.Helper()
	path := filepath.Join(t.TempDir(), "spec.yaml")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	d, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func TestCompileReview(t *testing.T) {
	d := load(t, `
review:
  pr: 123
  repo: owner/repo
  event: request_changes
  summary: |
    Two blocking issues.
  comments:
    - file: src/api.js
      line: 42
      body: |
        Unauthenticated endpoint.
    - file: src/auth.js
      line: 15
      start_line: 12
      side: RIGHT
      body: Use bcrypt.
`)
	f, err := d.Compile()
	if err != nil {
		t.Fatal(err)
	}
	if len(f.Ops) != 1 {
		t.Fatalf("want 1 op, got %d", len(f.Ops))
	}
	op := f.Ops[0]
	if op.Cmd != "api" || op.Args[0] != "repos/owner/repo/pulls/123/reviews" {
		t.Errorf("wrong target: %s %v", op.Cmd, op.Args)
	}
	content := op.Flags["input"].(map[string]any)["content"].(string)
	var p reviewPayload
	if err := json.Unmarshal([]byte(content), &p); err != nil {
		t.Fatal(err)
	}
	if p.Event != "REQUEST_CHANGES" || p.Body != "Two blocking issues." {
		t.Errorf("payload header: %+v", p)
	}
	if len(p.Comments) != 2 || p.Comments[0].Path != "src/api.js" ||
		p.Comments[1].StartLine != 12 || p.Comments[1].Side != "RIGHT" {
		t.Errorf("payload comments: %+v", p.Comments)
	}
}

func TestCompileDefaultsRepoPlaceholder(t *testing.T) {
	d := load(t, `
review:
  pr: 7
  summary: LGTM
  event: approve
`)
	f, err := d.Compile()
	if err != nil {
		t.Fatal(err)
	}
	if f.Ops[0].Args[0] != "repos/{owner}/{repo}/pulls/7/reviews" {
		t.Errorf("wrong endpoint: %v", f.Ops[0].Args)
	}
}

func TestCompileRejects(t *testing.T) {
	for name, content := range map[string]string{
		"bad event":    "review:\n  pr: 1\n  summary: x\n  event: nope\n",
		"no pr":        "review:\n  summary: x\n",
		"empty review": "review:\n  pr: 1\n",
		"bad comment":  "review:\n  pr: 1\n  comments:\n    - file: a.go\n",
	} {
		if _, err := load(t, content).Compile(); err == nil {
			t.Errorf("%s: want error, got nil", name)
		}
	}
}

func TestCompileUpdate(t *testing.T) {
	d := load(t, `
review:
  id: 555
  pr: 123
  repo: owner/repo
  event: request_changes
  summary: Updated summary.
  comments:
    - id: 901
      file: src/api.js
      line: 42
      body: Updated comment.
`)
	f, err := d.Compile()
	if err != nil {
		t.Fatal(err)
	}
	if len(f.Ops) != 2 {
		t.Fatalf("want 2 ops, got %d", len(f.Ops))
	}
	if f.Ops[0].Args[0] != "repos/owner/repo/pulls/123/reviews/555" ||
		f.Ops[0].Flags["method"] != "PUT" {
		t.Errorf("review update op: %+v", f.Ops[0])
	}
	if f.Ops[1].Args[0] != "repos/owner/repo/pulls/comments/901" ||
		f.Ops[1].Flags["method"] != "PATCH" {
		t.Errorf("comment update op: %+v", f.Ops[1])
	}
}

func TestCompileUpdateRejectsCommentWithoutID(t *testing.T) {
	d := load(t, `
review:
  id: 555
  pr: 123
  comments:
    - file: a.go
      line: 1
      body: new comment
`)
	if _, err := d.Compile(); err == nil {
		t.Error("want error for id-less comment in update mode, got nil")
	}
}

func TestCompileRejectsCommentIDWithoutReviewID(t *testing.T) {
	d := load(t, `
review:
  pr: 123
  comments:
    - id: 901
      file: a.go
      line: 1
      body: x
`)
	if _, err := d.Compile(); err == nil {
		t.Error("want error for comment id without review id, got nil")
	}
}
