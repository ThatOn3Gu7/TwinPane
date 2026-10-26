package fileext

import (
	"strings"
	"testing"
)

func TestCatalogIsOrganisedAndUnique(t *testing.T) {
	cats := Categories()
	if len(cats) < 10 {
		t.Fatalf("categories = %d, want the extensions grouped into many categories", len(cats))
	}
	if n := Count(); n < 150 {
		t.Fatalf("only %d predefined extensions, want a large catalogue", n)
	}
	seen := map[string]string{}
	for _, c := range cats {
		if c.Name == "" || len(c.Extensions) == 0 {
			t.Fatalf("empty category: %+v", c)
		}
		for _, e := range c.Extensions {
			if !strings.HasPrefix(e.Ext, ".") || e.Ext != strings.ToLower(e.Ext) {
				t.Fatalf("%q in %s: extensions must be lower case with a leading dot", e.Ext, c.Name)
			}
			if e.Name == "" {
				t.Fatalf("%s has no name", e.Ext)
			}
			if other, dup := seen[e.Ext]; dup {
				t.Fatalf("%s listed twice: %s and %s", e.Ext, other, c.Name)
			}
			seen[e.Ext] = c.Name
		}
	}
}

func TestFindIsCaseInsensitiveAndForgiving(t *testing.T) {
	for _, q := range []string{".md", "md", "MD", " .Md "} {
		e, ok := Find(q)
		if !ok || e.Ext != ".md" || e.Name != "Markdown" {
			t.Fatalf("Find(%q) = %+v, %v", q, e, ok)
		}
	}
	if _, ok := Find(".nope"); ok {
		t.Fatal("an unknown extension must not be found")
	}
}

func TestSearchFiltersAndRanks(t *testing.T) {
	// an exact extension match comes first
	res := Search("md")
	if len(res) == 0 || res[0].Extensions[0].Ext != ".md" {
		t.Fatalf("search md: first hit %+v", res)
	}
	// the leading dot is optional and case does not matter
	if r := Search(".PNG"); len(r) == 0 || r[0].Extensions[0].Ext != ".png" {
		t.Fatalf("search .PNG: %+v", r)
	}
	// a category name keeps the whole category
	images := Search("images")
	if len(images) == 0 || images[0].Name != "Images" {
		t.Fatalf("search images: %+v", images)
	}
	if got := len(images[0].Extensions); got < 15 {
		t.Fatalf("images category has %d extensions", got)
	}
	// empty query returns everything, in catalogue order
	all := Search("   ")
	if len(all) != len(Categories()) {
		t.Fatalf("empty search returned %d categories, want %d", len(all), len(Categories()))
	}
	// nothing matches
	if res := Search("zzzznotaformat"); len(res) != 0 {
		t.Fatalf("unexpected matches: %+v", res)
	}
	// descriptions are searchable too
	if res := Search("spreadsheet"); len(res) == 0 {
		t.Fatal("descriptions must be searchable")
	}
}

func TestNormalizeExtension(t *testing.T) {
	ok := map[string]string{
		"":          "",
		"   ":       "",
		".":         "",
		"...":       "",
		"md":        ".md",
		".md":       ".md",
		" MD ":      ".MD",
		"tar.gz":    ".tar.gz",
		".tar.gz":   ".tar.gz",
		"c++":       ".c++",
		"my-ext_2":  ".my-ext_2",
		"backup.1":  ".backup.1",
		"данные":    ".данные",
		"  .JSON  ": ".JSON",
	}
	for in, want := range ok {
		got, err := NormalizeExtension(in)
		if err != nil || got != want {
			t.Fatalf("NormalizeExtension(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	bad := []string{"a/b", `a\b`, "a b", "a:b", "a*b", "a?b", "a..b", "a.b c", strings.Repeat("x", maxExtLen+1), "a\x00b"}
	for _, in := range bad {
		if got, err := NormalizeExtension(in); err == nil {
			t.Fatalf("NormalizeExtension(%q) = %q, want an error", in, got)
		}
	}
}

func TestSplitExtension(t *testing.T) {
	cases := []struct{ name, stem, ext string }{
		{"notes", "notes", ""},
		{"notes.md", "notes", ".md"},
		{"notes.tar.gz", "notes.tar", ".gz"},
		{"a.b.c", "a.b", ".c"},
		{".bashrc", ".bashrc", ""},
		{".config.json", ".config", ".json"},
		{"notes.", "notes.", ""},
		{"", "", ""},
	}
	for _, c := range cases {
		stem, ext := SplitExtension(c.name)
		if stem != c.stem || ext != c.ext {
			t.Fatalf("SplitExtension(%q) = %q, %q; want %q, %q", c.name, stem, ext, c.stem, c.ext)
		}
	}
}

func TestFinalName(t *testing.T) {
	cases := []struct{ name, ext, want string }{
		// no preset: the typed name is used as written
		{"notes", "", "notes"},
		{"notes.md", "", "notes.md"},
		{"archive.tar.gz", "", "archive.tar.gz"},
		{"  spaced  ", "", "spaced"},
		// a bare name gets the extension
		{"notes", ".md", "notes.md"},
		// a matching extension is not duplicated (notes.md + .md)
		{"notes.md", ".md", "notes.md"},
		{"notes.MD", ".md", "notes.MD"},
		// a different extension is replaced, not stacked
		{"notes.txt", ".md", "notes.md"},
		{"archive.zip", ".tar.gz", "archive.tar.gz"},
		{"a.b.txt", ".md", "a.b.md"},
		// dotfiles keep their name and gain the extension
		{".bashrc", ".md", ".bashrc.md"},
		{".env", ".env", ".env"},
		// a trailing dot is not doubled
		{"notes.", ".md", "notes.md"},
		// an empty name plus an extension is just the extension
		{"", ".md", ".md"},
		{"   ", ".md", ".md"},
	}
	for _, c := range cases {
		if got := FinalName(c.name, c.ext); got != c.want {
			t.Fatalf("FinalName(%q, %q) = %q, want %q", c.name, c.ext, got, c.want)
		}
	}
}
