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
  const esc = (s) => s.replace(/[&<>"]/g, (c) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;" }[c]));
  const enc = (p) => p.split("/").map(encodeURIComponent).join("/");
  const api = async (u, opts) => {
    const r = await fetch(u, opts);
    if (!r.ok) throw new Error(await r.text());
    return r.json();
  };
  const post = (u, body) => api(u, { method: "POST", headers: { "X-Notesview": "1", "Content-Type": "application/json" }, body: JSON.stringify(body) });

  // ── theme ──
  const setTheme = (t) => { document.documentElement.dataset.theme = t; store.set("theme", t); };
  setTheme(store.get("theme", "dark"));
  $("#btn-theme").onclick = () => setTheme(document.documentElement.dataset.theme === "dark" ? "light" : "dark");

  // ── routing ──
  function parseHash() {
    const h = location.hash.replace(/^#/, "");
    const [p, q] = h.split("?");
    const line = Number(new URLSearchParams(q || "").get("line")) || 0;
    if (p === "/tasks") return { view: "tasks", line: 0 };
    if (p.startsWith("/note/")) return { view: "note", path: p.slice(6).split("/").map(decodeURIComponent).join("/"), line };
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
  note.addEventListener("change", async (e) => {
    const cb = e.target;
    if (cb.type !== "checkbox") return;
    const li = cb.closest("li[data-line]");
    if (!li || current.view !== "note") return;
    try { await post("/api/toggle", { path: current.path, line: Number(li.dataset.line) }); }
    catch (err) { cb.checked = !cb.checked; }
  });

  const GROUP_LABELS = { overdue: "Overdue", today: "Today", tomorrow: "Tomorrow", week: "This week", later: "Later", none: "No date" };
  const WEEK_GROUPS = new Set(["overdue", "today", "tomorrow", "week"]);
  let weekOnly = store.get("tasksWeekOnly", "0") === "1";

  function fmtDue(iso, today) {
    const d = new Date(iso + "T00:00:00"), t = new Date(today + "T00:00:00");
    const days = Math.round((d - t) / 86400000);
    const label = d.toLocaleDateString(undefined, { weekday: "short", month: "short", day: "numeric" });
    if (days < 0) return `${label} · ${-days}d overdue`;
    return label;
  }

  function taskRow(t, today) {
    const cls = t.class_name ? `<span class="tag-class" title="#${esc(t.class)}">${esc(t.class_name)}</span>` : "";
    const due = t.due ? `<span class="due${t.group === "overdue" ? " overdue" : ""}">${esc(fmtDue(t.due, today))}</span>` : "";
    return `<div class="task"><input type="checkbox" data-path="${esc(t.file)}" data-line="${t.line}">` +
      `<span class="task-text">${t.html || esc(t.display)}</span>${cls}${due}` +
      `<a class="src" href="#/note/${enc(t.file)}?line=${t.line}" title="${esc(t.file)}:${t.line}">${esc(t.title)}</a></div>`;
  }

  function taskSection(title, tasks, today) {
    let html = "";
    for (const g of Object.keys(GROUP_LABELS)) {
      if (weekOnly && !WEEK_GROUPS.has(g)) continue;
      const list = tasks.filter((t) => t.group === g);
      if (!list.length) continue;
      html += `<h3 class="group group-${g}">${GROUP_LABELS[g]} <small>${list.length}</small></h3>` + list.map((t) => taskRow(t, today)).join("");
    }
    return `<h2>${title}</h2>` + (html || `<p class="empty">${weekOnly ? "Nothing due this week." : "Nothing open."}</p>`);
  }

  async function renderTasks() {
    const d = await api("/api/tasks");
    note.className = "note tasks";
    backlinks.hidden = true;
    document.title = "Tasks — notesview";
    const filter = `<div class="task-filter"><button data-week="0" class="${weekOnly ? "" : "on"}">All</button>` +
      `<button data-week="1" class="${weekOnly ? "on" : ""}">Due this week</button></div>`;
    if (!d.tasks.length) { note.innerHTML = '<h1>Tasks</h1><p class="empty">No open tasks. 🎉</p>'; return; }
    note.innerHTML = "<h1>Tasks</h1>" + filter +
      taskSection("Homework", d.tasks.filter((t) => t.kind === "homework"), d.today) +
      taskSection("Other", d.tasks.filter((t) => t.kind !== "homework"), d.today);
  }
  note.addEventListener("click", (e) => {
    const b = e.target.closest && e.target.closest(".task-filter button");
    if (!b) return;
    weekOnly = b.dataset.week === "1";
    store.set("tasksWeekOnly", weekOnly ? "1" : "0");
    show(current, true);
  });
  note.addEventListener("change", async (e) => {
    const cb = e.target;
    if (cb.type !== "checkbox" || !cb.dataset.path) return;
    try { await post("/api/toggle", { path: cb.dataset.path, line: Number(cb.dataset.line) }); }
    catch (err) { cb.checked = !cb.checked; }
  });

  // ── sidebar ──
  let treeData = [];
  const closed = new Set(JSON.parse(store.get("closed", "[]")));
  function renderTree(nodes) {
    return nodes.map((n) => {
      if (n.dir) {
        return `<details data-dir="${esc(n.path)}" ${closed.has(n.path) ? "" : "open"}><summary>${esc(n.name)}</summary>` +
          `<div class="children">${renderTree(n.children || [])}</div></details>`;
      }
      return `<a href="#/note/${enc(n.path)}" data-path="${esc(n.path)}" class="${n.path === "inbox.md" ? "pinned" : ""}">${esc(n.name)}</a>`;
    }).join("");
  }
  function renderClasses(classes) {
    if (!classes.length) return "";
    return `<details data-dir="@classes" ${closed.has("@classes") ? "" : "open"} class="classes"><summary>Classes</summary><div class="children">` +
      classes.map((c) => `<a href="#/note/${enc(c.path)}" data-path="${esc(c.path)}" class="class-link" title="classes/${esc(c.folder)}">${esc(c.name)}</a>`).join("") +
      "</div></details>";
  }
  async function loadTree() {
    const [t, classes] = await Promise.all([api("/api/tree"), api("/api/classes").catch(() => [])]);
    treeData = t;
    const q = search.value.trim();
    if (q) return runSearch(q);
    const open = tree.scrollTop;
    tree.innerHTML = renderClasses(classes) + renderTree(treeData);
    tree.scrollTop = open;
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
    if (["INPUT", "TEXTAREA"].includes(document.activeElement.tagName)) return;
    const step = e.shiftKey ? main.clientHeight * 0.9 : 60;
    switch (e.key) {
      case "j": main.scrollBy(0, 60); break;
      case "k": main.scrollBy(0, -60); break;
      case "/": e.preventDefault(); search.focus(); search.select(); break;
      case "t": location.hash = "#/tasks"; break;
      case "g": goToday(); break;
      default: return;
    }
  });

  // ── live updates ──
  function connect() {
    const es = new EventSource("/events");
    es.addEventListener("change", () => {
      loadTree().catch(() => {});
      if (current.view === "tasks" || current.path) show(current, true);
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
