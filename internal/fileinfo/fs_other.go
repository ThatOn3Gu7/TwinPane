//go:build unix && !linux

package fileinfo

// fsSupportsChmod: no filesystem detection outside Linux/Android; Chmod
// verifies the result instead.
func fsSupportsChmod(string) (bool, string) { return true, "" }
