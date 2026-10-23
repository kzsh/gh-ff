package opsfile

import (
	"strings"
	"testing"
)

func TestMutationReason(t *testing.T) {
	cases := []struct {
		name    string
		op      Op
		mutates bool
	}{
		{"pr view", Op{Cmd: "pr view", Args: []string{"1"}}, false},
		{"pr review", Op{Cmd: "pr review", Args: []string{"1"}}, true},
		{"api default get", Op{Cmd: "api", Args: []string{"repos/o/r/pulls/1"}}, false},
		{"api explicit get", Op{Cmd: "api", Flags: map[string]any{"X": "get"}}, false},
		{"api post", Op{Cmd: "api", Flags: map[string]any{"method": "POST"}}, true},
		{"api implicit post", Op{Cmd: "api", Flags: map[string]any{"f": []any{"body=hi"}}}, true},
		{"api input body", Op{Cmd: "api", Flags: map[string]any{"input": map[string]any{"content": "{}"}}}, true},
		{"graphql query", Op{
			Cmd:   "api",
			Args:  []string{"graphql"},
			Flags: map[string]any{"f": []any{"query=query { viewer { login } }"}},
		}, false},
		{"graphql mutation", Op{
			Cmd:   "api",
			Args:  []string{"graphql"},
			Flags: map[string]any{"f": []any{"query=mutation { addComment(input: {}) { clientMutationId } }"}},
		}, true},
		{"graphql opaque", Op{Cmd: "api", Args: []string{"graphql"}}, true},
	}
	for _, c := range cases {
		reason := c.op.MutationReason()
		if (reason != "") != c.mutates {
			t.Errorf("%s: mutates=%v, reason=%q", c.name, reason != "", reason)
		}
	}
}

func TestAuditReadOnly(t *testing.T) {
	f := File{Ops: []Op{
		{Cmd: "pr view", Args: []string{"1"}},
		{Cmd: "pr review", Args: []string{"1"}},
	}}
	err := f.AuditReadOnly()
	if err == nil {
		t.Fatal("expected an error")
	}
	if !strings.Contains(err.Error(), "ops[1]") || strings.Contains(err.Error(), "ops[0]") {
		t.Errorf("error should name only ops[1]: %v", err)
	}
	if err := (&File{Ops: []Op{{Cmd: "pr list"}}}).AuditReadOnly(); err != nil {
		t.Errorf("read-only file rejected: %v", err)
	}
}
