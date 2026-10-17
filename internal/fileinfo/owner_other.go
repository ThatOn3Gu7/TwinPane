//go:build !unix

package fileinfo

import "io/fs"

func owner(fs.FileInfo) (int, int) { return -1, -1 }
func userName(int) string          { return "" }
func groupName(int) string         { return "" }

func chmodSupport(string, fs.FileInfo, int) (bool, string) {
	return false, "this system has no Unix permissions"
}
