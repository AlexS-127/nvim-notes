(() => {
  "use strict";
  const $ = (s) => document.querySelector(s);
  const note = $("#note"), main = $("#main"), tree = $("#tree"), backlinks = $("#backlinks");
  const search = $("#search");
  let current = { view: "note", path: "" };
  let pendingLine = 0;

  const store = {
    get(k, d) { try { return localStorage.getItem(k) ?? d; } catch (e) { return d; } },
    set(k, v) { try { localStorage.setItem(k, v); } catch (e) {} },
  };
  const esc = (s) => String(s).replace(/[&<>"]/g, (c) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;" }[c]));
  const enc = (p) => p.split("/").map(encodeURIComponent).join("/");
  const api = async (u, opts) => {
    const r = await fetch(u, opts);
    if (!r.ok) throw new Error(await r.text());
    return r.json();
  };
  const post = (u, body) => api(u, { method: "POST", headers: { "X-Notesview": "1", "Content-Type": "application/json" }, body: JSON.stringify(body) });

  // ── theme: palette and appearance come from config.json (index.html sets them before paint);
  // with no forced appearance it follows the system, live ──
  const systemDark = matchMedia("(prefers-color-scheme: dark)");
  let forcedMode = (window.__notesviewTheme || {}).mode || "";
  const applyTheme = () => { document.documentElement.dataset.theme = forcedMode || (systemDark.matches ? "dark" : "light"); };
  const loadConfig = async () => {
    const c = await api("/api/config");
    forcedMode = c.mode || "";
    if (c.palette) document.documentElement.dataset.palette = c.palette;
    applyTheme();
  };
  applyTheme();
  systemDark.addEventListener("change", applyTheme);
  try { localStorage.removeItem("theme"); } catch (e) {}   // a choice saved by the old toggle button

  // ── collapsible sidebar ──
  function setSidebar(collapsed) {
    document.body.classList.toggle("sidebar-collapsed", collapsed);
    store.set("sidebar", collapsed ? "collapsed" : "open");
  }
  const toggleSidebar = () => setSidebar(!document.body.classList.contains("sidebar-collapsed"));
  $("#btn-collapse").onclick = toggleSidebar;
  $("#btn-expand").onclick = toggleSidebar;

  // ── routing ──
  const dec = (p) => p.split("/").map(decodeURIComponent).join("/");
  function parseHash() {
    const h = location.hash.replace(/^#/, "");
    const [p, q] = h.split("?");
    const line = Number(new URLSearchParams(q || "").get("line")) || 0;
    if (p === "/tasks") return { view: "tasks", line: 0 };
    if (p.startsWith("/note/")) return { view: "note", path: dec(p.slice(6)), line };
    if (p.startsWith("/folder/")) return { view: "folder", path: dec(p.slice(8)), line: 0 };
    return null;
  }
  const go = (path, line) => { location.hash = "#/note/" + enc(path) + (line ? "?line=" + line : ""); };

  async function route() {
    const r = parseHash();
    if (!r) {
      try {
        const st = await api("/api/status");
        if (st.current && st.current.path === "@tasks") { location.hash = "#/tasks"; return; }
        if (st.current && st.current.path) return go(st.current.path, st.current.line);
        const t = await api("/api/today");
        return go(t.path);
      } catch (e) { return; }
    }
    pendingLine = r.line || 0;
    await show(r, false);
  }

  async function show(r, keepScroll) {
    const top = main.scrollTop;
    current = r;
    markActive();
    if (r.view === "tasks") {
      await renderTasks();
    } else if (r.view === "folder") {
      await renderFolder(r.path);
    } else {
      try {
        const d = await api("/api/note?path=" + encodeURIComponent(r.path));
        document.title = d.title + " — notesview";
        note.className = "note";
        note.innerHTML = d.html;
        enableCheckboxes();
        typesetMath(note);
        renderBacklinks(d.backlinks);
      } catch (e) {
        note.className = "note";
        note.innerHTML = `<h1>${esc(r.path)}</h1><p class="empty">This note doesn't exist yet. Create it from Neovim and it will appear here.</p>`;
        backlinks.hidden = true;
      }
    }
    if (keepScroll) main.scrollTop = top;
    else if (pendingLine) scrollToLine(pendingLine, true);
    else main.scrollTop = 0;
    pendingLine = 0;
  }

  // Typeset the $$…$$ spans the server emitted (TeX is their text content).
  function typesetMath(root) {
    if (!window.katex) return;
    for (const el of root.querySelectorAll(".math")) {
      try {
        katex.render(el.textContent, el, { displayMode: el.classList.contains("math-display"), throwOnError: false });
      } catch (e) { el.classList.add("math-error"); }
    }
  }

  function scrollToLine(line, flash) {
    let best = null;
    for (const el of note.querySelectorAll("[data-line]")) {
      if (Number(el.dataset.line) <= line) best = el; else break;
    }
    if (!best) { main.scrollTop = 0; return; }
    const y = best.getBoundingClientRect().top - main.getBoundingClientRect().top + main.scrollTop;
    main.scrollTop = Math.max(0, y - main.clientHeight / 3);
    if (flash) { best.classList.remove("flash"); void best.offsetWidth; best.classList.add("flash"); }
  }

  // ── rendering helpers ──
  function renderBacklinks(list) {
    if (!list || !list.length) { backlinks.hidden = true; return; }
    backlinks.hidden = false;
    backlinks.innerHTML = "<h3>Backlinks</h3>" +
      list.map((b) => `<a href="#/note/${enc(b.path)}">${esc(titleCase(b.title))}</a>`).join("");
  }

  function enableCheckboxes() {
    for (const cb of note.querySelectorAll("input[type=checkbox]")) cb.disabled = false;
  }
  // Checkboxes toggle the task in its file: in a note via the list item's
  // source line, in the Tasks view via data-path/data-line on the box.
  note.addEventListener("change", async (e) => {
    const cb = e.target;
    if (cb.type !== "checkbox") return;
    let path, line;
    if (cb.dataset.path) {
      path = cb.dataset.path; line = Number(cb.dataset.line);
      cb.closest(".task").classList.toggle("done", cb.checked);
    } else {
      const li = cb.closest("li[data-line]");
      if (!li || current.view !== "note") return;
      path = current.path; line = Number(li.dataset.line);
    }
    try { await post("/api/toggle", { path, line }); }
    catch (err) {
      cb.checked = !cb.checked;
      const t = cb.closest(".task");
      if (t) t.classList.toggle("done", cb.checked);
    }
  });

  // ── tasks (shared by the Tasks view and folder pages) ──
  // Within 7 days each date is its own heading (Today, Friday, October 2, …); beyond that, coarse buckets.
  const GROUP_LABELS = { overdue: "Overdue", week2: "More than a week", week3: "More than 2 weeks", later: "Later", none: "Better late than never" };
  const NEAR_GROUPS = new Set(["today", "tomorrow", "week"]);
  const WEEK_GROUPS = new Set(["overdue", "today", "tomorrow", "week"]);
  const dayHeading = (iso, today) => iso === today ? "Today"
    : new Date(iso + "T00:00:00").toLocaleDateString(undefined, { weekday: "long", month: "long", day: "numeric" });
  let weekOnly = store.get("tasksWeekOnly", "0") === "1";
  let hiddenCats = new Set(JSON.parse(store.get("tasksHidden", "[]"))); // deselected categories ("@general" = no category)

  // Display-only Title Case: small words stay lowercase (unless first/last),
  // and words that already have capitals (API, iPhone) are left alone.
  const SMALL = new Set("a an and as at but by for in nor of on or per so the to up via vs yet".split(" "));
  function titleCase(str) {
    const words = String(str || "").split(/(\s+)/);
    const idx = words.map((w, i) => (/\S/.test(w) ? i : -1)).filter((i) => i >= 0);
    return words.map((w, i) => {
      if (!/\S/.test(w) || /[A-Z]/.test(w)) return w;
      if (SMALL.has(w) && i !== idx[0] && i !== idx[idx.length - 1]) return w;
      return w.replace(/[a-z]/, (c) => c.toUpperCase());
    }).join("");
  }

  function fmtDue(iso, today) {
    const d = new Date(iso + "T00:00:00"), t = new Date(today + "T00:00:00");
    const days = Math.round((d - t) / 86400000);
    const label = d.toLocaleDateString(undefined, { weekday: "short", month: "short", day: "numeric" });
    return days < 0 ? `${label} · ${-days}d overdue` : label;
  }

  // ── folder colours: tag → swatch name; a topic inherits its category's colour ──
  const SWATCHES = ["red", "coral", "orange", "amber", "yellow", "lime", "green", "mint", "teal", "cyan", "sky", "blue", "navy", "indigo", "violet", "purple", "magenta", "pink", "rose", "brown", "olive", "slate", "gray", "black"];
  let folderColors = {};
  const colorOf = (tag) => (tag && (folderColors[tag] || folderColors[tag.split("/")[0]])) || "";
  const colorStyle = (tag) => { const c = colorOf(tag); return c ? ` style="--tc: var(--sw-${c})"` : ""; };

  function folderLabel(t) {
    if (!t.category) return `<span class="tag-folder general">General</span>`;
    const text = titleCase(t.topic ? `${t.category_name} · ${t.topic_name}` : t.category_name);
    return `<a class="tag-folder${colorOf(t.tag) ? " colored" : ""}"${colorStyle(t.tag)} href="#/folder/${enc(t.tag)}" title="#${esc(t.tag)}">${esc(text)}</a>`;
  }

  function taskRow(t, today) {
    const due = t.due ? `<span class="due${t.group === "overdue" ? " overdue" : ""}">${esc(fmtDue(t.due, today))}</span>` : "";
    return `<div class="task"><input type="checkbox" data-path="${esc(t.file)}" data-line="${t.line}">` +
      `<span class="task-text">${t.html || esc(t.display)}</span>${folderLabel(t)}${due || `<span class="due"></span>`}` +
      `<a class="src" href="#/note/${enc(t.file)}?line=${t.line}" title="${esc(t.file)}:${t.line}">${esc(titleCase(t.title))}</a></div>`;
  }

  function taskGroups(tasks, today, onlyWeek) {
    // tasks arrive sorted by group, then due date; keep that order, splitting near dates into one section each
    const sections = [];
    for (const t of tasks) {
      if (onlyWeek && !WEEK_GROUPS.has(t.group)) continue;
      const near = NEAR_GROUPS.has(t.group);
      const key = near ? t.due : t.group;
      let sec = sections[sections.length - 1];
      if (!sec || sec.key !== key) {
        sec = { key, group: t.group, label: near ? dayHeading(t.due, today) : GROUP_LABELS[t.group] || t.group, list: [] };
        sections.push(sec);
      }
      sec.list.push(t);
    }
    const html = sections.map((g) => `<h3 class="group group-${g.group}">${esc(g.label)} <small>[${g.list.length}]</small></h3>` +
      g.list.map((t) => taskRow(t, today)).join("")).join("");
    return html && `<div class="task task-head"><span></span><span>Task</span><span>Category</span><span>Due</span><span>Note</span></div>` + html;
  }

  const catKey = (t) => t.category || "@general";

  async function renderTasks() {
    const [d, folders] = await Promise.all([api("/api/tasks"), api("/api/folders").catch(() => [])]);
    note.className = "note tasks";
    backlinks.hidden = true;
    document.title = "Tasks — notesview";
    const categories = folders.filter((f) => f.kind === "category");
    const chips = [{ key: "@general", name: "General", tag: "" }].concat(categories.map((c) => ({ key: c.tag, name: c.name, tag: c.tag })));
    const allOn = chips.every((c) => !hiddenCats.has(c.key));
    const chipHtml = chips.map((c) => `<button class="chip${hiddenCats.has(c.key) ? "" : " on"}${colorOf(c.tag) ? " colored" : ""}"${colorStyle(c.tag)} data-cat="${esc(c.key)}">${esc(c.name)}</button>`).join("");
    const filter = `<div class="task-filter">` +
      `<label class="select-all"><input type="checkbox" id="cat-all"${allOn ? " checked" : ""}> Select all</label>` + chipHtml +
      `<span class="spacer"></span>` +
      `<button data-week="0" class="${weekOnly ? "" : "on"}">All dates</button>` +
      `<button data-week="1" class="${weekOnly ? "on" : ""}">Due this week</button></div>`;
    const tasks = d.tasks.filter((t) => !hiddenCats.has(catKey(t)));
    const body = taskGroups(tasks, d.today, weekOnly);
    note.innerHTML = "<h1>Tasks</h1>" + filter +
      (body || `<p class="empty">${d.tasks.length ? "Nothing matches these filters." : "No open tasks. 🎉"}</p>`);
    $("#cat-all").indeterminate = !allOn && chips.some((c) => !hiddenCats.has(c.key));
    note._chips = chips;
    typesetMath(note);
  }
  const saveHidden = () => { store.set("tasksHidden", JSON.stringify([...hiddenCats])); show(current, true); };
  note.addEventListener("click", (e) => {
    const b = e.target.closest && e.target.closest(".task-filter button");
    if (!b) return;
    if (b.dataset.cat) {
      hiddenCats.has(b.dataset.cat) ? hiddenCats.delete(b.dataset.cat) : hiddenCats.add(b.dataset.cat);
      return saveHidden();
    }
    weekOnly = b.dataset.week === "1";
    store.set("tasksWeekOnly", weekOnly ? "1" : "0");
    show(current, true);
  });
  note.addEventListener("change", (e) => {
    if (e.target.id !== "cat-all") return;
    // everything on → turn everything off; otherwise turn everything on
    const everyOn = !(note._chips || []).some((c) => hiddenCats.has(c.key));
    hiddenCats = everyOn ? new Set((note._chips || []).map((c) => c.key)) : new Set();
    saveHidden();
  });

  // ── folder pages (generated, nothing on disk) ──
  async function renderFolder(path) {
    note.className = "note tasks folder-page";
    backlinks.hidden = true;
    let d;
    try { d = await api("/api/folder?path=" + encodeURIComponent(path)); }
    catch (e) {
      note.innerHTML = `<h1>${esc(path)}</h1><p class="empty">There is no folder called ${esc(path)}.</p>`;
      return;
    }
    const f = d.folder;
    document.title = f.name + " — notesview";
    const parent = f.kind === "topic"
      ? ` · topic of <a href="#/folder/${enc(f.category)}">${esc(f.category_name)}</a>` : " · category";
    let html = `<h1>📁 ${esc(f.name)}</h1><p class="folder-meta">#${esc(f.tag)}${parent} · <code>${esc(f.path)}/</code></p>`;
    if (f.topics.length) {
      html += "<h2>Topics</h2><ul class=\"folder-list\">" + f.topics.map((t) =>
        `<li><a href="#/folder/${enc(t.tag)}">${esc(t.name)}</a><small>${t.count} note${t.count === 1 ? "" : "s"}</small></li>`).join("") + "</ul>";
    }
    html += "<h2>Notes</h2>" + (f.notes.length
      ? "<ul class=\"folder-list\">" + f.notes.map((n) => `<li><a href="#/note/${enc(n.path)}">${esc(titleCase(n.title))}</a></li>`).join("") + "</ul>"
      : `<p class="empty">No notes directly in this folder.</p>`);
    html += "<h2>Open tasks</h2>" + (taskGroups(f.tasks, d.today, false) || `<p class="empty">No open tasks.</p>`);
    note.innerHTML = html;
  }

  // ── sidebar ──
  let folderTags = {}; // folder path → tag, for the folder-page links in the tree
  const closed = new Set(JSON.parse(store.get("closed", "[]")));
  function renderTree(nodes) {
    return nodes.map((n) => {
      if (n.dir) {
        const tag = folderTags[n.path];
        const sw = tag ? `<button class="swatch${colorOf(tag) ? " set" : ""}"${colorStyle(tag)} data-tag="${esc(tag)}" title="Folder colour" aria-label="Folder colour"></button>` : "";
        const page = tag ? `<a class="folder-page" href="#/folder/${enc(tag)}" title="Folder page: notes, topics and tasks">↗</a>` : "";
        return `<details data-dir="${esc(n.path)}" ${closed.has(n.path) ? "" : "open"}><summary>${sw}<span class="dir-name">${esc(n.name)}</span>${page}</summary>` +
          `<div class="children">${renderTree(n.children || [])}</div></details>`;
      }
      return `<a href="#/note/${enc(n.path)}" data-path="${esc(n.path)}" class="note-link${n.path === "inbox.md" ? " pinned" : ""}">${esc(n.name)}</a>`;
    }).join("");
  }
  async function loadTree() {
    const [t, folders, colors] = await Promise.all([api("/api/tree"), api("/api/folders").catch(() => []), api("/api/folder-colors").catch(() => folderColors)]);
    folderColors = colors;
    folderTags = Object.fromEntries(folders.map((f) => [f.path, f.tag]));
    const q = search.value.trim();
    if (q) return runSearch(q);
    const top = tree.scrollTop;
    tree.innerHTML = renderTree(t);
    tree.scrollTop = top;
    markActive();
  }
  // colour picker: a small popover of squares next to the folder name
  let picker = null;
  const closePicker = () => { if (picker) { picker.remove(); picker = null; } };
  async function setColor(tag, color) {
    closePicker();
    folderColors = await post("/api/folder-color", { tag, color });
    await loadTree();
    if (current.view === "tasks" || current.view === "folder") show(current, true);
  }
  tree.addEventListener("click", (e) => {
    const b = e.target.closest("button.swatch");
    if (!b) return;
    e.preventDefault();   // don't fold the folder
    e.stopPropagation();
    const tag = b.dataset.tag, had = picker && picker.dataset.tag === tag;
    closePicker();
    if (had) return;
    picker = document.createElement("div");
    picker.className = "swatch-picker";
    picker.dataset.tag = tag;
    picker.innerHTML = SWATCHES.map((c) => `<button data-c="${c}" style="--tc: var(--sw-${c})" title="${c}" aria-label="${c}"${folderColors[tag] === c ? ' class="on"' : ""}></button>`).join("") +
      `<button data-c="" class="none" title="No colour" aria-label="No colour">×</button>`;
    document.body.appendChild(picker);
    const r = b.getBoundingClientRect();
    picker.style.left = r.left + "px";
    picker.style.top = r.bottom + 6 + "px";
    picker.addEventListener("click", (ev) => { const c = ev.target.closest("button"); if (c) setColor(tag, c.dataset.c); });
  });
  document.addEventListener("click", (e) => { if (picker && !picker.contains(e.target)) closePicker(); });
  document.addEventListener("keydown", (e) => { if (e.key === "Escape") closePicker(); });

  tree.addEventListener("toggle", (e) => {
    const d = e.target.dataset && e.target.dataset.dir;
    if (d === undefined) return;
    e.target.open ? closed.delete(d) : closed.add(d);
    store.set("closed", JSON.stringify([...closed]));
  }, true);
  function markActive() {
    for (const a of tree.querySelectorAll("a[data-path]")) a.classList.toggle("active", current.view === "note" && a.dataset.path === current.path);
    for (const a of tree.querySelectorAll("a.folder-page")) a.classList.toggle("active", current.view === "folder" && a.getAttribute("href") === "#/folder/" + enc(current.path));
  }
  let searchTimer;
  async function runSearch(q) {
    const hits = await api("/api/search?q=" + encodeURIComponent(q));
    if (search.value.trim() !== q) return;
    tree.innerHTML = hits.length ? hits.map((h) =>
      `<a class="hit" href="#/note/${enc(h.path)}">${esc(titleCase(h.title))}${h.snippet ? `<small>${esc(h.snippet)}</small>` : ""}</a>`).join("")
      : '<p class="empty" style="padding:8px">No matches</p>';
  }
  search.addEventListener("input", () => {
    clearTimeout(searchTimer);
    searchTimer = setTimeout(() => {
      const q = search.value.trim();
      (q ? runSearch(q) : loadTree()).catch(() => {});
    }, 120);
  });
  search.addEventListener("keydown", (e) => {
    if (e.key === "Escape") { search.value = ""; search.blur(); loadTree(); }
    if (e.key === "Enter") { const a = tree.querySelector("a.hit"); if (a) { location.hash = a.getAttribute("href"); } }
  });

  // ── links ──
  document.addEventListener("click", (e) => {
    const a = e.target.closest && e.target.closest("a[href]");
    if (!a) return;
    const href = a.getAttribute("href");
    if (/^(https?:|mailto:)/i.test(href)) {
      e.preventDefault();
      post("/api/openurl", { url: href }).catch(() => {});
    }
  });
  $("#btn-tasks").onclick = () => { location.hash = "#/tasks"; };
  $("#btn-today").onclick = goToday;
  // creates today's daily note (with carry-over) if it doesn't exist yet
  async function goToday() { const t = await post("/api/daily", {}); go(t.path); }

  // ── keyboard ──
  document.addEventListener("keydown", (e) => {
    if (e.ctrlKey || e.metaKey || e.altKey) return;
    if (["INPUT", "TEXTAREA", "SELECT"].includes(document.activeElement.tagName)) return;
    switch (e.key) {
      case "j": main.scrollBy(0, 60); break;
      case "k": main.scrollBy(0, -60); break;
      case "/":
        e.preventDefault();
        if (document.body.classList.contains("sidebar-collapsed")) setSidebar(false);
        search.focus(); search.select();
        break;
      case "t": location.hash = "#/tasks"; break;
      case "g": goToday(); break;
      case "b": toggleSidebar(); break;
      default: return;
    }
  });

  // ── live updates ──
  function connect() {
    const es = new EventSource("/events");
    es.addEventListener("change", (e) => {
      let m = {};
      try { m = JSON.parse(e.data); } catch (err) {}
      if (m.css) { const l = $("#custom-css"); l.href = "/custom.css?" + Date.now(); loadConfig().catch(() => {}); }
      loadTree().catch(() => {});
      if (current.view !== "note" || current.path) show(current, true);
    });
    // after a reconnect (server restart, laptop sleep) things may have changed
    es.addEventListener("open", () => { loadTree().catch(() => {}); });
    es.addEventListener("show", (e) => {
      const m = JSON.parse(e.data);
      if (!m.path) return;
      if (m.path === "@tasks") { location.hash = "#/tasks"; return; }
      if (current.view === "note" && current.path === m.path) { if (m.line) scrollToLine(m.line, false); return; }
      go(m.path, m.line);
    });
    es.addEventListener("scroll", (e) => {
      const m = JSON.parse(e.data);
      if (current.view !== "note") return;
      if (m.path && m.path !== current.path) return go(m.path, m.line);
      scrollToLine(m.line, false);
    });
  }

  window.addEventListener("hashchange", route);
  document.addEventListener("visibilitychange", () => { if (!document.hidden) loadTree().catch(() => {}); });
  loadTree().catch(() => {});
  route();
  connect();
})();
