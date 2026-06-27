package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRootCmdListLayers(t *testing.T) {
	repo := t.TempDir()
	writeFile(t, repo, "charts/apps/backend/values.yaml")
	writeFile(t, repo, "envs/project/dev/apps/values.yaml")

	cmd := newRootCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"project/dev/apps", "backend", "--layers", "--repo", repo})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !strings.Contains(out.String(), "chart defaults") {
		t.Errorf("expected resolved layers, got:\n%s", out.String())
	}
}

func TestRootCmdArgValidation(t *testing.T) {
	cmd := newRootCmd()
	cmd.SetOut(new(bytes.Buffer))
	cmd.SetErr(new(bytes.Buffer))
	cmd.SetArgs([]string{"only-one-arg"})

	if err := cmd.Execute(); err == nil {
		t.Fatal("expected an error for too few args")
	}
}

func writeFile(t *testing.T, root, rel string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatalf("write %s: %v", rel, err)
	}
}
