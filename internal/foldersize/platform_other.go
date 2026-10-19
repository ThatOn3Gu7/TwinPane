//go:build !unix && !windows

package foldersize

import (
	"io/fs"
	"os"
	"path/filepath"

	"github.com/ThatOn3Gu7/TwinPane/internal/storage"
)

func platformHasAllocated() bool { return false }

func openDirectory(path string) (Directory, error) { return os.Open(path) }

func platformAllocated(fs.FileInfo) (uint64, bool) { return 0, false }

func directoryIdentity(_ fs.FileInfo, path string) string {
	return "path:" + filepath.Clean(path)
}

// Unknown volume identity fails closed: the scanner reports a partial result
// and does not enter nested directories on platforms without a safe check.
func platformSameFilesystem(string, string, fs.FileInfo, fs.FileInfo, []storage.Mount, bool) (bool, bool) {
	return false, false
}
