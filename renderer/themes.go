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
//
// GhosttyLight / GhosttyDark are the matching Ghostty themes, written to Ghostty's config when the
// theme is chosen and sync_ghostty is on (Neovim draws transparently over the terminal, so the two
// must agree). Neovim's matching colorschemes are in nvim/lua/themesync.lua.
type Theme struct {
	Name, Desc                string
	Light, Dark, Only         string // chroma style for light / dark; Only forces an appearance
	GhosttyLight, GhosttyDark string
}

var themes = []Theme{
	{Name: "default", Desc: "clean GitHub-like reading theme", Light: "github", Dark: "github-dark", GhosttyLight: "GitHub Light Default", GhosttyDark: "GitHub Dark Default"},
	{Name: "nord", Desc: "arctic blue-greys", Light: "github", Dark: "nord", GhosttyLight: "Nord Light", GhosttyDark: "Nord"},
	{Name: "gruvbox", Desc: "warm retro earth tones", Light: "gruvbox-light", Dark: "gruvbox", GhosttyLight: "Gruvbox Light", GhosttyDark: "Gruvbox Dark"},
	{Name: "solarized", Desc: "Ethan Schoonover's low-contrast classic", Light: "solarized-light", Dark: "solarized-dark", GhosttyLight: "iTerm2 Solarized Light", GhosttyDark: "iTerm2 Solarized Dark"},
	{Name: "catppuccin", Desc: "soft pastels (latte / mocha)", Light: "catppuccin-latte", Dark: "catppuccin-mocha", GhosttyLight: "Catppuccin Latte", GhosttyDark: "Catppuccin Mocha"},
	{Name: "cappuccino", Desc: "catppuccin colours in Times New Roman, monospace numbers", Light: "catppuccin-latte", Dark: "catppuccin-mocha", GhosttyLight: "Catppuccin Latte", GhosttyDark: "Catppuccin Mocha"},
	{Name: "rose-pine", Desc: "dusky rose and gold (dawn / moon)", Light: "rose-pine-dawn", Dark: "rose-pine", GhosttyLight: "Rose Pine Dawn", GhosttyDark: "Rose Pine"},
	{Name: "tokyo-night", Desc: "neon-lit city night (day / night)", Light: "tokyonight-day", Dark: "tokyonight-night", GhosttyLight: "TokyoNight Day", GhosttyDark: "TokyoNight Night"},
	{Name: "horizon", Desc: "white, orange and blue: colourful headings, tinted sidebar", Light: "xcode", Dark: "xcode-dark", GhosttyLight: "Xcode Light", GhosttyDark: "Xcode Dark"},
	{Name: "dracula", Desc: "purple and pink on charcoal (dark only)", Dark: "dracula", Only: "dark", GhosttyDark: "Dracula"},
	{Name: "borland", Desc: "Turbo Pascal blue desk, yellow text, double rules, monospace (dark only)", Dark: "vim", Only: "dark", GhosttyDark: "Borland"},
	{Name: "terminal", Desc: "green phosphor CRT, monospace, scanlines (dark only)", Dark: "monokai", Only: "dark", GhosttyDark: "Retro"},
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
//	{ "theme_light": "borland", "theme_dark": "catppuccin", "appearance": "auto", "font": "inter",
//	  "opacity": 0.9, "sync_ghostty": true }
//
// The theme used depends on the appearance: theme_light while it is light, theme_dark while it is
// dark (Neovim reads the same keys, see nvim/lua/themesync.lua). "theme" is the older single
// choice and fills either slot that is missing. appearance is auto (follow the system), light or
// dark. font is optional and independent of the theme. opacity is the native app's background
// (0 or missing = Ghostty's background-opacity). sync_ghostty (default on) writes the matching
// Ghostty themes whenever the themes are changed from the Settings page.
type Config struct {
	Theme       string  `json:"theme"`
	ThemeLight  string  `json:"theme_light,omitempty"`
	ThemeDark   string  `json:"theme_dark,omitempty"`
	Appearance  string  `json:"appearance"`
	Font        string  `json:"font,omitempty"` // a preset name, any installed family name or a CSS stack, see fonts.go
	Opacity     float64 `json:"opacity,omitempty"`
	SyncGhostty *bool   `json:"sync_ghostty,omitempty"`
}

// GhosttySync is whether theme changes are written to Ghostty's config (default on).
func (c Config) GhosttySync() bool { return c.SyncGhostty == nil || *c.SyncGhostty }

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
	theme := func(name string, def string) string {
		if name == "" {
			return def
		}
		if t, ok := findTheme(name); ok {
			return t.Name
		}
		warn = fmt.Sprintf("unknown theme %q (available: %s)", name, themeNames())
		return def
	}
	c.Theme = theme(raw.Theme, c.Theme)
	c.ThemeLight = theme(raw.ThemeLight, c.Theme)
	c.ThemeDark = theme(raw.ThemeDark, c.Theme)
	c.Font = strings.TrimSpace(raw.Font)
	if raw.Opacity > 0 {
		c.Opacity = min(1, max(0.2, raw.Opacity))
	}
	c.SyncGhostty = raw.SyncGhostty
	switch a := strings.ToLower(raw.Appearance); a {
	case "light", "dark":
		c.Appearance = a
	case "", "auto":
	default:
		warn = fmt.Sprintf("appearance %q must be auto, light or dark", raw.Appearance)
	}
	return c, warn
}

// Resolved is the palette and forced mode for one appearance (light or dark): that slot's
// theme, and its own appearance when it only has one. With appearance "" it uses the configured
// appearance, and the dark slot while that is auto.
func (c Config) Resolved(appearance ...string) (palette, mode string) {
	a := c.Appearance
	if len(appearance) > 0 && appearance[0] != "" {
		a = appearance[0]
	}
	name := c.ThemeDark
	if a == "light" {
		name = c.ThemeLight
	}
	if name == "" {
		name = c.Theme
	}
	t, _ := findTheme(name)
	mode = ""
	if a != "auto" {
		mode = a
	}
	if t.Only != "" {
		mode = t.Only
	}
	return t.Name, mode
}

// SaveConfig writes config.json (the slots replace the older single "theme").
func SaveConfig(c Config) error {
	if c.ThemeLight == c.ThemeDark && c.ThemeLight != "" {
		c.Theme = c.ThemeLight
	} else if c.ThemeDark != "" {
		c.Theme = c.ThemeDark
	}
	b, _ := json.MarshalIndent(c, "", "  ")
	if err := os.MkdirAll(configDir(), 0o755); err != nil {
		return err
	}
	return writeAtomic(configPath(), append(b, '\n'))
}

// ghosttyConfigPath is the Ghostty config file (its target when it is a link), or "" for none.
// NOTESVIEW_GHOSTTY_CONFIG overrides it ("off" = none; the tests set that).
func ghosttyConfigPath() string {
	if p := os.Getenv("NOTESVIEW_GHOSTTY_CONFIG"); p != "" {
		if p == "off" {
			return ""
		}
		return p
	}
	home, _ := os.UserHomeDir()
	for _, p := range []string{filepath.Join(home, ".config", "ghostty", "config"), filepath.Join(home, "Library", "Application Support", "com.mitchellh.ghostty", "config")} {
		if real, err := filepath.EvalSymlinks(p); err == nil {
			return real
		}
	}
	return ""
}

// ghosttyThemeLine is Ghostty's theme setting for the two slots ("light:X,dark:Y").
func ghosttyThemeLine(c Config) string {
	pick := func(name string, light bool) string {
		t, _ := findTheme(name)
		if light && t.GhosttyLight != "" {
			return t.GhosttyLight
		}
		if t.GhosttyDark != "" {
			return t.GhosttyDark
		}
		return t.GhosttyLight
	}
	return fmt.Sprintf("theme = light:%s,dark:%s", pick(c.ThemeLight, true), pick(c.ThemeDark, false))
}

// SyncGhosttyTheme rewrites the theme line of Ghostty's config (adding one if missing) and asks
// running Ghostty to reload. It reports whether the file changed.
func SyncGhosttyTheme(c Config, path string) (bool, error) {
	if path == "" {
		return false, nil
	}
	b, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return false, err
	}
	want := ghosttyThemeLine(c)
	lines := strings.Split(strings.TrimRight(string(b), "\n"), "\n")
	found := false
	for i, l := range lines {
		if k, _, ok := strings.Cut(l, "="); ok && strings.TrimSpace(k) == "theme" && !strings.HasPrefix(strings.TrimSpace(l), "#") {
			if strings.TrimSpace(l) == want && !found {
				found = true
				continue
			}
			if !found {
				lines[i], found = want, true
			} else {
				lines[i] = "# " + l // a later theme line would win
			}
		}
	}
	if !found {
		if len(lines) == 1 && lines[0] == "" {
			lines = nil
		}
		lines = append(lines, want)
	}
	out := strings.Join(lines, "\n")
	if len(b) == 0 || strings.HasSuffix(string(b), "\n") {
		out += "\n" // keep the file's own ending
	}
	if out == string(b) {
		return false, nil
	}
	if err := writeAtomic(path, []byte(out)); err != nil {
		return false, err
	}
	reloadGhostty()
	return true, nil
}
