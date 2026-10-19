//go:build windows

package search

import (
	"fmt"
	"os"

	"github.com/ThatOn3Gu7/TwinPane/internal/textview"
)

func openRegularContentFile(path string) (*os.File, os.FileInfo, error) {
	before, err := os.Lstat(path)
	if err != nil {
		return nil, nil, err
	}
	if !before.Mode().IsRegular() {
		return nil, nil, fmt.Errorf("%s: %w", path, textview.ErrNotRegular)
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, nil, err
	}
	info, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, nil, err
	}
	if !info.Mode().IsRegular() {
		f.Close()
		return nil, nil, fmt.Errorf("%s: %w", path, textview.ErrNotRegular)
	}
	return f, info, nil
}
