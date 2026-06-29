// Package merge coalesces the value layers in order, replicating Helm's -f
// precedence by applying helm.sh/helm/v3/pkg/chartutil.MergeTables to each
// layer in turn (the new layer is authoritative, maps deep-merge, lists and
// scalars replace). It returns the cumulative merged values after each layer so
// the diff package can show what every layer changed.
package merge

import (
	"fmt"
	"os"
	"regexp"
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

// Cascade merges the value layers in order, returning the cumulative merged
// values after each layer so the diff package can show what every layer changed.
// Human layers (chart defaults + env deltas) have their {{ }} templated leaves
// stripped; the computed infra-contract layer resolves those templates against
// ctx (built by BuildContext from the globals chain + platform.generated.yaml)
// and surfaces the resolved values there. The versions layer (Kargo) is read
// as-is.
//
// Snapshots are read-only: later steps may share unmodified submaps with
// earlier ones, so callers must not mutate Step.Values.
func Cascade(ls []layers.Layer, ctx Values, chart string) ([]Step, error) {
	steps := make([]Step, 0, len(ls))
	cumulative := Values{}
	for _, l := range ls {
		overlay, err := layerOverlay(l, ls, ctx, chart)
		if err != nil {
			return nil, err
		}
		// MergeTables(dst, src) treats dst as authoritative, so the new layer
		// (overlay) wins over everything merged so far (cumulative). It mutates
		// only overlay; earlier snapshots are read but never written.
		cumulative = chartutil.MergeTables(overlay, cumulative)
		steps = append(steps, Step{Layer: l, Values: cumulative})
	}
	return steps, nil
}

// layerOverlay materializes a single layer's overlay. The infra-contract layer
// (l.IsContract) is COMPUTED: it resolves the {{ }} template refs accumulated
// across the human layers against ctx, contributing only those resolved keys.
// The versions layer is read verbatim. Every other (human) layer is read and has
// its templated leaves stripped, so an unresolved {{ }} placeholder never shows
// at the human layer -- its concrete value appears at the contract layer instead.
//
// Precedence note: the contract layer's keys are infra facts resolved from the
// human layers' templates; layer 7 keys are version pins (image tags). The two
// key sets are disjoint, so no value is ever set by both and the layer-6-before-7
// ordering shows no inversion.
func layerOverlay(l layers.Layer, ls []layers.Layer, ctx Values, chart string) (Values, error) {
	if l.IsContract() {
		raw, err := mergeHumanRaw(ls)
		if err != nil {
			return nil, err
		}
		resolved := resolveTemplatedOverlay(raw, ctx)
		if chart == "" {
			return resolved, nil
		}
		sub, ok := resolved[chart]
		if !ok {
			return Values{}, nil
		}
		subMap, ok := sub.(map[string]any)
		if !ok {
			return Values{}, nil
		}
		return Values{chart: Values(subMap)}, nil
	}
	vals, err := ReadValues(l.Path)
	if err != nil {
		return nil, err
	}
	if l.Kind != layers.KindVersions {
		stripTemplated(vals)
	}
	return vals, nil
}

// mergeHumanRaw merges the human layers (chart defaults + env deltas) in order
// WITHOUT stripping templates -- the raw view the contract layer resolves from.
func mergeHumanRaw(ls []layers.Layer) (Values, error) {
	raw := Values{}
	for _, l := range ls {
		if l.IsContract() || l.Kind == layers.KindVersions {
			continue
		}
		v, err := ReadValues(l.Path)
		if err != nil {
			return nil, err
		}
		raw = chartutil.MergeTables(v, raw)
	}
	return raw, nil
}

// stripTemplated recursively deletes string leaves containing a Go-template ref
// ("{{") from v, and prunes any map left empty as a result. A human value layer
// may reference a contract fact via such a template; the concrete value is
// supplied by the contract layer (resolved/<chart>.yaml), so the placeholder is
// hidden at the human layer.
func stripTemplated(v Values) {
	for k, val := range v {
		switch t := val.(type) {
		case string:
			if strings.Contains(t, "{{") {
				delete(v, k)
			}
		case map[string]any:
			stripTemplated(t)
			if len(t) == 0 {
				delete(v, k)
			}
		}
	}
}

// templateRef matches a Go-template field ref like "{{ .buckets.longhorn }}".
var templateRef = regexp.MustCompile(`\{\{\s*\.([a-zA-Z0-9_.]+)\s*\}\}`)

// resolveTemplatedOverlay returns an overlay holding only the leaves in raw whose
// string value carries a {{ }} template, each resolved against ctx. This is the
// infra contract's contribution: the env layers' templated refs (e.g.
// "s3://{{ .buckets.longhorn }}@{{ .global.region }}/") materialized to concrete
// values. Maps are recursed; a map with no templated leaf contributes nothing.
func resolveTemplatedOverlay(raw, ctx Values) Values {
	out := Values{}
	for k, val := range raw {
		switch t := val.(type) {
		case string:
			if strings.Contains(t, "{{") {
				out[k] = resolveTemplate(t, ctx)
			}
		case map[string]any:
			if child := resolveTemplatedOverlay(t, ctx); len(child) > 0 {
				out[k] = child
			}
		}
	}
	return out
}

// resolveTemplate substitutes each {{ .a.b }} ref in s with the dotted value
// from ctx (rendered with fmt). An unresolved ref is left intact, so a missing
// contract key is visible rather than silently blanked.
func resolveTemplate(s string, ctx Values) string {
	return templateRef.ReplaceAllStringFunc(s, func(m string) string {
		path := templateRef.FindStringSubmatch(m)[1]
		v := getPath(ctx, strings.Split(path, "."))
		if v == nil {
			return m
		}
		return fmt.Sprint(v)
	})
}

// getPath returns the value at the dotted segments in m, or nil if any segment
// is missing or traverses a non-map.
func getPath(m Values, segs []string) any {
	var cur any = m
	for _, s := range segs {
		mm, ok := cur.(map[string]any)
		if !ok {
			return nil
		}
		cur = mm[s]
	}
	return cur
}

// BuildContext deep-merges the context source files in order (later wins) into
// the map used to resolve {{ }} refs: the globals.yaml chain overlaid by the
// infra contract platform.generated.yaml (see layers.ContextPaths).
func BuildContext(paths []string) (Values, error) {
	ctx := Values{}
	for _, p := range paths {
		v, err := ReadValues(p)
		if err != nil {
			return nil, err
		}
		ctx = chartutil.MergeTables(v, ctx)
	}
	return ctx, nil
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
