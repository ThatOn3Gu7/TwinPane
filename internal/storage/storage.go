// Package storage reports the size and usage of the filesystem holding a
// folder, and where it is mounted. It makes one statfs call and reads
// /proc/self/mountinfo: no folder is scanned, nothing runs in the
// background. Values the system does not provide are reported as missing,
// never guessed.
package storage

import (
	"bufio"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// Mount is one line of /proc/self/mountinfo.
type Mount struct {
	Point   string // mount point
	Root    string // the folder of the filesystem mounted there
	FSType  string
	Source  string
	Options string // per-mount options (rw/ro, ...)
}

// ReadOnly reports a read-only mount.
func (m Mount) ReadOnly() bool {
	for _, o := range strings.Split(m.Options, ",") {
		if o == "ro" {
			return true
		}
	}
	return false
}

// Info describes the filesystem holding a folder.
type Info struct {
	Path     string // the folder asked about
	Resolved string // Path with symbolic links resolved (used for the mount)

	// Sizes, in bytes, valid when SizeErr is nil.
	Total uint64 // capacity
	Free  uint64 // free, including the space reserved for root
	Avail uint64 // free for unprivileged users (what TwinPane can use)
	// SizeErr tells why the sizes are unavailable.
	SizeErr error

	// Mount is valid when MountErr is nil.
	Mount    Mount
	MountErr error

	// FSType from statfs's filesystem magic number ("" when unknown);
	// used when the mount table cannot be read.
	MagicType string
	// ReadOnlyFlag is statfs's read-only flag.
	ReadOnlyFlag bool
}

// HasSizes reports whether the sizes are known and meaningful (some
// virtual filesystems report a capacity of zero).
func (i Info) HasSizes() bool { return i.SizeErr == nil && i.Total > 0 }

// Used is the space in use (Total - Free).
func (i Info) Used() uint64 {
	if i.Free > i.Total {
		return 0
	}
	return i.Total - i.Free
}

// Reserved is free space only root may use (Free - Avail).
func (i Info) Reserved() uint64 {
	if i.Avail > i.Free {
		return 0
	}
	return i.Free - i.Avail
}

// Percent is the share in use, as df computes it: Used / (Used + Avail),
// so space reserved for root does not count as available. ok is false
// when it cannot be computed.
func (i Info) Percent() (pct float64, ok bool) {
	if !i.HasSizes() {
		return 0, false
	}
	u := i.Used()
	d := u + i.Avail
	if d == 0 {
		return 0, false
	}
	return float64(u) * 100 / float64(d), true
}

// FSType returns the filesystem type from the mount table, else from the
// magic number, else "".
func (i Info) FSType() string {
	if i.MountErr == nil && i.Mount.FSType != "" {
		return i.Mount.FSType
	}
	return i.MagicType
}

// ReadOnly reports a read-only filesystem (mount option or statfs flag).
func (i Info) ReadOnly() bool {
	return (i.MountErr == nil && i.Mount.ReadOnly()) || i.ReadOnlyFlag
}

// MountInfoPath is the mount table read by Stat.
var MountInfoPath = "/proc/self/mountinfo"

// ErrUnsupported is returned where the platform offers no such data.
var ErrUnsupported = errors.New("not available on this system")

// Stat describes the filesystem holding folder path.
func Stat(path string) Info {
	in := Info{Path: path, Resolved: path}
	if r, err := filepath.EvalSymlinks(path); err == nil {
		in.Resolved = r
	}
	statfs(&in)
	f, err := os.Open(MountInfoPath)
	if err != nil {
		in.MountErr = mountErr(err)
		return in
	}
	defer f.Close()
	mounts, err := ParseMountinfo(f)
	if err != nil {
		in.MountErr = err
		return in
	}
	if m, ok := FindMount(mounts, in.Resolved); ok {
		in.Mount = m
	} else {
		in.MountErr = errors.New("not found in the mount table")
	}
	return in
}

func mountErr(err error) error {
	if errors.Is(err, os.ErrNotExist) {
		return ErrUnsupported
	}
	if errors.Is(err, os.ErrPermission) {
		return errors.New("the mount table cannot be read (permission denied)")
	}
	return err
}

// ParseMountinfo parses /proc/<pid>/mountinfo (see proc(5)):
//
//	36 35 98:0 /mnt1 /mnt/parent rw,noatime master:1 - ext3 /dev/root rw,errors=continue
func ParseMountinfo(r io.Reader) ([]Mount, error) {
	var out []Mount
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) < 10 {
			continue
		}
		sep := -1
		for i := 6; i < len(f); i++ {
			if f[i] == "-" {
				sep = i
				break
			}
		}
		if sep < 0 || sep+2 >= len(f) {
			continue
		}
		out = append(out, Mount{
			Root:    unescape(f[3]),
			Point:   unescape(f[4]),
			Options: f[5],
			FSType:  f[sep+1],
			Source:  unescape(f[sep+2]),
		})
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	if len(out) == 0 {
		return nil, errors.New("the mount table is empty")
	}
	return out, nil
}

// unescape decodes mountinfo's octal escapes (\040 for a space, ...).
func unescape(s string) string {
	if !strings.Contains(s, `\`) {
		return s
	}
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+3 < len(s) && isOct(s[i+1]) && isOct(s[i+2]) && isOct(s[i+3]) {
			b.WriteByte((s[i+1]-'0')<<6 | (s[i+2]-'0')<<3 | (s[i+3] - '0'))
			i += 3
			continue
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

func isOct(c byte) bool { return c >= '0' && c <= '7' }

// FindMount returns the mount whose mount point is the longest prefix of
// path (by whole path components). Later lines win among equal mount
// points: they are mounted on top.
func FindMount(mounts []Mount, path string) (Mount, bool) {
	path = filepath.Clean(path)
	best, found := Mount{}, false
	for _, m := range mounts {
		p := filepath.Clean(m.Point)
		if !within(path, p) {
			continue
		}
		if !found || len(p) >= len(filepath.Clean(best.Point)) {
			best, found = m, true
		}
	}
	return best, found
}

func within(path, dir string) bool {
	if dir == "/" {
		return strings.HasPrefix(path, "/")
	}
	return path == dir || strings.HasPrefix(path, dir+"/")
}
