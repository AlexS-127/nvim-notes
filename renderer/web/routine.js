// Morning routine card at the top of the Activity view (until the routine is complete or ended) and the
// Forecast tile. app.js calls in through window.nvRoutine with its own helpers (env), so this file has no
// globals besides that.
(() => {
  "use strict";
  const esc = (s) => String(s).replace(/[&<>"]/g, (c) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;" }[c]));

  function itemHtml(it, r) {
    if (it.kind === "forecast") {
      const f = r.forecast;
      const cur = f ? f.rating || 0 : 0;
      return `<li class="rt-item rt-forecast${it.done ? " done" : ""}"><span class="rt-label">${esc(it.label)}</span>
        <span class="rt-rate">${[1, 2, 3, 4, 5].map((n) => `<button data-rate="${n}" class="${cur === n ? "on" : ""}" title="${["", "Unproductive", "Below usual", "Usual", "Good", "Great"][n]}">${n}</button>`).join("")}</span></li>`;
    }
    return `<li class="rt-item${it.done ? " done" : ""}"><label><input type="checkbox" data-item="${esc(it.id)}"${it.done ? " checked" : ""}> <span class="rt-label">${esc(it.label)}</span></label></li>`;
  }

  function cardHtml(r) {
    if (!r || !r.show || !r.items.length) return "";
    const pts = r.done * r.item_pts;
    return `<section class="routine-card"><div class="rc-top"><small>Morning routine</small><span>${r.done} of ${r.items.length} · +${pts}${r.bonus ? ` (+${r.bonus} for all)` : ""}</span></div>
      <ul class="rt-list">${r.items.map((it) => itemHtml(it, r)).join("")}</ul>
      <div class="rt-foot"><small>Forecast: 1 unproductive · 3 a usual day · 5 a great day. It's compared with how the day turns out.</small><button class="rt-end">End routine</button></div></section>`;
  }

  // today's forecast (1-5; older ones were a score) — hidden until one is made
  function tileHtml(r) {
    const f = r && r.forecast;
    if (!f) return "";
    if (f.rating) return `<div class="tile"><small>Forecast</small><div class="big">${f.rating}<em>/ 5</em></div><span class="sub">how productive today should be</span></div>`;
    return `<div class="tile"><small>Forecast</small><div class="big">${f.strike}</div><span class="sub">older score forecast</span></div>`;
  }

  function bind(env) {
    const note = env.note, err = (e) => env.toast(String(e.message || e).replace(/^routine: /, "").trim());
    note.addEventListener("change", async (ev) => {
      const t = ev.target;
      if (!t.dataset || !t.dataset.item || !t.closest(".routine-card")) return;
      try { await env.post("/api/routine/tick", { item: t.dataset.item, done: t.checked }); env.refresh(); }
      catch (e) { err(e); t.checked = !t.checked; }
    });
    note.addEventListener("click", async (ev) => {
      const b = ev.target.closest && ev.target.closest(".rt-rate [data-rate]");
      if (!b) return;
      try { await env.post("/api/routine/forecast", { rating: +b.dataset.rate }); env.toast(`Forecast: ${b.dataset.rate} / 5`); env.refresh(); }
      catch (e) { err(e); }
    });
    note.addEventListener("click", async (ev) => {
      if (!ev.target.closest || !ev.target.closest(".rt-end")) return;
      try { await env.post("/api/routine/end", {}); env.refresh(); } catch (e) { err(e); }
    });
  }

  window.nvRoutine = { cardHtml, tileHtml, bind };
})();
