package main

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/BurntSushi/toml"
)

// Class is one entry of [[class]] in config.toml.
type Class struct {
	ID     string `toml:"id" json:"id"`
	Name   string `toml:"name" json:"name"`
	Folder string `toml:"folder" json:"folder"`
}

// Dir is the class folder relative to the notes folder (slash path).
func (c Class) Dir() string { return "classes/" + c.Folder }

type Config struct {
	Classes []Class `toml:"class" json:"classes"`
}

// configDir is ~/.config/notesview (or $XDG_CONFIG_HOME/notesview).
func configDir() string {
	if x := os.Getenv("XDG_CONFIG_HOME"); x != "" {
		return filepath.Join(x, "notesview")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "notesview")
}

// ConfigPath is $NOTESVIEW_CONFIG, else ~/.config/notesview/config.toml.
func ConfigPath() string {
	if p := os.Getenv("NOTESVIEW_CONFIG"); p != "" {
		return p
	}
	return filepath.Join(configDir(), "config.toml")
}

// ParseConfig reads config.toml content, dropping incomplete or duplicate
// classes. A missing name or folder falls back to the id.
func ParseConfig(data []byte) (*Config, error) {
	var raw Config
	if _, err := toml.Decode(string(data), &raw); err != nil {
		return nil, err
	}
	cfg := &Config{}
	seen := map[string]bool{}
	for _, c := range raw.Classes {
		c.ID = strings.ToLower(strings.TrimSpace(strings.TrimPrefix(c.ID, "#")))
		if c.ID == "" || seen[c.ID] || strings.ContainsAny(c.ID, " \t/#") {
			continue
		}
		if c.Name == "" {
			c.Name = c.ID
		}
		folder := strings.ReplaceAll(c.Folder, "\\", "/")
		c.Folder = strings.Trim(path.Clean("/"+folder), "/")
		if c.Folder == "" || strings.Contains(folder, "..") {
			c.Folder = c.ID
		}
		seen[c.ID] = true
		cfg.Classes = append(cfg.Classes, c)
	}
	return cfg, nil
}

// LoadConfig reads a config file. A missing file is an empty config.
func LoadConfig(p string) (*Config, error) {
	data, err := os.ReadFile(p)
	if os.IsNotExist(err) {
		return &Config{}, nil
	}
	if err != nil {
		return nil, err
	}
	cfg, err := ParseConfig(data)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", p, err)
	}
	return cfg, nil
}

// ClassByID finds a class by id (case-insensitive, optional leading #).
func (c *Config) ClassByID(id string) (Class, bool) {
	id = strings.ToLower(strings.TrimPrefix(id, "#"))
	for _, cl := range c.Classes {
		if cl.ID == id {
			return cl, true
		}
	}
	return Class{}, false
}

// ClassForPath returns the class whose folder contains rel.
func (c *Config) ClassForPath(rel string) (Class, bool) {
	low := strings.ToLower(rel)
	for _, cl := range c.Classes {
		if strings.HasPrefix(low, strings.ToLower(cl.Dir())+"/") {
			return cl, true
		}
	}
	return Class{}, false
}

// configCache reloads config.toml whenever its modification time or size
// changes, so edits apply without restarting the server.
type configCache struct {
	path string

	mu    sync.Mutex
	mtime time.Time
	size  int64
	cfg   *Config
	err   error
}

func newConfigCache(p string) *configCache { return &configCache{path: p} }

// Get returns the current config. On a parse error it keeps serving the
// last good config (or an empty one) and reports the error.
func (cc *configCache) Get() (*Config, error) {
	cc.mu.Lock()
	defer cc.mu.Unlock()
	var mt time.Time
	var sz int64 = -1
	if st, err := os.Stat(cc.path); err == nil {
		mt, sz = st.ModTime(), st.Size()
	}
	if cc.cfg != nil && mt.Equal(cc.mtime) && sz == cc.size {
		return cc.cfg, cc.err
	}
	cfg, err := LoadConfig(cc.path)
	cc.mtime, cc.size, cc.err = mt, sz, err
	if err == nil {
		cc.cfg = cfg
	} else if cc.cfg == nil {
		cc.cfg = &Config{}
	}
	return cc.cfg, cc.err
}
