package merge

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/jalet/whyx/internal/layers"
)

func TestReadValues(t *testing.T) {
	tests := []struct {
		name    string
		give    string
		want    Values
		wantErr bool
	}{
		{
			name: "scalars and nesting",
			give: "replicas: 2\nimage:\n  tag: v1\n",
			want: Values{"replicas": float64(2), "image": Values{"tag": "v1"}},
		},
		{name: "empty file", give: "", want: Values{}},
		{name: "comment only", give: "# nothing here\n", want: Values{}},
		{name: "invalid yaml", give: "a: : :\n", wantErr: true},
		{name: "non-map root", give: "- a\n- b\n", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := writeFile(t, "values.yaml", tt.give)
			got, err := ReadValues(path)
			if tt.wantErr {
				if err == nil {
					t.Fatal("want error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Errorf("mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestReadValuesMissingFile(t *testing.T) {
	if _, err := ReadValues(filepath.Join(t.TempDir(), "nope.yaml")); err == nil {
		t.Fatal("want error for missing file, got nil")
	}
}

func TestCascade(t *testing.T) {
	// Three layers exercising: scalar override, deep map merge, list replace,
	// and a key introduced only by a later layer.
	l1 := layer(t, KindFor(1), "replicas: 1\nimage:\n  repo: app\n  tag: dev\nports:\n  - 80\n")
	l2 := layer(t, KindFor(2), "replicas: 2\nimage:\n  tag: stage\nextra: true\n")
	l3 := layer(t, KindFor(3), "ports:\n  - 8080\n  - 8443\nimage:\n  tag: prod\n")

	steps, err := Cascade([]layers.Layer{l1, l2, l3}, Values{}, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(steps) != 3 {
		t.Fatalf("want 3 steps, got %d", len(steps))
	}

	// After layer 2: replicas overridden, image.tag overridden, image.repo kept,
	// extra added, ports unchanged.
	want2 := Values{
		"replicas": float64(2),
		"image":    Values{"repo": "app", "tag": "stage"},
		"ports":    []any{float64(80)},
		"extra":    true,
	}
	if diff := cmp.Diff(want2, steps[1].Values); diff != "" {
		t.Errorf("step 2 mismatch (-want +got):\n%s", diff)
	}

	// After layer 3: list replaced wholesale, image.tag overridden again.
	want3 := Values{
		"replicas": float64(2),
		"image":    Values{"repo": "app", "tag": "prod"},
		"ports":    []any{float64(8080), float64(8443)},
		"extra":    true,
	}
	if diff := cmp.Diff(want3, steps[2].Values); diff != "" {
		t.Errorf("step 3 mismatch (-want +got):\n%s", diff)
	}
}

func TestCascadeSnapshotsAreStable(t *testing.T) {
	// Regression guard: merging a later layer must not mutate earlier snapshots,
	// even though they may share substructure.
	l1 := layer(t, KindFor(1), "image:\n  repo: app\n  tag: dev\n")
	l2 := layer(t, KindFor(2), "image:\n  tag: prod\n  extra: x\n")

	steps, err := Cascade([]layers.Layer{l1, l2}, Values{}, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want1 := Values{"image": Values{"repo": "app", "tag": "dev"}}
	if diff := cmp.Diff(want1, steps[0].Values); diff != "" {
		t.Errorf("step 1 was mutated by a later layer (-want +got):\n%s", diff)
	}
}

func TestCascadeNullOverride(t *testing.T) {
	// A later layer setting a key to null wins (value becomes nil).
	l1 := layer(t, KindFor(1), "feature: enabled\n")
	l2 := layer(t, KindFor(2), "feature: null\n")
	steps, err := Cascade([]layers.Layer{l1, l2}, Values{}, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	v, ok := steps[1].Values["feature"]
	if !ok {
		t.Fatal("key 'feature' should be present")
	}
	if v != nil {
		t.Errorf("want nil, got %v", v)
	}
}

func TestCascadeContractResolvesTemplates(t *testing.T) {
	// The infra-contract layer is COMPUTED: it resolves the {{ }} refs in the
	// human layers against the context and contributes only those resolved keys.
	// The human layer's templated leaf is stripped (step 0); the resolved value
	// appears at the contract layer (step 1).
	human := layer(t, KindFor(5), "replicas: 2\nbackup:\n  target: \"s3://{{ .buckets.x }}@{{ .global.region }}/\"\n")
	ctx := Values{"buckets": Values{"x": "bkt"}, "global": Values{"region": "eu-north-1"}}

	steps, err := Cascade([]layers.Layer{human, contractLayer(t)}, ctx, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if diff := cmp.Diff(Values{"replicas": float64(2)}, steps[0].Values); diff != "" {
		t.Errorf("human layer should strip template (-want +got):\n%s", diff)
	}
	want := Values{"replicas": float64(2), "backup": Values{"target": "s3://bkt@eu-north-1/"}}
	if diff := cmp.Diff(want, steps[1].Values); diff != "" {
		t.Errorf("contract layer should add resolved value (-want +got):\n%s", diff)
	}
}

func TestCascadeContractFiltersToChart(t *testing.T) {
	// Env-level values.yaml files namespace values under the chart name. The
	// contract layer must only surface resolved templates for the queried chart,
	// not for other charts that share the same values file.
	body := "longhorn:\n  backup:\n    target: \"s3://{{ .buckets.longhorn }}/\"\nother-chart:\n  setting: \"{{ .other }}\"\n"
	human := layer(t, KindFor(5), body)
	ctx := Values{"buckets": Values{"longhorn": "bkt"}, "other": "val"}

	steps, err := Cascade([]layers.Layer{human, contractLayer(t)}, ctx, "longhorn")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := Values{
		"longhorn": Values{
			"backup": Values{"target": "s3://bkt/"},
		},
	}
	if diff := cmp.Diff(want, steps[1].Values); diff != "" {
		t.Errorf("contract should only include queried chart (-want +got):\n%s", diff)
	}
	if _, ok := steps[1].Values["other-chart"]; ok {
		t.Error("contract must not include other-chart keys")
	}
}

func TestCascadeContractEmpty(t *testing.T) {
	// No templated refs in the human layers -> the contract layer contributes
	// nothing (and an unresolved ref is left intact when ctx lacks the key).
	t.Run("no templates", func(t *testing.T) {
		human := layer(t, KindFor(5), "replicas: 2\n")
		steps, err := Cascade([]layers.Layer{human, contractLayer(t)}, Values{}, "")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if diff := cmp.Diff(Values{"replicas": float64(2)}, steps[1].Values); diff != "" {
			t.Errorf("contract should contribute nothing (-want +got):\n%s", diff)
		}
	})
	t.Run("unresolved ref left intact", func(t *testing.T) {
		human := layer(t, KindFor(5), "target: \"{{ .missing.key }}\"\n")
		steps, err := Cascade([]layers.Layer{human, contractLayer(t)}, Values{}, "")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if diff := cmp.Diff(Values{"target": "{{ .missing.key }}"}, steps[1].Values); diff != "" {
			t.Errorf("unresolved ref should stay intact (-want +got):\n%s", diff)
		}
	})
}

func TestCascadeStripsTemplatedHumanLayers(t *testing.T) {
	// A human layer that sets a key to a contract template has that leaf stripped
	// (and any map left empty pruned), so the unresolved placeholder never shows
	// at the human layer.
	human := layer(t, KindFor(5), "image:\n  tag: v1\nbackup:\n  target: \"s3://{{ .buckets.x }}/\"\n")
	steps, err := Cascade([]layers.Layer{human}, Values{}, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := Values{"image": Values{"tag": "v1"}}
	if diff := cmp.Diff(want, steps[0].Values); diff != "" {
		t.Errorf("templated leaf should be stripped (-want +got):\n%s", diff)
	}
}

func TestBuildContext(t *testing.T) {
	// Later files win: the contract (platform.generated.yaml) overlays globals.
	globals := writeFile(t, "globals.yaml", "global:\n  region: old\nextra: keep\n")
	contract := writeFile(t, "platform.generated.yaml", "global:\n  region: eu-north-1\n")
	ctx, err := BuildContext([]string{globals, contract})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := Values{"global": Values{"region": "eu-north-1"}, "extra": "keep"}
	if diff := cmp.Diff(want, ctx); diff != "" {
		t.Errorf("context mismatch (-want +got):\n%s", diff)
	}
}

func TestEffective(t *testing.T) {
	if diff := cmp.Diff(Values{}, Effective(nil)); diff != "" {
		t.Errorf("empty steps: %s", diff)
	}
	steps := []Step{{Values: Values{"a": float64(1)}}, {Values: Values{"a": float64(2)}}}
	if diff := cmp.Diff(Values{"a": float64(2)}, Effective(steps)); diff != "" {
		t.Errorf("effective mismatch: %s", diff)
	}
}

func FuzzParseValues(f *testing.F) {
	for _, seed := range []string{
		"", "---\n{}\n", "a: 1\n", "- x\n- y\n", "a:\n  b: [1, 2]\n", ": :\n", "--\n{}\n",
	} {
		f.Add([]byte(seed))
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		// Must never panic. On success the map must be non-nil.
		v, err := ParseValues(data)
		if err == nil && v == nil {
			t.Fatal("ParseValues returned a nil map with a nil error")
		}
	})
}

// KindFor maps a 1-based position to its layer Kind for test readability.
func KindFor(index int) layers.Kind { return layers.Kind(index) }

func layer(t *testing.T, kind layers.Kind, body string) layers.Layer {
	t.Helper()
	name := "L" + strings.ReplaceAll(kind.Name(), " ", "_") + ".yaml"
	return layers.Layer{Kind: kind, Path: writeFile(t, name, body)}
}

// contractLayer builds the computed KindContract layer. Its Path is only a
// presence marker (platform.generated.yaml); Cascade computes the overlay from
// the human layers' templates + the context, not from this file.
func contractLayer(t *testing.T) layers.Layer {
	t.Helper()
	return layers.Layer{Kind: layers.KindContract, Path: "platform.generated.yaml"}
}

func writeFile(t *testing.T, name, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
	return path
}
