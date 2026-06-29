// Package layers resolves the ordered set of value-file layers for a given
// (target, chart): the chart defaults under charts/<category>/<chart>, the
// envs/ delta files (_platform, tenant, env, cluster), the infra contract
// (computed by the merge package from the env layers' {{ }} templates against
// platform.generated.yaml, Pulumi) and the machine-owned per-chart version pin
// (versions/<chart>.yaml, Kargo). Missing files are skipped, since the delta
// layers are often absent.
package layers

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Operating errors. Callers handle these and exit non-zero; they never panic.
var (
	ErrInvalidTarget = errors.New("invalid target: want tenant/env/cluster")
	ErrChartNotFound = errors.New("chart not found under charts/{base,apps,vendor}")
	ErrNoLayers      = errors.New("no value files found for target and chart")
	ErrRepoNotFound  = errors.New("helm-charts repo root not found (need charts/ and envs/)")
)

// _chartCategories is the fixed, ordered set of chart category directories.
var _chartCategories = []string{"base", "apps", "vendor"}

// _repoSearchDepthMax bounds the upward walk in FindRepoRoot.
const _repoSearchDepthMax = 64

// Kind identifies a value-file layer in the merge order. Its integer value is
// the canonical 1-based merge position (chart defaults = 1, versions = 7), so a
// layer keeps its position even when earlier layers are absent.
type Kind int

// Layer kinds in canonical merge order, lowest precedence first.
const (
	KindChartDefaults Kind = iota + 1
	KindPlatform
	KindTenant
	KindEnv
	KindCluster
	KindContract
	KindVersions
)

// Name returns the human label for the layer.
func (k Kind) Name() string {
	switch k {
	case KindChartDefaults:
		return "chart defaults"
	case KindPlatform:
		return "platform-wide"
	case KindTenant:
		return "tenant-wide"
	case KindEnv:
		return "environment-wide"
	case KindCluster:
		return "cluster"
	case KindContract:
		return "infra contract"
	case KindVersions:
		return "promoted versions"
	default:
		return "unknown"
	}
}

// Owner returns who owns the layer.
func (k Kind) Owner() string {
	switch k {
	case KindChartDefaults:
		return "chart author"
	case KindPlatform, KindTenant, KindEnv, KindCluster:
		return "platform team"
	case KindContract:
		return "Pulumi (machine)"
	case KindVersions:
		return "Kargo (machine)"
	default:
		return "unknown"
	}
}

// Target is a deployment target, the envs path tenant/env/cluster.
type Target struct {
	Tenant  string
	Env     string
	Cluster string
}

// String renders the target as tenant/env/cluster.
func (t Target) String() string {
	return t.Tenant + "/" + t.Env + "/" + t.Cluster
}

// Layer is a single value file in the merge chain.
type Layer struct {
	Kind Kind
	// Path is the absolute path to the value file.
	Path string
}

// IsContract reports whether the layer is the infra-contract layer. This layer
// is COMPUTED, not a file overlay: the merge package resolves the env layers'
// {{ }} template refs (e.g. {{ .buckets.longhorn }}) against the contract context
// (see ContextPaths) and surfaces the resolved values here. The layer's Path is
// platform.generated.yaml, used only for the presence check and --list-layers.
func (l Layer) IsContract() bool { return l.Kind == KindContract }

// IsChartDefaults reports whether the layer is the chart author's values.yaml
// (charts/<category>/<chart>/values.yaml), the lowest-precedence layer.
func (l Layer) IsChartDefaults() bool { return l.Kind == KindChartDefaults }

// Index is the canonical 1-based merge position of the layer.
func (l Layer) Index() int { return int(l.Kind) }

// ParseTarget parses a tenant/env/cluster string. Each segment must be a
// non-empty, non-relative path element.
func ParseTarget(s string) (Target, error) {
	parts := strings.Split(s, "/")
	if len(parts) != 3 {
		return Target{}, fmt.Errorf("%q: %w", s, ErrInvalidTarget)
	}
	for _, p := range parts {
		if p == "" || p == "." || p == ".." {
			return Target{}, fmt.Errorf("%q: %w", s, ErrInvalidTarget)
		}
	}
	return Target{Tenant: parts[0], Env: parts[1], Cluster: parts[2]}, nil
}

// Resolve returns the existing value-file layers for target and chart, in merge
// order (lowest precedence first). Absent files are skipped. It errors if the
// chart cannot be located or if no value files exist for the target at all.
func Resolve(repoRoot string, target Target, chart string) ([]Layer, error) {
	if chart == "" {
		return nil, fmt.Errorf("empty chart name: %w", ErrChartNotFound)
	}

	category, err := chartCategory(repoRoot, chart)
	if err != nil {
		return nil, err
	}

	clusterDir := filepath.Join(repoRoot, "envs", target.Tenant, target.Env, target.Cluster)
	candidates := []struct {
		kind Kind
		path string
	}{
		{KindChartDefaults, filepath.Join(repoRoot, "charts", category, chart, "values.yaml")},
		{KindPlatform, filepath.Join(repoRoot, "envs", "_platform", "values.yaml")},
		{KindTenant, filepath.Join(repoRoot, "envs", target.Tenant, "values.yaml")},
		{KindEnv, filepath.Join(repoRoot, "envs", target.Tenant, target.Env, "values.yaml")},
		{KindCluster, filepath.Join(clusterDir, "values.yaml")},
		// Layer 6 is the infra contract -- COMPUTED, not a file overlay: the merge
		// package resolves the {{ }} template refs in the env values.yaml layers
		// (e.g. {{ .buckets.longhorn }}) against the contract context (the
		// globals.yaml chain overlaid by platform.generated.yaml) and surfaces the
		// resolved values here. The path below only gates the layer's presence and
		// labels it in --list-layers. See ContextPaths and merge.Cascade.
		{KindContract, filepath.Join(clusterDir, "platform.generated.yaml")},
		// Layer 7 is the per-chart, Kargo-owned image version pin.
		{KindVersions, filepath.Join(clusterDir, "versions", chart+".yaml")},
	}

	resolved := make([]Layer, 0, len(candidates))
	for _, c := range candidates {
		ok, err := isRegularFile(c.path)
		if err != nil {
			return nil, err
		}
		if ok {
			resolved = append(resolved, Layer{Kind: c.kind, Path: c.path})
		}
	}
	if len(resolved) == 0 {
		return nil, fmt.Errorf("%s chart %q: %w", target, chart, ErrNoLayers)
	}
	return resolved, nil
}

// ContextPaths returns the existing template-context source files for target, in
// deep-merge order (lowest precedence first): the globals.yaml chain (_platform <
// tenant < env < cluster) followed by the infra contract platform.generated.yaml,
// which is overlaid last and wins on any leaf collision. Absent files are skipped.
// The merge package builds the context from these to resolve the {{ }} refs in the
// env values layers (the computed infra-contract layer).
func ContextPaths(repoRoot string, target Target) ([]string, error) {
	clusterDir := filepath.Join(repoRoot, "envs", target.Tenant, target.Env, target.Cluster)
	candidates := []string{
		filepath.Join(repoRoot, "envs", "_platform", "globals.yaml"),
		filepath.Join(repoRoot, "envs", target.Tenant, "globals.yaml"),
		filepath.Join(repoRoot, "envs", target.Tenant, target.Env, "globals.yaml"),
		filepath.Join(clusterDir, "globals.yaml"),
		filepath.Join(clusterDir, "platform.generated.yaml"),
	}
	out := make([]string, 0, len(candidates))
	for _, p := range candidates {
		ok, err := isRegularFile(p)
		if err != nil {
			return nil, err
		}
		if ok {
			out = append(out, p)
		}
	}
	return out, nil
}

// CheckRepoRoot verifies that root is a helm-charts repo root: a directory
// holding both charts/ and envs/. Use it to validate an explicitly supplied
// --repo path, which -- unlike FindRepoRoot's auto-detection -- is otherwise
// trusted blindly, turning a wrong path into a misleading "chart not found".
func CheckRepoRoot(root string) error {
	if dirExists(filepath.Join(root, "charts")) && dirExists(filepath.Join(root, "envs")) {
		return nil
	}
	return fmt.Errorf("%q: %w", root, ErrRepoNotFound)
}

// FindRepoRoot walks up from start until it finds a directory containing both
// charts/ and envs/, returning that directory.
func FindRepoRoot(start string) (string, error) {
	dir, err := filepath.Abs(start)
	if err != nil {
		return "", fmt.Errorf("resolve %q: %w", start, err)
	}
	for range _repoSearchDepthMax {
		if dirExists(filepath.Join(dir, "charts")) && dirExists(filepath.Join(dir, "envs")) {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return "", fmt.Errorf("from %q: %w", start, ErrRepoNotFound)
}

// chartCategory returns the category directory (base, apps, or vendor) that
// contains the chart.
func chartCategory(repoRoot, chart string) (string, error) {
	for _, category := range _chartCategories {
		if dirExists(filepath.Join(repoRoot, "charts", category, chart)) {
			return category, nil
		}
	}
	return "", fmt.Errorf("%q: %w", chart, ErrChartNotFound)
}

func isRegularFile(path string) (bool, error) {
	info, err := os.Stat(path)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("stat %q: %w", path, err)
	}
	return info.Mode().IsRegular(), nil
}

func dirExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}
