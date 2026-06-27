// Package render presents the cascade in one of three formats: diff (the
// default, git-style with per-layer hunk headers), table, and json. Color is
// applied for the diff format on a TTY, and suppressed when --no-color is set,
// NO_COLOR is present, or stdout is not a terminal.
//
// Implemented in milestone 5.
package render
