package storage

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// an Android-like mount table (from the format in proc(5); paths as on a
// typical Android 11+ device)
const sample = `1 0 253:0 / / ro,relatime shared:1 - erofs /dev/block/dm-0 ro,user_xattr
20 1 0:5 / /dev rw,nosuid,relatime master:2 - tmpfs tmpfs rw,seclabel,mode=755
30 1 254:5 / /data rw,nosuid,nodev,noatime shared:30 - f2fs /dev/block/dm-5 rw,lazytime,seclabel
31 30 254:5 /media /data/media rw,nosuid,nodev,noatime shared:30 - f2fs /dev/block/dm-5 rw,lazytime
40 1 0:60 / /storage/emulated rw,nosuid,nodev,noexec,noatime shared:40 - fuse /dev/fuse rw,user_id=0,group_id=0,allow_other
41 1 0:61 / /mnt/my\040disk rw,relatime - vfat /dev/block/vold/public:179,1 rw,uid=1023
42 1 0:62 / /data2 rw - ext4 /dev/sdb1 rw
43 30 0:63 / /data rw - tmpfs over rw
broken line
`

func TestParseMountinfo(t *testing.T) {
	ms, err := ParseMountinfo(strings.NewReader(sample))
	if err != nil {
		t.Fatal(err)
	}
	if len(ms) != 8 {
		t.Fatalf("%d mounts", len(ms))
	}
	if m := ms[0]; m.Point != "/" || m.FSType != "erofs" || !m.ReadOnly() {
		t.Errorf("root: %+v", m)
	}
	if m := ms[3]; m.Root != "/media" || m.Point != "/data/media" || m.Source != "/dev/block/dm-5" {
		t.Errorf("bind: %+v", m)
	}
	if m := ms[5]; m.Point != "/mnt/my disk" || m.FSType != "vfat" {
		t.Errorf("escaped: %+v", m)
	}
	if _, err := ParseMountinfo(strings.NewReader("")); err == nil {
		t.Error("empty table accepted")
	}
}

func TestFindMount(t *testing.T) {
	ms, _ := ParseMountinfo(strings.NewReader(sample))
	cases := map[string]string{
		"/data/data/com.termux/files/home": "/data", // the later /data (tmpfs) is mounted on top
		"/data/media/0/Download":           "/data/media",
		"/data2/x":                         "/data2", // not /data
		"/storage/emulated/0/DCIM":         "/storage/emulated",
		"/mnt/my disk/a":                   "/mnt/my disk",
		"/system/bin":                      "/",
		"/":                                "/",
	}
	for p, want := range cases {
		m, ok := FindMount(ms, p)
		if !ok || m.Point != want {
			t.Errorf("FindMount(%q) = %q, want %q", p, m.Point, want)
		}
	}
	if m, _ := FindMount(ms, "/data/x"); m.FSType != "tmpfs" {
		t.Errorf("top mount must win: %+v", m)
	}
}

func TestMath(t *testing.T) {
	// 100 GiB disk, 40 GiB free of which 5 GiB reserved for root
	const G = 1 << 30
	in := Info{Total: 100 * G, Free: 40 * G, Avail: 35 * G}
	if in.Used() != 60*G || in.Reserved() != 5*G {
		t.Errorf("used %d reserved %d", in.Used(), in.Reserved())
	}
	pct, ok := in.Percent()
	if !ok || pct < 63.15 || pct > 63.17 { // 60 / (60+35) like df
		t.Errorf("percent %v %v", pct, ok)
	}
	full := Info{Total: 10, Free: 0, Avail: 0}
	if pct, ok := full.Percent(); !ok || pct != 100 {
		t.Errorf("full %v", pct)
	}
	// unavailable values are reported as such
	if _, ok := (Info{SizeErr: errors.New("x"), Total: 10}).Percent(); ok {
		t.Error("percent without sizes")
	}
	if (Info{}).HasSizes() {
		t.Error("zero capacity counts as known")
	}
	if _, ok := (Info{}).Percent(); ok {
		t.Error("percent of a zero-size filesystem")
	}
	// inconsistent numbers never underflow
	odd := Info{Total: 5, Free: 9, Avail: 12}
	if odd.Used() != 0 || odd.Reserved() != 0 {
		t.Error("underflow")
	}
}

func TestFSTypeAndReadOnly(t *testing.T) {
	in := Info{Mount: Mount{FSType: "f2fs", Options: "rw,noatime"}, MagicType: "ext4"}
	if in.FSType() != "f2fs" || in.ReadOnly() {
		t.Error("mount table first")
	}
	in.MountErr = errors.New("unreadable")
	if in.FSType() != "ext4" {
		t.Error("magic fallback")
	}
	in.ReadOnlyFlag = true
	if !in.ReadOnly() {
		t.Error("statfs read-only flag")
	}
}

func TestStatHere(t *testing.T) {
	dir := t.TempDir()
	link := filepath.Join(t.TempDir(), "link")
	os.Symlink(dir, link)
	in := Stat(link)
	if runtime.GOOS != "linux" && runtime.GOOS != "android" {
		if in.SizeErr == nil {
			t.Error("sizes claimed on an unsupported system")
		}
		return
	}
	if in.SizeErr != nil || !in.HasSizes() || in.Avail > in.Free || in.Free > in.Total {
		t.Fatalf("statfs: %+v", in)
	}
	if in.Resolved != dir {
		t.Errorf("resolved %q, want %q", in.Resolved, dir)
	}
	if in.MountErr == nil && !within(dir, in.Mount.Point) {
		t.Errorf("mount %q does not contain %q", in.Mount.Point, dir)
	}
	if in.FSType() == "" {
		t.Error("no filesystem type")
	}
}

func TestStatUnavailable(t *testing.T) {
	old := MountInfoPath
	defer func() { MountInfoPath = old }()
	MountInfoPath = filepath.Join(t.TempDir(), "nope")
	in := Stat(t.TempDir())
	if !errors.Is(in.MountErr, ErrUnsupported) {
		t.Errorf("missing table: %v", in.MountErr)
	}
	in = Stat(filepath.Join(t.TempDir(), "gone"))
	if in.SizeErr == nil {
		t.Error("sizes for a missing folder")
	}
}
