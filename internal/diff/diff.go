// Package diff compares two cumulative value snapshots and reports the changed
// leaf paths (add, replace, remove). Values are flattened to path segments;
// maps are recursed, while scalars, lists, and empty maps are leaves compared
// wholesale -- matching Helm's replace-not-merge semantics for lists.
package diff

import (
	"reflect"
	"slices"
	"strings"
)

// Op is the kind of change to a leaf.
type Op int

// Change operations: a leaf was added, replaced, or removed.
const (
	OpAdd Op = iota + 1
	OpReplace
	OpRemove
)

// String returns the lowercase name of the op (add, replace, remove).
func (o Op) String() string {
	switch o {
	case OpAdd:
		return "add"
	case OpReplace:
		return "replace"
	case OpRemove:
		return "remove"
	default:
		return "unknown"
	}
}

// Symbol returns the git-diff-style marker for the op.
func (o Op) Symbol() string {
	switch o {
	case OpAdd:
		return "+"
	case OpReplace:
		return "~"
	case OpRemove:
		return "-"
	default:
		return "?"
	}
}

// Change is a single leaf change between two snapshots.
type Change struct {
	Path []string // path segments from the root to the leaf
	Op   Op
	Old  any // previous value; nil for OpAdd
	New  any // new value; nil for OpRemove
}

// Dotted renders the path segments joined by ".".
func (c Change) Dotted() string { return strings.Join(c.Path, ".") }

// Between returns the changes that turn prev into next, sorted by path. A leaf
// differs when its values are not deeply equal.
func Between(prev, next map[string]any) []Change {
	prevLeaves := flatten(prev)
	nextLeaves := flatten(next)

	changes := make([]Change, 0)
	for key, nl := range nextLeaves {
		switch pl, ok := prevLeaves[key]; {
		case !ok:
			changes = append(changes, Change{Path: nl.segs, Op: OpAdd, New: nl.val})
		case !reflect.DeepEqual(pl.val, nl.val):
			changes = append(changes,
				Change{Path: nl.segs, Op: OpReplace, Old: pl.val, New: nl.val})
		}
	}
	for key, pl := range prevLeaves {
		if _, ok := nextLeaves[key]; !ok {
			changes = append(changes, Change{Path: pl.segs, Op: OpRemove, Old: pl.val})
		}
	}
	slices.SortFunc(changes, func(a, b Change) int {
		return slices.Compare(a.Path, b.Path)
	})
	return changes
}

type leaf struct {
	segs []string
	val  any
}

// _pathSep separates segments in the internal flatten key. NUL never appears in
// a YAML key, so the key stays unambiguous even when a segment contains a dot.
const _pathSep = "\x00"

func flatten(m map[string]any) map[string]leaf {
	leaves := make(map[string]leaf)
	var walk func(prefix []string, node map[string]any)
	walk = func(prefix []string, node map[string]any) {
		for k, v := range node {
			segs := make([]string, 0, len(prefix)+1)
			segs = append(segs, prefix...)
			segs = append(segs, k)
			if child, ok := v.(map[string]any); ok && len(child) > 0 {
				walk(segs, child)
				continue
			}
			leaves[strings.Join(segs, _pathSep)] = leaf{segs: segs, val: v}
		}
	}
	walk(nil, m)
	return leaves
}
