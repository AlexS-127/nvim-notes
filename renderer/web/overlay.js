// Overlay numbers: today's score, pace (ticks every second, like the Activity tile), tasks done, open tasks due
// today, quiz time and new words, from /api/activity every 10 s and whenever the notes change; focus and the
// activity category of the last minute (/api/data/now, every 30 s) and the next class within 3 hours
// (/api/calendar/upcoming). With overlay_debug (Settings → Overlay) a panel shows what every sensor sees
// (/api/data/now?debug=1 every 5 s). The app sizes its window to the content ("size:W:H"). Theme applies live.
(() => {
  "use strict";
  const $ = (id) => document.getElementById(id);
  const { slopeFn, slopeFmt, paceTier } = window.nvPace;
  const fmtStudy = (sec) => { const m = Math.round(sec / 60); return m >= 60 ? `${Math.floor(m / 60)}h ${m % 60}m` : `${m}m`; };
  let slope = null;
  const nv = window.webkit && window.webkit.messageHandlers && window.webkit.messageHandlers.nv;
  const esc = (v) => String(v ?? "").replace(/[&<>"]/g, (c) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;" }[c]));
  // tell the app how big the content is, so the window fits it
  let lastSize = "";
  function fit() {
    const b = $("ov-wrap").getBoundingClientRect(), sz = `size:${Math.ceil(b.width)}:${Math.ceil(b.height)}`;
    if (sz !== lastSize && nv) { lastSize = sz; nv.postMessage(sz); }
  }

  async function load() {
    try {
      const r = await fetch("/api/activity");
      if (!r.ok) throw new Error();
      const a = await r.json(), day = a.days[a.today] || {};
      $("ov-score").textContent = (a.scores[a.today] || {}).total ?? 0;
      $("ov-done").textContent = day.done || 0;
      $("ov-quiz").textContent = fmtStudy(a.study_today || 0);
      $("ov-words").textContent = (a.words_today || 0).toLocaleString();
      fit();
      slope = slopeFn(a).at;
      $("ov").classList.remove("offline");
      tick();
    } catch (e) { $("ov").classList.add("offline"); }
  }
  function tick() {
    if (!slope) return;
    const v = slope(Date.now()), el = $("ov-pace");
    el.textContent = slopeFmt(v);
    el.dataset.tier = paceTier(v);
  }

  // the theme for the current appearance, live (Settings, macOS switching light/dark)
  const dark = matchMedia("(prefers-color-scheme: dark)");
  let cfg = window.__notesviewTheme || {};
  function applyTheme() {
    const h = document.documentElement, a = cfg.appearance || (dark.matches ? "dark" : "light"), slot = cfg[a] || { palette: cfg.palette, mode: cfg.mode };
    if (slot.palette) h.dataset.palette = slot.palette;
    h.dataset.theme = slot.mode || a;
    ["body", "heading", "ui"].forEach((k) => cfg.font ? h.style.setProperty("--font-" + k, cfg.font) : h.style.removeProperty("--font-" + k));
  }
  dark.addEventListener("change", applyTheme);

  // ── the current minute (feature store): focus, category, due today; debug panel ──
  let debug = !!cfg.overlay_debug, nowTimer = null;
  const fmtAge = (s) => (s < 90 ? `${s}s` : s < 5400 ? `${Math.round(s / 60)}m` : `${Math.round(s / 3600)}h`);
  async function loadNow() {
    try {
      const d = await (await fetch("/api/data/now" + (debug ? "?debug=1" : ""))).json();
      const due = d.due_today || 0;
      $("ov-due").textContent = due;
      $("ov-due-s").hidden = !due;
      $("ov-focus-s").hidden = d.focus === undefined && !d.cat;
      if (d.focus !== undefined) {
        $("ov-focus").textContent = d.active ? Math.round(d.focus * 100) + "%" : "away";
        $("ov-focus").dataset.band = !d.active ? "" : d.focus < 0.4 ? "low" : d.focus >= 0.75 ? "high" : "";
      } else $("ov-focus").textContent = "–";
      $("ov-cat").textContent = d.cat || "focus";
      $("ov-debug").hidden = !debug;
      if (debug) $("ov-debug").innerHTML = debugHtml(d);
    } catch (e) {}
    fit();
  }
  function schedule() {
    clearInterval(nowTimer);
    nowTimer = setInterval(loadNow, debug ? 5000 : 30000);
    loadNow();
  }

  // the source categories of the minute, the one the feature store picked highlighted
  const CAT_SOURCES = [["nvim_cat", "neovim"], ["browser_cat", "site"], ["window_cat", "window"], ["screen_class", "screen"], ["app_cat", "app"], ["sys_app_cat", "sampler"]];
  const SHOW = { // the fields worth seeing per sensor; others are left out
    input: ["keys", "clicks", "scroll", "idle"], apps: ["app", "switches", "locked", "display_sleep", "power", "battery"], window: ["cat", "conf", "app"],
    browser: ["cat", "browser"], screen: ["class", "conf", "from", "words", "app"], camera: ["present", "facing", "perclos", "yawn", "frames"],
    mic: ["db", "speech", "device"], place: ["place"], media: ["playing", "app"], nvim: ["folder", "keys", "ins", "bs", "bursts"], sys: ["app", "idle"],
    weather: ["temperature_2m_max", "precipitation_sum", "cloud_cover_mean"], screentime: ["bundle", "cat", "device"], pmset: ["ev"], messages: ["app", "n"], health: ["metric", "qty"],
  };
  function debugHtml(d) {
    if (!d.minute) return `<h4>No feature row yet</h4>`;
    const r = d.row || {}, c = r.c || {}, v = r.v || {}, mask = r.mask || {};
    const fmt = (x) => (typeof x === "number" ? Math.round(x * 100) / 100 : x === true ? "yes" : x === false ? "no" : x);
    let h = `<h4>Minute ${esc(d.minute)} · feature store</h4>`;
    h += `<div class="ovd-row"><span class="ovd-k">category</span><span class="ovd-pick">${esc(c.cat || "–")}</span><span>${CAT_SOURCES.filter(([k]) => c[k]).map(([k, l]) => `${l} <span class="${c[k] === c.cat ? "ovd-pick" : ""}">${esc(c[k])}</span>`).join(" · ") || "no source"}</span></div>`;
    h += `<div class="ovd-row"><span class="ovd-k">focus</span><span>${d.focus !== undefined ? Math.round(d.focus * 100) + "%" : "–"}</span><span>${d.active ? "active" : "inactive"} · keys ${fmt(v.keys ?? "–")} · switches ${fmt(v.switches ?? "–")} · present ${fmt(v.present ?? "–")} · place ${esc(c.place || "–")}</span></div>`;
    const off = Object.entries(mask).filter(([, m]) => m !== "on").map(([k, m]) => `${k} ${m}`);
    h += `<div class="ovd-row"><span class="ovd-k">masked</span><span>${off.length}</span><span class="${off.length ? "ovd-warn" : ""}">${esc(off.join(" · ") || "every sensor reporting")}</span></div>`;
    const zs = Object.entries(r.z || {}).filter(([, z]) => Math.abs(z) >= 2).map(([k, z]) => `${k} ${z > 0 ? "+" : ""}${fmt(z)}`);
    if (zs.length) h += `<div class="ovd-row"><span class="ovd-k">unusual</span><span>${zs.length}</span><span>${esc(zs.join(" · "))}</span></div>`;
    h += `<h4>Sensors · newest reading</h4>`;
    const live = d.live || {}, ages = d.ages || {}, iv = d.intervals || {};
    for (const sn of Object.keys(live).sort()) {
      const age = ages[sn], exp = iv[sn], stale = exp && age > exp * 3, rec = live[sn];
      const keys = SHOW[sn] || Object.keys(rec).filter((k) => k !== "at" && !/hash/.test(k)).slice(0, 6);
      const body = keys.filter((k) => rec[k] !== undefined && rec[k] !== "").map((k) => `${k} ${esc(fmt(rec[k]))}`).join(" · ");
      h += `<div class="ovd-row"><span class="ovd-k">${esc(sn)}</span><span class="${!exp ? "ovd-off" : stale ? "ovd-stale" : "ovd-ok"}">${age !== undefined ? fmtAge(age) : "–"}</span><span>${body || "–"}</span></div>`;
    }
    const hp = d.helper || {};
    h += `<h4>Helper · labels · quality</h4><div class="ovd-row"><span class="ovd-k">helper</span><span class="${hp.running ? "ovd-ok" : "ovd-stale"}">${hp.running ? "on" : "off"}</span><span>${hp.running ? "pid " + esc(hp.pid) : hp.installed ? "stopped" : "not installed"} · ${d.queue || 0} to label</span></div>`;
    for (const q of d.quality || []) h += `<div class="ovd-row"><span class="ovd-k">${esc(q.sensor)}</span><span class="ovd-warn">${esc(q.status)}</span><span>${esc(q.check)}: ${esc(q.detail)}</span></div>`;
    return h;
  }

  // next class: shown from 3 hours before until it ends
  async function loadClass() {
    try {
      const d = await (await fetch("/api/calendar/upcoming")).json(), now = Date.now();
      const c = (d.classes || []).find((x) => !x.all_day && new Date(x.end).getTime() > now);
      const start = c && new Date(c.start).getTime(), mins = c && Math.round((start - now) / 60000);
      $("ov-class-s").hidden = !c || mins > 180;
      if (c && mins <= 180) {
        $("ov-class").textContent = mins <= 0 ? "now" : mins < 60 ? `${mins}m` : `${Math.floor(mins / 60)}h ${mins % 60}m`;
        $("ov-class").toggleAttribute("data-soon", mins <= 15);
        $("ov-class-t").textContent = (c.exam ? "Exam · " : "") + c.title + (c.location ? " · " + c.location : "");
      }
    } catch (e) {}
    fit();
  }

  const es = new EventSource("/events");
  es.addEventListener("change", async (e) => {
    let m = {};
    try { m = JSON.parse(e.data); } catch (err) {}
    if (m.css) {
      try {
        cfg = await (await fetch("/api/config")).json(); applyTheme();
        if (!!cfg.overlay_debug !== debug) { debug = !!cfg.overlay_debug; schedule(); }
      } catch (err) {}
    }
    load();
  });
  es.addEventListener("open", load);

  // focus check-in (labels.go): while one is open the strip asks, and the app listens for ⌥⌘1–5
  let asking = "";
  async function prompts() {
    try {
      const d = await (await fetch("/api/data/prompts")).json(), open = d.prompts.checkin || "";
      if (open !== asking) {
        asking = open;
        $("ov-ask").hidden = !open;
        fit();
        if (nv) nv.postMessage(open ? "checkin-on" : "checkin-off");
      }
    } catch (e) {}
  }
  // called by NotesView.app when ⌥⌘1–5 is pressed
  window.nvCheckin = async (n) => {
    if (!asking) return;
    try {
      await fetch("/api/data/checkin", { method: "POST", headers: { "X-Notesview": "1", "Content-Type": "application/json" }, body: JSON.stringify({ prompted: asking, focus: n }) });
      $("ov-ask").querySelector("b").textContent = "✓ " + n;
      setTimeout(prompts, 1500);
    } catch (e) {}
  };

  load();
  prompts();
  schedule();
  loadClass();
  setInterval(loadClass, 60000);
  setInterval(load, 10000);
  setInterval(tick, 1000);
  setInterval(prompts, 30000);
})();
