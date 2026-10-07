// Overlay numbers: today's score, pace (ticks every second, like the Activity tile), tasks done, quiz time and
// new words, from /api/activity every 10 s and whenever the notes change. Theme changes apply live.
(() => {
  "use strict";
  const $ = (id) => document.getElementById(id);
  const { slopeFn, slopeFmt, paceTier } = window.nvPace;
  const fmtStudy = (sec) => { const m = Math.round(sec / 60); return m >= 60 ? `${Math.floor(m / 60)}h ${m % 60}m` : `${m}m`; };
  let slope = null;

  async function load() {
    try {
      const r = await fetch("/api/activity");
      if (!r.ok) throw new Error();
      const a = await r.json(), day = a.days[a.today] || {};
      $("ov-score").textContent = (a.scores[a.today] || {}).total ?? 0;
      $("ov-done").textContent = day.done || 0;
      $("ov-quiz").textContent = fmtStudy(a.study_today || 0);
      $("ov-words").textContent = (a.words_today || 0).toLocaleString();
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

  const es = new EventSource("/events");
  es.addEventListener("change", async (e) => {
    let m = {};
    try { m = JSON.parse(e.data); } catch (err) {}
    if (m.css) { try { cfg = await (await fetch("/api/config")).json(); applyTheme(); } catch (err) {} }
    load();
  });
  es.addEventListener("open", load);

  // focus check-in (labels.go): while one is open the strip asks, and the app listens for ⌥⌘1–5
  let asking = "";
  const nv = window.webkit && window.webkit.messageHandlers && window.webkit.messageHandlers.nv;
  async function prompts() {
    try {
      const d = await (await fetch("/api/data/prompts")).json(), open = d.prompts.checkin || "";
      if (open !== asking) {
        asking = open;
        $("ov-ask").hidden = !open;
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
  setInterval(load, 10000);
  setInterval(tick, 1000);
  setInterval(prompts, 30000);
})();
