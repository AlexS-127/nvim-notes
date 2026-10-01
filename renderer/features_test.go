package main

import (
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Thursday 1 October 2026, noon.
var testNow = time.Date(2026, 10, 1, 12, 0, 0, 0, time.Local)

func testConfig(t *testing.T) *Config {
	t.Helper()
	cfg, err := ParseConfig([]byte(`
[[class]]
id = "act200"
name = "ACT 200"
folder = "act200"

[[class]]
id = "PSYC110"
name = "PSYC 110"
folder = "psyc110"

[[class]]
id = "bus300"
name = "BUS 300"
folder = "bus300"
`))
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}

func readFile(t *testing.T, s *Store, rel string) string {
	t.Helper()
	b, err := s.Read(rel)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// ── config ──

func TestConfigParsing(t *testing.T) {
	cfg, err := ParseConfig([]byte(`
[[class]]
id = "#Math101"
[[class]]
id = "math101"
name = "dup"
[[class]]
name = "no id"
[[class]]
id = "evil"
folder = "../../etc"
`))
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Classes) != 2 {
		t.Fatalf("want 2 classes, got %+v", cfg.Classes)
	}
	if c := cfg.Classes[0]; c.ID != "math101" || c.Name != "math101" || c.Folder != "math101" {
		t.Errorf("defaults not applied: %+v", c)
	}
	if c := cfg.Classes[1]; c.Folder != "evil" {
		t.Errorf("folder escaping classes/ should fall back to the id: %+v", c)
	}
	if _, err := ParseConfig([]byte("[[class]\nid=")); err == nil {
		t.Error("bad TOML should fail")
	}
	// the shipped config parses and has the three starter classes
	shipped, err := LoadConfig(filepath.Join("..", "notesview", "config.toml"))
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, c := range shipped.Classes {
		ids = append(ids, c.ID+"="+c.Name+"@"+c.Dir())
	}
	if got := strings.Join(ids, " "); got != "act200=ACT 200@classes/act200 psyc110=PSYC 110@classes/psyc110 bus300=BUS 300@classes/bus300" {
		t.Errorf("shipped config: %s", got)
	}
	if cfg, err := LoadConfig(filepath.Join(t.TempDir(), "missing.toml")); err != nil || len(cfg.Classes) != 0 {
		t.Errorf("missing config should be empty, got %v %v", cfg, err)
	}
}

func TestConfigReloadsWhenChanged(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.toml")
	os.WriteFile(p, []byte("[[class]]\nid = \"a\"\n"), 0o644)
	cc := newConfigCache(p)
	if cfg, _ := cc.Get(); len(cfg.Classes) != 1 {
		t.Fatalf("first load: %+v", cfg)
	}
	os.WriteFile(p, []byte("[[class]]\nid = \"a\"\n[[class]]\nid = \"b\"\n"), 0o644)
	os.Chtimes(p, time.Now().Add(time.Second), time.Now().Add(time.Second))
	if cfg, _ := cc.Get(); len(cfg.Classes) != 2 {
		t.Fatalf("after edit: %+v", cfg)
	}
	os.WriteFile(p, []byte("[[class]\nbroken"), 0o644)
	os.Chtimes(p, time.Now().Add(2*time.Second), time.Now().Add(2*time.Second))
	if cfg, err := cc.Get(); err == nil || len(cfg.Classes) != 2 {
		t.Fatalf("a broken edit should report an error and keep the last good config: %+v %v", cfg, err)
	}
}

// ── natural dates ──

func TestNaturalDates(t *testing.T) {
	cases := map[string]string{
		"@today":                              "@2026-10-01",
		"@tomorrow":                           "@2026-10-02",
		"@Tomorrow":                           "@2026-10-02",
		"@fri":                                "@2026-10-02", // next occurrence
		"@thu":                                "@2026-10-08", // today is Thursday: the next one
		"@wed":                                "@2026-10-07",
		"@mon":                                "@2026-10-05",
		"@sun":                                "@2026-10-04",
		"@oct6":                               "@2026-10-06",
		"@sep6":                               "@2027-09-06", // already passed this year
		"@10/6":                               "@2026-10-06",
		"@1/15":                               "@2027-01-15",
		"@+3d":                                "@2026-10-04",
		"@+2w":                                "@2026-10-15",
		"@2026-10-09":                         "@2026-10-09", // already ISO
		"@someday":                            "@someday",    // unknown
		"@feb30":                              "@feb30",      // no such day
		"@13/1":                               "@13/1",
		"@fri-ish":                            "@fri-ish",
		"me@tomorrow.com":                     "me@tomorrow.com", // not after whitespace
		"- [ ] read ch. 4 @fri #act200":       "- [ ] read ch. 4 @2026-10-02 #act200",
		"  - [ ] a @today, b @+1d. (c @oct6)": "  - [ ] a @2026-10-01, b @2026-10-02. (c @2026-10-06)",
		"no dates here":                       "no dates here",
	}
	for in, want := range cases {
		if got := ConvertNaturalDates(in, testNow); got != want {
			t.Errorf("ConvertNaturalDates(%q) = %q, want %q", in, got, want)
		}
	}
	// Feb 29 skips to the next leap year
	if d, ok := ResolveNaturalDate("feb29", testNow); !ok || d.Format(isoDate) != "2028-02-29" {
		t.Errorf("feb29 -> %v %v", d, ok)
	}
}

// ── task parsing ──

func TestParseTasksRules(t *testing.T) {
	cfg := testConfig(t)
	src := "# Lecture\n" +
		"- [ ] problem set @2026-10-02\n" + // 2: class from folder
		"- [x] done thing\n" + // 3
		"- [>] moved thing → [[2026-10-01]]\n" + // 4
		"  * [ ] nested #Exam #psyc110\n" + // 5: folder wins over tag
		"- [ ] code `@2026-01-01 #bus300` only\n" + // 6
		"- [ ] bad date @2026-13-45\n" // 7
	tasks := ParseTasks("classes/act200/2026-10-01.md", []byte(src), cfg)
	if len(tasks) != 6 {
		t.Fatalf("want 6 tasks, got %d: %+v", len(tasks), tasks)
	}
	want := []struct {
		line          int
		state, due    string
		class, kind   string
		text, display string
	}{
		{2, StateOpen, "2026-10-02", "act200", KindHomework, "problem set @2026-10-02", "problem set"},
		{3, StateDone, "", "act200", KindHomework, "done thing", "done thing"},
		{4, StateMoved, "", "act200", KindHomework, "moved thing → [[2026-10-01]]", "moved thing → [[2026-10-01]]"},
		{5, StateOpen, "", "act200", KindHomework, "nested #Exam #psyc110", "nested #Exam #psyc110"},
		{6, StateOpen, "", "act200", KindHomework, "code `@2026-01-01 #bus300` only", "code `@2026-01-01 #bus300` only"},
		{7, StateOpen, "", "act200", KindHomework, "bad date @2026-13-45", "bad date @2026-13-45"},
	}
	for i, w := range want {
		g := tasks[i]
		if g.Line != w.line || g.State != w.state || g.Due != w.due || g.Class != w.class || g.Kind != w.kind ||
			g.Text != w.text || g.Display != w.display || g.File != "classes/act200/2026-10-01.md" || g.Title != "Lecture" {
			t.Errorf("task %d:\n got %+v\nwant %+v", i, g, w)
		}
	}
	if tags := strings.Join(tasks[3].Tags, ","); tags != "exam,psyc110" {
		t.Errorf("tags = %s", tags)
	}
	if len(tasks[4].Tags) != 0 {
		t.Errorf("tags inside code should not count: %v", tasks[4].Tags)
	}

	// subtasks inherit their parent task's class
	sub := ParseTasks("inbox.md", []byte("- [ ] essay #psyc110\n  - [ ] outline\n\n  - [ ] draft\n- [ ] unrelated\n"), cfg)
	if sub[1].Class != "psyc110" || sub[2].Class != "psyc110" || sub[3].Class != "" {
		t.Errorf("inheritance: %+v", sub)
	}

	// outside a class folder the class comes from a #tag
	other := ParseTasks("inbox.md", []byte(
		"- [ ] essay #PSYC110 @2026-10-05\n- [ ] groceries #home\n- [ ] read #act2000\n- [ ] url http://x.com/#bus300\n"), cfg)
	if other[0].Class != "psyc110" || other[0].ClassName != "PSYC 110" || other[0].Kind != KindHomework ||
		other[0].Display != "essay" || other[0].Due != "2026-10-05" {
		t.Errorf("tagged homework: %+v", other[0])
	}
	for _, o := range other[1:] {
		if o.Class != "" || o.Kind != KindOther {
			t.Errorf("should be other: %+v", o)
		}
	}
}

func TestDateGroups(t *testing.T) {
	cases := map[string]string{
		"2026-09-30": GroupOverdue,
		"2025-01-01": GroupOverdue,
		"2026-10-01": GroupToday,
		"2026-10-02": GroupTomorrow,
		"2026-10-03": GroupWeek,
		"2026-10-08": GroupWeek,
		"2026-10-09": GroupLater,
		"":           GroupNone,
		"garbage":    GroupNone,
	}
	for due, want := range cases {
		if got := DateGroup(due, testNow); got != want {
			t.Errorf("DateGroup(%q) = %s, want %s", due, got, want)
		}
	}
}

func TestCollectTasksOrderAndFilter(t *testing.T) {
	s := newTestStore(t, map[string]string{
		"inbox.md":                     "- [ ] later thing @2026-12-01\n- [ ] hw from inbox #bus300 @2026-09-28\n- [ ] undated\n",
		"classes/act200/2026-10-01.md": "## Homework\n- [ ] ch 4 @2026-10-02\n- [x] done hw\n",
		"daily/2026-09-30.md":          "- [>] carried → [[2026-10-01]]\n- [ ] today thing @2026-10-01\n",
	})
	got := s.CollectTasks(testConfig(t), TaskQuery{Now: testNow})
	var order []string
	for _, x := range got {
		order = append(order, x.Kind+"/"+x.Group+"/"+x.Display)
	}
	want := []string{
		"homework/overdue/hw from inbox",
		"homework/tomorrow/ch 4",
		"other/today/today thing",
		"other/later/later thing",
		"other/none/undated",
	}
	if strings.Join(order, "\n") != strings.Join(want, "\n") {
		t.Errorf("order:\n%s\nwant:\n%s", strings.Join(order, "\n"), strings.Join(want, "\n"))
	}
}

// ── daily notes ──

func TestDailyCarryOver(t *testing.T) {
	old := "# Tuesday\n\n## Tasks\n\n" +
		"- [ ] call bank\n" +
		"  - [ ] find account number\n" +
		"  - [x] look up hours\n" +
		"\n" +
		"    some note under it\n" +
		"  - [ ] quiz prep #act200\n" +
		"- [x] finished\n" +
		"- [ ] read chapter #psyc110 @2026-10-03\n" +
		"  - [ ] stays with its homework parent\n" +
		"* [ ] buy milk @2026-10-01\n" +
		"- [>] already moved → [[2026-09-29]]\n" +
		"- not a task\n" +
		"  - [ ] nested under a plain bullet\n" +
		"```\n- [ ] in code\n```\n" +
		"\n## Notes\n\nstuff\n"
	s := newTestStore(t, map[string]string{
		"daily/2026-09-28.md": "- [ ] older note task\n",
		"daily/2026-09-29.md": old, // two days back: the most recent earlier note
		"daily/2026-10-05.md": "- [ ] future note\n",
		"daily/notes.md":      "- [ ] not a daily note\n",
	})
	cfg := testConfig(t)
	res, err := s.EnsureDaily(cfg, testNow, true)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Created || res.From != "daily/2026-09-29.md" || res.Moved != 3 || res.Path != "daily/2026-10-01.md" {
		t.Errorf("result: %+v", res)
	}
	wantNew := "# Thursday, October 01 2026\n\n## Tasks\n\n" +
		"- [ ] call bank\n" +
		"  - [ ] find account number\n" +
		"  - [x] look up hours\n" +
		"\n" +
		"    some note under it\n" +
		"* [ ] buy milk @2026-10-01\n" +
		"- [ ] nested under a plain bullet\n" +
		"\n## Notes\n\n"
	if got := readFile(t, s, "daily/2026-10-01.md"); got != wantNew {
		t.Errorf("new note:\n%s\nwant:\n%s", got, wantNew)
	}
	wantOld := "# Tuesday\n\n## Tasks\n\n" +
		"- [>] call bank → [[2026-10-01]]\n" +
		"  - [ ] quiz prep #act200\n" +
		"- [x] finished\n" +
		"- [ ] read chapter #psyc110 @2026-10-03\n" +
		"  - [ ] stays with its homework parent\n" +
		"* [>] buy milk @2026-10-01 → [[2026-10-01]]\n" +
		"- [>] already moved → [[2026-09-29]]\n" +
		"- not a task\n" +
		"  - [>] nested under a plain bullet → [[2026-10-01]]\n" +
		"```\n- [ ] in code\n```\n" +
		"\n## Notes\n\nstuff\n"
	if got := readFile(t, s, "daily/2026-09-29.md"); got != wantOld {
		t.Errorf("old note:\n%s\nwant:\n%s", got, wantOld)
	}
	if got := readFile(t, s, "daily/2026-09-28.md"); got != "- [ ] older note task\n" {
		t.Errorf("older note touched: %q", got)
	}
	// moved tasks are gone from the task list; homework stays put
	for _, x := range s.CollectTasks(cfg, TaskQuery{Now: testNow}) {
		if x.File == "daily/2026-09-29.md" && (x.Kind != KindHomework || x.Class == "") {
			t.Errorf("non-homework task left in old note: %+v", x)
		}
	}

	// once the note exists nothing more happens
	res, err = s.EnsureDaily(cfg, testNow, true)
	if err != nil || res.Created || res.Moved != 0 {
		t.Errorf("second run: %+v %v", res, err)
	}
	if got := readFile(t, s, "daily/2026-10-01.md"); got != wantNew {
		t.Error("second run changed today's note")
	}
}

func TestDailyWithoutPreviousNote(t *testing.T) {
	s := newTestStore(t, nil)
	res, err := s.EnsureDaily(testConfig(t), testNow, true)
	if err != nil || !res.Created || res.Moved != 0 {
		t.Fatalf("%+v %v", res, err)
	}
	if got := readFile(t, s, res.Path); got != "# Thursday, October 01 2026\n\n## Tasks\n\n\n## Notes\n\n" {
		t.Errorf("template: %q", got)
	}
	// a past note gets the template but no carry-over
	s2 := newTestStore(t, map[string]string{"daily/2026-09-20.md": "- [ ] x\n"})
	res, _ = s2.EnsureDaily(testConfig(t), testNow.AddDate(0, 0, -1), false)
	if res.Moved != 0 || readFile(t, s2, "daily/2026-09-20.md") != "- [ ] x\n" {
		t.Errorf("carry-over without carry flag: %+v", res)
	}
}

func TestCarryOverKeepsCRLF(t *testing.T) {
	keep, moved, n := carryOver(strings.Split("- [ ] a\r\n  - [ ] b\r\n", "\n"), &Config{}, "daily/x.md", "2026-10-01")
	if n != 1 || strings.Join(keep, "\n") != "- [>] a → [[2026-10-01]]\r\n" || strings.Join(moved, "|") != "- [ ] a|  - [ ] b" {
		t.Errorf("keep=%q moved=%q n=%d", keep, moved, n)
	}
}

// ── classes, lecture notes, capture ──

func TestLectureNote(t *testing.T) {
	s := newTestStore(t, nil)
	c, _ := testConfig(t).ClassByID("act200")
	rel, err := s.EnsureLecture(c, testNow)
	if err != nil || rel != "classes/act200/2026-10-01.md" {
		t.Fatalf("%q %v", rel, err)
	}
	note := readFile(t, s, rel)
	for _, want := range []string{"# ACT 200 — Thursday, October 1 2026\n", "## Topics", "## Notes", "## Key terms", "## Questions", "## Homework"} {
		if !strings.Contains(note, want) {
			t.Errorf("lecture note missing %q:\n%s", want, note)
		}
	}
	idx := readFile(t, s, "classes/act200/index.md")
	if !strings.HasPrefix(idx, "# ACT 200\n") || !strings.Contains(idx, "Tasks view") {
		t.Errorf("index:\n%s", idx)
	}
	// existing notes are never overwritten
	os.WriteFile(filepath.Join(s.Root, "classes/act200/index.md"), []byte("mine"), 0o644)
	os.WriteFile(filepath.Join(s.Root, rel), []byte("edited"), 0o644)
	s.EnsureLecture(c, testNow)
	if readFile(t, s, rel) != "edited" || readFile(t, s, "classes/act200/index.md") != "mine" {
		t.Error("existing files were overwritten")
	}
}

func TestCaptureLandsInHomework(t *testing.T) {
	s := newTestStore(t, nil)
	line, err := s.Capture("finish worksheet @tomorrow #act200", testNow)
	if err != nil {
		t.Fatal(err)
	}
	if line != "- [ ] finish worksheet @2026-10-02 #act200 _(Oct 01 12:00)_" {
		t.Errorf("line: %q", line)
	}
	os.WriteFile(filepath.Join(s.Root, "inbox.md"), []byte(readFile(t, s, "inbox.md")+"no newline at end"), 0o644)
	s.Capture("  buy\nmilk  ", testNow)
	want := "# Inbox\n\n- [ ] finish worksheet @2026-10-02 #act200 _(Oct 01 12:00)_\nno newline at end\n- [ ] buy milk _(Oct 01 12:00)_\n"
	if got := readFile(t, s, "inbox.md"); got != want {
		t.Errorf("inbox:\n%q\nwant\n%q", got, want)
	}
	tasks := s.CollectTasks(testConfig(t), TaskQuery{Now: testNow})
	if tasks[0].Kind != KindHomework || tasks[0].Class != "act200" || tasks[0].Group != GroupTomorrow {
		t.Errorf("captured task: %+v", tasks[0])
	}
	if _, err := s.Capture("   ", testNow); err == nil {
		t.Error("empty capture should fail")
	}
}

// ── CLI ──

func TestCLICommands(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(t.TempDir(), "config.toml")
	os.WriteFile(cfgPath, []byte("[[class]]\nid = \"act200\"\nname = \"ACT 200\"\nfolder = \"act200\"\n"), 0o644)
	t.Setenv("NOTESVIEW_CONFIG", cfgPath)
	run := func(args ...string) (string, int) {
		var out, errb strings.Builder
		code := runCommand(args[0], args[1:], &out, &errb)
		return out.String() + errb.String(), code
	}
	if out, code := run("date", "--", "- [ ] x @today"); code != 0 || out != "- [ ] x @"+time.Now().Format(isoDate)+"\n" {
		t.Errorf("date: %q %d", out, code)
	}
	if out, code := run("date", "- [ ] no flags needed @nope"); code != 0 || out != "- [ ] no flags needed @nope\n" {
		t.Errorf("date without --: %q %d", out, code)
	}
	if _, code := run("capture", "--dir", dir, "--", "hw @tomorrow #act200"); code != 0 {
		t.Fatal("capture failed")
	}
	out, code := run("tasks", "--json", "--dir", dir)
	var tasks []Task
	if err := json.Unmarshal([]byte(out), &tasks); err != nil || code != 0 || len(tasks) != 1 {
		t.Fatalf("tasks --json: %q %v", out, err)
	}
	if tasks[0].Kind != KindHomework || tasks[0].Group != GroupTomorrow || tasks[0].File != "inbox.md" || tasks[0].Line != 3 {
		t.Errorf("task: %+v", tasks[0])
	}
	if out, code := run("lecture", "--dir", dir, "act200"); code != 0 || !strings.HasSuffix(strings.TrimSpace(out), "classes/act200/"+time.Now().Format(isoDate)+".md") {
		t.Errorf("lecture: %q", out)
	}
	if _, code := run("lecture", "--dir", dir, "nope"); code == 0 {
		t.Error("unknown class should fail")
	}
	if out, code := run("daily", "--dir", dir); code != 0 || !strings.HasSuffix(strings.TrimSpace(out), "daily/"+time.Now().Format(isoDate)+".md") {
		t.Errorf("daily: %q", out)
	}
	if out, _ := run("classes", "--json"); !strings.Contains(out, `"name":"ACT 200"`) {
		t.Errorf("classes: %q", out)
	}
}

func TestVersionCompare(t *testing.T) {
	cases := []struct {
		a, b string
		less bool
	}{
		{"0.1.0", "0.2.0", true}, {"v0.2.0", "0.2.0", false}, {"0.10.0", "0.9.9", false},
		{"0.2", "0.2.1", true}, {"1.0.0", "0.9.0", false}, {"dev", "0.2.0", false},
	}
	for _, c := range cases {
		if got := versionLess(c.a, c.b); got != c.less {
			t.Errorf("versionLess(%s,%s) = %v", c.a, c.b, got)
		}
	}
}

// ── viewer ──

func TestRenderMovedAndInline(t *testing.T) {
	s := newTestStore(t, map[string]string{"Other.md": "# O"})
	out, err := NewMarkdown(s, "daily/x.md").Render([]byte("- [>] moved → [[Other]]\n- [ ] open\n"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, `class="task-moved"`) || !strings.Contains(out, `moved-box`) || strings.Contains(out, "[&gt;]") {
		t.Errorf("moved item not rendered:\n%s", out)
	}
	md := NewMarkdown(s, "")
	if got := md.RenderInline("inbox.md", "read **ch 4** and [[Other]]"); got != `read <strong>ch 4</strong> and <a class="wikilink" href="#/note/Other.md">Other</a>` {
		t.Errorf("inline: %s", got)
	}
	if got := md.RenderInline("inbox.md", "1. <b>"); strings.Contains(got, "<ol") || strings.Contains(got, "<b>") {
		t.Errorf("non-paragraph should be escaped text: %s", got)
	}
}

func TestTasksAndDailyAPI(t *testing.T) {
	s := newTestStore(t, map[string]string{"inbox.md": "- [ ] hw **bold** #act200\n- [>] gone → [[x]]\n"})
	srv := NewServer(s, 0)
	cfgPath := filepath.Join(t.TempDir(), "config.toml")
	os.WriteFile(cfgPath, []byte("[[class]]\nid = \"act200\"\nname = \"ACT 200\"\nfolder = \"act200\"\n"), 0o644)
	srv.cfg = newConfigCache(cfgPath)
	h := srv.Handler()

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/tasks", nil)
	req.Host = "127.0.0.1:7777"
	h.ServeHTTP(rec, req)
	var body struct {
		Today string `json:"today"`
		Tasks []Task `json:"tasks"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Tasks) != 1 || body.Tasks[0].ClassName != "ACT 200" || body.Tasks[0].HTML != "hw <strong>bold</strong>" {
		t.Errorf("tasks: %+v", body.Tasks)
	}

	rec = httptest.NewRecorder()
	req = httptest.NewRequest("GET", "/api/classes", nil)
	req.Host = "127.0.0.1:7777"
	h.ServeHTTP(rec, req)
	if !strings.Contains(rec.Body.String(), `"path":"classes/act200/index.md"`) {
		t.Errorf("classes: %s", rec.Body.String())
	}

	rec = httptest.NewRecorder()
	req = httptest.NewRequest("POST", "/api/daily", strings.NewReader("{}"))
	req.Host = "127.0.0.1:7777"
	req.Header.Set("X-Notesview", "1")
	h.ServeHTTP(rec, req)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"created":true`) {
		t.Errorf("daily: %d %s", rec.Code, rec.Body.String())
	}
}

func TestWatcherReportsTreeChanges(t *testing.T) {
	s := newTestStore(t, map[string]string{"a.md": "x"})
	srv := NewServer(s, 0)
	srv.cfg = newConfigCache(filepath.Join(t.TempDir(), "config.toml"))
	stop, err := srv.Watch()
	if err != nil {
		t.Skip("no file watching here:", err)
	}
	defer stop()
	ch := make(chan event, 16)
	srv.mu.Lock()
	srv.clients[ch] = struct{}{}
	srv.mu.Unlock()
	wait := func(what string) event {
		t.Helper()
		select {
		case e := <-ch:
			return e
		case <-time.After(3 * time.Second):
			t.Fatalf("no change event after %s", what)
		}
		return event{}
	}
	os.MkdirAll(filepath.Join(s.Root, "new", "deeper"), 0o755)
	if e := wait("mkdir"); !strings.Contains(e.Data, `"tree":true`) {
		t.Errorf("mkdir: %+v", e)
	}
	time.Sleep(150 * time.Millisecond)
	os.WriteFile(filepath.Join(s.Root, "new", "deeper", "b.md"), []byte("x"), 0o644)
	if e := wait("file in new folder"); !strings.Contains(e.Data, `"tree":true`) {
		t.Errorf("new file: %+v", e)
	}
	os.Rename(filepath.Join(s.Root, "new"), filepath.Join(s.Root, "renamed"))
	wait("rename")
	time.Sleep(150 * time.Millisecond)
	os.WriteFile(filepath.Join(s.Root, "renamed", "deeper", "c.md"), []byte("x"), 0o644)
	if e := wait("file in renamed folder"); !strings.Contains(e.Data, `"tree":true`) {
		t.Errorf("file in renamed folder: %+v", e)
	}
}
