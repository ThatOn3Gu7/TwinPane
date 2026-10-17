package fsys

import (
	"fmt"
	"strings"
	"time"
)

// HumanSize formats a byte count using binary units (1 KiB = 1024 B).
func HumanSize(n int64) string {
	if n < 0 {
		return "?"
	}
	if n < 1024 {
		return fmt.Sprintf("%d B", n)
	}
	units := []string{"K", "M", "G", "T", "P", "E"}
	v := float64(n)
	i := -1
	for v >= 1024 && i < len(units)-1 {
		v /= 1024
		i++
	}
	if v < 10 {
		return fmt.Sprintf("%.1f %s", v, units[i])
	}
	return fmt.Sprintf("%.0f %s", v, units[i])
}

// FormatTime renders a modification time compactly. Times within the last
// ~6 months show "Jan 02 15:04", older ones "Jan 02  2006".
func FormatTime(t, now time.Time) string {
	if t.IsZero() {
		return ""
	}
	if now.Sub(t) < 180*24*time.Hour && t.Before(now.Add(24*time.Hour)) {
		return t.Format("Jan 02 15:04")
	}
	return t.Format("Jan 02  2006")
}

// SanitizeName replaces control characters (newlines, escape sequences, ...)
// so that hostile file names cannot corrupt the terminal display.
func SanitizeName(s string) string {
	clean := true
	for _, r := range s {
		if r < 0x20 || r == 0x7f || (r >= 0x80 && r < 0xa0) {
			clean = false
			break
		}
	}
	if clean {
		return s
	}
	var b strings.Builder
	for _, r := range s {
		if r < 0x20 || r == 0x7f || (r >= 0x80 && r < 0xa0) {
			b.WriteRune('?')
		} else {
			b.WriteRune(r)
		}
	}
	return b.String()
}
