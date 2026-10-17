package proc

import (
	"errors"
	"os"
	"strings"
)

// candidates adds the executable extensions from %PATHEXT%.
func candidates(p string) []string {
	exts := strings.Split(strings.ToLower(os.Getenv("PATHEXT")), ";")
	if len(exts) == 1 && exts[0] == "" {
		exts = []string{".com", ".exe", ".bat", ".cmd"}
	}
	out := []string{}
	lower := strings.ToLower(p)
	for _, e := range exts {
		if e != "" && strings.HasSuffix(lower, e) {
			return []string{p}
		}
	}
	for _, e := range exts {
		if e != "" {
			out = append(out, p+e)
		}
	}
	return out
}

func executable(p string) error {
	fi, err := os.Stat(p)
	if err != nil {
		return err
	}
	if fi.IsDir() {
		return errors.New("is a directory")
	}
	return nil
}
