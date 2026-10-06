// Morning routine card at the top of the Activity view (until the routine is complete or ended) and the
// Forecast tile. app.js calls in through window.nvRoutine with its own helpers (env), so this file has no
// globals besides that.
(() => {
  "use strict";
  const esc = (s) => String(s).replace(/[&<>"]/g, (c) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;" }[c]));
  const pct = (c) => Math.round((c || 0.8) * 100);

  function itemHtml(it, r) {
    if (it.kind === "forecast") {
      const f = r.forecast;
      return `<li class="rt-item rt-forecast${it.done ? " done" : ""}"><span class="rt-label">${esc(it.label)}</span>
        <form class="rt-call"><input name="strike" inputmode="numeric" placeholder="score" value="${f ? f.strike : ""}" aria-label="Score you are ${pct(r.conf)}% sure to reach today" autocomplete="off"><button>${f ? "Change" : "Buy call"}</button></form></li>`;
    }
    return `<li class="rt-item${it.done ? " done" : ""}"><label><input type="checkbox" data-item="${esc(it.id)}"${it.done ? " checked" : ""}> <span class="rt-label">${esc(it.label)}</span></label></li>`;
  }

  function cardHtml(r) {
    if (!r || !r.show || !r.items.length) return "";
    const pts = r.done * r.item_pts;
    return `<section class="routine-card"><div class="rc-top"><small>Morning routine</small><span>${r.done} of ${r.items.length} · +${pts}${r.bonus ? ` (+${r.bonus} for all)` : ""}</span></div>
      <ul class="rt-list">${r.items.map((it) => itemHtml(it, r)).join("")}</ul>
      <div class="rt-foot"><small>The call option: the score you're ${pct(r.conf)}% sure to reach today.</small><button class="rt-end">End routine</button></div></section>`;
  }

  // today's forecast against the score so far (hidden until one is made)
  function tileHtml(r, total) {
    const f = r && r.forecast;
    if (!f) return "";
    const left = f.strike - total;
    return `<div class="tile${left <= 0 ? " rt-hit" : ""}"><small>Forecast (${pct(f.conf)}%)</small><div class="big">${f.strike}</div>
      <span class="sub">${left <= 0 ? "✓ reached" : `${left} to go`}</span></div>`;
  }

  function bind(env) {
    const note = env.note, err = (e) => env.toast(String(e.message || e).replace(/^routine: /, "").trim());
    note.addEventListener("change", async (ev) => {
      const t = ev.target;
      if (!t.dataset || !t.dataset.item || !t.closest(".routine-card")) return;
      try { await env.post("/api/routine/tick", { item: t.dataset.item, done: t.checked }); env.refresh(); }
      catch (e) { err(e); t.checked = !t.checked; }
    });
    note.addEventListener("submit", async (ev) => {
      const f = ev.target;
      if (!f.classList.contains("rt-call")) return;
      ev.preventDefault();
      const v = f.elements.strike.value.trim();
      if (!/^\d+$/.test(v)) return env.toast("The forecast is a score: a whole number");
      try { await env.post("/api/routine/forecast", { strike: +v }); env.toast(`Call bought: ${v} or more today`); env.refresh(); }
      catch (e) { err(e); }
    });
    note.addEventListener("click", async (ev) => {
      if (!ev.target.closest || !ev.target.closest(".rt-end")) return;
      try { await env.post("/api/routine/end", {}); env.refresh(); } catch (e) { err(e); }
    });
  }

  window.nvRoutine = { cardHtml, tileHtml, bind };
})();
