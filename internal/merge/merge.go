// Package merge coalesces the value layers in order, replicating Helm's -f
// precedence by applying helm.sh/helm/v3/pkg/chartutil.MergeTables to each
// layer in turn (the new layer is authoritative, maps deep-merge, lists and
// scalars replace). It returns the cumulative merged values after each layer so
// the diff package can show what every layer changed.
package merge

import (
	"fmt"
	"os"

	"helm.sh/helm/v3/pkg/chartutil"
	"sigs.k8s.io/yaml"

	"github.com/jalet/whyx/internal/layers"
)

// Values is a decoded value tree, matching Helm's value representation:
// string keys and JSON-shaped scalars (numbers decode to float64).
type Values = map[string]any

// Step is the cumulative merged values after applying one layer, in merge
// order (lowest precedence first).
type Step struct {
	Layer  layers.Layer
	Values Values
}

// Cascade reads each layer's value file and merges them in order. The snapshot
// in each Step is the result of merging that layer over all earlier ones.
//
// Snapshots are read-only: later steps may share unmodified submaps with
// earlier ones, so callers must not mutate Step.Values.
func Cascade(ls []layers.Layer) ([]Step, error) {
	steps := make([]Step, 0, len(ls))
	cumulative := Values{}
	for _, l := range ls {
		vals, err := ReadValues(l.Path)
		if err != nil {
			return nil, err
		}
		// MergeTables(dst, src) treats dst as authoritative, so the new layer
		// (vals) wins over everything merged so far (cumulative). This call
		// mutates only vals; cumulative -- and thus earlier snapshots -- is read
		// but never written.
		cumulative = chartutil.MergeTables(vals, cumulative)
		steps = append(steps, Step{Layer: l, Values: cumulative})
	}
	return steps, nil
}

// ReadValues decodes a YAML value file into a Values map. An empty or
// whitespace-only file yields an empty (non-nil) map.
func ReadValues(path string) (Values, error) {
	// G304: reading value files by path is the tool's purpose; paths come from
	// layers.Resolve, built within repoRoot from traversal-checked target
	// segments and fixed file names.
	data, err := os.ReadFile(path) //nolint:gosec
	if err != nil {
		return nil, fmt.Errorf("read %q: %w", path, err)
	}
	v, err := ParseValues(data)
	if err != nil {
		return nil, fmt.Errorf("%q: %w", path, err)
	}
	return v, nil
}

// ParseValues decodes YAML bytes into a Values map. Empty input yields an empty
// (non-nil) map; a non-mapping root is an error.
func ParseValues(data []byte) (Values, error) {
	var raw any
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return nil, err
	}
	if raw == nil {
		return Values{}, nil
	}
	v, ok := raw.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("value file root must be a YAML mapping, got %T "+
			"(a leading \"--\" instead of \"---\" is a common cause)", raw)
	}
	return v, nil
}

// Effective returns the final merged values -- the last step's snapshot -- or an
// empty map if there are no steps.
func Effective(steps []Step) Values {
	if len(steps) == 0 {
		return Values{}
	}
	return steps[len(steps)-1].Values
}
