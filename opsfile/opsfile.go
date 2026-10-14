// Package opsfile implements layer 1: a YAML file whose entries map
// parameters onto gh CLI invocations. Any gh operation is expressible,
// including arbitrary REST/GraphQL calls via the "api" subcommand.
package opsfile

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

type File struct {
	Version int  `yaml:"version"`
	Ops     []Op `yaml:"ops"`
}

type Op struct {
	// Cmd is the gh subcommand path, space-separated: "pr review", "api".
	Cmd  string   `yaml:"cmd"`
	Args []string `yaml:"args,omitempty"`
	// Flags maps flag names (without dashes) to values:
	//   string/int/bool    -> --name=value
	//   list               -> --name=v1 --name=v2
	//   null               -> --name (bare)
	//   {content: "..."}   -> --name=<path of a file materialized from content>
	// The content form keeps specs self-contained where gh expects a file
	// path (body-file, input, ...).
	Flags map[string]any `yaml:"flags,omitempty"`
}

// Materialize writes every {content: ...} flag value to a file under dir
// and replaces the value with the file path. prefix distinguishes ops
// sharing dir.
func (op *Op) Materialize(dir, prefix string) error {
	for name, v := range op.Flags {
		m, ok := v.(map[string]any)
		if !ok {
			continue
		}
		content, ok := m["content"].(string)
		if !ok || len(m) != 1 {
			return fmt.Errorf("flag %q: mapping value must be exactly {content: <string>}", name)
		}
		path := filepath.Join(dir, prefix+name)
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			return err
		}
		op.Flags[name] = path
	}
	return nil
}

func Load(path string) (*File, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	dec := yaml.NewDecoder(strings.NewReader(string(raw)))
	dec.KnownFields(true)
	var f File
	if err := dec.Decode(&f); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if f.Version != 1 {
		return nil, fmt.Errorf("%s: unsupported version %d (want 1)", path, f.Version)
	}
	for i, op := range f.Ops {
		if strings.TrimSpace(op.Cmd) == "" {
			return nil, fmt.Errorf("%s: ops[%d]: missing cmd", path, i)
		}
	}
	return &f, nil
}

// Argv returns the gh argument vector for the op (without the leading "gh").
// Flags are emitted in sorted name order so output is deterministic.
func (op *Op) Argv() ([]string, error) {
	argv := strings.Fields(op.Cmd)
	argv = append(argv, op.Args...)

	names := make([]string, 0, len(op.Flags))
	for name := range op.Flags {
		names = append(names, name)
	}
	sort.Strings(names)

	for _, name := range names {
		dash := "--"
		if len(name) == 1 {
			dash = "-"
		}
		switch v := op.Flags[name].(type) {
		case nil:
			argv = append(argv, dash+name)
		case []any:
			for _, item := range v {
				s, err := scalar(item)
				if err != nil {
					return nil, fmt.Errorf("flag %q: %w", name, err)
				}
				argv = append(argv, dash+name+"="+s)
			}
		default:
			s, err := scalar(v)
			if err != nil {
				return nil, fmt.Errorf("flag %q: %w", name, err)
			}
			argv = append(argv, dash+name+"="+s)
		}
	}
	return argv, nil
}

func scalar(v any) (string, error) {
	switch v := v.(type) {
	case string:
		return v, nil
	case bool:
		return fmt.Sprintf("%t", v), nil
	case int, int64, uint64, float64:
		return fmt.Sprintf("%v", v), nil
	default:
		return "", fmt.Errorf("unsupported value type %T", v)
	}
}
