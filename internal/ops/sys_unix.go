//go:build unix

package ops

import (
	"errors"
	"os"
	"syscall"
)

// sameDevice reports whether two existing paths live on the same filesystem,
// i.e. whether rename(2) between them can succeed.
func sameDevice(a, b string) (bool, error) {
	ai, err := os.Stat(a)
	if err != nil {
		return false, err
	}
	bi, err := os.Stat(b)
	if err != nil {
		return false, err
	}
	as, ok1 := ai.Sys().(*syscall.Stat_t)
	bs, ok2 := bi.Sys().(*syscall.Stat_t)
	if !ok1 || !ok2 {
		return false, errors.New("cannot determine device")
	}
	return as.Dev == bs.Dev, nil
}

func isCrossDevice(err error) bool { return errors.Is(err, syscall.EXDEV) }

func isNoSpace(err error) bool {
	return errors.Is(err, syscall.ENOSPC) || errors.Is(err, syscall.EDQUOT)
}
