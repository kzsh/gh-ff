// Package spec implements layer 2: task-shaped YAML documents with
// markdown block-scalar bodies, compiled to layer-1 ops files.
package spec

import (
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"

	"gh-from-file/opsfile"
)

// Doc is a layer-2 document, discriminated by its single top-level key.
type Doc struct {
	Review    *Review    `yaml:"review"`
	Reference *Reference `yaml:"reference,omitempty"`
}

type Review struct {
	// ID is written back by gh-ff after a successful submission. When set,
	// submission updates the existing review instead of creating one.
	ID   int64  `yaml:"id,omitempty"`
	PR   int    `yaml:"pr"`
	Repo string `yaml:"repo,omitempty"` // owner/name; defaults to the current repo
	// Draft keeps the review pending: it cannot set an event, since
	// publishing is what an event asks for.
	Draft bool `yaml:"draft,omitempty"`
	// Event: approve | request_changes | comment (case-insensitive).
	// Empty leaves the review pending.
	Event    string    `yaml:"event,omitempty"`
	Summary  string    `yaml:"summary,omitempty"`
	Comments []Comment `yaml:"comments,omitempty"`
}

type Comment struct {
	ID        int64  `yaml:"id,omitempty"` // written back by gh-ff; enables updates
	File      string `yaml:"file"`
	Line      int    `yaml:"line"`
	StartLine int    `yaml:"start_line,omitempty"` // start of a multi-line range
	Side      string `yaml:"side,omitempty"`       // LEFT | RIGHT
	Body      string `yaml:"body"`
}

var events = map[string]string{
	"approve":         "APPROVE",
	"request_changes": "REQUEST_CHANGES",
	"comment":         "COMMENT",
}

func Load(path string) (*Doc, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	dec := yaml.NewDecoder(strings.NewReader(string(raw)))
	dec.KnownFields(true)
	var d Doc
	if err := dec.Decode(&d); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if d.Review == nil {
		return nil, fmt.Errorf("%s: no recognized document type (want top-level 'review')", path)
	}
	return &d, nil
}

// Compile lowers the document to a layer-1 ops file.
func (d *Doc) Compile() (*opsfile.File, error) {
	ops, err := d.Review.compile()
	if err != nil {
		return nil, err
	}
	return &opsfile.File{Version: 1, Ops: ops}, nil
}

// RepoPath is the repo segment for API endpoints; the placeholders are
// resolved by gh api from the current repo.
func (r *Review) RepoPath() string {
	if r.Repo != "" {
		return r.Repo
	}
	return "{owner}/{repo}"
}

// REST create-review payload: one request carries the summary, event, and
// every inline comment.
type reviewPayload struct {
	Body     string           `json:"body,omitempty"`
	Event    string           `json:"event,omitempty"`
	Comments []commentPayload `json:"comments,omitempty"`
}

type commentPayload struct {
	Path      string `json:"path"`
	Line      int    `json:"line"`
	StartLine int    `json:"start_line,omitempty"`
	Side      string `json:"side,omitempty"`
	Body      string `json:"body"`
}

func (r *Review) compile() ([]opsfile.Op, error) {
	if r.PR <= 0 {
		return nil, fmt.Errorf("review: missing or invalid pr number")
	}
	if r.Draft && r.Event != "" {
		return nil, fmt.Errorf("review: draft cannot set event %q: drop draft to publish", r.Event)
	}
	if !r.hasContent() {
		if r.Draft {
			return nil, fmt.Errorf("review: draft needs a summary or a comment")
		}
		if r.Event == "" {
			return nil, fmt.Errorf("review: needs a summary, a comment, or an event")
		}
	}
	event, err := r.apiEvent()
	if err != nil {
		return nil, err
	}
	if r.ID != 0 {
		return r.compileUpdate()
	}
	for i, c := range r.Comments {
		if c.ID != 0 {
			return nil, fmt.Errorf("review: comments[%d] has an id but the review does not; remove the id or restore the review id", i)
		}
	}
	p := reviewPayload{Body: strings.TrimSpace(r.Summary)}
	for i, c := range r.Comments {
		if c.File == "" || c.Line <= 0 || strings.TrimSpace(c.Body) == "" {
			return nil, fmt.Errorf("review: comments[%d]: file, line, and body are required", i)
		}
		p.Comments = append(p.Comments, commentPayload{
			Path:      c.File,
			Line:      c.Line,
			StartLine: c.StartLine,
			Side:      c.Side,
			Body:      strings.TrimSpace(c.Body),
		})
	}
	if r.SingleStep() {
		p.Event = event
		op, err := r.createOp(p)
		return []opsfile.Op{op}, err
	}
	create, err := r.createOp(p)
	if err != nil {
		return nil, err
	}
	ops := []opsfile.Op{create}
	if event != "" {
		// The review id is unknown until the create returns; submit
		// substitutes it. Compiled output shows the placeholder.
		publish, err := r.publishOp("REVIEW_ID", event)
		if err != nil {
			return nil, err
		}
		ops = append(ops, publish)
	}
	return ops, nil
}

// SingleStep reports whether the review is created in one request: only a
// bodiless approval, which has no content to duplicate. Every other review
// is created in two steps: as a pending review, whose id gh-ff records
// along with its comment ids, then published with the event. Every id is
// in the file before the review is visible, and GitHub's
// one-pending-review-per-user limit makes a repeated create fail instead
// of duplicating. The two-step route also allows bodiless REQUEST_CHANGES
// and COMMENT: create-review requires a body for those events, but a
// pending create has no event, and submit-review's body is optional.
func (r *Review) SingleStep() bool {
	return !r.hasContent() && strings.EqualFold(r.Event, "approve")
}

func (r *Review) hasContent() bool {
	return strings.TrimSpace(r.Summary) != "" || len(r.Comments) > 0
}

func (r *Review) apiEvent() (string, error) {
	if r.Event == "" {
		return "", nil
	}
	ev, ok := events[strings.ToLower(r.Event)]
	if !ok {
		return "", fmt.Errorf("review: event %q (want approve, request_changes, or comment)", r.Event)
	}
	return ev, nil
}

func (r *Review) createOp(p reviewPayload) (opsfile.Op, error) {
	body, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return opsfile.Op{}, err
	}
	return opsfile.Op{
		Cmd:  "api",
		Args: []string{fmt.Sprintf("repos/%s/pulls/%d/reviews", r.RepoPath(), r.PR)},
		Flags: map[string]any{
			"method": "POST",
			"input":  map[string]any{"content": string(body) + "\n"},
		},
	}, nil
}

// PublishOp submits pending review id with the file's event.
func (r *Review) PublishOp(id int64) (opsfile.Op, error) {
	event, err := r.apiEvent()
	if err != nil {
		return opsfile.Op{}, err
	}
	return r.publishOp(strconv.FormatInt(id, 10), event)
}

// publishOp resends the summary, so the published body does not depend on
// whether publishing without one keeps the pending review's body. An empty
// summary is omitted.
func (r *Review) publishOp(id, event string) (opsfile.Op, error) {
	body, err := json.MarshalIndent(reviewPayload{Body: strings.TrimSpace(r.Summary), Event: event}, "", "  ")
	if err != nil {
		return opsfile.Op{}, err
	}
	return opsfile.Op{
		Cmd:  "api",
		Args: []string{fmt.Sprintf("repos/%s/pulls/%d/reviews/%s/events", r.RepoPath(), r.PR, id)},
		Flags: map[string]any{
			"method": "POST",
			"input":  map[string]any{"content": string(body) + "\n"},
		},
	}, nil
}

// GetReviewOp fetches one review, for its state.
func (r *Review) GetReviewOp(id int64) opsfile.Op {
	return opsfile.Op{
		Cmd:   "api",
		Args:  []string{fmt.Sprintf("repos/%s/pulls/%d/reviews/%d", r.RepoPath(), r.PR, id)},
		Flags: map[string]any{"method": "GET"},
	}
}

// compileUpdate lowers a review that was already submitted (id present).
// GitHub permits updating only bodies: the review summary via PUT, each
// comment via PATCH. The verdict (event) of a published review cannot be
// changed; submit publishes with it only if the review is still pending.
// A comment without an id would require a create, so it aborts the compile.
func (r *Review) compileUpdate() ([]opsfile.Op, error) {
	var ops []opsfile.Op
	jsonBody := func(text string) (map[string]any, error) {
		body, err := json.MarshalIndent(map[string]string{"body": text}, "", "  ")
		if err != nil {
			return nil, err
		}
		return map[string]any{"content": string(body) + "\n"}, nil
	}
	if s := strings.TrimSpace(r.Summary); s != "" {
		input, err := jsonBody(s)
		if err != nil {
			return nil, err
		}
		ops = append(ops, opsfile.Op{
			Cmd:   "api",
			Args:  []string{fmt.Sprintf("repos/%s/pulls/%d/reviews/%d", r.RepoPath(), r.PR, r.ID)},
			Flags: map[string]any{"method": "PUT", "input": input},
		})
	}
	for i, c := range r.Comments {
		if c.ID == 0 {
			return nil, fmt.Errorf("review: comments[%d] has no id: updating review %d cannot create comments; if it was submitted with the review, recover its id with gh-ff sync; otherwise remove it or submit it in a new review", i, r.ID)
		}
		body := strings.TrimSpace(c.Body)
		if body == "" {
			return nil, fmt.Errorf("review: comments[%d]: body is required", i)
		}
		input, err := jsonBody(body)
		if err != nil {
			return nil, err
		}
		ops = append(ops, opsfile.Op{
			Cmd:   "api",
			Args:  []string{fmt.Sprintf("repos/%s/pulls/comments/%d", r.RepoPath(), c.ID)},
			Flags: map[string]any{"method": "PATCH", "input": input},
		})
	}
	return ops, nil
}

// ListCommentsOp lists a review's comments, used to pair server ids with
// file comments.
func (r *Review) ListCommentsOp(reviewID int64) opsfile.Op {
	return opsfile.Op{
		Cmd:   "api",
		Args:  []string{fmt.Sprintf("repos/%s/pulls/%d/reviews/%d/comments", r.RepoPath(), r.PR, reviewID)},
		Flags: map[string]any{"method": "GET", "F": "per_page=100"},
	}
}

// ListReviewsOp lists the PR's reviews.
func (r *Review) ListReviewsOp() opsfile.Op {
	return opsfile.Op{
		Cmd:   "api",
		Args:  []string{fmt.Sprintf("repos/%s/pulls/%d/reviews", r.RepoPath(), r.PR)},
		Flags: map[string]any{"method": "GET", "F": "per_page=100"},
	}
}
