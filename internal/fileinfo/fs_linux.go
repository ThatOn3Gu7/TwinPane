//go:build linux

package fileinfo

import "golang.org/x/sys/unix"

// Filesystems that do not store Unix permissions (or, like Android's shared
// storage, compute them): a chmod there fails or is silently ignored.
var noChmodFS = map[int64]string{
	0x65735546: "FUSE (Android shared storage or a FUSE mount)",
	0x5DCA2DF5: "sdcardfs (Android shared storage)",
	0x00050001: "esdfs (Android shared storage)",
	0x4d44:     "FAT",
	0x2011BAB0: "exFAT",
}

func fsSupportsChmod(path string) (bool, string) {
	var st unix.Statfs_t
	if err := unix.Statfs(path, &st); err != nil {
		return true, "" // unknown: try, and verify afterwards (Chmod)
	}
	if name, ok := noChmodFS[int64(st.Type)]; ok {
		return false, "this storage (" + name + ") does not support changing Unix permissions"
	}
	if st.Flags&unix.ST_RDONLY != 0 {
		return false, "the filesystem is mounted read-only"
	}
	return true, ""
}
