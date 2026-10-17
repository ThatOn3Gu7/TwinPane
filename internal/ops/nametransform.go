package ops

import (
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// Pure name transformations used by bulk rename. They never touch the
// filesystem; PlanBatchRename validates their output.

// SplitExt splits a name into stem and extension (with its dot). Folders and
// names whose only dot is leading (".bashrc") have no extension.
func SplitExt(name string, isDir bool) (stem, ext string) {
	if isDir {
		return name, ""
	}
	trimmed := strings.TrimLeft(name, ".")
	i := strings.LastIndexByte(trimmed, '.')
	if i <= 0 || i == len(trimmed)-1 {
		return name, ""
	}
	cut := len(name) - len(trimmed) + i
	return name[:cut], name[cut:]
}

// ReplaceNames replaces every match of find in each name. With useRegex,
// find is a Go regular expression (RE2 syntax) and repl may use $1 / ${name};
// otherwise both are literal text. Matching ignores case unless matchCase.
func ReplaceNames(names []string, find, repl string, useRegex, matchCase bool) ([]string, error) {
	if find == "" {
		return nil, errors.New("enter the text to find")
	}
	pat := find
	if !useRegex {
		pat = regexp.QuoteMeta(find)
		repl = strings.ReplaceAll(repl, "$", "$$")
	}
	if !matchCase {
		pat = "(?i)" + pat
	}
	re, err := regexp.Compile(pat)
	if err != nil {
		return nil, fmt.Errorf("invalid regular expression: %v", err)
	}
	out := make([]string, len(names))
	for i, n := range names {
		out[i] = re.ReplaceAllString(n, repl)
	}
	return out, nil
}

// TemplateItem is the input of ApplyTemplate.
type TemplateItem struct {
	Name  string
	IsDir bool
}

// ApplyTemplate builds names from a pattern:
//
//	{name}  the name without its extension
//	{ext}   the extension including the dot ("" if none)
//	{n}     a counter: 1, 2, 3 … in list order
//	{n:3}   the counter zero-padded to 3 digits (001, 002 …)
//	{{ }}   literal braces
func ApplyTemplate(items []TemplateItem, tpl string) ([]string, error) {
	type part struct {
		lit   string
		field string // "", "name", "ext", "n"
		width int
	}
	var parts []part
	var lit strings.Builder
	for i := 0; i < len(tpl); i++ {
		c := tpl[i]
		switch {
		case c == '{' && i+1 < len(tpl) && tpl[i+1] == '{':
			lit.WriteByte('{')
			i++
		case c == '}' && i+1 < len(tpl) && tpl[i+1] == '}':
			lit.WriteByte('}')
			i++
		case c == '{':
			end := strings.IndexByte(tpl[i:], '}')
			if end < 0 {
				return nil, errors.New("unclosed { in pattern (use {{ for a literal brace)")
			}
			spec := tpl[i+1 : i+end]
			p := part{}
			switch {
			case spec == "name" || spec == "ext" || spec == "n":
				p.field = spec
			case strings.HasPrefix(spec, "n:"):
				w, err := strconv.Atoi(spec[2:])
				if err != nil || w < 1 || w > 9 {
					return nil, fmt.Errorf("invalid counter {%s}: use {n:1} … {n:9}", spec)
				}
				p.field, p.width = "n", w
			default:
				return nil, fmt.Errorf("unknown placeholder {%s}: use {name}, {ext}, {n} or {n:3}", spec)
			}
			if lit.Len() > 0 {
				parts = append(parts, part{lit: lit.String()})
				lit.Reset()
			}
			parts = append(parts, p)
			i += end
		case c == '}':
			return nil, errors.New("unmatched } in pattern (use }} for a literal brace)")
		default:
			lit.WriteByte(c)
		}
	}
	if lit.Len() > 0 {
		parts = append(parts, part{lit: lit.String()})
	}
	out := make([]string, len(items))
	for i, it := range items {
		stem, ext := SplitExt(it.Name, it.IsDir)
		var b strings.Builder
		for _, p := range parts {
			switch p.field {
			case "":
				b.WriteString(p.lit)
			case "name":
				b.WriteString(stem)
			case "ext":
				b.WriteString(ext)
			case "n":
				b.WriteString(fmt.Sprintf("%0*d", p.width, i+1))
			}
		}
		out[i] = b.String()
	}
	return out, nil
}

// LowerNames / UpperNames change the case of whole names.
func LowerNames(names []string) []string { return mapNames(names, strings.ToLower) }
func UpperNames(names []string) []string { return mapNames(names, strings.ToUpper) }

func mapNames(names []string, f func(string) string) []string {
	out := make([]string, len(names))
	for i, n := range names {
		out[i] = f(n)
	}
	return out
}

// ParseNameList parses names edited in a text editor, one per line, for
// count items. A trailing newline and Windows line endings are accepted;
// the number of lines must match exactly so names stay in their rows.
func ParseNameList(data string, count int) ([]string, error) {
	data = strings.ReplaceAll(data, "\r\n", "\n")
	data = strings.TrimSuffix(data, "\n")
	lines := strings.Split(data, "\n")
	if data == "" {
		lines = nil
	}
	if len(lines) != count {
		return nil, fmt.Errorf("the file must keep exactly %d lines, one name per item in the same order; it has %d", count, len(lines))
	}
	return lines, nil
}
