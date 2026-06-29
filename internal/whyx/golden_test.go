package whyx

import (
	"bytes"
	"flag"
	"os"
	"path/filepath"
	"testing"
)

var update = flag.Bool("update", false, "update golden files")

// TestGoldenCascade renders a fixed multi-layer fixture and compares the full
// cascade against a committed golden file. Regenerate with `go test -update`.
func TestGoldenCascade(t *testing.T) {
	repo := newContentFixture(t, map[string]string{
		"charts/apps/backend/values.yaml": "replicas: 1\nimage:\n  repo: app\n  tag: dev\n",
		"envs/_platform/values.yaml":      "common: true\n",
		// The cluster layer sets image.registry from a contract template; the
		// templated leaf is stripped from layer 5 and surfaces (resolved) at the
		// computed infra-contract layer (layer 6), using platform.generated.yaml.
		"envs/project/dev/apps/values.yaml":             "replicas: 2\nimage:\n  registry: \"{{ .registry }}\"\n",
		"envs/project/dev/apps/platform.generated.yaml": "registry: ecr.example\n",
		"envs/project/dev/apps/versions/backend.yaml":   "image:\n  tag: prod\n",
	})

	var out bytes.Buffer
	cfg := Config{Target: "project/dev/apps", Chart: "backend", RepoRoot: repo}
	if err := Run(t.Context(), cfg, &out); err != nil {
		t.Fatalf("run: %v", err)
	}

	golden := filepath.Join("testdata", "cascade.golden")
	if *update {
		if err := os.MkdirAll("testdata", 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(golden, out.Bytes(), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(golden) //nolint:gosec // fixed testdata path
	if err != nil {
		t.Fatalf("read golden (run `go test -update` to create it): %v", err)
	}
	if out.String() != string(want) {
		t.Errorf("cascade != golden\n--- got ---\n%s\n--- want ---\n%s", out.String(), want)
	}
}
