package workflow

import (
	"bytes"
	"os"
	"testing"
)

// The repository runs its own end-to-end test through a copy of the template,
// so the two must not drift apart.
func TestRepositoryCopyMatchesTemplate(t *testing.T) {
	got, err := os.ReadFile("../../" + File)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, Template) {
		t.Fatalf("%s differs from internal/workflow/offload.yml; copy the template over it", File)
	}
}

func TestTemplatePassesInputsThroughEnvOnly(t *testing.T) {
	// Expressions inside `run:` scripts would allow script injection.
	inRun := false
	for _, line := range bytes.Split(Template, []byte("\n")) {
		trimmed := bytes.TrimSpace(line)
		switch {
		case bytes.HasPrefix(trimmed, []byte("run: |")):
			inRun = true
		case bytes.HasPrefix(trimmed, []byte("- name:")):
			inRun = false
		}
		if inRun && bytes.Contains(line, []byte("${{")) {
			t.Errorf("expression inside a run script: %s", trimmed)
		}
	}
}
