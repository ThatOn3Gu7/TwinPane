//go:build unix

package foldersize

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/ThatOn3Gu7/TwinPane/internal/storage"
)

func TestLinuxMountBoundaryRequiresMountInfo(t *testing.T) {
	if runtime.GOOS != "linux" && runtime.GOOS != "android" {
		t.Skip("Linux/Android mountinfo policy")
	}
	root := t.TempDir()
	info, err := os.Stat(root)
	if err != nil {
		t.Fatal(err)
	}
	same, known := platformSameFilesystem(root, filepath.Join(root, "child"), info, info, nil, false)
	if same || known {
		t.Fatalf("unknown mount table must fail closed, got same=%v known=%v", same, known)
	}
}

func TestLinuxMountBoundaryComparesMountPoints(t *testing.T) {
	if runtime.GOOS != "linux" && runtime.GOOS != "android" {
		t.Skip("Linux/Android mountinfo policy")
	}
	mounts := []storage.Mount{{Point: "/"}, {Point: "/root"}, {Point: "/root/mnt"}}
	for _, tc := range []struct {
		root, child string
		wantSame    bool
	}{
		{root: "/root", child: "/root/child", wantSame: true},
		{root: "/root", child: "/root/mnt/child", wantSame: false},
	} {
		same, known := platformSameFilesystem(tc.root, tc.child, nil, nil, mounts, true)
		if !known || same != tc.wantSame {
			t.Errorf("mount boundary %q → %q = same %v, known %v; want %v, true", tc.root, tc.child, same, known, tc.wantSame)
		}
	}
}
