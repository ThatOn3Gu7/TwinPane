package proc

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestNoSeccompUnsafeProcessCalls keeps os/exec's lookup and process start
// out of TwinPane's code (see the package documentation): on Android they
// make system calls that kill the program with SIGSYS. Windows-only files
// are exempt.
func TestNoSeccompUnsafeProcessCalls(t *testing.T) {
	forbidden := []string{"exec.LookPath(", "exec.Command(", "exec.CommandContext(", ".Cmd.Run()", ".Cmd.Start()",
		"os.StartProcess(", ".Output()", ".CombinedOutput()", "tea.ExecProcess("}
	exempt := map[string]bool{"spawn_windows.go": true, "process_windows.go": true, "process_stub.go": true}
	root := filepath.Join("..", "..")
	checked := 0
	err := filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() && (d.Name() == "dist" || strings.HasPrefix(d.Name(), ".")) && p != root {
			return filepath.SkipDir
		}
		if d.IsDir() || !strings.HasSuffix(p, ".go") || strings.HasSuffix(p, "_test.go") || exempt[d.Name()] {
			return nil
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		checked++
		for i, line := range strings.Split(string(data), "\n") {
			code := line
			if j := strings.Index(code, "//"); j >= 0 {
				code = code[:j]
			}
			for _, f := range forbidden {
				if strings.Contains(code, f) {
					t.Errorf("%s:%d uses %s - use proc.LookPath / proc.Start instead", p, i+1, f)
				}
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if checked < 30 {
		t.Fatalf("only %d files checked: wrong root?", checked)
	}
}
