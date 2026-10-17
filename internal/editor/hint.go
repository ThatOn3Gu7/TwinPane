package editor

import (
	"path/filepath"
	"strings"
)

// SaveHint tells the user how to save and quit a particular editor. TwinPane
// cannot draw inside an external editor (the editor owns the terminal), so
// the hint is shown just before the editor starts and again afterwards if
// the file was left unchanged.
type SaveHint struct {
	Name  string // editor name for messages ("Neovim")
	Known bool   // false: generic advice only
	Short string // one line: "Esc, then :w save · :q quit · :wq save & quit"
	Save  string // shortest way to save, for "not saved" messages
}

// family keys are matched against the executable's base name.
var saveHints = []struct {
	names []string
	hint  SaveHint
}{
	{[]string{"nvim"}, SaveHint{Name: "Neovim", Known: true,
		Short: "Esc, then :w save · :q quit · :wq save & quit · :q! quit without saving",
		Save:  "Esc, then :w (or :wq to save and quit)"}},
	{[]string{"vim", "gvim", "mvim", "vimx"}, SaveHint{Name: "Vim", Known: true,
		Short: "Esc, then :w save · :q quit · :wq save & quit · :q! quit without saving",
		Save:  "Esc, then :w (or :wq to save and quit)"}},
	{[]string{"vi", "nvi", "elvis", "view"}, SaveHint{Name: "vi", Known: true,
		Short: "Esc, then :w save · :q quit · :wq save & quit · :q! quit without saving",
		Save:  "Esc, then :w (or :wq to save and quit)"}},
	{[]string{"hx", "helix"}, SaveHint{Name: "Helix", Known: true,
		Short: "Esc, then :w save · :q quit · :wq save & quit · :q! quit without saving",
		Save:  "Esc, then :w (or :wq to save and quit)"}},
	{[]string{"kak"}, SaveHint{Name: "Kakoune", Known: true,
		Short: "Esc, then :w save · :q quit · :wq save & quit · :q! quit without saving",
		Save:  "Esc, then :w (or :wq to save and quit)"}},
	{[]string{"nano", "rnano", "pico"}, SaveHint{Name: "nano", Known: true,
		Short: "Ctrl+O then Enter save · Ctrl+X quit (asks about unsaved changes)",
		Save:  "Ctrl+O then Enter (Ctrl+X quits)"}},
	{[]string{"micro"}, SaveHint{Name: "micro", Known: true,
		Short: "Ctrl+S save · Ctrl+Q quit",
		Save:  "Ctrl+S (Ctrl+Q quits)"}},
	{[]string{"emacs", "emacs-nox", "emacsclient", "mg"}, SaveHint{Name: "Emacs", Known: true,
		Short: "Ctrl+X Ctrl+S save · Ctrl+X Ctrl+C quit",
		Save:  "Ctrl+X Ctrl+S (Ctrl+X Ctrl+C quits)"}},
	{[]string{"joe"}, SaveHint{Name: "Joe", Known: true,
		Short: "Ctrl+K D save · Ctrl+K X save & quit · Ctrl+C quit without saving",
		Save:  "Ctrl+K D (Ctrl+K X saves and quits)"}},
	{[]string{"mcedit"}, SaveHint{Name: "mcedit", Known: true,
		Short: "F2 save · F10 quit",
		Save:  "F2 (F10 quits)"}},
	{[]string{"code", "codium", "subl", "gedit", "kate", "notepad"}, SaveHint{Name: "", Known: true,
		Short: "Ctrl+S save in the editor window, then close it (or its tab) to return to TwinPane",
		Save:  "Ctrl+S in the editor window, then close it"}},
}

// family returns the normalised base name: lower case, without ".exe" and
// without a ".suffix" such as vim.basic, vim.tiny or nvim.appimage.
func family(path string) string {
	b := strings.ToLower(filepath.Base(path))
	if i := strings.LastIndexAny(b, `/\`); i >= 0 { // Windows paths on any OS
		b = b[i+1:]
	}
	b = strings.TrimSuffix(b, ".exe")
	if i := strings.IndexByte(b, '.'); i > 0 {
		b = b[:i]
	}
	return b
}

func lookupHint(fam string) (SaveHint, bool) {
	for _, h := range saveHints {
		for _, n := range h.names {
			if n == fam {
				return h.hint, true
			}
		}
	}
	return SaveHint{}, false
}

// Hint returns the save/quit instructions for e. The executable is
// identified by its base name; when that is unknown (a wrapper such as
// "editor" or a symlink) the symlink target is tried. Unknown editors get a
// generic hint that points at the editor's own help.
func (e Editor) Hint() SaveHint {
	name := e.Label
	fam := family(e.Path)
	h, ok := lookupHint(fam)
	if !ok && e.Path != "" {
		if real, err := filepath.EvalSymlinks(e.Path); err == nil {
			h, ok = lookupHint(family(real))
		}
	}
	if !ok {
		if name == "" {
			name = e.Display()
		}
		return SaveHint{Name: name, Known: false,
			Short: "see " + name + "'s own help for how to save and quit; TwinPane continues when it exits",
			Save:  "see " + name + "'s own help for how to save"}
	}
	if h.Name == "" {
		h.Name = name
		if h.Name == "" {
			h.Name = e.Display()
		}
	}
	return h
}
