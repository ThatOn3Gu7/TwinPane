//go:build unix

package foldersize

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"syscall"

	"golang.org/x/sys/unix"

	"github.com/ThatOn3Gu7/TwinPane/internal/storage"
)

func platformHasAllocated() bool { return true }

func openDirectory(path string) (Directory, error) {
	fd, err := unix.Open(path, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if err != nil {
		return nil, err
	}
	file := os.NewFile(uintptr(fd), path)
	if file == nil {
		_ = unix.Close(fd)
		return nil, errors.New("cannot wrap opened directory handle")
	}
	return file, nil
}

func platformAllocated(info fs.FileInfo) (uint64, bool) {
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok || st.Blocks < 0 {
		return 0, false
	}
	blocks := uint64(st.Blocks)
	if blocks > ^uint64(0)/512 {
		return 0, false
	}
	return blocks * 512, true
}

func directoryIdentity(info fs.FileInfo, path string) string {
	if st, ok := info.Sys().(*syscall.Stat_t); ok {
		return fmt.Sprintf("%d:%d", st.Dev, st.Ino)
	}
	return "path:" + filepath.Clean(path)
}

func platformSameFilesystem(rootPath, childPath string, rootInfo, childInfo fs.FileInfo, mounts []storage.Mount, mountsKnown bool) (bool, bool) {
	if runtime.GOOS == "linux" || runtime.GOOS == "android" {
		if !mountsKnown {
			return false, false
		}
		rootMount, rootOK := storage.FindMount(mounts, rootPath)
		childMount, childOK := storage.FindMount(mounts, childPath)
		if !rootOK || !childOK {
			return false, false
		}
		return filepath.Clean(rootMount.Point) == filepath.Clean(childMount.Point), true
	}
	root, rootOK := rootInfo.Sys().(*syscall.Stat_t)
	child, childOK := childInfo.Sys().(*syscall.Stat_t)
	if !rootOK || !childOK {
		return false, false
	}
	return root.Dev == child.Dev, true
}
