package spec

import (
	"bytes"
	"fmt"
	"os"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// SubmittedComment is the slice of a review-comment API object needed to
// pair server ids with file comments.
type SubmittedComment struct {
	ID   int64  `json:"id"`
	Path string `json:"path"`
	Body string `json:"body"`
}

// MatchCommentIDs pairs each file comment with a submitted comment by
// (path, trimmed body); duplicates pair in order of appearance.
func MatchCommentIDs(comments []Comment, submitted []SubmittedComment) ([]int64, error) {
	used := make([]bool, len(submitted))
	ids := make([]int64, len(comments))
	for i, c := range comments {
		found := false
		for j, s := range submitted {
			if !used[j] && s.Path == c.File && strings.TrimSpace(s.Body) == strings.TrimSpace(c.Body) {
				ids[i] = s.ID
				used[j] = true
				found = true
				break
			}
		}
		if !found {
			return nil, fmt.Errorf("comments[%d] (%s:%d): no matching submitted comment", i, c.File, c.Line)
		}
	}
	return ids, nil
}

// WriteIDs inserts the submitted review id and per-comment ids into the
// spec file in place. Edits the YAML AST rather than re-marshaling the
// parsed document, so the user's comments and block scalars survive.
func WriteIDs(path string, reviewID int64, commentIDs []int64) error {
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var doc yaml.Node
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		return err
	}
	if len(doc.Content) == 0 {
		return fmt.Errorf("%s: empty document", path)
	}
	review := mapValue(doc.Content[0], "review")
	if review == nil {
		return fmt.Errorf("%s: no review mapping", path)
	}
	upsertID(review, reviewID)
	if comments := mapValue(review, "comments"); comments != nil {
		for i, item := range comments.Content {
			if i < len(commentIDs) && commentIDs[i] != 0 {
				upsertID(item, commentIDs[i])
			}
		}
	}

	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(&doc); err != nil {
		return err
	}
	if err := enc.Close(); err != nil {
		return err
	}
	return os.WriteFile(path, buf.Bytes(), 0o644)
}

// mapValue returns the value node for key in a mapping node, or nil.
func mapValue(mapping *yaml.Node, key string) *yaml.Node {
	if mapping.Kind != yaml.MappingNode {
		return nil
	}
	// mapping.Content alternates key, value.
	for i := 0; i+1 < len(mapping.Content); i += 2 {
		if mapping.Content[i].Value == key {
			return mapping.Content[i+1]
		}
	}
	return nil
}

// upsertID sets id: <n> in a mapping node, inserting it first if absent.
func upsertID(mapping *yaml.Node, id int64) {
	val := strconv.FormatInt(id, 10)
	if existing := mapValue(mapping, "id"); existing != nil {
		existing.SetString(val)
		existing.Tag = "!!int"
		return
	}
	key := &yaml.Node{Kind: yaml.ScalarNode, Value: "id"}
	value := &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!int", Value: val}
	// Move any head comment (e.g. a list item's leading comment) to the
	// new first key so it stays above the entry.
	if len(mapping.Content) > 0 {
		key.HeadComment = mapping.Content[0].HeadComment
		mapping.Content[0].HeadComment = ""
	}
	mapping.Content = append([]*yaml.Node{key, value}, mapping.Content...)
}
