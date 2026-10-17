package ops

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// Trasher moves items to a recoverable trash.
type Trasher interface {
	// Name is a short user-facing description of the trash location.
	Name() string
	// Available reports whether path can be trashed (and why not).
	Available(path string) (bool, string)
	// Trash moves path into the trash. It must never fall back to deleting.
	Trash(path string) error
}

// DefaultTrasher returns the trash implementation for the current platform.
func DefaultTrasher() Trasher {
	home := homeDirFn()
	switch runtime.GOOS {
	case "darwin":
		if home == "" {
			return NoTrash{Reason: "home directory unknown"}
		}
		return &MacTrash{Dir: filepath.Join(home, ".Trash")}
	case "linux", "freebsd", "openbsd", "netbsd", "dragonfly", "solaris", "illumos":
		dir := os.Getenv("XDG_DATA_HOME")
		if dir == "" || !filepath.IsAbs(dir) {
			if home == "" {
				return NoTrash{Reason: "home directory unknown"}
			}
			dir = filepath.Join(home, ".local", "share")
		}
		return &FreeDesktopTrash{Dir: filepath.Join(dir, "Trash")}
	default:
		return NoTrash{Reason: "no supported trash on " + runtime.GOOS}
	}
}

// NoTrash is used where no reliable trash exists.
type NoTrash struct{ Reason string }

func (n NoTrash) Name() string                    { return "none" }
func (n NoTrash) Available(string) (bool, string) { return false, n.Reason }
func (n NoTrash) Trash(string) error              { return errors.New(n.Reason) }

// FreeDesktopTrash implements the freedesktop.org Trash specification for the
// user's home trash ($XDG_DATA_HOME/Trash). Items are moved with rename(2),
// so only items on the same filesystem as the trash are eligible; anything
// else is reported as unavailable instead of being copied.
type FreeDesktopTrash struct {
	Dir string
	Now func() time.Time
}

func (t *FreeDesktopTrash) Name() string { return t.Dir }

func (t *FreeDesktopTrash) Available(path string) (bool, string) {
	if t.Dir == "" {
		return false, "trash location unknown"
	}
	for _, sub := range []string{"files", "info"} {
		if err := os.MkdirAll(filepath.Join(t.Dir, sub), 0o700); err != nil {
			return false, fmt.Sprintf("cannot create trash folder: %v", err)
		}
	}
	return checkTrashTarget(path, t.Dir)
}

func checkTrashTarget(path, trashDir string) (bool, string) {
	if ti, err := os.Stat(trashDir); err == nil {
		if inside, _ := isInside(filepath.Dir(path), ti); inside {
			return false, "item is already inside the trash"
		}
	}
	same, err := sameDeviceFn(filepath.Dir(path), trashDir)
	if err != nil {
		return false, fmt.Sprintf("cannot check filesystem: %v", err)
	}
	if !same {
		return false, fmt.Sprintf("%s is on a different filesystem than the trash (%s)", filepath.Base(path), trashDir)
	}
	return true, ""
}

func (t *FreeDesktopTrash) Trash(path string) error {
	abs, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	if ok, why := t.Available(abs); !ok {
		return errors.New(why)
	}
	now := time.Now
	if t.Now != nil {
		now = t.Now
	}
	base := filepath.Base(abs)
	ext := filepath.Ext(base)
	stem := strings.TrimSuffix(base, ext)
	for i := 1; i < 10000; i++ {
		name := base
		if i > 1 {
			name = fmt.Sprintf("%s.%d%s", stem, i, ext)
		}
		infoPath := filepath.Join(t.Dir, "info", name+".trashinfo")
		filesPath := filepath.Join(t.Dir, "files", name)
		f, err := os.OpenFile(infoPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if errors.Is(err, os.ErrExist) {
			continue
		}
		if err != nil {
			return fmt.Errorf("cannot write trash info: %w", err)
		}
		if _, err := os.Lstat(filesPath); err == nil {
			f.Close()
			os.Remove(infoPath)
			continue
		}
		content := "[Trash Info]\nPath=" + (&url.URL{Path: abs}).EscapedPath() +
			"\nDeletionDate=" + now().Format("2006-01-02T15:04:05") + "\n"
		_, werr := f.WriteString(content)
		cerr := f.Close()
		if werr != nil || cerr != nil {
			os.Remove(infoPath)
			return fmt.Errorf("cannot write trash info: %v", errors.Join(werr, cerr))
		}
		if err := renameNoReplace(abs, filesPath); err != nil {
			os.Remove(infoPath)
			return err
		}
		return nil
	}
	return errors.New("too many items with the same name in the trash")
}

// MacTrash moves items into ~/.Trash (same volume only). It does not record
// "Put Back" metadata, but items can be dragged back from the Finder trash.
type MacTrash struct{ Dir string }

func (t *MacTrash) Name() string { return t.Dir }

func (t *MacTrash) Available(path string) (bool, string) {
	if fi, err := os.Stat(t.Dir); err != nil || !fi.IsDir() {
		return false, "~/.Trash not found"
	}
	return checkTrashTarget(path, t.Dir)
}

func (t *MacTrash) Trash(path string) error {
	if ok, why := t.Available(path); !ok {
		return errors.New(why)
	}
	base := filepath.Base(path)
	ext := filepath.Ext(base)
	stem := strings.TrimSuffix(base, ext)
	for i := 1; i < 10000; i++ {
		name := base
		if i > 1 {
			name = fmt.Sprintf("%s %d%s", stem, i, ext)
		}
		dst := filepath.Join(t.Dir, name)
		if _, err := os.Lstat(dst); err == nil {
			continue
		}
		return renameNoReplace(path, dst)
	}
	return errors.New("too many items with the same name in the trash")
}
