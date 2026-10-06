package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestThemesHaveCSSAndChroma(t *testing.T) {
	css, err := webFS.ReadFile("web/themes.css")
	if err != nil {
		t.Fatal(err)
	}
	chroma := ChromaCSS()
	for _, th := range themes {
		if th.Name == "default" {
			continue
		}
		for _, mode := range []string{"light", "dark"} {
			if mode == "light" && th.Only == "dark" {
				continue
			}
			sel := `[data-palette="` + th.Name + `"][data-theme="` + mode + `"]`
			if !strings.Contains(string(css), sel) {
				t.Errorf("themes.css has no block for %s", sel)
			}
			if !strings.Contains(chroma, `:root[data-palette="`+th.Name+`"][data-theme="`+mode+`"]`) {
				t.Errorf("no chroma css for %s %s", th.Name, mode)
			}
		}
	}
}

func TestLoadConfig(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	if c, w := LoadConfig(); c.Theme != "default" || w != "" {
		t.Fatalf("missing file: %+v %q", c, w)
	}
	write := func(s string) {
		os.MkdirAll(filepath.Join(dir, "notesview"), 0o755)
		os.WriteFile(filepath.Join(dir, "notesview", "config.json"), []byte(s), 0o644)
	}
	write(`{"theme":"Nord","appearance":"light"}`)
	c, w := LoadConfig()
	if p, m := c.Resolved(); p != "nord" || m != "light" || w != "" {
		t.Fatalf("got %s %s %q", p, m, w)
	}
	write(`{"theme":"dracula","appearance":"light"}`)
	c, _ = LoadConfig()
	if _, m := c.Resolved(); m != "dark" {
		t.Fatalf("dracula must force dark, got %q", m)
	}
	write(`{"theme":"nope"}`)
	if c, w := LoadConfig(); c.Theme != "default" || !strings.Contains(w, "unknown theme") {
		t.Fatalf("unknown theme: %+v %q", c, w)
	}
	write(`{bad`)
	if _, w := LoadConfig(); w == "" {
		t.Fatal("expected a warning for bad JSON")
	}
}

func TestFolderColors(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	if err := setFolderColor("act-200", "blue"); err != nil {
		t.Fatal(err)
	}
	if err := setFolderColor("personal", "nope"); err != nil { // stored, but filtered out on load
		t.Fatal(err)
	}
	m := loadFolderColors()
	if m["act-200"] != "blue" || len(m) != 1 {
		t.Fatalf("got %v", m)
	}
	_ = setFolderColor("act-200", "")
	if len(loadFolderColors()) != 0 {
		t.Fatal("colour not cleared")
	}
}

func TestFontStack(t *testing.T) {
	for in, want := range map[string]string{
		"":               "",
		"default":        "",
		" Default ":      "",
		"georgia":        `Georgia, "Times New Roman", serif`,
		"Source Serif":   `"Source Serif 4", Georgia, serif`, // spaces match the preset name
		"Gill Sans":      `"Gill Sans", sans-serif`,
		`Ev"il;{}`:       `"Evil", sans-serif`,
		`"Inter", Arial`: `"Inter", Arial`,
	} {
		got := fontStack(in)
		if got != want {
			t.Errorf("fontStack(%q) = %q, want %q", in, got, want)
		}
	}
	if fontStack("INTER") != fontStack("inter") || fontStack("jetbrains mono") != fontStack("jetbrains-mono") {
		t.Error("preset names ignore case and spaces")
	}
}

func TestFontConfigRoundTrip(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	var out, errb strings.Builder
	if code := runCommand("font", []string{"Gill Sans"}, &out, &errb); code != 0 {
		t.Fatalf("font: %d %s", code, errb.String())
	}
	if c, _ := LoadConfig(); c.Font != "Gill Sans" || themeInfo()["font"] != `"Gill Sans", sans-serif` {
		t.Errorf("config: %+v info %v", c, themeInfo())
	}
	runCommand("theme", []string{"nord"}, &out, &errb) // choosing a theme keeps the font
	if c, _ := LoadConfig(); c.Theme != "nord" || c.Font != "Gill Sans" {
		t.Errorf("theme dropped the font: %+v", c)
	}
	runCommand("font", []string{"default"}, &out, &errb)
	if c, _ := LoadConfig(); c.Font != "" || themeInfo()["font"] != "" {
		t.Errorf("default should clear the font: %+v", c)
	}
}
