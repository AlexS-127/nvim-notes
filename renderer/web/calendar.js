// Classes page (attendance), the calendars panel of the Settings page (import schedules) and the
// "next class" card of the Activity view. app.js calls in through
// window.nvCalendar with its own helpers (env), so this file has no globals besides that.
(() => {
  "use strict";
  const pad = (n) => String(n).padStart(2, "0");
  const iso = (d) => `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}`;
  const day = (s) => new Date(s + "T00:00:00");
  const addDays = (d, n) => { const x = new Date(d); x.setDate(x.getDate() + n); return x; };
  const monday = (d) => addDays(d, -((d.getDay() + 6) % 7));
  const hm = (d) => d.toLocaleTimeString(undefined, { hour: "numeric", minute: "2-digit" });
  const COLORS = ["blue", "green", "orange", "violet", "teal", "rose", "amber", "indigo", "mint", "pink"];
  const DOW = ["Mon", "Tue", "Wed", "Thu", "Fri", "Sat", "Sun"];
  const HOUR = 46; // px per hour in the week view
  const esc = (s) => String(s).replace(/[&<>"]/g, (c) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;" }[c]));

  const until = (ms) => {
    const m = Math.round(ms / 60000);
    return m < 1 ? "now" : m < 60 ? `in ${m} min` : m < 1440 ? `in ${Math.floor(m / 60)} h ${m % 60} min` : `in ${Math.round(m / 1440)} d`;
  };
  const span = (e) => e.all_day ? "All day" : `${hm(new Date(e.start))} – ${hm(new Date(e.end))}`;
  const colorStyle = (c) => ` style="--tc: var(--sw-${esc(c || "blue")})"`;

  // what a class's check-in looks like right now: [label, state]
  function checkState(e, d) {
    const early = d.early_min || 15, pts = d.points || 5;
    if (e.checked) return [`✓ Checked in at ${hm(new Date(e.checked))}`, "done"];
    if (e.can_check) return [`Check in  +${pts}`, "open"];
    if (e.missed) return ["Missed: no check-in", "missed"];
    const opens = new Date(new Date(e.start).getTime() - early * 60000);
    return [`Check-in opens ${hm(opens)}`, "wait"];
  }

  // ── next class card (Activity view) ──
  function nextClassHtml(up) {
    if (!up) return "";
    if (!up.classes.length) {
      return up.calendars ? "" : `<p class="act-note nc-hint"><a href="#/classes">Import your class schedule (.ics)</a> to check in to classes for points.</p>`;
    }
    const [e, ...later] = up.classes, now = new Date(up.now), st = new Date(e.start);
    const [label, state] = checkState(e, up);
    const when = e.can_check && !e.checked ? (st <= now ? "happening now" : until(st - now)) : st <= now ? "happening now" : until(st - now);
    const day0 = iso(now) === e.date ? "" : st.toLocaleDateString(undefined, { weekday: "long" }) + " · ";
    return `<section class="next-class" data-state="${state}">
      <div class="nc-main"><small>Next class</small><div class="nc-title">${esc(e.title)}</div>
        <div class="nc-meta">${esc(day0 + span(e))}${e.location ? " · " + esc(e.location) : ""} · ${esc(when)}</div></div>
      ${state === "open" ? `<button class="nc-btn" data-checkin="${esc(e.id)}">${esc(label)}</button>` : `<span class="nc-status nc-${state}">${esc(label)}</span>`}
      ${later.length ? `<ul class="nc-later">${later.slice(0, 2).map((l) => `<li><span>${esc(l.date === iso(now) ? "Today" : new Date(l.start).toLocaleDateString(undefined, { weekday: "short" }))} ${esc(hm(new Date(l.start)))}</span> ${esc(l.title)}</li>`).join("")}</ul>` : ""}
    </section>`;
  }

  let ncTimer;
  // keep the card fresh (countdown, check-in window opening); its button is handled in bind()
  function startNextClassTimer(env, root) {
    clearInterval(ncTimer);
    ncTimer = setInterval(async () => {
      const el = root.querySelector(".next-class, .nc-hint");
      if (!root.isConnected || !el || !env.isActive("activity")) return clearInterval(ncTimer);
      try {
        const html = nextClassHtml(await env.api("/api/calendar/upcoming"));
        if (!html) return;
        const t = document.createElement("div");
        t.innerHTML = html;
        el.replaceWith(t.firstElementChild);
      } catch (err) {}
    }, 30000);
  }

  // ── classes page (#/classes, key c): import .ics schedules, see attendance ──
  let asClass = null;

  function panelHtml(d) {
    const rows = d.calendars.map((c) => `<div class="cp-row" data-cal="${esc(c.id)}"${colorStyle(c.color)}>
        <input type="checkbox" data-f="enabled"${c.enabled ? " checked" : ""} title="Use this calendar">
        <button class="cp-dot" data-f="color" title="Change colour"></button>
        <input class="cp-name" data-f="name" value="${esc(c.name)}" spellcheck="false" aria-label="Calendar name">
        <label class="cp-class" title="Events in this calendar are classes: check in for points"><input type="checkbox" data-f="class"${c.class ? " checked" : ""}> classes</label>
        <small>${c.events} event${c.events === 1 ? "" : "s"}</small>
        <button class="cp-del" data-f="delete" title="Remove this calendar" aria-label="Remove calendar">×</button></div>`).join("");
    return `<div class="cal-panel">${rows || `<p class="act-note">No calendars yet.</p>`}
      <div class="cp-import">
        <button class="on" id="cal-import">Import .ics…</button>
        <input id="cal-file" type="file" accept=".ics,text/calendar" multiple hidden>
        <label class="cp-class"><input type="checkbox" id="cal-asclass"${asClass ? " checked" : ""}> these are classes (check-in earns ${d.points} points)</label>
        <small>Or drop .ics files on this page. Importing a file with the same name as a calendar updates it.</small>
      </div></div>`;
  }

  const pct = (c) => (c.held ? Math.round(c.attended / c.held * 100) : null);

  function attendanceHtml(d) {
    if (!d.attendance.length) return `<p class="act-note">Attendance appears here once a class has ended.</p>`;
    const sum = d.summary;
    return `<table class="att-table"><thead><tr><th>Class</th><th>Attended</th><th></th></tr></thead><tbody>${d.attendance.map((a) => {
      const p = pct(a);
      return `<tr${colorStyle(a.color)}><td><i></i>${esc(a.title)}</td><td>${a.attended} of ${a.held}</td><td class="att-bar"><span style="width:${p}%"></span><b>${p}%</b></td></tr>`;
    }).join("")}</tbody></table>
    <p class="act-note">Counted from the day each calendar was imported. Overall ${sum.total_pct < 0 ? "–" : sum.total_pct + "%"}.</p>`;
  }

  async function render(env) {
    const d = await env.api("/api/calendar"), note = env.note;
    note.className = "note classes";
    env.setTitle("Classes");
    note.innerHTML = `<h1>Classes</h1>
      <p class="act-note">The next class shows on the Activity view and the start page, where you can check in from ${d.early_min} minutes before it starts until it ends, for ${d.points} points. Import or change calendars in <a href="#/settings">Settings</a>.</p>
      ${d.calendars.length ? "" : `<p class="act-note">No calendars yet: <a href="#/settings">import your class schedule (.ics)</a>.</p>`}
      <h2>Attendance</h2>${attendanceHtml(d)}`;
    note._cal = { env, d };
  }

  // the calendars panel for the Settings page (import, rename, colour, classes switch, remove)
  async function panel(env) {
    const d = await env.api("/api/calendar");
    if (asClass === null) asClass = env.store.get("calAsClass", "1") === "1";
    env.note._cal = { env, d };
    return panelHtml(d);
  }

  async function importFiles(env, files) {
    let n = 0;
    for (const f of files) {
      if (!/\.ics$/i.test(f.name) && !/calendar/i.test(f.type)) { env.toast(`${f.name}: not an .ics file`); continue; }
      try {
        const m = await env.post("/api/calendar/import", { name: f.name.replace(/\.ics$/i, ""), ics: await f.text(), class: asClass });
        env.toast(`Imported ${m.name}: ${m.events} event${m.events === 1 ? "" : "s"}`);
        n++;
      } catch (err) { env.toast(`${f.name}: ${String(err.message || err).trim()}`); }
    }
    if (n) env.refresh();
  }

  // one set of listeners on the page element
  function bind(env) {
    const note = env.note, onPage = () => (note.classList.contains("classes") || note.classList.contains("settings")) && note._cal;
    note.addEventListener("click", async (ev) => {
      const t = ev.target, q = (sel) => t.closest && t.closest(sel);
      let b;
      if ((b = q("[data-checkin]"))) { // a check-in button on the Activity card
        b.disabled = true;
        try { const r = await env.post("/api/calendar/checkin", { id: b.dataset.checkin }); env.toast(`+${r.points} · checked in to ${r.event.title}`); env.refresh(); }
        catch (err) { env.toast(String(err.message || err).replace(/^check-in: /, "")); b.disabled = false; }
        return;
      }
      if (!onPage()) return;
      if (q("#cal-import")) return note.querySelector("#cal-file").click();
      const row = q(".cp-row");
      if (row && (b = q("[data-f]"))) {
        const id = row.dataset.cal, cal = note._cal.d.calendars.find((x) => x.id === id), f = b.dataset.f;
        if (f === "color") await env.post("/api/calendar/update", { id, color: COLORS[(COLORS.indexOf(cal.color) + 1) % COLORS.length] });
        else if (f === "delete") { if (!confirm(`Remove “${cal.name}”? Your check-ins and their points are kept.`)) return; await env.post("/api/calendar/delete", { id }); }
        else return;
        env.refresh();
      }
    });
    note.addEventListener("change", async (ev) => {
      const t = ev.target;
      if (!onPage()) return;
      if (t.id === "cal-file") { const files = [...t.files]; t.value = ""; return importFiles(env, files); }
      if (t.id === "cal-asclass") { asClass = t.checked; env.store.set("calAsClass", t.checked ? "1" : "0"); return; }
      const row = t.closest(".cp-row"), f = t.dataset.f;
      if (!row || !f) return;
      const patch = { id: row.dataset.cal };
      if (f === "enabled" || f === "class") patch[f] = t.checked; else if (f === "name") patch.name = t.value; else return;
      await env.post("/api/calendar/update", patch);
      env.refresh();
    });
    note.addEventListener("dragover", (ev) => { if (onPage() && ev.dataTransfer && [...ev.dataTransfer.types].includes("Files")) { ev.preventDefault(); note.classList.add("dropping"); } });
    note.addEventListener("dragleave", (ev) => { if (ev.target === note) note.classList.remove("dropping"); });
    note.addEventListener("drop", (ev) => {
      note.classList.remove("dropping");
      if (!onPage() || !ev.dataTransfer || !ev.dataTransfer.files.length) return;
      ev.preventDefault();
      importFiles(env, [...ev.dataTransfer.files]);
    });
  }

  window.nvCalendar = { render, bind, panel, nextClassHtml, startNextClassTimer };
})();
