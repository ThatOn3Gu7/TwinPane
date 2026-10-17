package ops

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// ErrInvalidName is wrapped by ValidateName failures.
var ErrInvalidName = errors.New("invalid name")

// Validate checks a copy/move request before anything is touched. It returns
// the first problem found, phrased for the user.
func Validate(req Request) error {
	if len(req.Sources) == 0 {
		return errors.New("nothing to " + req.Kind.String())
	}
	if !filepath.IsAbs(req.DestDir) {
		return fmt.Errorf("destination %q is not an absolute path", req.DestDir)
	}
	di, err := os.Stat(req.DestDir)
	if err != nil {
		return fmt.Errorf("destination: %w", err)
	}
	if !di.IsDir() {
		return fmt.Errorf("destination %s is not a folder", req.DestDir)
	}
	for _, src := range req.Sources {
		if err := validateSource(req.Kind, src, req.DestDir, di); err != nil {
			return err
		}
	}
	return nil
}

func validateSource(kind Kind, src, destDir string, destInfo os.FileInfo) error {
	if !filepath.IsAbs(src) {
		return fmt.Errorf("source %q is not an absolute path", src)
	}
	src = filepath.Clean(src)
	base := filepath.Base(src)
	if base == "." || base == ".." || base == string(filepath.Separator) || src == filepath.Dir(src) {
		return fmt.Errorf("cannot %s %q", kind, src)
	}
	si, err := os.Lstat(src)
	if err != nil {
		return fmt.Errorf("source: %w", err)
	}
	// Same folder? (compare by identity, robust against symlinks/case)
	if pi, err := os.Stat(filepath.Dir(src)); err == nil && os.SameFile(pi, destInfo) {
		if kind == Move {
			return fmt.Errorf("%s is already in %s", base, destDir)
		}
		return fmt.Errorf("source and destination folder are the same (%s)", destDir)
	}
	// Directory into itself or a descendant?
	if si.IsDir() {
		if inside, _ := isInside(destDir, si); inside {
			return fmt.Errorf("cannot %s folder %q into itself or one of its subfolders", kind, base)
		}
	}
	return nil
}

// isInside reports whether dir equals, or is a descendant of, the directory
// described by ancestor. It walks up from the real (symlink-resolved) path of
// dir comparing file identity, so aliases via symlinks or case differences
// are detected.
func isInside(dir string, ancestor os.FileInfo) (bool, error) {
	real, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return false, err
	}
	for d := real; ; d = filepath.Dir(d) {
		if fi, err := os.Stat(d); err == nil && os.SameFile(fi, ancestor) {
			return true, nil
		}
		if filepath.Dir(d) == d {
			return false, nil
		}
	}
}

// ValidateName checks a single file name typed by the user (rename).
func ValidateName(name string) error {
	switch {
	case strings.TrimSpace(name) == "":
		return fmt.Errorf("%w: name cannot be empty", ErrInvalidName)
	case name == "." || name == "..":
		return fmt.Errorf("%w: %q is reserved", ErrInvalidName, name)
	case strings.ContainsRune(name, '/'):
		return fmt.Errorf("%w: name cannot contain '/'", ErrInvalidName)
	case len(name) > 255:
		return fmt.Errorf("%w: name is longer than 255 bytes", ErrInvalidName)
	}
	for _, r := range name {
		if r < 0x20 || r == 0x7f {
			return fmt.Errorf("%w: name cannot contain control characters", ErrInvalidName)
		}
	}
	if runtime.GOOS == "windows" {
		if strings.ContainsAny(name, `\<>:"|?*`) {
			return fmt.Errorf(`%w: name cannot contain any of \ < > : " | ? *`, ErrInvalidName)
		}
		if strings.HasSuffix(name, ".") || strings.HasSuffix(name, " ") {
			return fmt.Errorf("%w: name cannot end with a dot or space", ErrInvalidName)
		}
		stem := strings.ToUpper(strings.SplitN(name, ".", 2)[0])
		switch stem {
		case "CON", "PRN", "AUX", "NUL", "COM1", "COM2", "COM3", "COM4", "COM5", "COM6", "COM7", "COM8", "COM9",
			"LPT1", "LPT2", "LPT3", "LPT4", "LPT5", "LPT6", "LPT7", "LPT8", "LPT9":
			return fmt.Errorf("%w: %q is a reserved device name", ErrInvalidName, stem)
		}
	}
	return nil
}

// Rename renames dir/oldName to dir/newName. It never replaces an existing
// entry: if newName exists (and is not the same file, e.g. a case-only
// rename on a case-insensitive filesystem) it returns an error.
func Rename(dir, oldName, newName string) error {
	if err := ValidateName(newName); err != nil {
		return err
	}
	if oldName == newName {
		return nil
	}
	if filepath.Base(oldName) != oldName || oldName == "." || oldName == ".." {
		return fmt.Errorf("%w: %q", ErrInvalidName, oldName)
	}
	src := filepath.Join(dir, oldName)
	dst := filepath.Join(dir, newName)
	si, err := os.Lstat(src)
	if err != nil {
		return err
	}
	if di, err := os.Lstat(dst); err == nil {
		if !os.SameFile(si, di) {
			return fmt.Errorf("%q already exists", newName)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return renameNoReplace(src, dst)
}
