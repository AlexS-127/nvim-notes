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

  // ── theme: always the system appearance, live (index.html sets it before paint) ──
  const systemDark = matchMedia("(prefers-color-scheme: dark)");
  const applyTheme = (dark) => { document.documentElement.dataset.theme = dark ? "dark" : "light"; };
  applyTheme(systemDark.matches);
  systemDark.addEventListener("change", (e) => applyTheme(e.matches));
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
      list.map((b) => `<a href="#/note/${enc(b.path)}">${esc(b.title)}</a>`).join("");
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
  const GROUP_LABELS = { overdue: "Overdue", today: "Today", tomorrow: "Tomorrow", week: "This week", later: "Later", none: "No date" };
  const WEEK_GROUPS = new Set(["overdue", "today", "tomorrow", "week"]);
  let weekOnly = store.get("tasksWeekOnly", "0") === "1";
  let categoryFilter = store.get("tasksCategory", ""); // "" = all, "@general" = no category

  function fmtDue(iso, today) {
    const d = new Date(iso + "T00:00:00"), t = new Date(today + "T00:00:00");
    const days = Math.round((d - t) / 86400000);
    const label = d.toLocaleDateString(undefined, { weekday: "short", month: "short", day: "numeric" });
    return days < 0 ? `${label} · ${-days}d overdue` : label;
  }

  function folderLabel(t) {
    if (!t.category) return `<span class="tag-folder general">General</span>`;
    const text = t.topic ? `${t.category_name} · ${t.topic_name}` : t.category_name;
    return `<a class="tag-folder" href="#/folder/${enc(t.tag)}" title="#${esc(t.tag)}">${esc(text)}</a>`;
  }

  function taskRow(t, today) {
    const due = t.due ? `<span class="due${t.group === "overdue" ? " overdue" : ""}">${esc(fmtDue(t.due, today))}</span>` : "";
    return `<div class="task"><input type="checkbox" data-path="${esc(t.file)}" data-line="${t.line}">` +
      `<span class="task-text">${t.html || esc(t.display)}</span>${folderLabel(t)}${due}` +
      `<a class="src" href="#/note/${enc(t.file)}?line=${t.line}" title="${esc(t.file)}:${t.line}">${esc(t.title)}</a></div>`;
  }

  function taskGroups(tasks, today, onlyWeek) {
    let html = "";
    for (const g of Object.keys(GROUP_LABELS)) {
      if (onlyWeek && !WEEK_GROUPS.has(g)) continue;
      const list = tasks.filter((t) => t.group === g);
      if (!list.length) continue;
      html += `<h3 class="group group-${g}">${GROUP_LABELS[g]} <small>${list.length}</small></h3>` + list.map((t) => taskRow(t, today)).join("");
    }
    return html;
  }

  async function renderTasks() {
    const [d, folders] = await Promise.all([api("/api/tasks"), api("/api/folders").catch(() => [])]);
    note.className = "note tasks";
    backlinks.hidden = true;
    document.title = "Tasks — notesview";
    const categories = folders.filter((f) => f.kind === "category");
    if (categoryFilter && categoryFilter !== "@general" && !categories.some((c) => c.tag === categoryFilter)) categoryFilter = "";
    const options = [`<option value="">All categories</option>`, `<option value="@general">General</option>`]
      .concat(categories.map((c) => `<option value="${esc(c.tag)}">${esc(c.name)}</option>`)).join("");
    const filter = `<div class="task-filter">` +
      `<select id="cat-filter" class="${categoryFilter ? "on" : ""}" title="Category">${options}</select>` +
      `<button data-week="0" class="${weekOnly ? "" : "on"}">All dates</button>` +
      `<button data-week="1" class="${weekOnly ? "on" : ""}">Due this week</button></div>`;
    const tasks = d.tasks.filter((t) => !categoryFilter || (categoryFilter === "@general" ? !t.category : t.category === categoryFilter));
    const body = taskGroups(tasks, d.today, weekOnly);
    note.innerHTML = "<h1>Tasks</h1>" + filter +
      (body || `<p class="empty">${d.tasks.length ? "Nothing matches these filters." : "No open tasks. 🎉"}</p>`);
    $("#cat-filter").value = categoryFilter;
  }
  note.addEventListener("click", (e) => {
    const b = e.target.closest && e.target.closest(".task-filter button");
    if (!b) return;
    weekOnly = b.dataset.week === "1";
    store.set("tasksWeekOnly", weekOnly ? "1" : "0");
    show(current, true);
  });
  note.addEventListener("change", (e) => {
    if (e.target.id !== "cat-filter") return;
    categoryFilter = e.target.value;
    store.set("tasksCategory", categoryFilter);
    show(current, true);
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
      ? "<ul class=\"folder-list\">" + f.notes.map((n) => `<li><a href="#/note/${enc(n.path)}">${esc(n.title)}</a></li>`).join("") + "</ul>"
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
        const page = tag ? `<a class="folder-page" href="#/folder/${enc(tag)}" title="Folder page: notes, topics and tasks">↗</a>` : "";
        return `<details data-dir="${esc(n.path)}" ${closed.has(n.path) ? "" : "open"}><summary><span class="dir-name">${esc(n.name)}</span>${page}</summary>` +
          `<div class="children">${renderTree(n.children || [])}</div></details>`;
      }
      return `<a href="#/note/${enc(n.path)}" data-path="${esc(n.path)}" class="note-link${n.path === "inbox.md" ? " pinned" : ""}">${esc(n.name)}</a>`;
    }).join("");
  }
  async function loadTree() {
    const [t, folders] = await Promise.all([api("/api/tree"), api("/api/folders").catch(() => [])]);
    folderTags = Object.fromEntries(folders.map((f) => [f.path, f.tag]));
    const q = search.value.trim();
    if (q) return runSearch(q);
    const top = tree.scrollTop;
    tree.innerHTML = renderTree(t);
    tree.scrollTop = top;
    markActive();
  }
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
      `<a class="hit" href="#/note/${enc(h.path)}">${esc(h.title)}${h.snippet ? `<small>${esc(h.snippet)}</small>` : ""}</a>`).join("")
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
      if (m.css) { const l = $("#custom-css"); l.href = "/custom.css?" + Date.now(); }
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
