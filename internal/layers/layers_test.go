package layers

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/go-cmp/cmp"
)

func TestParseTarget(t *testing.T) {
	tests := []struct {
		name    string
		give    string
		want    Target
		wantErr bool
	}{
		{
			name: "valid",
			give: "project/dev/apps",
			want: Target{Tenant: "project", Env: "dev", Cluster: "apps"},
		},
		{name: "too few segments", give: "project/dev", wantErr: true},
		{name: "too many segments", give: "a/b/c/d", wantErr: true},
		{name: "empty segment", give: "project//apps", wantErr: true},
		{name: "dot dot traversal", give: "project/../apps", wantErr: true},
		{name: "empty string", give: "", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseTarget(tt.give)
			if tt.wantErr {
				if !errors.Is(err, ErrInvalidTarget) {
					t.Fatalf("want ErrInvalidTarget, got %v", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Errorf("target mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestResolve(t *testing.T) {
	target := Target{Tenant: "project", Env: "dev", Cluster: "apps"}

	tests := []struct {
		name      string
		give      []string // files to create under the fixture repo
		chart     string
		wantKinds []Kind
		wantErr   error
	}{
		{
			name: "full chain in merge order",
			give: []string{
				"charts/apps/backend/values.yaml",
				"envs/_platform/values.yaml",
				"envs/project/values.yaml",
				"envs/project/dev/values.yaml",
				"envs/project/dev/apps/values.yaml",
				"envs/project/dev/apps/enabled/backend.yaml",
				"envs/project/dev/apps/versions.generated.yaml",
			},
			chart: "backend",
			wantKinds: []Kind{
				KindChartDefaults, KindPlatform, KindTenant, KindEnv,
				KindCluster, KindContract, KindVersions,
			},
		},
		{
			name: "absent enabled file skips contract layer",
			give: []string{
				"charts/apps/backend/values.yaml",
				"envs/project/dev/apps/values.yaml",
				// no enabled/backend.yaml -- contract layer is skipped
			},
			chart:     "backend",
			wantKinds: []Kind{KindChartDefaults, KindCluster},
		},
		{
			name: "delta-only layers skip absent files",
			give: []string{
				"charts/apps/backend/values.yaml",
				"envs/_platform/values.yaml",
				"envs/project/dev/apps/versions.generated.yaml",
			},
			chart:     "backend",
			wantKinds: []Kind{KindChartDefaults, KindPlatform, KindVersions},
		},
		{
			name: "category base",
			give: []string{
				"charts/base/nats/values.yaml",
				"envs/project/dev/apps/values.yaml",
			},
			chart:     "nats",
			wantKinds: []Kind{KindChartDefaults, KindCluster},
		},
		{
			name: "category vendor",
			give: []string{
				"charts/vendor/some-app/values.yaml",
				"envs/project/dev/apps/enabled/some-app.yaml",
			},
			chart:     "some-app",
			wantKinds: []Kind{KindChartDefaults, KindContract},
		},
		{
			name:    "chart not found",
			give:    []string{"envs/project/dev/apps/values.yaml"},
			chart:   "ghost",
			wantErr: ErrChartNotFound,
		},
		{
			name:    "no value files at all",
			give:    []string{"charts/apps/lonely/Chart.yaml"},
			chart:   "lonely",
			wantErr: ErrNoLayers,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := newFixture(t, tt.give)
			got, err := Resolve(root, target, tt.chart)
			if tt.wantErr != nil {
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("want %v, got %v", tt.wantErr, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			want := wantLayers(root, target, tt.chart, tt.wantKinds)
			if diff := cmp.Diff(want, got); diff != "" {
				t.Errorf("layers mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestResolveCategoryPrecedence(t *testing.T) {
	// A chart present in multiple categories resolves to the first match in
	// {base, apps, vendor}.
	root := newFixture(t, []string{
		"charts/apps/dup/values.yaml",
		"charts/base/dup/values.yaml",
		"envs/project/dev/apps/values.yaml",
	})
	target := Target{Tenant: "project", Env: "dev", Cluster: "apps"}

	got, err := Resolve(root, target, "dup")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	wantPath := filepath.Join(root, "charts", "base", "dup", "values.yaml")
	if got[0].Kind != KindChartDefaults || got[0].Path != wantPath {
		t.Errorf("chart defaults: want base path %q, got %+v", wantPath, got[0])
	}
}

func TestFindRepoRoot(t *testing.T) {
	root := newFixture(t, []string{
		"charts/apps/backend/values.yaml",
		"envs/project/dev/apps/values.yaml",
	})
	nested := filepath.Join(root, "envs", "project", "dev", "apps")

	got, err := FindRepoRoot(nested)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != root {
		t.Errorf("repo root: want %q, got %q", root, got)
	}

	if _, err := FindRepoRoot(t.TempDir()); !errors.Is(err, ErrRepoNotFound) {
		t.Errorf("want ErrRepoNotFound, got %v", err)
	}
}

func TestCheckRepoRoot(t *testing.T) {
	root := newFixture(t, []string{
		"charts/apps/backend/values.yaml",
		"envs/project/dev/apps/values.yaml",
	})
	if err := CheckRepoRoot(root); err != nil {
		t.Errorf("valid repo root: unexpected error %v", err)
	}

	// A path missing charts/ and envs/ (here: never created) must be rejected.
	missing := filepath.Join(t.TempDir(), "nope")
	if err := CheckRepoRoot(missing); !errors.Is(err, ErrRepoNotFound) {
		t.Errorf("want ErrRepoNotFound, got %v", err)
	}

	// A directory with charts/ but no envs/ is not a repo root.
	half := newFixture(t, []string{"charts/apps/backend/values.yaml"})
	if err := CheckRepoRoot(half); !errors.Is(err, ErrRepoNotFound) {
		t.Errorf("charts/ only: want ErrRepoNotFound, got %v", err)
	}
}

func FuzzParseTarget(f *testing.F) {
	for _, seed := range []string{"project/dev/apps", "a/b", "", "x//y", "a/../b"} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, s string) {
		got, err := ParseTarget(s)
		if err != nil {
			return
		}
		// A parsed target must round-trip through String().
		again, err := ParseTarget(got.String())
		if err != nil {
			t.Fatalf("round-trip failed for %q: %v", s, err)
		}
		if again != got {
			t.Fatalf("round-trip mismatch: %+v != %+v", again, got)
		}
	})
}

// newFixture creates a temp repo containing the given files (empty) and returns
// its root.
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

// wantLayers builds the expected layers for the given kinds, deriving each
// path from the fixture root the same way Resolve does.
func wantLayers(root string, target Target, chart string, kinds []Kind) []Layer {
	category := map[string]string{
		"backend": "apps", "nats": "base", "some-app": "vendor",
	}[chart]
	clusterDir := filepath.Join(root, "envs", target.Tenant, target.Env, target.Cluster)
	pathByKind := map[Kind]string{
		KindChartDefaults: filepath.Join(root, "charts", category, chart, "values.yaml"),
		KindPlatform:      filepath.Join(root, "envs", "_platform", "values.yaml"),
		KindTenant:        filepath.Join(root, "envs", target.Tenant, "values.yaml"),
		KindEnv:           filepath.Join(root, "envs", target.Tenant, target.Env, "values.yaml"),
		KindCluster:       filepath.Join(clusterDir, "values.yaml"),
		KindContract:      filepath.Join(clusterDir, "enabled", chart+".yaml"),
		KindVersions:      filepath.Join(clusterDir, "versions.generated.yaml"),
	}
	want := make([]Layer, 0, len(kinds))
	for _, k := range kinds {
		want = append(want, Layer{Kind: k, Path: pathByKind[k]})
	}
	return want
}
