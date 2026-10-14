package opsfile

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestArgv(t *testing.T) {
	op := Op{
		Cmd:  "pr review",
		Args: []string{"123"},
		Flags: map[string]any{
			"event":     "REQUEST_CHANGES",
			"body-file": "./review.md",
			"R":         "owner/repo",
			"f":         []any{"a=1", "b=2"},
			"paginate":  nil,
			"limit":     30,
			"draft":     true,
		},
	}
	got, err := op.Argv()
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"pr", "review", "123",
		"-R=owner/repo",
		"--body-file=./review.md",
		"--draft=true",
		"--event=REQUEST_CHANGES",
		"-f=a=1", "-f=b=2",
		"--limit=30",
		"--paginate",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %q\nwant %q", got, want)
	}
}

func writeTemp(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "ops.yaml")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLoad(t *testing.T) {
	f, err := Load(writeTemp(t, `
version: 1
ops:
  - cmd: api
    args: ["repos/{owner}/{repo}/pulls/1/comments"]
    flags:
      method: POST
      input: ./comment.json
`))
	if err != nil {
		t.Fatal(err)
	}
	if len(f.Ops) != 1 || f.Ops[0].Cmd != "api" {
		t.Errorf("unexpected parse: %+v", f)
	}
}

func TestLoadRejectsUnknownField(t *testing.T) {
	_, err := Load(writeTemp(t, `
version: 1
ops:
  - cmd: api
    arguments: ["x"]
`))
	if err == nil {
		t.Error("want error for unknown field, got nil")
	}
}

func TestLoadRejectsMissingCmd(t *testing.T) {
	_, err := Load(writeTemp(t, `
version: 1
ops:
  - args: ["x"]
`))
	if err == nil {
		t.Error("want error for missing cmd, got nil")
	}
}

func TestMaterialize(t *testing.T) {
	op := Op{
		Cmd: "pr review",
		Flags: map[string]any{
			"body-file": map[string]any{"content": "## Review\nhi\n"},
			"event":     "COMMENT",
		},
	}
	dir := t.TempDir()
	if err := op.Materialize(dir, "op0-"); err != nil {
		t.Fatal(err)
	}
	path, ok := op.Flags["body-file"].(string)
	if !ok {
		t.Fatalf("body-file not replaced with path: %v", op.Flags["body-file"])
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != "## Review\nhi\n" {
		t.Errorf("content mismatch: %q", raw)
	}
	if op.Flags["event"] != "COMMENT" {
		t.Errorf("unrelated flag touched: %v", op.Flags["event"])
	}
}

func TestMaterializeRejectsBadMapping(t *testing.T) {
	op := Op{Cmd: "api", Flags: map[string]any{
		"input": map[string]any{"content": "x", "extra": 1},
	}}
	if err := op.Materialize(t.TempDir(), "op0-"); err == nil {
		t.Error("want error for extra keys, got nil")
	}
}
