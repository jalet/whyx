// Package render presents the cascade in one of three formats: diff (the
// default, git-style with per-layer hunk headers and color), table, and json.
// Color is a caller-supplied flag (see Options.Color); the package itself does
// no TTY detection. Path segments that contain dots or other non-identifier
// characters are bracket-quoted so the display is unambiguous.
package render

import (
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"strconv"
	"strings"
	"text/tabwriter"

	"github.com/jalet/whyx/internal/diff"
	"github.com/jalet/whyx/internal/merge"
)

// Format selects the cascade output format.
type Format int

// Output formats.
const (
	FormatDiff Format = iota + 1
	FormatTable
	FormatJSON
)

// ErrUnknownFormat is returned by ParseFormat for an unrecognized name.
var ErrUnknownFormat = fmt.Errorf("unknown format")

// ParseFormat maps a format name to a Format.
func ParseFormat(s string) (Format, error) {
	switch s {
	case "", "diff":
		return FormatDiff, nil
	case "table":
		return FormatTable, nil
	case "json":
		return FormatJSON, nil
	default:
		return 0, fmt.Errorf("%q: %w (want diff, table, or json)", s, ErrUnknownFormat)
	}
}

// Options configures cascade rendering.
type Options struct {
	Format Format
	Key    string // focused dotted key; "" renders the full cascade
	Color  bool   // colorize the diff format (ignored by table/json)
}

// Cascade renders the merge steps to w in the configured format.
func Cascade(w io.Writer, steps []merge.Step, opts Options) error {
	lcs := collect(steps, opts.Key)
	switch opts.Format {
	case FormatJSON:
		return renderJSON(w, lcs)
	case FormatTable:
		return renderTable(w, lcs)
	default:
		return renderDiff(w, lcs, opts)
	}
}

// layerChanges pairs a step with its changes against the prior cumulative state.
type layerChanges struct {
	step    merge.Step
	changes []diff.Change
}

func collect(steps []merge.Step, key string) []layerChanges {
	out := make([]layerChanges, 0, len(steps))
	prev := map[string]any{}
	for _, s := range steps {
		changes := diff.Between(prev, s.Values)
		prev = s.Values
		if key != "" {
			changes = filterKey(changes, key)
		}
		out = append(out, layerChanges{step: s, changes: changes})
	}
	return out
}

// anyChanges reports whether any layer contributed a change.
func anyChanges(lcs []layerChanges) bool {
	for _, lc := range lcs {
		if len(lc.changes) > 0 {
			return true
		}
	}
	return false
}

func filterKey(changes []diff.Change, key string) []diff.Change {
	out := make([]diff.Change, 0, len(changes))
	for _, c := range changes {
		if d := c.Dotted(); d == key || strings.HasPrefix(d, key+".") {
			out = append(out, c)
		}
	}
	return out
}

// _noLayersMsg is printed (and exit 0) when a chart sets no helm values at all
// -- e.g. a raw-manifest (type: path) chart whose only layers are empty delta
// files and an empty contract projection. "No helm values" is a valid outcome,
// not an error.
const _noLayersMsg = "(no helm value layers -- raw-manifest chart)"

func renderDiff(w io.Writer, lcs []layerChanges, opts Options) error {
	c := colorizer{on: opts.Color}
	focused := opts.Key != ""
	if !focused && !anyChanges(lcs) {
		_, err := fmt.Fprintln(w, c.paint(_dim, _noLayersMsg))
		return err
	}
	for _, lc := range lcs {
		if focused && len(lc.changes) == 0 {
			continue // skip layers that do not touch the focused key
		}
		header := fmt.Sprintf("@@ layer %d · %s · %s @@",
			lc.step.Layer.Index(), lc.step.Layer.Kind.Name(), lc.step.Layer.Kind.Owner())
		if _, err := fmt.Fprintln(w, c.paint(_cyan, header)); err != nil {
			return err
		}
		if len(lc.changes) == 0 {
			if _, err := fmt.Fprintln(w, c.paint(_dim, "  (no changes)")); err != nil {
				return err
			}
		}
		for _, ch := range lc.changes {
			if _, err := fmt.Fprintln(w, diffLine(c, ch)); err != nil {
				return err
			}
		}
	}
	if focused {
		return writeResolved(w, c, lcs, opts.Key)
	}
	return nil
}

func diffLine(c colorizer, ch diff.Change) string {
	path := displayPath(ch.Path)
	switch ch.Op {
	case diff.OpAdd:
		return c.paint(_green, fmt.Sprintf("  + %s: %s", path, fmtVal(ch.New)))
	case diff.OpRemove:
		return c.paint(_red, fmt.Sprintf("  - %s: %s", path, fmtVal(ch.Old)))
	default:
		return c.paint(_yellow, fmt.Sprintf("  ~ %s: %s -> %s", path, fmtVal(ch.Old), fmtVal(ch.New)))
	}
}

// writeResolved prints the focused key's final value and the layer that set it.
func writeResolved(w io.Writer, c colorizer, lcs []layerChanges, key string) error {
	var winner *merge.Step
	var final any
	for i := range lcs {
		for _, ch := range lcs[i].changes {
			if ch.Dotted() == key && ch.Op != diff.OpRemove {
				winner = &lcs[i].step
				final = ch.New
			}
		}
	}
	if winner == nil {
		return nil
	}
	if _, err := fmt.Fprintln(w, c.paint(_bold, fmt.Sprintf("= %s: %s", key, fmtVal(final)))); err != nil {
		return err
	}
	_, err := fmt.Fprintf(w, "  set by layer %d · %s · %s\n",
		winner.Layer.Index(), winner.Layer.Kind.Name(), winner.Layer.Kind.Owner())
	return err
}

func renderTable(w io.Writer, lcs []layerChanges) error {
	tw := tabwriter.NewWriter(w, 0, 2, 2, ' ', 0)
	if _, err := fmt.Fprintln(tw, "LAYER\tOWNER\tOP\tPATH\tVALUE"); err != nil {
		return err
	}
	for _, lc := range lcs {
		for _, ch := range lc.changes {
			value := fmtVal(ch.New)
			switch ch.Op {
			case diff.OpReplace:
				value = fmtVal(ch.Old) + " -> " + fmtVal(ch.New)
			case diff.OpRemove:
				value = fmtVal(ch.Old)
			}
			if _, err := fmt.Fprintf(tw, "%d\t%s\t%s\t%s\t%s\n",
				lc.step.Layer.Index(), lc.step.Layer.Kind.Owner(), ch.Op.Symbol(),
				displayPath(ch.Path), value); err != nil {
				return err
			}
		}
	}
	return tw.Flush()
}

func renderJSON(w io.Writer, lcs []layerChanges) error {
	type jsonChange struct {
		Op     string   `json:"op"`
		Path   []string `json:"path"`
		Dotted string   `json:"dotted"`
		Old    any      `json:"old"`
		New    any      `json:"new"`
	}
	type jsonLayer struct {
		Index   int          `json:"index"`
		Name    string       `json:"name"`
		Owner   string       `json:"owner"`
		Changes []jsonChange `json:"changes"`
	}

	layersOut := make([]jsonLayer, 0, len(lcs))
	for _, lc := range lcs {
		jl := jsonLayer{
			Index:   lc.step.Layer.Index(),
			Name:    lc.step.Layer.Kind.Name(),
			Owner:   lc.step.Layer.Kind.Owner(),
			Changes: make([]jsonChange, 0, len(lc.changes)),
		}
		for _, ch := range lc.changes {
			jl.Changes = append(jl.Changes, jsonChange{
				Op: ch.Op.String(), Path: ch.Path, Dotted: ch.Dotted(), Old: ch.Old, New: ch.New,
			})
		}
		layersOut = append(layersOut, jl)
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(layersOut)
}

// _cleanSeg matches a path segment that needs no quoting.
var _cleanSeg = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

// displayPath renders segments as a dotted path, bracket-quoting any segment
// that is not a plain identifier (e.g. a key literally named "datasources.yaml"
// becomes datasources["datasources.yaml"].apiVersion).
func displayPath(segs []string) string {
	var b strings.Builder
	for i, s := range segs {
		if _cleanSeg.MatchString(s) {
			if i > 0 {
				b.WriteByte('.')
			}
			b.WriteString(s)
			continue
		}
		b.WriteByte('[')
		b.WriteString(strconv.Quote(s))
		b.WriteByte(']')
	}
	return b.String()
}

// fmtVal renders a leaf value compactly: scalars as-is, lists/maps as compact
// JSON.
func fmtVal(v any) string {
	switch t := v.(type) {
	case nil:
		return "null"
	case string:
		return t
	case bool, float64:
		return fmt.Sprintf("%v", t)
	default:
		if b, err := json.Marshal(v); err == nil {
			return string(b)
		}
		return fmt.Sprintf("%v", v)
	}
}

// ANSI color codes.
const (
	_green  = "32"
	_red    = "31"
	_yellow = "33"
	_cyan   = "36"
	_dim    = "2"
	_bold   = "1"
)

type colorizer struct{ on bool }

func (c colorizer) paint(code, s string) string {
	if !c.on {
		return s
	}
	return "\x1b[" + code + "m" + s + "\x1b[0m"
}
