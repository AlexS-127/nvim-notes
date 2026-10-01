package main

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"sync"
)

// Folder colours are the small squares in the sidebar: tag → swatch name.
// They live next to config.json so they survive restarts and are shared by
// every window, and a topic without its own colour inherits its category's.
var swatches = map[string]bool{"red": true, "coral": true, "orange": true, "amber": true, "yellow": true, "lime": true, "green": true, "mint": true, "teal": true, "cyan": true, "sky": true, "blue": true, "navy": true, "indigo": true, "violet": true, "purple": true, "magenta": true, "pink": true, "rose": true, "brown": true, "olive": true, "slate": true, "gray": true, "black": true}

var colorsMu sync.Mutex

func colorsPath() string { return filepath.Join(configDir(), "folder-colors.json") }

func loadFolderColors() map[string]string {
	m := map[string]string{}
	if b, err := os.ReadFile(colorsPath()); err == nil {
		_ = json.Unmarshal(b, &m)
	}
	for k, v := range m {
		if !swatches[v] {
			delete(m, k)
		}
	}
	return m
}

// setFolderColor sets (or, with colour "", clears) the colour of a folder tag.
func setFolderColor(tag, color string) error {
	colorsMu.Lock()
	defer colorsMu.Unlock()
	m := loadFolderColors()
	if color == "" {
		delete(m, tag)
	} else {
		m[tag] = color
	}
	b, _ := json.MarshalIndent(m, "", "  ")
	if err := os.MkdirAll(configDir(), 0o755); err != nil {
		return err
	}
	tmp := colorsPath() + ".tmp"
	if err := os.WriteFile(tmp, append(b, '\n'), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, colorsPath())
}

func (s *Server) handleFolderColor(w http.ResponseWriter, r *http.Request) {
	var in struct{ Tag, Color string }
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<12)).Decode(&in); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	f, ok := s.store.Folders().Resolve(in.Tag)
	if !ok {
		http.Error(w, "no such folder", http.StatusNotFound)
		return
	}
	if in.Color != "" && !swatches[in.Color] {
		http.Error(w, "unknown colour", http.StatusBadRequest)
		return
	}
	if err := setFolderColor(f.Tag, in.Color); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, loadFolderColors())
}
