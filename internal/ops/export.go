package ops

// Exported building blocks for other packages that write files with the
// same safety rules (internal/archive: ZIP extraction and creation).

// TempPrefix starts the names of TwinPane's temporary files.
const TempPrefix = tmpPrefix

// PlaceFile renames a finished temporary file onto dst. Without overwrite it
// refuses (atomically where the platform allows) to replace anything that
// exists at dst.
func PlaceFile(tmp, dst string, overwrite bool) error { return placeFile(tmp, dst, overwrite) }

// FreeSpace returns the bytes available to unprivileged users in dir's
// filesystem (an error where unknown).
func FreeSpace(dir string) (uint64, error) { return freeSpace(dir) }

// IsNoSpace reports a "disk full" error.
func IsNoSpace(err error) bool { return isNoSpace(err) }
