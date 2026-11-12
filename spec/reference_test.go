package spec

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBuildThreads(t *testing.T) {
	line := 42
	comments := []APIReviewComment{
		{ID: 30, InReplyToID: 10, User: apiUser{"bob"}, Path: "b.go", Body: "second reply"},
		{ID: 10, User: apiUser{"me"}, Path: "b.go", Line: &line, Body: "root"},
		{ID: 20, InReplyToID: 10, User: apiUser{"alice"}, Path: "b.go", Body: "first reply"},
		{ID: 5, User: apiUser{"alice"}, Path: "a.go", OriginalLine: 7, Body: "stale"},
	}
	th := buildThreads(comments)
	if len(th) != 2 {
		t.Fatalf("want 2 threads, got %+v", th)
	}
	if th[0].File != "a.go" || !th[0].Outdated || th[0].Line != 7 {
		t.Errorf("outdated thread: %+v", th[0])
	}
	got := []int64{}
	for _, c := range th[1].Comments {
		got = append(got, c.ID)
	}
	if th[1].Line != 42 || len(got) != 3 || got[0] != 10 || got[1] != 20 || got[2] != 30 {
		t.Errorf("thread order: line %d ids %v", th[1].Line, got)
	}
}

func TestWriteReference(t *testing.T) {
	path := filepath.Join(t.TempDir(), "review.yaml")
	orig := `review:
  id: 555
  pr: 123
  # keep this comment
  summary: |
    Mine.
reference:
  conversation:
    - id: 1
      author: stale
      body: replaced
`
	if err := os.WriteFile(path, []byte(orig), 0o644); err != nil {
		t.Fatal(err)
	}
	ref := BuildReference(nil, nil, []APIIssueComment{{ID: 2, User: apiUser{"bob"}, Body: "line one\r\nline two"}})
	if err := WriteReference(path, ref); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(path)
	text := string(raw)
	if !strings.Contains(text, "# keep this comment") || !strings.Contains(text, "body: |") {
		t.Errorf("comment lost or body not a block scalar:\n%s", text)
	}
	d, err := Load(path)
	if err != nil {
		t.Fatalf("written file does not load: %v\n%s", err, text)
	}
	if d.Review.ID != 555 || d.Review.Summary != "Mine.\n" {
		t.Errorf("review damaged: %+v", d.Review)
	}
	c := d.Reference.Conversation
	if len(c) != 1 || c[0].ID != 2 || c[0].Body != "line one\nline two\n" {
		t.Errorf("reference not replaced: %+v", c)
	}
}
