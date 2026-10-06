package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestThemeSlotsAndGhosttySync(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	os.MkdirAll(filepath.Join(dir, "notesview"), 0o755)
	os.WriteFile(filepath.Join(dir, "notesview", "config.json"), []byte(`{"theme":"borland","appearance":"auto"}`), 0o644)
	c, _ := LoadConfig()
	if c.ThemeLight != "borland" || c.ThemeDark != "borland" {
		t.Fatalf("old single theme fills both slots: %+v", c)
	}
	light, dark := "gruvbox", "catppuccin"
	c, changed, err := ApplyAppearance(AppearancePatch{ThemeLight: &light, ThemeDark: &dark})
	if err != nil || !changed {
		t.Fatal(err, changed)
	}
	if p, m := c.Resolved("light"); p != "gruvbox" || m != "light" {
		t.Fatalf("light slot %s %s", p, m)
	}
	if p, _ := c.Resolved("dark"); p != "catppuccin" {
		t.Fatalf("dark slot %s", p)
	}
	bad := "nope"
	if _, _, err := ApplyAppearance(AppearancePatch{ThemeDark: &bad}); err == nil {
		t.Fatal("unknown theme accepted")
	}
	op := 0.05
	c, _, _ = ApplyAppearance(AppearancePatch{Opacity: &op})
	if c.Opacity != 0.2 {
		t.Fatalf("opacity clamped: %v", c.Opacity)
	}
	if info := themeInfo(); info["light"].(map[string]string)["palette"] != "gruvbox" || info["opacity"] != 0.2 {
		t.Fatalf("theme info %v", info)
	}

	gc := filepath.Join(dir, "ghostty")
	os.WriteFile(gc, []byte("font-size = 14\ntheme = light:Borland,dark:Catppuccin Mocha\n# theme = old\nbackground-opacity = 0.75\n"), 0o644)
	t.Setenv("NOTESVIEW_GHOSTTY_CONFIG", gc)
	if ok, err := SyncGhosttyTheme(c, gc); !ok || err != nil {
		t.Fatal(ok, err)
	}
	b, _ := os.ReadFile(gc)
	want := "font-size = 14\ntheme = light:Gruvbox Light,dark:Catppuccin Mocha\n# theme = old\nbackground-opacity = 0.75\n"
	if string(b) != want {
		t.Fatalf("ghostty config:\n%s", b)
	}
	if ok, _ := SyncGhosttyTheme(c, gc); ok {
		t.Fatal("unchanged config rewritten")
	}
	if ghosttyOpacity() != 0.75 {
		t.Fatalf("ghostty opacity %v", ghosttyOpacity())
	}
	// a dark-only theme in the light slot uses its dark Ghostty theme
	b1 := "borland"
	c, _, _ = ApplyAppearance(AppearancePatch{ThemeLight: &b1})
	if l := ghosttyThemeLine(c); l != "theme = light:Borland,dark:Catppuccin Mocha" {
		t.Fatalf("line %q", l)
	}
	if !strings.Contains(string(b), "font-size") {
		t.Fatal("other lines lost")
	}
}

func TestSetRoutineItems(t *testing.T) {
	s := newTestStore(t, map[string]string{"inbox.md": "# Inbox\n"})
	items, err := s.SetRoutineItems([]RoutineItem{{Label: "Stretch"}, {Label: "Stretch"}, {ID: "shower", Label: " Shower "}, {Label: ""}, {Label: "Call", Kind: routineForecast}})
	if err != nil || len(items) != 4 || items[0].ID != "stretch" || items[1].ID != "stretch-2" || items[2].Label != "Shower" || items[3].Kind != routineForecast {
		t.Fatalf("%+v %v", items, err)
	}
	if got := s.RoutineItems(); len(got) != 4 {
		t.Fatalf("read back %+v", got)
	}
	if _, err := s.SetRoutineItems([]RoutineItem{{Label: "a", Kind: routineForecast}, {Label: "b", Kind: routineForecast}}); err == nil {
		t.Fatal("two forecast items accepted")
	}
}
