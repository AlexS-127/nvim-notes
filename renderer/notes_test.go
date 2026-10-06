package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
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

func TestTaskOrder(t *testing.T) {
	s := newTestStore(t, map[string]string{
		"zeta/n.md":  "- [ ] z-undated\n- [ ] z-soon @2026-10-02\n",
		"alpha/n.md": "- [ ] a-undated\n- [ ] a-soon @2026-10-02\n- [ ] a-later @2026-12-01\n",
		"loose.md":   "- [ ] loose-undated\n- [ ] loose-soon @2026-10-02\n- [ ] loose-today @2026-10-01\n",
		"beta/n.md":  "- [ ] b-week @2026-10-05\n- [ ] b-late @2026-09-01\n",
	})
	var got []string
	for _, tk := range s.CollectTasks(TaskQuery{Now: testNow}) {
		got = append(got, tk.Display)
	}
	want := []string{
		"b-late",                         // overdue
		"loose-today",                    // today
		"a-soon", "z-soon", "loose-soon", // tomorrow: by folder, no folder last
		"b-week",                                  // week
		"a-later",                                 // later
		"a-undated", "z-undated", "loose-undated", // no due date: own section at the bottom, by folder
	}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("order:\n got %v\nwant %v", got, want)
	}
}

func TestToggleMovesToDone(t *testing.T) {
	orig := "# T\r\n- [ ] a\r\n- [x] b\r\n  - [ ] c\r\nlast line no newline"
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
	st := " ✅ " + time.Now().Format(doneStampFmt)
	// ticking moves the task to a new Done section, line endings kept
	check(2, "# T\r\n- [x] b\r\n  - [ ] c\r\nlast line no newline\r\n\r\n## Done\r\n- [x] a"+st)
	// unticking it moves it back above Done, after the last open task block
	check(7, "# T\r\n- [x] b\r\n  - [ ] c\r\n- [ ] a\r\nlast line no newline\r\n\r\n## Done")
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

func TestRenderInlineTask(t *testing.T) {
	m := NewMarkdown(newTestStore(t, map[string]string{"Some Note.md": "x", "projects/spec.md": "x"}), "")
	cases := []struct{ src, want string }{
		{"hi _(Sep 30 21:39)_", `hi <em>(Sep 30 21:39)</em>`},
		{"**bold** and ~~gone~~ and `a<b`", `<strong>bold</strong> and <del>gone</del> and <code>a&lt;b</code>`},
		{"see [[Some Note]]", `see <a class="wikilink" href="#/note/Some%20Note.md">Some Note</a>`},
		{"see [[Missing|later]]", `see <a class="wikilink missing" title="Note does not exist yet" href="#/note/Missing.md">later</a>`},
		{"read [the spec](spec.md) first", `read <a href="#/note/projects/spec.md">the spec</a> first`},
		{"visit https://example.com", `visit <a href="https://example.com">https://example.com</a>`},
		{"# not a heading", `# not a heading`},
		{"> not a quote", `&gt; not a quote`},
		{"<script>x</script> y", `<!-- raw HTML omitted -->x<!-- raw HTML omitted --> y`},
	}
	for _, c := range cases {
		got := m.RenderInline("projects/todo.md", c.src)
		if got != c.want {
			t.Errorf("RenderInline(%q)\n got %q\nwant %q", c.src, got, c.want)
		}
	}
}

func TestTasksAPIIncludesHTML(t *testing.T) {
	s := newTestStore(t, map[string]string{
		"inbox.md":     "# Inbox\n- [ ] hi _(Sep 30 21:39)_ and [[Some Note]]\n- [ ] see [docs](https://example.com/a?b=1&c=2)\n",
		"Some Note.md": "# Some *Note*\n",
	})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/tasks", nil)
	req.Host = "127.0.0.1:7777"
	NewServer(s, 0).Handler().ServeHTTP(rec, req)
	var resp struct {
		Tasks []Task `json:"tasks"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("%v: %s", err, rec.Body)
	}
	tasks := resp.Tasks
	if len(tasks) != 2 {
		t.Fatalf("unexpected tasks: %+v", tasks)
	}
	if tasks[0].Text != "hi _(Sep 30 21:39)_ and [[Some Note]]" {
		t.Errorf("text should stay raw: %q", tasks[0].Text)
	}
	if want := `hi <em>(Sep 30 21:39)</em> and <a class="wikilink" href="#/note/Some%20Note.md">Some Note</a>`; tasks[0].HTML != want {
		t.Errorf("html = %q, want %q", tasks[0].HTML, want)
	}
	if want := `see <a href="https://example.com/a?b=1&amp;c=2">docs</a>`; tasks[1].HTML != want {
		t.Errorf("html = %q, want %q", tasks[1].HTML, want)
	}
}

func TestPlainTextTitlesAndSnippets(t *testing.T) {
	for src, want := range map[string]string{
		"**Big** [[Plan|plan]]":      "Big plan",
		"Notes on `go:embed` _now_":  "Notes on go:embed now",
		"A [link](x.md) and ~~old~~": "A link and old",
		`Escaped \*stars\*`:          "Escaped *stars*",
		"plain":                      "plain",
	} {
		if got := PlainText(src); got != want {
			t.Errorf("PlainText(%q) = %q, want %q", src, got, want)
		}
	}
	s := newTestStore(t, map[string]string{
		"a.md": "# The *Big* Plan\n\nlinks to [[b]]\n",
		"b.md": "# B\n\n- [ ] ship **the** `widget`\n",
	})
	if got := Title("a.md", []byte("# The *Big* Plan\n")); got != "The Big Plan" {
		t.Errorf("Title = %q", got)
	}
	if bl := s.Backlinks("b.md"); len(bl) != 1 || bl[0]["title"] != "The Big Plan" {
		t.Errorf("backlinks = %+v", bl)
	}
	if hits := s.Search("widget"); len(hits) != 1 || hits[0].Snippet != "ship the widget" {
		t.Errorf("search = %+v", hits)
	}
}

func TestCustomCSSChangeTriggersReload(t *testing.T) {
	repo := t.TempDir()
	target := filepath.Join(repo, "custom.css")
	if err := os.WriteFile(target, []byte("/* nothing */"), 0o644); err != nil {
		t.Fatal(err)
	}
	// like install.sh: the config file is a symlink into the repo
	cfg := t.TempDir()
	link := filepath.Join(cfg, "custom.css")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	srv := NewServer(newTestStore(t, nil), 0)
	srv.css = link
	stop, err := srv.Watch()
	if err != nil {
		t.Skip("no file watching here:", err)
	}
	defer stop()
	ch := make(chan event, 4)
	srv.mu.Lock()
	srv.clients[ch] = true
	srv.mu.Unlock()
	if err := os.WriteFile(target, []byte(":root { --font-size: 20px; }"), 0o644); err != nil {
		t.Fatal(err)
	}
	select {
	case e := <-ch:
		if !strings.Contains(e.Data, `"css":true`) {
			t.Errorf("event = %+v, want css change", e)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("no reload event after editing custom.css")
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/custom.css", nil)
	req.Host = "127.0.0.1"
	srv.Handler().ServeHTTP(rec, req)
	if !strings.Contains(rec.Body.String(), "--font-size: 20px") {
		t.Errorf("custom.css served %q", rec.Body)
	}
}

func TestMathRendering(t *testing.T) {
	m := NewMarkdown(&Store{}, "a.md")
	out, err := m.Render([]byte("Inline $$x^2 < y$$ here.\n\n$$\n\\frac{a}{b}\n$$\n\n$$E=mc^2$$\n\nCost is $5 and $10.\n"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`<span class="math math-inline">x^2 &lt; y</span>`,
		`\frac{a}{b}`, `class="math math-display"`, `E=mc^2</div>`, "Cost is $5 and $10.",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
}

func TestMathDelimiters(t *testing.T) {
	m := NewMarkdown(&Store{}, "a.md")
	cases := []struct{ in, want string }{
		{"Inline $x^2$ and $a_i + b_i$.", `<span class="math math-inline">x^2</span> and <span class="math math-inline">a_i + b_i</span>`},
		{`Paren \(x<y\) here`, `<span class="math math-inline">x&lt;y</span>`},
		{`Inline \[y\] here`, `<span class="math math-display">y</span>`},
		{"\\[\n\\frac{a}{b}\n\\]\n", `<div class="math math-display"`},
		{"wrapped $$a\n+b$$ done", `<span class="math math-inline">a` + "\n" + `+b</span>`},
		{`price \$5 and $x\$y$`, `<span class="math math-inline">x\$y</span>`},
		{"Cost is $5 and $10.", "Cost is $5 and $10."},
		{"a $ b $ c", "a $ b $ c"},
	}
	for _, c := range cases {
		out, err := m.Render([]byte(c.in))
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(out, c.want) {
			t.Errorf("%q: missing %q in:\n%s", c.in, c.want, out)
		}
	}
}
