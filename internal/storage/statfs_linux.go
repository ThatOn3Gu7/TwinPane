//go:build linux

package storage

import "golang.org/x/sys/unix"

// magicNames maps statfs filesystem magic numbers to names (used when the
// mount table cannot be read).
var magicNames = map[int64]string{
	0xEF53:     "ext4",
	0xF2F52010: "f2fs",
	0x65735546: "fuse",
	0x5DCA2DF5: "sdcardfs",
	0x00050001: "esdfs",
	0x01021994: "tmpfs",
	0x794c7630: "overlay",
	0x9123683E: "btrfs",
	0x58465342: "xfs",
	0x4d44:     "vfat",
	0x2011BAB0: "exfat",
	0x5346544e: "ntfs",
	0xE0F5E1E2: "erofs",
	0x73717368: "squashfs",
	0x6969:     "nfs",
	0xFF534D42: "cifs",
	0x9fa0:     "proc",
}

func statfs(in *Info) {
	var st unix.Statfs_t
	if err := unix.Statfs(in.Path, &st); err != nil {
		in.SizeErr = err
		return
	}
	unit := uint64(st.Frsize) //nolint:unconvert
	if unit == 0 {
		unit = uint64(st.Bsize) //nolint:unconvert
	}
	in.Total = uint64(st.Blocks) * unit       //nolint:unconvert
	in.Free = uint64(st.Bfree) * unit         //nolint:unconvert
	in.Avail = uint64(st.Bavail) * unit       //nolint:unconvert
	in.MagicType = magicNames[int64(st.Type)] //nolint:unconvert
	in.ReadOnlyFlag = st.Flags&unix.ST_RDONLY != 0
}
