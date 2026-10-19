//go:build windows

package foldersize

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"unsafe"

	"github.com/ThatOn3Gu7/TwinPane/internal/storage"
)

var getVolumePathNameW = syscall.NewLazyDLL("kernel32.dll").NewProc("GetVolumePathNameW")

func platformHasAllocated() bool { return false }

func openDirectory(path string) (Directory, error) {
	name, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return nil, err
	}
	handle, err := syscall.CreateFile(
		name,
		syscall.GENERIC_READ,
		syscall.FILE_SHARE_READ|syscall.FILE_SHARE_WRITE|syscall.FILE_SHARE_DELETE,
		nil,
		syscall.OPEN_EXISTING,
		syscall.FILE_FLAG_BACKUP_SEMANTICS|syscall.FILE_FLAG_OPEN_REPARSE_POINT,
		0,
	)
	if err != nil {
		return nil, err
	}
	file := os.NewFile(uintptr(handle), path)
	if file == nil {
		_ = syscall.CloseHandle(handle)
		return nil, syscall.EINVAL
	}
	return file, nil
}

func platformAllocated(fs.FileInfo) (uint64, bool) { return 0, false }

func directoryIdentity(_ fs.FileInfo, path string) string {
	return "path:" + strings.ToLower(filepath.Clean(path))
}

func platformSameFilesystem(rootPath, childPath string, _, _ fs.FileInfo, _ []storage.Mount, _ bool) (bool, bool) {
	rootVolume, rootOK := windowsVolumePath(rootPath)
	childVolume, childOK := windowsVolumePath(childPath)
	if !rootOK || !childOK {
		return false, false
	}
	return strings.EqualFold(rootVolume, childVolume), true
}

func windowsVolumePath(path string) (string, bool) {
	name, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return "", false
	}
	buf := make([]uint16, 1024)
	r1, _, _ := getVolumePathNameW.Call(
		uintptr(unsafe.Pointer(name)),
		uintptr(unsafe.Pointer(&buf[0])),
		uintptr(len(buf)),
	)
	if r1 == 0 {
		return "", false
	}
	return syscall.UTF16ToString(buf), true
}
