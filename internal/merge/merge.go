// Package merge coalesces the value layers in order, replicating Helm's -f
// precedence by applying helm.sh/helm/v3/pkg/chartutil.MergeTables to each
// layer in turn (the new layer is authoritative, maps deep-merge, lists and
// scalars replace). It returns the cumulative merged values after each layer so
// the diff package can show what every layer changed.
package merge

import (
	"fmt"
	"os"
	"strings"

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
		vals, err := layerValues(l)
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

// layerValues materializes a layer into a Values overlay. Most layers are raw
// value files, decoded as-is. The infra-contract layer (layer 6) is special:
// its file is the chart's ArgoCD source manifest, and only the .helmParameters
// it names get projected into a nested overlay.
//
// Precedence note: ArgoCD applies helm.parameters (--set) AFTER all valueFiles,
// so semantically the contract outranks even layer 7. We still present it as
// layer 6 (before layer 7) because the key sets are disjoint -- contract keys
// are infra facts (buckets/region/kms), layer 7 keys are version pins (image
// tags) -- so no value is ever set by both and the ordering shows no inversion.
func layerValues(l layers.Layer) (Values, error) {
	if l.IsContractProjection() {
		return readContractProjection(l.Path)
	}
	return ReadValues(l.Path)
}

// helmParameter is a single ArgoCD --set entry: a dotted name and its value.
type helmParameter struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// sourceManifest is the slice of the ArgoCD source manifest we care about.
type sourceManifest struct {
	HelmParameters []helmParameter `json:"helmParameters"`
}

// readContractProjection reads enabled/<chart>.yaml and projects its
// .helmParameters into a value overlay. Each dotted name (e.g.
// "defaultBackupStore.backupTarget") expands into a nested map, and the value
// is kept as a STRING -- matching Helm --set, which never type-coerces. An
// empty or absent helmParameters list yields an empty (non-nil) overlay, so the
// chart consumes nothing from the contract.
func readContractProjection(path string) (Values, error) {
	// G304: see ReadValues; the path is built by layers.Resolve within repoRoot.
	data, err := os.ReadFile(path) //nolint:gosec
	if err != nil {
		return nil, fmt.Errorf("read %q: %w", path, err)
	}
	var src sourceManifest
	if err := yaml.Unmarshal(data, &src); err != nil {
		return nil, fmt.Errorf("%q: %w", path, err)
	}
	out := Values{}
	for _, p := range src.HelmParameters {
		if p.Name == "" {
			continue
		}
		setDotted(out, p.Name, p.Value)
	}
	return out, nil
}

// setDotted assigns value at the dotted path in m, creating intermediate maps
// as needed. A non-map node along the path is overwritten with a fresh map so
// the assignment always lands (helmParameter names are flat, non-conflicting).
func setDotted(m Values, dotted string, value any) {
	segs := strings.Split(dotted, ".")
	node := m
	for _, seg := range segs[:len(segs)-1] {
		child, ok := node[seg].(Values)
		if !ok {
			child = Values{}
			node[seg] = child
		}
		node = child
	}
	node[segs[len(segs)-1]] = value
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
