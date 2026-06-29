// Package whyx orchestrates the provenance pipeline: resolve the value-file
// layers for a (target, chart), merge them in order with Helm's coalescing,
// diff each step against the previous cumulative state, and render the result.
package whyx

import (
	"context"
	"fmt"
	"io"
	"os"

	"sigs.k8s.io/yaml"

	"github.com/jalet/whyx/internal/layers"
	"github.com/jalet/whyx/internal/merge"
	"github.com/jalet/whyx/internal/render"
)

// Config holds the resolved inputs for a single whyx invocation.
type Config struct {
	// Target is the envs path "tenant/env/cluster", e.g. "project/dev/apps".
	Target string
	// Chart is the chart name, e.g. "backend".
	Chart string
	// Key, when non-empty, is the dotted value path to trace, e.g. "image.tag".
	Key string

	// RepoRoot is the helm-charts checkout; empty means auto-detect by walking
	// up for a directory containing both charts/ and envs/.
	RepoRoot string
	// Format selects the output renderer: "diff" (default), "table", or "json".
	Format string

	// Effective prints only the final merged values, skipping the cascade.
	Effective bool
	// ListLayers prints the resolved layer files in order and exits.
	ListLayers bool
	// ChartDefaults includes the chart-defaults layer (1) in the cascade; it is
	// hidden by default. A focused key always shows it regardless.
	ChartDefaults bool
	// NoColor disables ANSI color (also honored: NO_COLOR env, non-TTY stdout).
	NoColor bool
	// Verbose enables diagnostic logging on stderr.
	Verbose bool
}

// Run executes the provenance pipeline for cfg, writing the rendered output to
// out. Diagnostics, when enabled, go to stderr.
func Run(ctx context.Context, cfg Config, out io.Writer) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	target, err := layers.ParseTarget(cfg.Target)
	if err != nil {
		return err
	}

	repoRoot := cfg.RepoRoot
	if repoRoot == "" {
		cwd, err := os.Getwd()
		if err != nil {
			return fmt.Errorf("working directory: %w", err)
		}
		if repoRoot, err = layers.FindRepoRoot(cwd); err != nil {
			return err
		}
	} else if err := layers.CheckRepoRoot(repoRoot); err != nil {
		// An explicit --repo is trusted as-is; verify it actually is a repo root
		// so a wrong path fails clearly rather than as "chart not found".
		return err
	}

	resolved, err := layers.Resolve(repoRoot, target, cfg.Chart)
	if err != nil {
		return err
	}

	if cfg.ListLayers {
		return writeLayers(out, resolved)
	}

	ctxPaths, err := layers.ContextPaths(repoRoot, target)
	if err != nil {
		return err
	}
	tmplCtx, err := merge.BuildContext(ctxPaths)
	if err != nil {
		return err
	}

	steps, err := merge.Cascade(resolved, tmplCtx)
	if err != nil {
		return err
	}

	if cfg.Effective {
		return writeEffective(out, steps)
	}

	format, err := render.ParseFormat(cfg.Format)
	if err != nil {
		return err
	}
	return render.Cascade(out, steps, render.Options{
		Format:            format,
		Key:               cfg.Key,
		Color:             useColor(cfg, out),
		ShowChartDefaults: cfg.ChartDefaults,
	})
}

// useColor decides whether to colorize: off when --no-color or NO_COLOR is set,
// otherwise on only when out is a terminal (a character device).
func useColor(cfg Config, out io.Writer) bool {
	if cfg.NoColor || os.Getenv("NO_COLOR") != "" {
		return false
	}
	f, ok := out.(*os.File)
	if !ok {
		return false
	}
	info, err := f.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}

// writeEffective prints the final merged values as YAML.
func writeEffective(out io.Writer, steps []merge.Step) error {
	data, err := yaml.Marshal(merge.Effective(steps))
	if err != nil {
		return fmt.Errorf("marshal effective values: %w", err)
	}
	_, err = out.Write(data)
	return err
}

// writeLayers prints the resolved layers in merge order, one per line.
func writeLayers(w io.Writer, resolved []layers.Layer) error {
	for _, l := range resolved {
		if _, err := fmt.Fprintf(w, "%d  %-17s %-16s %s\n",
			l.Index(), l.Kind.Name(), l.Kind.Owner(), l.Path); err != nil {
			return err
		}
	}
	return nil
}
