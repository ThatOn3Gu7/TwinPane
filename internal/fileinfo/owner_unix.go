//go:build unix

package fileinfo

import (
	"io/fs"
	"os"
	"os/user"
	"runtime"
	"strconv"
	"syscall"
)

func owner(fi fs.FileInfo) (uid, gid int) {
	if st, ok := fi.Sys().(*syscall.Stat_t); ok {
		return int(st.Uid), int(st.Gid)
	}
	return -1, -1
}

// androidName names Android user IDs the way Termux's `id` shows them:
// app IDs as u<user>_a<app> (e.g. 10234 → u0_a234), a few system IDs by name.
func androidName(id int) string {
	switch id {
	case 0:
		return "root"
	case 1000:
		return "system"
	case 1015:
		return "sdcard_rw"
	case 1023:
		return "media_rw"
	case 1028:
		return "sdcard_r"
	case 2000:
		return "shell"
	case 9997:
		return "everybody"
	}
	userID, app := id/100000, id%100000
	switch {
	case app >= 10000 && app < 20000:
		return "u" + strconv.Itoa(userID) + "_a" + strconv.Itoa(app-10000)
	case app >= 50000 && app < 60000:
		return "u" + strconv.Itoa(userID) + "_a" + strconv.Itoa(app-50000) + "_cache"
	}
	return ""
}

func userName(uid int) string {
	if runtime.GOOS == "android" {
		return androidName(uid)
	}
	if u, err := user.LookupId(strconv.Itoa(uid)); err == nil {
		return u.Username
	}
	return ""
}

func groupName(gid int) string {
	if runtime.GOOS == "android" {
		return androidName(gid)
	}
	if g, err := user.LookupGroupId(strconv.Itoa(gid)); err == nil {
		return g.Name
	}
	return ""
}

// chmodSupport decides whether to offer permission changes for path.
func chmodSupport(path string, fi fs.FileInfo, uid int) (bool, string) {
	if fi.Mode()&fs.ModeSymlink != 0 {
		return false, "a symbolic link has no permissions of its own (its target's apply)"
	}
	if euid := os.Geteuid(); uid >= 0 && euid != 0 && uid != euid {
		return false, "owned by another user: only the owner can change the permissions"
	}
	if ok, why := fsSupportsChmod(path); !ok {
		return false, why
	}
	return true, ""
}
