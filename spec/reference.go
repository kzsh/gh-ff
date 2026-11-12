package spec

import (
	"bytes"
	"cmp"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"slices"
	"strings"

	"gopkg.in/yaml.v3"

	"gh-from-file/opsfile"
)

// Reference is the PR's existing discussion, written by gh-ff sync for
// reading only. Compile never reads it; sync replaces it wholesale.
type Reference struct {
	Reviews      []RefReview  `yaml:"reviews,omitempty"`
	Threads      []Thread     `yaml:"threads,omitempty"`
	Conversation []RefComment `yaml:"conversation,omitempty"`
}

type RefReview struct {
	ID     int64  `yaml:"id"`
	Author string `yaml:"author"`
	State  string `yaml:"state"`
	Body   string `yaml:"body,omitempty"`
}

// Thread is an inline review comment and its replies, oldest first.
type Thread struct {
	File string `yaml:"file"`
	Line int    `yaml:"line,omitempty"`
	// Outdated: the anchor no longer exists in the current diff; Line is
	// the line in the diff the comment was made against.
	Outdated bool         `yaml:"outdated,omitempty"`
	Comments []RefComment `yaml:"comments"`
}

type RefComment struct {
	ID     int64  `yaml:"id"`
	Author string `yaml:"author"`
	Body   string `yaml:"body"`
}

// API shapes for the three listings sync reads.

type apiUser struct {
	Login string `json:"login"`
}

type APIReview struct {
	ID    int64   `json:"id"`
	User  apiUser `json:"user"`
	State string  `json:"state"`
	Body  string  `json:"body"`
}

type APIReviewComment struct {
	ID           int64   `json:"id"`
	InReplyToID  int64   `json:"in_reply_to_id"`
	User         apiUser `json:"user"`
	Path         string  `json:"path"`
	Line         *int    `json:"line"` // null once outdated
	OriginalLine int     `json:"original_line"`
	Body         string  `json:"body"`
}

type APIIssueComment struct {
	ID   int64   `json:"id"`
	User apiUser `json:"user"`
	Body string  `json:"body"`
}

// ListPRCommentsOp lists every inline review comment on the PR.
func (r *Review) ListPRCommentsOp() opsfile.Op {
	return opsfile.Op{
		Cmd:   "api",
		Args:  []string{fmt.Sprintf("repos/%s/pulls/%d/comments", r.RepoPath(), r.PR)},
		Flags: map[string]any{"method": "GET", "F": "per_page=100"},
	}
}

// ListIssueCommentsOp lists the PR's top-level conversation comments.
func (r *Review) ListIssueCommentsOp() opsfile.Op {
	return opsfile.Op{
		Cmd:   "api",
		Args:  []string{fmt.Sprintf("repos/%s/issues/%d/comments", r.RepoPath(), r.PR)},
		Flags: map[string]any{"method": "GET", "F": "per_page=100"},
	}
}

// DecodePages decodes `gh api --paginate` output, which for list endpoints
// is one JSON array per page, concatenated.
func DecodePages[T any](data []byte) ([]T, error) {
	var all []T
	dec := json.NewDecoder(bytes.NewReader(data))
	for {
		var page []T
		err := dec.Decode(&page)
		if errors.Is(err, io.EOF) {
			return all, nil
		}
		if err != nil {
			return nil, err
		}
		all = append(all, page...)
	}
}

// BuildReference shapes the API listings into the reference section.
// Reviews with no body and a COMMENTED state are dropped: GitHub creates
// one per inline comment or reply, and their content is in Threads.
func BuildReference(reviews []APIReview, comments []APIReviewComment, issue []APIIssueComment) *Reference {
	ref := &Reference{}
	for _, rv := range reviews {
		body := normBody(rv.Body)
		if body == "" && rv.State == "COMMENTED" {
			continue
		}
		ref.Reviews = append(ref.Reviews, RefReview{ID: rv.ID, Author: rv.User.Login, State: rv.State, Body: body})
	}
	ref.Threads = buildThreads(comments)
	for _, c := range issue {
		ref.Conversation = append(ref.Conversation, RefComment{ID: c.ID, Author: c.User.Login, Body: normBody(c.Body)})
	}
	return ref
}

// buildThreads groups comments under their root. GitHub sets
// in_reply_to_id to the thread root for every reply, so one level suffices.
// Threads sort by file then line; comments within a thread by id, which
// GitHub assigns in creation order.
func buildThreads(comments []APIReviewComment) []Thread {
	byRoot := map[int64]int{}
	var threads []Thread
	sorted := slices.SortedFunc(slices.Values(comments), func(a, b APIReviewComment) int { return cmp.Compare(a.ID, b.ID) })
	for _, c := range sorted {
		rc := RefComment{ID: c.ID, Author: c.User.Login, Body: normBody(c.Body)}
		if i, ok := byRoot[c.InReplyToID]; ok && c.InReplyToID != 0 {
			threads[i].Comments = append(threads[i].Comments, rc)
			continue
		}
		t := Thread{File: c.Path, Comments: []RefComment{rc}}
		if c.Line != nil {
			t.Line = *c.Line
		} else {
			t.Line, t.Outdated = c.OriginalLine, true
		}
		byRoot[c.ID] = len(threads)
		threads = append(threads, t)
	}
	slices.SortStableFunc(threads, func(a, b Thread) int {
		return cmp.Or(cmp.Compare(a.File, b.File), cmp.Compare(a.Line, b.Line))
	})
	return threads
}

// normBody strips CRs (bodies typed in the web UI use CRLF, which forces
// yaml.v3 into double-quoted style instead of a block scalar) and trims.
func normBody(s string) string {
	s = strings.TrimSpace(strings.ReplaceAll(s, "\r\n", "\n"))
	if strings.Contains(s, "\n") {
		s += "\n"
	}
	return s
}

const referenceComment = "Written by gh-ff sync: others' and your existing PR comments, for reading.\nReplaced on every sync; never submitted."

// WriteReference sets the top-level reference key in the spec file,
// replacing any previous one, and leaves the rest of the file intact.
func WriteReference(path string, ref *Reference) error {
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var doc yaml.Node
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		return err
	}
	if len(doc.Content) == 0 || doc.Content[0].Kind != yaml.MappingNode {
		return fmt.Errorf("%s: not a mapping document", path)
	}
	var val yaml.Node
	if err := val.Encode(ref); err != nil {
		return err
	}
	root := doc.Content[0]
	for i := 0; i+1 < len(root.Content); i += 2 {
		if root.Content[i].Value == "reference" {
			root.Content = slices.Delete(root.Content, i, i+2)
			break
		}
	}
	key := &yaml.Node{Kind: yaml.ScalarNode, Value: "reference", HeadComment: referenceComment}
	root.Content = append(root.Content, key, &val)
	return writeNode(path, &doc)
}
