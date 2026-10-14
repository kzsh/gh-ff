package spec

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWriteIDs(t *testing.T) {
	path := filepath.Join(t.TempDir(), "review.yaml")
	orig := `review:
  pr: 123
  event: request_changes
  summary: |
    Two issues.
  comments:
    # keep this comment
    - file: src/api.js
      line: 42
      body: |
        First.
    - file: src/auth.js
      line: 15
      body: Second.
`
	if err := os.WriteFile(path, []byte(orig), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := WriteIDs(path, 555, []int64{901, 902}); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(path)
	text := string(raw)
	if !strings.Contains(text, "# keep this comment") {
		t.Error("yaml comment lost")
	}
	d, err := Load(path)
	if err != nil {
		t.Fatalf("written file does not load: %v\n%s", err, text)
	}
	if d.Review.ID != 555 || d.Review.Comments[0].ID != 901 || d.Review.Comments[1].ID != 902 {
		t.Errorf("ids not written: %+v", d.Review)
	}
	if d.Review.Summary != "Two issues.\n" {
		t.Errorf("block scalar damaged: %q", d.Review.Summary)
	}
	// Idempotent: writing again overwrites in place.
	if err := WriteIDs(path, 555, []int64{901, 902}); err != nil {
		t.Fatal(err)
	}
	if d2, err := Load(path); err != nil || d2.Review.ID != 555 {
		t.Errorf("second write broke the file: %v", err)
	}
}

func TestMatchCommentIDs(t *testing.T) {
	comments := []Comment{
		{File: "a.go", Line: 1, Body: "dup\n"},
		{File: "a.go", Line: 9, Body: "dup"},
		{File: "b.go", Line: 2, Body: "other"},
	}
	submitted := []SubmittedComment{
		{ID: 11, Path: "a.go", Body: "dup"},
		{ID: 12, Path: "a.go", Body: "dup"},
		{ID: 13, Path: "b.go", Body: "other"},
	}
	ids, err := MatchCommentIDs(comments, submitted)
	if err != nil {
		t.Fatal(err)
	}
	if ids[0] != 11 || ids[1] != 12 || ids[2] != 13 {
		t.Errorf("got %v", ids)
	}
	if _, err := MatchCommentIDs([]Comment{{File: "c.go", Body: "missing"}}, submitted); err == nil {
		t.Error("want error for unmatched comment, got nil")
	}
}
