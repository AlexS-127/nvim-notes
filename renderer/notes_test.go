package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func newTestStore(t *testing.T, files map[string]string) *Store {
	t.Helper()
	dir := t.TempDir()
	for name, content := range files {
		p := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	s, err := NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestWikiLinkResolution(t *testing.T) {
	s := newTestStore(t, map[string]string{
		"Project Plan.md":     "# Plan",
		"ideas/Rust.md":       "# Rust",
		"deep/a/Rust.md":      "# Other Rust",
		"daily/2024-01-01.md": "x",
	})
	cases := []struct {
		target, want string
		ok           bool
	}{
		{"Project Plan", "Project Plan.md", true},
		{"project plan", "Project Plan.md", true},
		{"Project Plan#Heading", "Project Plan.md", true},
		{"rust", "ideas/Rust.md", true}, // shallowest wins
		{"deep/a/rust", "deep/a/Rust.md", true},
		{"2024-01-01", "daily/2024-01-01.md", true},
		{"Nope", "", false},
	}
	for _, c := range cases {
		got, ok := s.ResolveWiki(c.target)
		if got != c.want || ok != c.ok {
			t.Errorf("ResolveWiki(%q) = %q,%v; want %q,%v", c.target, got, ok, c.want, c.ok)
		}
	}
}

func TestTaskGathering(t *testing.T) {
	s := newTestStore(t, map[string]string{
		"a.md": "# Alpha\n- [ ] one\n- [x] done\n  - [ ] nested\n```\n- [ ] in code\n```\n",
		"b.md": "no tasks here\n",
		"c.md": "# Gamma\n\n* [ ] star task\n",
	})
	got := s.CollectTasks(TaskQuery{})
	if len(got) != 3 {
		t.Fatalf("want 3 open tasks, got %d: %+v", len(got), got)
	}
	if got[0].File != "a.md" || got[0].Title != "Alpha" || got[0].Line != 2 || got[0].Text != "one" {
		t.Errorf("unexpected first task: %+v", got[0])
	}
	if got[1].File != "a.md" || got[1].Line != 4 || got[1].Text != "nested" || got[1].Indent != 2 {
		t.Errorf("unexpected nested task: %+v", got[1])
	}
	if got[2].File != "c.md" || got[2].Line != 3 {
		t.Errorf("unexpected c.md task: %+v", got[2])
	}
	if all := s.CollectTasks(TaskQuery{All: true}); len(all) != 4 {
		t.Errorf("--all should include the done task, got %d", len(all))
	}
}

func TestToggleChangesOnlyOneLine(t *testing.T) {
	orig := "# T\r\n- [ ] a\r\n- [x] b\n  - [ ] c\nlast line no newline"
	s := newTestStore(t, map[string]string{"n.md": orig})
	check := func(line int, want string) {
		t.Helper()
		if err := s.ToggleCheckbox("n.md", line); err != nil {
			t.Fatal(err)
		}
		b, _ := s.Read("n.md")
		if string(b) != want {
			t.Fatalf("after toggling line %d:\n got %q\nwant %q", line, b, want)
		}
	}
	check(2, "# T\r\n- [x] a\r\n- [x] b\n  - [ ] c\nlast line no newline")
	check(3, "# T\r\n- [x] a\r\n- [ ] b\n  - [ ] c\nlast line no newline")
	check(4, "# T\r\n- [x] a\r\n- [ ] b\n  - [x] c\nlast line no newline")
	if err := s.ToggleCheckbox("n.md", 1); err == nil {
		t.Error("toggling a non-task line should fail")
	}
	if err := s.ToggleCheckbox("n.md", 99); err == nil {
		t.Error("out-of-range line should fail")
	}
	entries, _ := os.ReadDir(s.Root)
	if len(entries) != 1 {
		t.Errorf("temp files left behind: %v", entries)
	}
}

func TestRejectsPathsOutsideNotes(t *testing.T) {
	s := newTestStore(t, map[string]string{"ok.md": "# ok"})
	outside := filepath.Join(filepath.Dir(s.Root), "secret.txt")
	os.WriteFile(outside, []byte("secret"), 0o644)
	defer os.Remove(outside)
	os.Symlink(outside, filepath.Join(s.Root, "link.md"))

	for _, p := range []string{"../secret.txt", "a/../../secret.txt", "..", "ok/../../x", "link.md", ""} {
		if _, err := s.Resolve(p); err == nil {
			t.Errorf("Resolve(%q) should fail", p)
		}
	}
	if _, err := s.Resolve("ok.md"); err != nil {
		t.Errorf("Resolve(ok.md): %v", err)
	}
	if _, err := s.Rel(outside); err == nil {
		t.Error("Rel of absolute outside path should fail")
	}
	if err := s.ToggleCheckbox("../secret.txt", 1); err == nil {
		t.Error("toggle outside should fail")
	}

	h := NewServer(s, 0).Handler()
	for _, u := range []string{"/files/../secret.txt", "/files/%2e%2e/secret.txt", "/files/link.md", "/api/note?path=../secret.txt"} {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest("GET", u, nil)
		req.Host = "127.0.0.1:7777"
		h.ServeHTTP(rec, req)
		if rec.Code == 200 && strings.Contains(rec.Body.String(), "secret") {
			t.Errorf("GET %s leaked outside file", u)
		}
		if rec.Code == 200 {
			t.Errorf("GET %s returned 200", u)
		}
	}
}

func TestPostGuardsAndHostCheck(t *testing.T) {
	s := newTestStore(t, map[string]string{"a.md": "- [ ] x\n"})
	h := NewServer(s, 0).Handler()
	do := func(method, host, hdr string) int {
		req := httptest.NewRequest(method, "/api/toggle", strings.NewReader(`{"path":"a.md","line":1}`))
		req.Host = host
		if hdr != "" {
			req.Header.Set("X-Notesview", hdr)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec.Code
	}
	if c := do("POST", "127.0.0.1:7777", ""); c != http.StatusForbidden {
		t.Errorf("missing header: %d", c)
	}
	if c := do("POST", "evil.example:7777", "1"); c != http.StatusForbidden {
		t.Errorf("bad host: %d", c)
	}
	if c := do("POST", "127.0.0.1:7777", "1"); c != 200 {
		t.Errorf("valid toggle: %d", c)
	}
}

func TestRenderSample(t *testing.T) {
	s := newTestStore(t, map[string]string{
		"daily/today.md": "# Today\n",
		"Other.md":       "# Other",
	})
	src := "# Title\n\nSee [[Other]], [[Other|alias]] and [[Ghost]].\n\n- [ ] open task\n- [x] done task\n\n```go\nfunc main() {}\n```\n\n> [!NOTE]\n> hello\n\n![pic](assets/x.png)\n\n[rel](../Other.md)\n"
	out, err := NewMarkdown(s, "daily/today.md").Render([]byte(src))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`<h1 data-line="1">`,
		`href="#/note/Other.md"`, `>alias</a>`,
		`wikilink missing`,
		`type="checkbox"`, `<li data-line="5">`,
		`<pre class="chroma" data-line="8" data-lang="go">`, `<span class="kd">func</span>`,
		`callout callout-note`, `callout-title">Note`,
		`src="/files/daily/assets/x.png"`,
		`href="#/note/Other.md"`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q\n%s", want, out)
		}
	}
}
