// Package diff compares two cumulative value snapshots and reports the changed
// leaf paths (add, replace, remove). Values are flattened to dotted paths;
// lists are compared wholesale, matching Helm's replace-not-merge semantics.
//
// Implemented in milestone 4.
package diff
