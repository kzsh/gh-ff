package main

import (
	"os"
	"path/filepath"
	"testing"
)

// Every template must parse, validate, and compile as emitted.
func TestTemplates(t *testing.T) {
	subFor := map[string]string{"review": "submit", "ops": "run"}
	for name, tmpl := range templates {
		path := filepath.Join(t.TempDir(), name+".yaml")
		if err := os.WriteFile(path, []byte(tmpl), 0o644); err != nil {
			t.Fatal(err)
		}
		f, err := loadFor(subFor[name], path)
		if err != nil {
			t.Errorf("template %q does not load: %v", name, err)
			continue
		}
		for i, op := range f.Ops {
			if err := op.Materialize(t.TempDir(), "t-"); err != nil {
				t.Errorf("template %q ops[%d]: %v", name, i, err)
			}
			if _, err := op.Argv(); err != nil {
				t.Errorf("template %q ops[%d]: %v", name, i, err)
			}
		}
	}
}
