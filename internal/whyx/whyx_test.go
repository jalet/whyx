package whyx

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jalet/whyx/internal/layers"
	"github.com/jalet/whyx/internal/render"
)

func TestRunListLayers(t *testing.T) {
	repo := newFixture(t, []string{
		"charts/apps/backend/values.yaml",
		"envs/_platform/values.yaml",
		"envs/project/values.yaml",
		"envs/project/dev/apps/values.yaml",
		"envs/project/dev/apps/enabled/backend.yaml",
		"envs/project/dev/apps/versions.generated.yaml",
	})

	var out bytes.Buffer
	cfg := Config{Target: "project/dev/apps", Chart: "backend", RepoRoot: repo, ListLayers: true}
	if err := Run(t.Context(), cfg, &out); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	got := out.String()
	// Canonical indices, absent layer 4 skipped, in merge order.
	wantOrder := []string{
		"1  chart defaults",
		"2  platform-wide",
		"3  tenant-wide",
		"5  cluster",
		"6  infra contract",
		"7  promoted versions",
	}
	lines := strings.Split(strings.TrimSpace(got), "\n")
	if len(lines) != len(wantOrder) {
		t.Fatalf("want %d lines, got %d:\n%s", len(wantOrder), len(lines), got)
	}
	for i, want := range wantOrder {
		if !strings.HasPrefix(lines[i], want) {
			t.Errorf("line %d: want prefix %q, got %q", i, want, lines[i])
		}
	}
	if strings.Contains(got, "environment-wide") {
		t.Errorf("absent env layer should not appear:\n%s", got)
	}
}

func TestRunErrors(t *testing.T) {
	repo := newFixture(t, []string{
		"charts/apps/backend/values.yaml",
		"envs/project/dev/apps/values.yaml",
	})

	tests := []struct {
		name    string
		cfg     Config
		wantErr error
	}{
		{
			name:    "invalid target",
			cfg:     Config{Target: "project/dev", Chart: "backend", RepoRoot: repo, ListLayers: true},
			wantErr: layers.ErrInvalidTarget,
		},
		{
			name:    "chart not found",
			cfg:     Config{Target: "project/dev/apps", Chart: "ghost", RepoRoot: repo, ListLayers: true},
			wantErr: layers.ErrChartNotFound,
		},
		{
			name:    "no value files",
			cfg:     Config{Target: "project/dev/apps", Chart: "backend", RepoRoot: t.TempDir(), ListLayers: true},
			wantErr: layers.ErrChartNotFound, // empty repo: chart dir missing
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var out bytes.Buffer
			err := Run(t.Context(), tt.cfg, &out)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("want %v, got %v", tt.wantErr, err)
			}
		})
	}
}

func TestRunInvalidFormat(t *testing.T) {
	repo := newContentFixture(t, map[string]string{
		"charts/apps/backend/values.yaml":   "a: 1\n",
		"envs/project/dev/apps/values.yaml": "b: 2\n",
	})
	cfg := Config{Target: "project/dev/apps", Chart: "backend", RepoRoot: repo, Format: "xml"}
	var out bytes.Buffer
	if err := Run(t.Context(), cfg, &out); !errors.Is(err, render.ErrUnknownFormat) {
		t.Fatalf("want ErrUnknownFormat, got %v", err)
	}
}

func TestRunCancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	cfg := Config{Target: "project/dev/apps", Chart: "backend", RepoRoot: t.TempDir()}
	if err := Run(ctx, cfg, nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("want context.Canceled, got %v", err)
	}
}

func TestWriteLayers(t *testing.T) {
	resolved := []layers.Layer{
		{Kind: layers.KindChartDefaults, Path: "/repo/charts/apps/backend/values.yaml"},
		{Kind: layers.KindContract, Path: "/repo/envs/c/e/cl/enabled/backend.yaml"},
	}
	var out bytes.Buffer
	if err := writeLayers(&out, resolved); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := "1  chart defaults    chart author     /repo/charts/apps/backend/values.yaml\n" +
		"6  infra contract    Pulumi (machine) /repo/envs/c/e/cl/enabled/backend.yaml\n"
	if out.String() != want {
		t.Errorf("output mismatch:\nwant:\n%q\ngot:\n%q", want, out.String())
	}
}

func TestRunCascade(t *testing.T) {
	repo := newContentFixture(t, map[string]string{
		"charts/apps/backend/values.yaml":               "replicas: 1\nimage:\n  tag: dev\n",
		"envs/project/dev/apps/values.yaml":             "replicas: 2\n",
		"envs/project/dev/apps/versions.generated.yaml": "image:\n  tag: prod\n",
	})
	var out bytes.Buffer
	cfg := Config{Target: "project/dev/apps", Chart: "backend", RepoRoot: repo}
	if err := Run(t.Context(), cfg, &out); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got := out.String()
	for _, want := range []string{
		"@@ layer 1", "+ image.tag: dev", "+ replicas: 1",
		"@@ layer 5", "~ replicas: 1 -> 2",
		"@@ layer 7", "~ image.tag: dev -> prod",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in cascade:\n%s", want, got)
		}
	}
}

func TestRunCascadeFocused(t *testing.T) {
	repo := newContentFixture(t, map[string]string{
		"charts/apps/backend/values.yaml":               "replicas: 1\nimage:\n  tag: dev\n",
		"envs/project/dev/apps/values.yaml":             "replicas: 2\n",
		"envs/project/dev/apps/versions.generated.yaml": "image:\n  tag: prod\n",
	})
	var out bytes.Buffer
	cfg := Config{Target: "project/dev/apps", Chart: "backend", RepoRoot: repo, Key: "image.tag"}
	if err := Run(t.Context(), cfg, &out); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got := out.String()
	if strings.Contains(got, "replicas") {
		t.Errorf("focused image.tag should not mention replicas:\n%s", got)
	}
	if strings.Contains(got, "layer 5") {
		t.Errorf("focused mode should skip layer 5 (only touches replicas):\n%s", got)
	}
	if !strings.Contains(got, "~ image.tag: dev -> prod") {
		t.Errorf("expected image.tag lineage:\n%s", got)
	}
}

func TestRunNoHelmValueLayers(t *testing.T) {
	// A raw-manifest (type: path) chart: no chart defaults, only empty delta
	// files and an empty contract projection. The cascade sets nothing, so whyx
	// prints the friendly no-layers message and exits 0 -- not an error.
	repo := newContentFixture(t, map[string]string{
		"charts/apps/echoserver/Chart.yaml":             "name: echoserver\n",
		"envs/project/dev/apps/values.yaml":             "{}\n",
		"envs/project/dev/apps/enabled/echoserver.yaml": "type: path\nhelmParameters: []\n",
	})
	var out bytes.Buffer
	cfg := Config{Target: "project/dev/apps", Chart: "echoserver", RepoRoot: repo}
	if err := Run(t.Context(), cfg, &out); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got := strings.TrimSpace(out.String())
	want := "(no helm value layers -- raw-manifest chart)"
	if got != want {
		t.Errorf("want friendly message %q, got:\n%s", want, got)
	}
}

func newContentFixture(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for rel, body := range files {
		path := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
	}
	return root
}

func newFixture(t *testing.T, files []string) string {
	t.Helper()
	root := t.TempDir()
	for _, rel := range files {
		path := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.WriteFile(path, nil, 0o600); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
	}
	return root
}
