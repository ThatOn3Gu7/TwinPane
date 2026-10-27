//go:build windows

package activity

// lockFile is a no-op where flock does not exist. Rotation may then happen
// twice when two instances race, which loses a filename but never content.
func lockFile(string) func() { return func() {} }
