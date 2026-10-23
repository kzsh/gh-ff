package opsfile

import (
	"fmt"
	"regexp"
	"strings"
)

// readOnlyVerbs are the trailing gh subcommand words that only read.
// Anything else is assumed to mutate, so an unrecognized or newly added
// gh command fails closed under --read-only.
var readOnlyVerbs = map[string]bool{
	"view":    true,
	"list":    true,
	"ls":      true,
	"status":  true,
	"diff":    true,
	"checks":  true,
	"search":  true,
	"browse":  true,
	"version": true,
	"help":    true,
}

// readOnlyMethods are the HTTP methods that do not mutate.
var readOnlyMethods = map[string]bool{"GET": true, "HEAD": true, "OPTIONS": true}

// fieldFlags are the gh api flags that supply a request body; gh switches
// the default method from GET to POST when any of them is present.
var fieldFlags = map[string]bool{
	"f": true, "F": true, "field": true, "raw-field": true, "input": true,
}

// graphqlMutation matches a top-level GraphQL mutation operation, which
// may be preceded by a name or variable list on the same keyword.
var graphqlMutation = regexp.MustCompile(`(?m)^\s*mutation\b`)

// MutationReason reports why the op may mutate server state, or "" if it
// is read-only. The analysis is static and conservative: it errs toward
// calling an op a mutation.
func (op *Op) MutationReason() string {
	fields := strings.Fields(op.Cmd)
	if len(fields) == 0 {
		return "missing cmd"
	}
	if fields[0] != "api" {
		verb := fields[len(fields)-1]
		if readOnlyVerbs[verb] {
			return ""
		}
		return fmt.Sprintf("%q is not a known read-only gh command", op.Cmd)
	}

	method, explicit := op.methodFlag()
	for _, arg := range op.Args {
		if strings.EqualFold(arg, "graphql") {
			return op.graphqlReason()
		}
	}
	if explicit {
		if readOnlyMethods[method] {
			return ""
		}
		return fmt.Sprintf("api request uses method %s", method)
	}
	for name := range op.Flags {
		if fieldFlags[name] {
			return fmt.Sprintf("api request has no method but sends a body (--%s), which gh issues as POST", name)
		}
	}
	return ""
}

// methodFlag returns the explicit HTTP method from -X/--method, if any.
func (op *Op) methodFlag() (string, bool) {
	for _, name := range []string{"method", "X"} {
		if v, ok := op.Flags[name]; ok {
			if s, err := scalar(v); err == nil {
				return strings.ToUpper(strings.TrimSpace(s)), true
			}
			return "", true
		}
	}
	return "", false
}

// graphqlReason classifies a `gh api graphql` call by its query text.
// GraphQL is always POSTed, so the method says nothing; the document does.
func (op *Op) graphqlReason() string {
	var sawQuery bool
	for name, v := range op.Flags {
		if !fieldFlags[name] {
			continue
		}
		for _, s := range flagStrings(v) {
			if !strings.Contains(s, "{") && !strings.Contains(s, "mutation") {
				continue // a plain variable binding, not the document
			}
			sawQuery = true
			if graphqlMutation.MatchString(trimQueryPrefix(s)) {
				return "graphql document contains a mutation"
			}
		}
	}
	if !sawQuery {
		return "graphql request with no readable query document"
	}
	return ""
}

// trimQueryPrefix strips a leading `query=` field name so the document
// itself is what gets matched.
func trimQueryPrefix(s string) string {
	if _, doc, ok := strings.Cut(s, "="); ok {
		return doc
	}
	return s
}

// flagStrings yields the string values a flag carries, including the
// {content: ...} form and lists.
func flagStrings(v any) []string {
	switch v := v.(type) {
	case string:
		return []string{v}
	case []any:
		var out []string
		for _, item := range v {
			out = append(out, flagStrings(item)...)
		}
		return out
	case map[string]any:
		if s, ok := v["content"].(string); ok {
			return []string{s}
		}
	}
	return nil
}

// AuditReadOnly returns an error naming every op that may mutate.
func (f *File) AuditReadOnly() error {
	var bad []string
	for i, op := range f.Ops {
		if reason := op.MutationReason(); reason != "" {
			bad = append(bad, fmt.Sprintf("  ops[%d] (cmd %q): %s", i, op.Cmd, reason))
		}
	}
	if len(bad) == 0 {
		return nil
	}
	return fmt.Errorf("--read-only: aborting, %d of %d ops would mutate:\n%s",
		len(bad), len(f.Ops), strings.Join(bad, "\n"))
}
