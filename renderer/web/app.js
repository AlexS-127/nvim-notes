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
    if (p === "/activity") return { view: "activity", line: 0 };
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
    } else if (r.view === "activity") {
      await renderActivity(keepScroll);
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
    try {
      await post("/api/toggle", { path, line });
      if (cb.checked) celebrate(cb);
    } catch (err) {
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
    const diff = t.difficulty ? `<span class="diff diff-${t.difficulty}" title="Difficulty ${t.difficulty} of 3">${"●".repeat(t.difficulty)}${"○".repeat(3 - t.difficulty)}</span>` : `<span class="diff"></span>`;
    return `<div class="task"><input type="checkbox" data-path="${esc(t.file)}" data-line="${t.line}">` +
      `<span class="task-text">${t.html || esc(t.display)}</span>${folderLabel(t)}${diff}${due || `<span class="due"></span>`}</div>`;
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
    return html && `<div class="task task-head"><span></span><span>Task</span><span>Category</span><span>Level</span><span>Due</span></div>` + html;
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

  // ── activity: calendar heatmap and today's completed tasks ──
  const MILESTONES = [[10, "Getting going"], [25, "On a roll"], [50, "Task slayer"], [100, "Centurion"], [250, "Unstoppable"], [500, "Legend"], [1000, "Mythic"]];
  const DOW = ["Mon", "Tue", "Wed", "Thu", "Fri", "Sat", "Sun"];
  const isoOf = (d) => `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, "0")}-${String(d.getDate()).padStart(2, "0")}`;
  const dayOf = (iso) => new Date(iso + "T00:00:00");
  const plural = (n, w) => `${n} ${w}${n === 1 ? "" : "s"}`;
  const fmtStudy = (sec) => { const m = Math.round(sec / 60); return m >= 60 ? `${Math.floor(m / 60)}h ${m % 60}m` : `${m}m`; };
  let actMetric = store.get("actMetric", "both");
  if (actMetric !== "score") actMetric = "both";
  let scoreMode = store.get("scoreMode", "today");

  function levelOf(n) { return n <= 0 ? 0 : n === 1 ? 1 : n <= 3 ? 2 : n <= 6 ? 3 : 4; }
  const scoreLevel = (n) => (n <= 0 ? 0 : n < 25 ? 1 : n < 50 ? 2 : n < 75 ? 3 : 4);
  const metricVal = (d) => (d ? d.done + d.created : 0);
  const cellLevel = (d) => actMetric === "score" ? scoreLevel(d ? d.score || 0 : 0) : levelOf(metricVal(d));

  function heatmapSvg(a) {
    const today = dayOf(a.today), C = 12, G = 3, left = 28, top = 18;
    const start = new Date(today); start.setDate(start.getDate() - ((start.getDay() + 6) % 7) - 52 * 7);
    let cells = "", months = "", lastMonth = -1;
    for (let w = 0; w < 53; w++) {
      for (let r = 0; r < 7; r++) {
        const d = new Date(start); d.setDate(d.getDate() + w * 7 + r);
        if (d > today) continue;
        const iso = isoOf(d), st = { done: 0, created: 0, ...(a.days[iso] || {}), score: (a.scores[iso] || {}).total || 0 };
        if (r === 0 && d.getMonth() !== lastMonth && d.getDate() <= 7) {
          months += `<text x="${left + w * (C + G)}" y="10" class="hm-label">${d.toLocaleDateString(undefined, { month: "short" })}</text>`;
          lastMonth = d.getMonth();
        }
        cells += `<rect class="hm-cell l${cellLevel(st)}${iso === a.today ? " today" : ""}" x="${left + w * (C + G)}" y="${top + r * (C + G)}" width="${C}" height="${C}" rx="3" data-d="${iso}" data-done="${st.done}" data-created="${st.created}" data-study="${st.study || 0}" data-words="${st.words || 0}" data-score="${st.score}"/>`;
      }
    }
    const dows = DOW.map((n, r) => n ? `<text x="0" y="${top + r * (C + G) + 10}" class="hm-label">${n}</text>` : "").join("");
    const W = left + 53 * (C + G);
    return `<svg class="heatmap" viewBox="0 0 ${W} ${top + 7 * (C + G)}" role="img" aria-label="Calendar of tasks done and created over the last year">${months}${dows}${cells}</svg>`;
  }

  function countUp(el, to, animate) {
    if (!animate || matchMedia("(prefers-reduced-motion: reduce)").matches || to < 1) { el.textContent = to; return; }
    const t0 = performance.now(), dur = 900;
    const step = (t) => { const k = Math.min(1, (t - t0) / dur); el.textContent = Math.round(to * (1 - Math.pow(1 - k, 3))); if (k < 1) requestAnimationFrame(step); };
    requestAnimationFrame(step);
  }

  function recentSection(a) {
    return `<h2>Completed today</h2>${a.recent && a.recent.length
      ? `<ul class="recent">${a.recent.map((t) => `<li><span class="rt-text">${esc(t.text)}</span></li>`).join("")}</ul>`
      : `<p class="act-note">Nothing completed yet today.</p>`}`;
  }

  // today's breakdown: element, quantity, points; hovering an element shows its rule
  function scoreRows(sc, hints) {
    const rows = [
      ["Tasks completed", sc.done, hints.done, sc.done_pts],
      ["Tasks created", sc.created, hints.created, sc.created_pts],
      ["Quiz time", fmtStudy(sc.study), hints.study, sc.study_pts],
      ["New words", (sc.words || 0).toLocaleString(), hints.words, sc.words_pts || 0],
      ["Overdue tasks", sc.overdue, hints.overdue, sc.overdue_pts],
    ];
    return rows.map(([l, n, rule, pts]) => `<tr class="${pts < 0 ? "neg" : ""}"><td class="el" title="${esc(rule)}">${l}</td><td class="n">${n}</td><td class="pts">${pts < 0 ? "−" + -pts : pts}</td></tr>`).join("");
  }

  const hhmm = (d) => d.toLocaleTimeString(undefined, { hour: "numeric", minute: "2-digit" });

  // smooth curve through [x, y] points: monotone cubic (Fritsch-Carlson), so it never overshoots the data
  function curvePath(P) {
    const n = P.length, f = (v) => v.toFixed(1);
    if (n < 2) return n ? `M${f(P[0][0])} ${f(P[0][1])}` : "";
    const dx = [], m = [], t = [];
    for (let i = 0; i < n - 1; i++) { dx[i] = P[i + 1][0] - P[i][0]; m[i] = dx[i] ? (P[i + 1][1] - P[i][1]) / dx[i] : 0; }
    t[0] = m[0]; t[n - 1] = m[n - 2];
    for (let i = 1; i < n - 1; i++) t[i] = m[i - 1] * m[i] <= 0 ? 0 : (m[i - 1] + m[i]) / 2;
    for (let i = 0; i < n - 1; i++) {
      if (m[i] === 0) { t[i] = t[i + 1] = 0; continue; }
      const a = t[i] / m[i], b = t[i + 1] / m[i], h = Math.hypot(a, b);
      if (h > 3) { t[i] = 3 * a / h * m[i]; t[i + 1] = 3 * b / h * m[i]; }
    }
    let d = `M${f(P[0][0])} ${f(P[0][1])}`;
    for (let i = 0; i < n - 1; i++) d += `C${f(P[i][0] + dx[i] / 3)} ${f(P[i][1] + t[i] * dx[i] / 3)} ${f(P[i + 1][0] - dx[i] / 3)} ${f(P[i + 1][1] - t[i + 1] * dx[i] / 3)} ${f(P[i + 1][0])} ${f(P[i + 1][1])}`;
    return d;
  }

  // score line graph. "today": a curved line through the day; "daily": one point per day.
  function scoreLineSvg(a, mode) {
    const W = 1200, H = 266, L = 64, R = 20, B = 46, T = 18;
    let pts, xs, labels;
    if (mode === "today") {
      const day0 = dayOf(a.today).getTime(), span = 24 * 3600e3, now = Date.now();
      pts = a.score_line.map((p) => ({ t: new Date(p.at).getTime(), v: p.total, tip: `${hhmm(new Date(p.at))}` }));
      if (pts.length) pts.push({ t: Math.max(now, pts[pts.length - 1].t), v: pts[pts.length - 1].v, end: true });
      xs = (t) => L + (W - L - R) * Math.min(1, Math.max(0, (t - day0) / span));
      labels = [0, 3, 6, 9, 12, 15, 18, 21, 24].map((h) => [xs(day0 + h * 3600e3), h === 24 ? "" : hhmm(new Date(day0 + h * 3600e3))]);
    } else {
      const keys = Object.keys(a.scores).sort(), start = dayOf(keys[0]), today = dayOf(a.today), days = [];
      for (let d = new Date(Math.max(start, new Date(today.getFullYear(), today.getMonth(), today.getDate() - 59))); d <= today; d.setDate(d.getDate() + 1)) days.push(isoOf(d));
      pts = days.map((k, i) => ({ t: i, v: (a.scores[k] || {}).total || 0, tip: dayOf(k).toLocaleDateString(undefined, { weekday: "short", month: "short", day: "numeric" }), k }));
      const n = Math.max(1, days.length - 1);
      xs = (i) => L + (W - L - R) * (days.length > 1 ? i / n : .5);
      const every = Math.max(1, Math.ceil(days.length / 14));
      labels = days.map((k, i) => (i % every === 0 || i === days.length - 1) && (days.length - 1 - i >= every / 2 || i === days.length - 1) ? [xs(i), dayOf(k).toLocaleDateString(undefined, { month: "short", day: "numeric" })] : null).filter(Boolean);
    }
    const top = Math.max(10, Math.ceil(Math.max(0, ...pts.map((p) => p.v)) / 10) * 10), y = (v) => T + (H - T - B) * (1 - v / top), base = y(0);
    let g = "";
    for (let i = 0; i <= top; i += top / 4) g += `<line class="wk-grid" x1="${L}" x2="${W - R}" y1="${y(i)}" y2="${y(i)}"/><text class="hm-label" x="${L - 6}" y="${y(i) + 4}" text-anchor="end">${Math.round(i)}</text>`;
    const xl = labels.map(([x, l]) => `<text class="hm-label" x="${x}" y="${H - 10}" text-anchor="middle">${l}</text>`).join("");
    if (!pts.length) return `<svg class="weekly" viewBox="0 0 ${W} ${H}">${g}${xl}<text class="hm-label" x="${W / 2}" y="${H / 2}" text-anchor="middle">No score recorded yet today</text></svg>`;
    let d = "";
    if (mode === "today") d = curvePath(pts.map((p) => [xs(p.t), y(p.v)]));
    else pts.forEach((p, i) => { d += `${i ? "L" : "M"}${xs(p.t)} ${y(p.v)}`; });
    const last = pts[pts.length - 1];
    const area = `${d}V${base}H${xs(pts[0].t)}Z`;
    const dots = pts.filter((p) => !p.end).map((p) => `<circle class="sc-dot" cx="${xs(p.t)}" cy="${y(p.v)}" r="${mode === "today" ? 3 : 3.5}" data-tip="${esc(p.tip)}" data-v="${p.v}"/>`).join("");
    return `<svg class="weekly score-line" viewBox="0 0 ${W} ${H}" role="img" aria-label="Productivity score" data-geo="${[L, W - R, T, H - B, 0, top].join()}">${g}${xl}<path class="sc-area" d="${area}"/><path class="sc-path" d="${d}"/>${dots}<circle class="sc-now" cx="${xs(last.t)}" cy="${y(last.v)}" r="4.5"/><circle class="sc-hover" r="4.5" hidden/></svg>`;
  }

  // slope graph: derivative of today's score (points per hour). The step log is smoothed with a gaussian, so the slope is a curve.
  // slope in points per 10 minutes at time t (ms)
  function slopeFn(a) {
    const jumps = a.score_line.slice(1).map((p, i) => ({ t: new Date(p.at).getTime(), dv: p.total - a.score_line[i].total })).filter((j) => j.dv);
    const sigma = 8 * 60e3, unit = 10 * 60e3;
    return { jumps, at: (t) => jumps.reduce((s, j) => s + j.dv * Math.exp(-((t - j.t) ** 2) / (2 * sigma * sigma)) / (sigma * Math.sqrt(2 * Math.PI)), 0) * unit };
  }

  function slopeSvg(a) {
    const W = 1200, H = 266, L = 88, R = 20, B = 46, T = 18;
    const day0 = dayOf(a.today).getTime(), span = 24 * 3600e3, now = Math.min(Date.now(), day0 + span);
    const xs = (t) => L + (W - L - R) * Math.min(1, Math.max(0, (t - day0) / span));
    const labels = [0, 3, 6, 9, 12, 15, 18, 21].map((h) => [xs(day0 + h * 3600e3), hhmm(new Date(day0 + h * 3600e3))]);
    const xl = labels.map(([x, l]) => `<text class="hm-label" x="${x}" y="${H - 10}" text-anchor="middle">${l}</text>`).join("");
    const { jumps, at: slope } = slopeFn(a), step = 60e3;
    const pts = [];
    for (let t = day0; t <= now; t += step) pts.push({ t, v: slope(t) });
    pts.push({ t: now, v: slope(now) });
    const hi = Math.max(.5, ...pts.map((p) => p.v)), lo = Math.min(0, ...pts.map((p) => p.v));
    const top = Math.ceil(hi * 2) / 2, bot = Math.floor(lo * 2) / 2;
    const y = (v) => T + (H - T - B) * (1 - (v - bot) / (top - bot));
    let g = "";
    for (let i = 0; i <= 4; i++) { const v = bot + (top - bot) * i / 4; g += `<line class="wk-grid" x1="${L}" x2="${W - R}" y1="${y(v)}" y2="${y(v)}"/><text class="hm-label" x="${L - 6}" y="${y(v) + 4}" text-anchor="end">${Math.round(v * 100) / 100}</text>`; }
    g += `<text class="hm-label" transform="rotate(-90 18 ${(T + H - B) / 2})" x="18" y="${(T + H - B) / 2}" text-anchor="middle">per 10 min</text>`;
    if (!jumps.length) return `<svg class="weekly" viewBox="0 0 ${W} ${H}">${g}${xl}<text class="hm-label" x="${W / 2}" y="${H / 2}" text-anchor="middle">No score change yet today</text></svg>`;
    let d = "";
    d = curvePath(pts.map((p) => [xs(p.t), y(p.v)]));
    const last = pts[pts.length - 1];
    return `<svg class="weekly score-line" viewBox="0 0 ${W} ${H}" role="img" aria-label="Score slope, points per 10 minutes" data-geo="${[L, W - R, T, H - B, bot, top].join()}">${g}${xl}<line class="wk-grid" x1="${L}" x2="${W - R}" y1="${y(0)}" y2="${y(0)}" style="stroke-width:1.5"/><path class="sc-path" d="${d}"/><circle class="sc-now" cx="${xs(last.t)}" cy="${y(last.v)}" r="4.5"/><circle class="sc-hover" r="4.5" hidden/></svg>`;
  }

  function scoreSection(a) {
    const sc = a.scores[a.today];
    return `<div class="score-card">
        <div class="score-num" id="act-score">${sc.total}<small>Score</small></div>
        <table class="score-calc"><tbody>${scoreRows(sc, a.score_hints)}</tbody></table>
      </div>
      <div class="act-head"><div class="sc-read">--.--</div><div class="task-filter score-mode">
        ${[["today", "Today"], ["slope", "Slope"], ["daily", "Daily"]].map(([k, l]) => `<button data-mode="${k}" class="${scoreMode === k ? "on" : ""}">${l}</button>`).join("")}</div></div>
      <div class="wk-wrap">${scoreMode === "slope" ? slopeSvg(a) : scoreLineSvg(a, scoreMode)}</div>`;
  }

  async function renderActivity(keepScroll) {
    const a = await api("/api/activity");
    note.className = "note activity";
    backlinks.hidden = true;
    document.title = "Activity — notesview";
    const today = a.days[a.today] || { done: 0, created: 0 };

    note.innerHTML = `<h1>Activity</h1>
      <div class="act-tiles">
        <div class="tile hero"><small>Tasks completed</small><div class="big" id="act-total">${a.total_done}</div></div>
        <div class="tile"><small>Today</small><div class="big">${today.done}<em>done</em></div>
          <span class="sub">${today.created} made</span></div>
        <div class="tile"><small>Quiz time</small><div class="big">${fmtStudy(a.study_today)}<em>today</em></div>
          <span class="sub">${fmtStudy(a.study_week)} this week · ${fmtStudy(a.study_total)} in all</span></div>
        <div class="tile"><small>New words</small><div class="big">${(a.words_today || 0).toLocaleString()}<em>today</em></div>
          <span class="sub">${(a.words_week || 0).toLocaleString()} this week · ${(a.words_total || 0).toLocaleString()} in all</span></div>
        <div class="tile"><small>Current slope</small><div class="big"><span id="act-slope">--.--</span><em>/10 min</em></div></div>
      </div>
      ${scoreSection(a)}
      <div class="act-head"><div class="task-filter act-metric">
        ${[["both", "Done + made"], ["score", "Score"]].map(([k, l]) => `<button data-metric="${k}" class="${actMetric === k ? "on" : ""}">${l}</button>`).join("")}</div></div>
      <div class="hm-wrap">${heatmapSvg(a)}</div>
      <div class="hm-legend">Less ${[0, 1, 2, 3, 4].map((l) => `<i class="hm-cell l${l}"></i>`).join("")} More</div>
      ${recentSection(a)}`;
    countUp($("#act-total"), a.total_done, !keepScroll);
    note._activity = a;
    // current slope tile ticks in real time (it decays between score changes)
    const sl = $("#act-slope"), sf = slopeFn(a).at;
    let timer, prev;
    const tick = () => { if (!sl.isConnected) return clearInterval(timer); const v = +sf(Date.now()).toFixed(2);
      if (prev !== undefined && v !== prev) sl.dataset.sign = v > prev ? "up" : "down"; // colour = direction of the last change
      prev = v; sl.textContent = v.toFixed(2); };
    tick(); timer = setInterval(tick, 1000);
  }

  // metric switch, tooltips
  const tip = document.createElement("div");
  tip.className = "act-tip"; tip.hidden = true; document.body.appendChild(tip);
  const showTip = (e, html) => { tip.innerHTML = html; tip.hidden = false; const w = tip.offsetWidth; tip.style.left = Math.max(8, Math.min(innerWidth - w - 8, e.clientX - w / 2)) + "px"; tip.style.top = e.clientY - tip.offsetHeight - 14 + "px"; };
  // hovering a score/slope graph: read the value of the drawn curve at the cursor into the label above its top-left corner
  function scoreHover(e) {
    note.querySelectorAll("svg.score-line").forEach((svg) => {
      const read = note.querySelector(".sc-read"), dot = svg.querySelector(".sc-hover"), path = svg.querySelector(".sc-path");
      if (!read || !dot || !path || !svg.dataset.geo) return;
      const [L, Rr, T, Bt, bot, top] = svg.dataset.geo.split(",").map(Number), r = svg.getBoundingClientRect();
      const px = (e.clientX - r.left) * svg.viewBox.baseVal.width / r.width, py = (e.clientY - r.top) * svg.viewBox.baseVal.height / r.height;
      if (px < L || px > Rr || py < 0 || py > Bt + 46 || !(e.target.closest && e.target.closest("svg") === svg)) { read.textContent = "--.--"; dot.setAttribute("hidden", ""); return; }
      let lo = 0, hi = path.getTotalLength();
      for (let i = 0; i < 24; i++) { const mid = (lo + hi) / 2; if (path.getPointAtLength(mid).x < px) lo = mid; else hi = mid; }
      const p = path.getPointAtLength(hi), v = bot + (top - bot) * (1 - (p.y - T) / (Bt - T));
      read.textContent = (Math.abs(v) < 0.005 ? 0 : v).toFixed(2);
      dot.setAttribute("cx", p.x); dot.setAttribute("cy", p.y); dot.removeAttribute("hidden");
    });
  }
  note.addEventListener("mousemove", (e) => {
    const c = e.target.closest && e.target.closest(".hm-cell[data-d]");
    scoreHover(e);
    if (c) {
      const d = Number(c.dataset.done), m = Number(c.dataset.created);
      showTip(e, `<b>${dayOf(c.dataset.d).toLocaleDateString(undefined, { weekday: "short", month: "short", day: "numeric", year: "numeric" })}</b><br>${d || m ? `${plural(d, "task")} done · ${m} made` : "No activity"}${Number(c.dataset.study) ? `<br>${fmtStudy(Number(c.dataset.study))} of quiz` : ""}${Number(c.dataset.words) ? `<br>${Number(c.dataset.words).toLocaleString()} new words` : ""}${Number(c.dataset.score) ? `<br>Score ${c.dataset.score}` : ""}`);
    } else tip.hidden = true;
  });
  note.addEventListener("mouseleave", () => { tip.hidden = true; note.querySelectorAll(".sc-read").forEach((t) => { t.textContent = "--.--"; }); note.querySelectorAll(".sc-hover").forEach((c) => c.setAttribute("hidden", "")); });
  note.addEventListener("click", (e) => {
    const b = e.target.closest && e.target.closest(".act-metric button");
    if (b) { actMetric = b.dataset.metric; store.set("actMetric", actMetric); show(current, true); }
    const m = e.target.closest && e.target.closest(".score-mode button");
    if (m) { scoreMode = m.dataset.mode; store.set("scoreMode", scoreMode); show(current, true); }
  });

  // ── rewards: confetti when a task is ticked; a toast for milestones ──
  function celebrate(cb) {
    if (matchMedia("(prefers-reduced-motion: reduce)").matches) return;
    const r = cb.getBoundingClientRect(), cx = r.left + r.width / 2, cy = r.top + r.height / 2;
    const colors = ["var(--accent)", "var(--c-tip)", "var(--c-warning)", "var(--c-important)", "var(--c-danger)"];
    for (let i = 0; i < 16; i++) {
      const p = document.createElement("i");
      p.className = "confetti";
      p.style.cssText = `left:${cx}px;top:${cy}px;background:${colors[i % colors.length]}`;
      document.body.appendChild(p);
      const a = Math.random() * Math.PI * 2, d = 24 + Math.random() * 40;
      p.animate([{ transform: "translate(0,0) scale(1)", opacity: 1 },
        { transform: `translate(${Math.cos(a) * d}px,${Math.sin(a) * d - 18}px) rotate(${Math.random() * 360}deg) scale(1)`, opacity: 1, offset: .6 },
        { transform: `translate(${Math.cos(a) * d * 1.1}px,${Math.sin(a) * d + 24}px) rotate(${Math.random() * 540}deg) scale(.6)`, opacity: 0 }],
        { duration: 700 + Math.random() * 250, easing: "cubic-bezier(.2,.7,.4,1)" }).onfinish = () => p.remove();
    }
    api("/api/activity").then((a) => {
      const m = MILESTONES.find(([n]) => n === a.total_done);
      if (m) toast(`${a.total_done} tasks done: ${m[1]}`);
    }).catch(() => {});
  }
  function toast(msg) {
    const el = document.createElement("div");
    el.className = "toast"; el.textContent = msg;
    document.body.appendChild(el);
    setTimeout(() => el.classList.add("out"), 2600);
    setTimeout(() => el.remove(), 3100);
  }

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
  $("#btn-activity").onclick = () => { location.hash = "#/activity"; };
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
      case "a": location.hash = "#/activity"; break;
      case "g": goToday(); break;
      case "b": toggleSidebar(); break;
      default: return;
    }
  });

  // ── live updates ──
  function connect() {
    const es = new EventSource("/events" + (new URLSearchParams(location.search).get("app") === "1" ? "?app=1" : ""));
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
