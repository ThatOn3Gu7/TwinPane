package compress

import (
	"fmt"
	"os"
	"strings"
)

// Capability is a format together with whether it can be created here.
type Capability struct {
	Format
	Available bool
	Tool      string // absolute path of the external program used (7Z)
	Backend   string // "built in" or the program's name
	Reason    string // why it is unavailable
}

// LookPathFunc finds an executable on $PATH (proc.LookPath in production,
// a fake in tests).
type LookPathFunc func(name string) (string, error)

// Detect reports every format with its availability. Built-in formats are
// always available; 7Z needs one of its programs on $PATH. lookPath may be
// nil (no external program is ever found).
func Detect(lookPath LookPathFunc) []Capability {
	out := make([]Capability, 0, len(table))
	for _, f := range table {
		c := Capability{Format: f, Available: true, Backend: "built in"}
		if f.External() {
			c.Available = false
			for _, t := range f.Tools {
				if lookPath == nil {
					break
				}
				if p, err := lookPath(t); err == nil && p != "" {
					c.Available, c.Tool, c.Backend = true, p, t
					break
				}
			}
			if !c.Available {
				c.Reason = missingToolReason(f)
			}
		}
		out = append(out, c)
	}
	return out
}

// missingToolReason explains which program to install.
func missingToolReason(f Format) string {
	tools := strings.Join(f.Tools, ", ")
	if OnTermux(os.Getenv) {
		return fmt.Sprintf("needs 7-Zip (%s), not found — Termux: pkg install 7zip", tools)
	}
	return fmt.Sprintf("needs 7-Zip (%s), not found on $PATH — install 7-Zip or p7zip", tools)
}

// OnTermux reports whether the process runs inside Termux.
func OnTermux(getenv func(string) string) bool {
	return getenv("TERMUX_VERSION") != "" || strings.Contains(getenv("PREFIX"), "/com.termux/")
}

// Selection describes what is to be compressed.
type Selection struct {
	Items int // number of selected items
	// SingleFile is true when exactly one item is selected and it is a
	// regular file (or a link to one).
	SingleFile bool
}

// Check reports why c cannot be used for sel (nil when it can).
func (c Capability) Check(sel Selection) error {
	if !c.Available {
		return fmt.Errorf("%s is not available: %s", c.Name, c.Reason)
	}
	if sel.Items == 0 {
		return fmt.Errorf("nothing selected")
	}
	if c.Kind == Single && !sel.SingleFile {
		if sel.Items > 1 {
			return fmt.Errorf("%s compresses a single file: %d items are selected — use an archive format (%s, ZIP, …) for several", c.Name, sel.Items, archiveFor(c.Codec))
		}
		return fmt.Errorf("%s compresses a single file, not a folder — use an archive format (%s, ZIP, …)", c.Name, archiveFor(c.Codec))
	}
	return nil
}

// Find returns the capability with the given ID from caps.
func Find(caps []Capability, id ID) (Capability, bool) {
	for _, c := range caps {
		if c.ID == id {
			return c, true
		}
	}
	return Capability{}, false
}

// archiveFor names the TAR format using codec ("TAR.GZ" for Gzip).
func archiveFor(codec Codec) string {
	for _, f := range table {
		if f.Kind == Archive && f.Codec == codec && len(f.Tools) == 0 && f.ID != ZIP {
			return f.Name
		}
	}
	return "TAR"
}
