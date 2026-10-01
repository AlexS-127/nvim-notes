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
