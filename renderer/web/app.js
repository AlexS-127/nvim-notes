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

  // ── activity: calendar heatmap, streaks, milestones and the weekly chart ──
  const MILESTONES = [[10, "Getting going"], [25, "On a roll"], [50, "Task slayer"], [100, "Centurion"], [250, "Unstoppable"], [500, "Legend"], [1000, "Mythic"]];
  const FULLDAY = ["Monday", "Tuesday", "Wednesday", "Thursday", "Friday", "Saturday", "Sunday"];
  const DOW = ["Mon", "", "Wed", "", "Fri", "", ""];
  const isoOf = (d) => `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, "0")}-${String(d.getDate()).padStart(2, "0")}`;
  const dayOf = (iso) => new Date(iso + "T00:00:00");
  const plural = (n, w) => `${n} ${w}${n === 1 ? "" : "s"}`;
  const fmtStudy = (sec) => { const m = Math.round(sec / 60); return m >= 60 ? `${Math.floor(m / 60)}h ${m % 60}m` : `${m}m`; };
  const fmtAvg = (n) => (Math.round(n * 10) / 10).toString();
  let actMetric = store.get("actMetric", "both");

  function levelOf(n) { return n <= 0 ? 0 : n === 1 ? 1 : n <= 3 ? 2 : n <= 6 ? 3 : 4; }
  const studyLevel = (sec) => { const m = sec / 60; return sec <= 0 ? 0 : m < 5 ? 1 : m < 15 ? 2 : m < 30 ? 3 : 4; };
  const scoreLevel = (n) => (n <= 0 ? 0 : n < 25 ? 1 : n < 50 ? 2 : n < 75 ? 3 : 4);
  const signed = (n) => (n > 0 ? `+${n}` : n < 0 ? `−${-n}` : "0");
  const metricVal = (d) => !d ? 0 : actMetric === "done" ? d.done : actMetric === "created" ? d.created : d.done + d.created;
  const cellLevel = (d) => actMetric === "quiz" ? studyLevel(d ? d.study || 0 : 0) : actMetric === "score" ? scoreLevel(d ? d.score || 0 : 0) : levelOf(metricVal(d));

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
        cells += `<rect class="hm-cell l${cellLevel(st)}${iso === a.today ? " today" : ""}" x="${left + w * (C + G)}" y="${top + r * (C + G)}" width="${C}" height="${C}" rx="3" data-d="${iso}" data-done="${st.done}" data-created="${st.created}" data-study="${st.study || 0}" data-score="${st.score}"/>`;
      }
    }
    const dows = DOW.map((n, r) => n ? `<text x="0" y="${top + r * (C + G) + 10}" class="hm-label">${n}</text>` : "").join("");
    const W = left + 53 * (C + G);
    return `<svg class="heatmap" viewBox="0 0 ${W} ${top + 7 * (C + G)}" role="img" aria-label="Calendar of tasks done and created over the last year">${months}${dows}${cells}</svg>`;
  }

  function weeklySvg(a) {
    const W = 640, H = 220, L = 34, B = 26, T = 14, n = a.weeks.length, slot = (W - L) / n, bw = Math.min(30, slot * .62);
    const max = Math.max(1, ...a.weeks.map((w) => w.avg_done + w.avg_created));
    const top = Math.max(1, Math.ceil(max)), y = (v) => T + (H - T - B) * (1 - v / top);
    let g = "";
    for (let i = 0; i <= top; i += Math.max(1, Math.ceil(top / 4))) g += `<line class="wk-grid" x1="${L}" x2="${W}" y1="${y(i)}" y2="${y(i)}"/><text class="hm-label" x="${L - 6}" y="${y(i) + 4}" text-anchor="end">${i}</text>`;
    const bars = a.weeks.map((w, i) => {
      const x = L + i * slot + (slot - bw) / 2, last = i === n - 1;
      const dh = (H - T - B) * w.avg_done / top, ch = (H - T - B) * w.avg_created / top;
      const base = y(0);
      const doneRect = dh > 0 ? `<rect class="wk-done${last ? " live" : ""}" x="${x}" y="${base - dh}" width="${bw}" height="${dh}" rx="4"/>` : "";
      const madeRect = ch > 0 ? `<rect class="wk-made${last ? " live" : ""}" x="${x}" y="${base - dh - ch - (dh > 0 ? 2 : 0)}" width="${bw}" height="${ch}" rx="4"/>` : "";
      const label = dayOf(w.start).toLocaleDateString(undefined, { month: "short", day: "numeric" });
      return `<g class="wk" data-i="${i}"><rect class="wk-hit" x="${L + i * slot}" y="${T}" width="${slot}" height="${H - T}"/>${doneRect}${madeRect}` +
        `<text class="hm-label" x="${x + bw / 2}" y="${H - 8}" text-anchor="middle">${i === n - 1 ? "now" : label}</text></g>`;
    }).join("");
    return `<svg class="weekly" viewBox="0 0 ${W} ${H}" role="img" aria-label="Average tasks per day, by week">${g}${bars}</svg>`;
  }

  function weekdaySvg(a) {
    const W = 640, H = 190, L = 34, B = 26, T = 14, n = 7, slot = (W - L) / n, bw = Math.min(38, slot * .62);
    const max = Math.max(1, ...a.weekdays.map((w) => w.avg_done + w.avg_created));
    const top = Math.max(1, Math.ceil(max)), y = (v) => T + (H - T - B) * (1 - v / top), base = y(0);
    let g = "";
    for (let i = 0; i <= top; i += Math.max(1, Math.ceil(top / 4))) g += `<line class="wk-grid" x1="${L}" x2="${W}" y1="${y(i)}" y2="${y(i)}"/><text class="hm-label" x="${L - 6}" y="${y(i) + 4}" text-anchor="end">${i}</text>`;
    const bars = a.weekdays.map((w, i) => {
      const x = L + i * slot + (slot - bw) / 2, dh = (H - T - B) * w.avg_done / top, ch = (H - T - B) * w.avg_created / top, best = i === a.best_weekday;
      return `<g class="wd" data-i="${i}"><rect class="wk-hit" x="${L + i * slot}" y="${T}" width="${slot}" height="${H - T}"/>` +
        (dh > 0 ? `<rect class="wk-done${best ? " best" : ""}" x="${x}" y="${base - dh}" width="${bw}" height="${dh}" rx="4"/>` : "") +
        (ch > 0 ? `<rect class="wk-made" x="${x}" y="${base - dh - ch - (dh > 0 ? 2 : 0)}" width="${bw}" height="${ch}" rx="4"/>` : "") +
        `<text class="hm-label${best ? " best" : ""}" x="${x + bw / 2}" y="${H - 8}" text-anchor="middle">${w.name}</text></g>`;
    }).join("");
    return `<svg class="weekly" viewBox="0 0 ${W} ${H}" role="img" aria-label="Average tasks done and made per day of the week">${g}${bars}</svg>`;
  }

  function countUp(el, to, animate) {
    if (!animate || matchMedia("(prefers-reduced-motion: reduce)").matches || to < 1) { el.textContent = to; return; }
    const t0 = performance.now(), dur = 900;
    const step = (t) => { const k = Math.min(1, (t - t0) / dur); el.textContent = Math.round(to * (1 - Math.pow(1 - k, 3))); if (k < 1) requestAnimationFrame(step); };
    requestAnimationFrame(step);
  }

  function distSvg(d) {
    const cap = 12, W = 640, H = 200, L = 34, B = 26, T = 22, n = Math.min(d.counts.length, cap + 1);
    const counts = d.counts.slice(0, n).map((c, i) => (i === cap ? d.counts.slice(cap).reduce((x, y) => x + y, 0) : c));
    const slot = (W - L) / Math.max(n, 6), bw = Math.min(34, slot * .7);
    const top = Math.max(1, ...counts), y = (v) => T + (H - T - B) * (1 - v / top), base = y(0);
    const cx = (v) => L + slot * (Math.min(v, cap) + .5);
    let g = "";
    for (let i = 0; i <= top; i += Math.max(1, Math.ceil(top / 4))) g += `<line class="wk-grid" x1="${L}" x2="${W}" y1="${y(i)}" y2="${y(i)}"/><text class="hm-label" x="${L - 6}" y="${y(i) + 4}" text-anchor="end">${i}</text>`;
    const bars = counts.map((c, i) => {
      const h = base - y(c), x = cx(i) - bw / 2, isToday = Math.min(d.today, cap) === i;
      return `<g class="ds" data-n="${i}"><rect class="wk-hit" x="${L + i * slot}" y="${T}" width="${slot}" height="${H - T}"/>` +
        (c > 0 ? `<rect class="wk-done${isToday ? " best" : " dim"}" x="${x}" y="${y(c)}" width="${bw}" height="${h}" rx="4"/>` : "") +
        `<text class="hm-label${isToday ? " best" : ""}" x="${cx(i)}" y="${H - 8}" text-anchor="middle">${i === cap ? cap + "+" : i}</text></g>`;
    }).join("");
    const mark = (v, label, cls, dy) => `<line class="ds-mark ${cls}" x1="${cx(v)}" x2="${cx(v)}" y1="${T - 4}" y2="${base}"/><text class="hm-label ds-lbl" x="${cx(v) + 4}" y="${T + dy}">${label}</text>`;
    return `<svg class="weekly" viewBox="0 0 ${W} ${H}" role="img" aria-label="Histogram of tasks done per day">${g}${bars}${mark(d.median, "median", "med", 6)}${d.sigma > 0 ? mark(d.mean + d.sigma, "1 sigma", "sig", 6) : ""}</svg>`;
  }

  function distText(d) {
    if (d.days < 2) return "Needs a couple of finished days to compare against.";
    const med = d.to_median > 0 ? `${plural(d.to_median, "task")} away from being better than the median` : "already better than the median";
    const sig = d.to_sigma > 0 ? `${plural(d.to_sigma, "task")} away from 1 sigma` : "already past 1 sigma";
    return `${med}, ${sig}`.replace(/^./, (c) => c.toUpperCase());
  }

  // the calculation behind one day's score, one row per part
  function scoreRows(sc) {
    const rows = [
      ["Tasks completed", sc.done, "10 each, up to 50", sc.done_pts],
      ["Tasks created", sc.created, "2 each, up to 10", sc.created_pts],
      ["Quiz time", fmtStudy(sc.study), "1 a minute, up to 40", sc.study_pts],
      [sc.live ? "Overdue (if the day ended now)" : "Overdue at end of day", sc.overdue, "−5 each, up to −30", sc.overdue_pts],
    ];
    return rows.map(([l, n, rule, pts]) => `<tr class="${pts < 0 ? "neg" : ""}"><td>${l}</td><td class="n">${n}</td><td class="rule">${rule}</td><td class="pts">${signed(pts)}</td></tr>`).join("");
  }

  function scoreSection(a) {
    const sc = a.scores[a.today];
    const days = Object.keys(a.scores).sort().reverse().slice(0, 14);
    const rows = days.map((k) => {
      const x = a.scores[k];
      const label = k === a.today ? "Today" : dayOf(k).toLocaleDateString(undefined, { weekday: "short", month: "short", day: "numeric" });
      return `<tr><td>${label}${x.live ? ' <em class="live">live</em>' : ""}</td><td class="n">${x.done}</td><td class="n">${x.created}</td><td class="n">${fmtStudy(x.study)}</td>` +
        `<td class="n${x.overdue ? " neg" : ""}">${x.overdue}</td><td class="bar"><i class="l${scoreLevel(x.total)}" style="width:${x.total}%"></i></td><td class="n tot">${x.total}</td></tr>`;
    }).join("");
    return `<h2>Productivity score</h2>
      <div class="score-card">
        <div class="score-main"><div class="score-num" id="act-score">${sc.total}<em>/100</em></div>
          <span class="sub">Today so far${sc.overdue ? ` · ${plural(sc.overdue, "overdue task")} cost${sc.overdue === 1 ? "s" : ""} you ${-sc.overdue_pts}` : ""}</span></div>
        <table class="score-calc"><tbody>${scoreRows(sc)}</tbody>
          <tfoot><tr><td colspan="3">Score (0 to 100)</td><td class="pts">${sc.total}</td></tr></tfoot></table>
      </div>
      <p class="act-note" style="margin:6px 0 4px">Updates as you work. A task counts as overdue at the end of a day when it was due that day or earlier and still open; for today that is what would count if the day ended now.</p>
      <table class="score-days"><thead><tr><th>Day</th><th>Done</th><th>Made</th><th>Quiz</th><th>Overdue</th><th colspan="2">Score</th></tr></thead><tbody>${rows}</tbody></table>`;
  }

  async function renderActivity(keepScroll) {
    const a = await api("/api/activity");
    note.className = "note activity";
    backlinks.hidden = true;
    document.title = "Activity — notesview";
    const wk = a.weeks[a.weeks.length - 1];
    const today = a.days[a.today] || { done: 0, created: 0 };
    const next = MILESTONES.find(([m]) => m > a.total_done), prior = [...MILESTONES].reverse().find(([m]) => m <= a.total_done);
    const rank = prior ? prior[1] : "Fresh start";
    const pct = next ? Math.round(100 * (a.total_done - (prior ? prior[0] : 0)) / (next[0] - (prior ? prior[0] : 0))) : 100;
    const streakMsg = a.streak > 0 ? (today.done > 0 ? "Keep it going tomorrow" : "Finish one today to keep it") : "Finish a task to start one";

    note.innerHTML = `<h1>Activity</h1>
      <div class="act-tiles">
        <div class="tile hero"><small>Tasks completed</small><div class="big" id="act-total">${a.total_done}</div>
          <span class="sub">+${wk.done} this week${a.total_created ? ` · ${a.total_created} created in all` : ""}</span></div>
        <div class="tile"><small>Streak</small><div class="big">🔥 ${a.streak}<em>${a.streak === 1 ? "day" : "days"}</em></div>
          <span class="sub">${streakMsg} · best ${a.best_streak}</span></div>
        <div class="tile"><small>Today</small><div class="big">${today.done}<em>done</em></div>
          <span class="sub">${today.created} made</span></div>
        <div class="tile"><small>Quiz time</small><div class="big">${fmtStudy(a.study_today)}<em>today</em></div>
          <span class="sub">${fmtStudy(a.study_week)} this week · ${fmtStudy(a.study_total)} in all</span></div>
      </div>
      <div class="level"><span class="rank">${esc(rank)}</span>
        <div class="bar${next ? "" : " full"}"><i style="width:${pct}%"></i></div>
        <span class="sub">${next ? `${next[0] - a.total_done} more to <b>${esc(next[1])}</b> (${next[0]})` : "Top rank reached"}</span></div>
      ${scoreSection(a)}
      <div class="act-head"><div class="task-filter act-metric">
        ${[["both", "Done + made"], ["done", "Done"], ["created", "Made"], ["quiz", "Quiz time"], ["score", "Score"]].map(([k, l]) => `<button data-metric="${k}" class="${actMetric === k ? "on" : ""}">${l}</button>`).join("")}</div></div>
      <div class="hm-wrap">${heatmapSvg(a)}</div>
      <div class="hm-legend">Less ${[0, 1, 2, 3, 4].map((l) => `<i class="hm-cell l${l}"></i>`).join("")} More</div>
      <h2>Weekly</h2>
      <div class="wk-legend"><span><i class="sw done"></i>Done</span><span><i class="sw made"></i>Made</span></div>
      <div class="wk-wrap">${weeklySvg(a)}</div>
      <h2>Daily</h2>
      <div class="wk-wrap">${weekdaySvg(a)}</div>
      <h2>Distribution</h2>
      <p class="act-note" style="margin:-2px 0 4px">${esc(distText(a.distribution))}</p>
      ${a.distribution.days ? `<div class="wk-wrap">${distSvg(a.distribution)}</div>` : ""}
      ${a.earlier ? `<p class="act-note">${plural(a.earlier, "task")} completed before tracking began count toward your total but not the calendar.</p>` : ""}`;
    countUp($("#act-total"), a.total_done, !keepScroll);
    note._activity = a;
  }

  // metric switch, tooltips
  const tip = document.createElement("div");
  tip.className = "act-tip"; tip.hidden = true; document.body.appendChild(tip);
  const showTip = (e, html) => { tip.innerHTML = html; tip.hidden = false; const w = tip.offsetWidth; tip.style.left = Math.max(8, Math.min(innerWidth - w - 8, e.clientX - w / 2)) + "px"; tip.style.top = e.clientY - tip.offsetHeight - 14 + "px"; };
  note.addEventListener("mousemove", (e) => {
    const c = e.target.closest && e.target.closest(".hm-cell[data-d]"), w = e.target.closest && e.target.closest("g.wk"), wd = e.target.closest && e.target.closest("g.wd"), ds = e.target.closest && e.target.closest("g.ds");
    if (c) {
      const d = Number(c.dataset.done), m = Number(c.dataset.created);
      showTip(e, `<b>${dayOf(c.dataset.d).toLocaleDateString(undefined, { weekday: "short", month: "short", day: "numeric", year: "numeric" })}</b><br>${d || m ? `${plural(d, "task")} done · ${m} made` : "No activity"}${Number(c.dataset.study) ? `<br>${fmtStudy(Number(c.dataset.study))} of quiz` : ""}${Number(c.dataset.score) ? `<br>Score ${c.dataset.score}/100` : ""}`);
    } else if (w && note._activity) {
      const k = note._activity.weeks[Number(w.dataset.i)];
      showTip(e, `<b>Week of ${dayOf(k.start).toLocaleDateString(undefined, { month: "short", day: "numeric" })}</b><br>${fmtAvg(k.avg_done)} done / day (${k.done} in ${plural(k.days, "day")})<br>${fmtAvg(k.avg_created)} made / day (${k.created})<br>Running total ${k.total}`);
    } else if (wd && note._activity) {
      const k = note._activity.weekdays[Number(wd.dataset.i)];
      showTip(e, `<b>${FULLDAY[Number(wd.dataset.i)]}s</b><br>${fmtAvg(k.avg_done)} done on average (${k.done} over ${plural(k.count, "day")})<br>${fmtAvg(k.avg_created)} made on average (${k.created})`);
    } else if (ds && note._activity) {
      const d = note._activity.distribution, n = Number(ds.dataset.n), cap = 12;
      const days = n === cap ? d.counts.slice(cap).reduce((x, y) => x + y, 0) : d.counts[n] || 0;
      showTip(e, `<b>${n === cap ? cap + "+" : n} ${n === 1 ? "task" : "tasks"} a day</b><br>${plural(days, "day")} of ${d.days}`);
    } else tip.hidden = true;
  });
  note.addEventListener("mouseleave", () => { tip.hidden = true; });
  note.addEventListener("click", (e) => {
    const b = e.target.closest && e.target.closest(".act-metric button");
    if (b) { actMetric = b.dataset.metric; store.set("actMetric", actMetric); show(current, true); }
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
