// Package fileinfo gathers the metadata shown in TwinPane's Properties
// dialog and changes Unix permissions safely. It has no UI dependencies.
package fileinfo

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"time"
)

// Info is what the Properties dialog shows for one item.
type Info struct {
	Path, Name, Dir string
	Type            string // "regular file", "folder", "symbolic link", ...
	IsDir           bool
	IsLink          bool
	LinkTarget      string // raw target of a link
	TargetType      string // type of what a link resolves to; "" if broken
	TargetErr       error  // why a link's target cannot be resolved
	Size            int64  // bytes (of the link itself for links)
	Mode            fs.FileMode
	ModTime         time.Time
	UID, GID        int    // -1 if unknown
	Owner, Group    string // names where known, else ""
	Items           int    // entries in a folder, -1 if unknown / not a folder
	ItemsErr        error

	// ChmodOK says whether TwinPane offers to change the permissions;
	// ChmodWhy explains why not.
	ChmodOK  bool
	ChmodWhy string
}

// Stat gathers the information for path (without following a final link).
func Stat(path string) (*Info, error) {
	fi, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	in := &Info{
		Path: path, Name: filepath.Base(path), Dir: filepath.Dir(path),
		Size: fi.Size(), Mode: fi.Mode(), ModTime: fi.ModTime(),
		UID: -1, GID: -1, Items: -1, Type: TypeName(fi.Mode()),
	}
	in.UID, in.GID = owner(fi)
	if in.UID >= 0 {
		in.Owner = userName(in.UID)
		in.Group = groupName(in.GID)
	}
	if fi.Mode()&fs.ModeSymlink != 0 {
		in.IsLink = true
		in.LinkTarget, _ = os.Readlink(path)
		if ti, err := os.Stat(path); err == nil {
			in.TargetType = TypeName(ti.Mode())
			in.IsDir = ti.IsDir()
		} else {
			in.TargetErr = err
		}
	} else if fi.IsDir() {
		in.IsDir = true
		if es, err := os.ReadDir(path); err == nil {
			in.Items = len(es) // direct entries only: no recursive scanning
		} else {
			in.ItemsErr = err
		}
	}
	in.ChmodOK, in.ChmodWhy = chmodSupport(path, fi, in.UID)
	return in, nil
}

// TypeName describes a file mode's type.
func TypeName(m fs.FileMode) string {
	switch {
	case m.IsDir():
		return "folder"
	case m&fs.ModeSymlink != 0:
		return "symbolic link"
	case m.IsRegular():
		if m.Perm()&0o111 != 0 {
			return "regular file (executable)"
		}
		return "regular file"
	case m&fs.ModeNamedPipe != 0:
		return "named pipe (FIFO)"
	case m&fs.ModeSocket != 0:
		return "socket"
	case m&fs.ModeCharDevice != 0:
		return "character device"
	case m&fs.ModeDevice != 0:
		return "block device"
	}
	return "special file"
}

// Special are the permission bits beyond rwx.
const Special = fs.ModeSetuid | fs.ModeSetgid | fs.ModeSticky

// Octal renders the permission bits as octal: "0644", or "4755" with
// set-user-ID / set-group-ID / sticky bits.
func Octal(m fs.FileMode) string {
	v := uint32(m.Perm())
	if m&fs.ModeSetuid != 0 {
		v |= 0o4000
	}
	if m&fs.ModeSetgid != 0 {
		v |= 0o2000
	}
	if m&fs.ModeSticky != 0 {
		v |= 0o1000
	}
	return fmt.Sprintf("%04o", v)
}

// Symbolic renders "rwxr-xr-x" (with s/S/t/T for the special bits).
func Symbolic(m fs.FileMode) string {
	const rwx = "rwxrwxrwx"
	b := []byte("---------")
	for i := 0; i < 9; i++ {
		if m.Perm()&(1<<uint(8-i)) != 0 {
			b[i] = rwx[i]
		}
	}
	special := func(i int, on bool, set, unset byte) {
		if !on {
			return
		}
		if b[i] == 'x' {
			b[i] = set
		} else {
			b[i] = unset
		}
	}
	special(2, m&fs.ModeSetuid != 0, 's', 'S')
	special(5, m&fs.ModeSetgid != 0, 's', 'S')
	special(8, m&fs.ModeSticky != 0, 't', 'T')
	return string(b)
}

// ParseOctal parses "644", "0644" or "4755".
func ParseOctal(s string) (fs.FileMode, error) {
	v, err := strconv.ParseUint(s, 8, 32)
	if err != nil || v > 0o7777 || s == "" {
		return 0, fmt.Errorf("%q is not an octal permission (e.g. 644 or 0755)", s)
	}
	m := fs.FileMode(v & 0o777)
	if v&0o4000 != 0 {
		m |= fs.ModeSetuid
	}
	if v&0o2000 != 0 {
		m |= fs.ModeSetgid
	}
	if v&0o1000 != 0 {
		m |= fs.ModeSticky
	}
	return m, nil
}

// ErrIgnored is returned when the filesystem accepted a permission change
// but did not apply it (some Android and FAT storage does that).
var ErrIgnored = errors.New("the filesystem did not apply the change (it does not support Unix permissions)")

// Chmod sets the permission bits (rwx and special bits of perm) of path and
// verifies the result. Links are refused (their own mode cannot be set).
func Chmod(path string, perm fs.FileMode) error {
	fi, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if fi.Mode()&fs.ModeSymlink != 0 {
		return errors.New("the permissions of a symbolic link cannot be changed")
	}
	want := perm & (fs.ModePerm | Special)
	if err := os.Chmod(path, want); err != nil {
		return err
	}
	after, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if runtime.GOOS != "windows" && after.Mode()&(fs.ModePerm|Special) != want {
		return fmt.Errorf("%w: it is %s, not %s", ErrIgnored, Octal(after.Mode()), Octal(want))
	}
	return nil
}
