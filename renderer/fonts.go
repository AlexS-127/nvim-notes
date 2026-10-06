package main

import (
	"fmt"
	"strings"
)

// A font preset is a CSS font-family stack, picked with the "font" key of config.json
// (or `notesview font NAME`) independently of the theme. It replaces the body, heading
// and interface fonts; code and the number readouts keep the theme's code font.
// Besides these names, "font" accepts any installed family name ("Gill Sans") or a full
// CSS stack (containing a comma). Inter, Lora, Merriweather and Source Serif 4 are bundled
// (web/fonts); the rest are macOS system fonts, or fall back along their stack.
type Font struct{ Name, Desc, Stack string }

const (
	sysSans = `-apple-system, BlinkMacSystemFont, "SF Pro Text", "Segoe UI", system-ui, sans-serif`
	sysMono = `"SF Mono", ui-monospace, Menlo, Consolas, monospace`
)

var fonts = []Font{
	{"default", "whatever the theme sets", ""},
	{"system", "San Francisco, the macOS interface font (Apple Notes)", sysSans},
	{"inter", "Inter, the Notion / Obsidian / Linear sans (bundled)", `"Inter", ` + sysSans},
	{"avenir", "Avenir Next, rounded and friendly (Bear)", `"Avenir Next", Avenir, ` + sysSans},
	{"helvetica", "Helvetica Neue, the neutral classic", `"Helvetica Neue", Helvetica, Arial, sans-serif`},
	{"optima", "Optima, flared humanist sans", `Optima, Candara, "Segoe UI", sans-serif`},
	{"georgia", "Georgia, screen-first serif", `Georgia, "Times New Roman", serif`},
	{"times", "Times New Roman", `"Times New Roman", Times, serif`},
	{"charter", "Charter, sturdy reading serif", `Charter, "Bitstream Charter", Georgia, serif`},
	{"palatino", "Palatino, calligraphic serif", `Palatino, "Palatino Linotype", "Book Antiqua", serif`},
	{"iowan", "Iowan Old Style, the Apple Books serif", `"Iowan Old Style", Palatino, Georgia, serif`},
	{"new-york", "New York, Apple's serif", `ui-serif, "New York", Georgia, serif`},
	{"baskerville", "Baskerville, high-contrast serif", `Baskerville, "Baskerville Old Face", Georgia, serif`},
	{"source-serif", "Source Serif 4 (bundled)", `"Source Serif 4", Georgia, serif`},
	{"lora", "Lora, brushed contemporary serif (bundled)", `"Lora", Georgia, serif`},
	{"merriweather", "Merriweather, sturdy screen serif (bundled)", `"Merriweather", Georgia, serif`},
	{"noteworthy", "Noteworthy, the Apple Notes handwriting", `Noteworthy, "Marker Felt", "Comic Sans MS", cursive`},
	{"typewriter", "American Typewriter", `"American Typewriter", "Courier New", serif`},
	{"sf-mono", "SF Mono", sysMono},
	{"menlo", "Menlo", `Menlo, Monaco, Consolas, monospace`},
	{"jetbrains-mono", "JetBrains Mono (if installed)", `"JetBrains Mono", ` + sysMono},
	{"courier", "Courier New", `"Courier New", Courier, monospace`},
}

func fontKey(s string) string {
	return strings.ReplaceAll(strings.ToLower(strings.TrimSpace(s)), " ", "-")
}

// fontStack turns the config value into a CSS font-family list: "" for none or "default",
// a preset's stack, a sanitised raw stack (contains a comma), or one quoted family name.
func fontStack(name string) string {
	name = strings.TrimSpace(name)
	if name == "" || fontKey(name) == "default" {
		return ""
	}
	for _, f := range fonts {
		if f.Name == fontKey(name) {
			return f.Stack
		}
	}
	clean := strings.Map(func(r rune) rune {
		if strings.ContainsRune(`;{}\<>`+"\n\r", r) {
			return -1
		}
		return r
	}, name)
	if strings.Contains(clean, ",") {
		return clean
	}
	return fmt.Sprintf(`"%s", sans-serif`, strings.ReplaceAll(clean, `"`, ""))
}

func fontNames() string {
	var n []string
	for _, f := range fonts {
		n = append(n, f.Name)
	}
	return strings.Join(n, ", ")
}
