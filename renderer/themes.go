package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// A theme is a block of CSS variables in web/themes.css, selected with
// html[data-palette="<name>"], plus the chroma styles used for code.
// Only is "light" or "dark" for themes that exist in one appearance.
type Theme struct {
	Name, Desc        string
	Light, Dark, Only string // chroma style for light / dark; Only forces an appearance
}

var themes = []Theme{
	{Name: "default", Desc: "clean GitHub-like reading theme", Light: "github", Dark: "github-dark"},
	{Name: "nord", Desc: "arctic blue-greys", Light: "github", Dark: "nord"},
	{Name: "gruvbox", Desc: "warm retro earth tones", Light: "gruvbox-light", Dark: "gruvbox"},
	{Name: "solarized", Desc: "Ethan Schoonover's low-contrast classic", Light: "solarized-light", Dark: "solarized-dark"},
	{Name: "catppuccin", Desc: "soft pastels (latte / mocha)", Light: "catppuccin-latte", Dark: "catppuccin-mocha"},
	{Name: "rose-pine", Desc: "dusky rose and gold (dawn / moon)", Light: "rose-pine-dawn", Dark: "rose-pine"},
	{Name: "tokyo-night", Desc: "neon-lit city night (day / night)", Light: "tokyonight-day", Dark: "tokyonight-night"},
	{Name: "dracula", Desc: "purple and pink on charcoal (dark only)", Dark: "dracula", Only: "dark"},
	{Name: "terminal", Desc: "green phosphor CRT, monospace, scanlines (dark only)", Dark: "monokai", Only: "dark"},
}

func findTheme(name string) (Theme, bool) {
	name = strings.ToLower(strings.TrimSpace(name))
	for _, t := range themes {
		if t.Name == name {
			return t, true
		}
	}
	return themes[0], false
}

func themeNames() string {
	var n []string
	for _, t := range themes {
		n = append(n, t.Name)
	}
	sort.Strings(n)
	return strings.Join(n, ", ")
}

// Config is ~/.config/notesview/config.json:
//
//	{ "theme": "nord", "appearance": "auto" }
//
// appearance is auto (follow the system), light or dark.
type Config struct {
	Theme      string `json:"theme"`
	Appearance string `json:"appearance"`
}

func configPath() string { return filepath.Join(configDir(), "config.json") }

// LoadConfig never fails: a missing or broken file gives the defaults, and
// the second result is a warning for an unknown theme or unreadable file.
func LoadConfig() (Config, string) {
	c := Config{Theme: "default", Appearance: "auto"}
	b, err := os.ReadFile(configPath())
	if err != nil {
		return c, ""
	}
	var raw Config
	if err := json.Unmarshal(b, &raw); err != nil {
		return c, "config.json is not valid JSON: " + err.Error()
	}
	warn := ""
	if raw.Theme != "" {
		if t, ok := findTheme(raw.Theme); ok {
			c.Theme = t.Name
		} else {
			warn = fmt.Sprintf("unknown theme %q (available: %s)", raw.Theme, themeNames())
		}
	}
	switch a := strings.ToLower(raw.Appearance); a {
	case "light", "dark":
		c.Appearance = a
	case "", "auto":
	default:
		warn = fmt.Sprintf("appearance %q must be auto, light or dark", raw.Appearance)
	}
	return c, warn
}

// Resolved is what the page needs: the palette and the forced mode ("" = follow the system).
func (c Config) Resolved() (palette, mode string) {
	t, _ := findTheme(c.Theme)
	mode = ""
	if c.Appearance != "auto" {
		mode = c.Appearance
	}
	if t.Only != "" {
		mode = t.Only
	}
	return t.Name, mode
}
