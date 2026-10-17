//go:build !linux && !darwin && !windows

package ops

// Platforms without an atomic no-replace rename: check, then rename.
func renameNoReplace(src, dst string) error { return checkedRename(src, dst) }
