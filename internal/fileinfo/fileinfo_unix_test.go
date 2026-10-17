//go:build unix

package fileinfo

import (
	"strings"
	"testing"
)

func TestAndroidNames(t *testing.T) {
	for id, want := range map[int]string{10234: "u0_a234", 1010234: "u10_a234", 0: "root", 2000: "shell", 50234: "u0_a234_cache", 4242: ""} {
		if got := androidName(id); got != want {
			t.Errorf("androidName(%d) = %q, want %q", id, got, want)
		}
	}
	if !strings.HasPrefix(TypeName(0o755), "regular file (executable)") {
		t.Fatal(TypeName(0o755))
	}
}
