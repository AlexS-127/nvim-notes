// Data: the home card with the optional labels (focus check-in, evening check-out, labelling queue; no
// points) and the Data tab (#/data, key y). app.js calls in through window.nvData with its own helpers (env),
// so this file has no globals besides that.
(() => {
  "use strict";
  const esc = (s) => String(s ?? "").replace(/[&<>"]/g, (c) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;" }[c]));
  const msg = (e) => String(e.message || e).replace(/^label: /, "").trim();
  const rateRow = (name, label, hint) => `<div class="dl-rate" data-name="${name}"><span title="${esc(hint || "")}">${label}</span>${[1, 2, 3, 4, 5].map((n) => `<button type="button" data-v="${n}">${n}</button>`).join("")}</div>`;

  // ── home card ──
  function queueItemHtml(it, cats, places) {
    const opts = (it.kind === "place" ? places : cats).map((c) => `<option value="${c}"${it.guess === c ? " selected" : ""}>${c}</option>`).join("");
    const what = it.kind === "place" ? "Where is this network?" : it.kind === "screen" ? `Screen at ${esc((it.first || "").slice(11, 16))}${it.app ? " in " + esc(it.app) : ""}` : esc(it.text);
    return `<div class="dl-item" data-kind="${esc(it.kind)}" data-hash="${esc(it.hash)}" data-tokens="${esc((it.tokens || []).join(","))}">
      <div class="dl-what"><small>${esc(it.kind)}${it.app && it.kind !== "screen" ? " · " + esc(it.app) : ""} · seen ${it.count}×</small><span>${what}</span></div>
      <select aria-label="Category">${it.guess ? "" : `<option value="">choose…</option>`}${opts}</select>
      <button type="button" class="dl-save">Label</button><button type="button" class="set-icon dl-skip" title="Skip">×</button></div>`;
  }

  // the sensor helper: state and Stop / Start / Restart (launch agent local.notesview-sense)
  function helperHtml(h) {
    if (!h || !h.installed) return "";
    return `<div class="dl-block dl-helper"><span class="dt-state dt-${h.running ? "on" : "off"}">Sensors ${h.running ? "running" : "stopped"}</span>
      ${h.running ? `<button type="button" data-helper="stop">Stop</button><button type="button" data-helper="restart">Restart</button>` : `<button type="button" data-helper="start">Start</button>`}
      <small>${h.running ? "camera, sound, screen, apps… (switches on the Data tab)" : "stays stopped until Start or your next login"}</small></div>`;
  }

  function cardHtml(d) {
    if (!d) return "";
    const p = d.prompts, q = d.queue || [];
    let body = helperHtml(d.helper);
    const fails = d.quality ? d.quality.checks.filter((c) => c.status === "fail") : [];
    if (fails.length) body += `<div class="dl-block"><span class="dt-state dt-denied">Data quality: ${fails.length} problem${fails.length === 1 ? "" : "s"}</span><small>${esc(fails.slice(0, 2).map((c) => c.sensor + ": " + c.detail).join(" · "))}</small><a href="#/data">details</a></div>`;
    if (p.checkin) body += `<div class="dl-block" data-block="checkin" data-prompted="${esc(p.checkin)}">${rateRow("focus", "Focused right now?", "1 not at all · 5 fully focused")}<button type="button" class="set-icon dl-skip-prompt" title="Skip">×</button></div>`;
    if (p.checkout) body += `<form class="dl-block dl-checkout" data-block="checkout"><b>Evening check-out</b>
      ${rateRow("productivity", "Productive today", "1 not at all · 5 very")}${rateRow("energy", "Energy", "1 drained · 5 full of energy")}${rateRow("mood", "Mood", "1 low · 5 great")}${rateRow("sleep", "Sleep last night", "1 terrible · 5 great")}
      <input name="note" placeholder="What helped or hurt today? (optional)" autocomplete="off"><div class="dl-actions"><button>Save</button><button type="button" class="dl-skip-prompt">Not today</button></div></form>`;
    if (q.length) body += `<div class="dl-block"><b>Help label your data</b> <small>(${q.length})</small>${q.slice(0, 3).map((it) => queueItemHtml(it, d.categories, d.places)).join("")}</div>`;
    if (!body) return "";
    return `<section class="data-card"><div class="rc-top"><small>Your data · optional, no points</small><a href="#/data">Data tab</a></div>${body}</section>`;
  }

  async function load(env) {
    try {
      const [p, q, h, qual] = await Promise.all([env.api("/api/data/prompts"), env.api("/api/data/queue"), env.api("/api/data/helper").catch(() => null), env.api("/api/data/quality").catch(() => null)]);
      return { prompts: p.prompts, queue: q.queue, categories: q.categories, places: q.places, helper: h, quality: qual };
    } catch (e) { return null; }
  }

  function bind(env) {
    const note = env.note;
    note.addEventListener("click", async (ev) => {
      const t = ev.target, q = (s) => t.closest && t.closest(s);
      let b;
      try {
        if ((b = q("[data-helper]"))) {
          b.disabled = true;
          const st = await env.post("/api/data/helper", { action: b.dataset.helper });
          env.toast(`Sensors ${st.running ? "running" : "stopped"}`);
          return setTimeout(() => env.refresh(), 800);
        }
        if ((b = q(".dl-rate button"))) {
          const row = b.closest(".dl-rate"), block = b.closest(".dl-block");
          row.querySelectorAll("button").forEach((x) => x.classList.toggle("on", x === b));
          row.dataset.value = b.dataset.v;
          if (block.dataset.block === "checkin") { await env.post("/api/data/checkin", { prompted: block.dataset.prompted, focus: +b.dataset.v }); env.toast("Thanks"); env.refresh(); }
          return;
        }
        if ((b = q(".dl-skip-prompt"))) {
          const block = b.closest(".dl-block");
          await env.post("/api/data/skip", { target: block.dataset.block, key: block.dataset.prompted || "" });
          return env.refresh();
        }
        if ((b = q(".dl-save, .dl-skip"))) {
          const it = b.closest(".dl-item"), sel = it.querySelector("select");
          if (b.classList.contains("dl-skip")) await env.post("/api/data/skip", { target: it.dataset.kind, key: it.dataset.hash });
          else {
            if (!sel.value) return env.toast("Choose a category");
            await env.post("/api/data/label", { kind: it.dataset.kind, hash: it.dataset.hash, category: sel.value, tokens: it.dataset.tokens ? it.dataset.tokens.split(",") : [] });
          }
          return env.refresh();
        }
      } catch (e) { env.toast(msg(e)); }
    });
    note.addEventListener("submit", async (ev) => {
      const f = ev.target;
      if (!f.classList.contains("dl-checkout")) return;
      ev.preventDefault();
      const v = (n) => +(f.querySelector(`.dl-rate[data-name="${n}"]`).dataset.value || 0);
      try {
        await env.post("/api/data/checkout", { productivity: v("productivity"), energy: v("energy"), mood: v("mood"), sleep: v("sleep"), note: f.elements.note.value });
        env.toast("Checked out for today"); env.refresh();
      } catch (e) { env.toast(msg(e)); }
    });
  }

  // ── Data tab (#/data, key y) ──
  const CAT_COLORS = { study: "var(--sw-blue)", problem: "var(--sw-indigo)", writing: "var(--sw-teal)", reading: "var(--sw-cyan)", admin: "var(--sw-slate)", comms: "var(--sw-amber)",
    entertainment: "var(--sw-red)", social: "var(--sw-pink)", news: "var(--sw-orange)", shopping: "var(--sw-brown)", other: "var(--sw-gray)", browser: "var(--sw-gray)", unknown: "var(--sw-gray)" };
  const PLACE_COLORS = { home: "var(--sw-green)", library: "var(--sw-blue)", class: "var(--sw-violet)", cafe: "var(--sw-amber)", other: "var(--sw-gray)", unknown: "var(--sw-gray)", none: "transparent" };
  const STATE_LABEL = { on: "collecting", waiting: "waiting for data", denied: "permission needed", absent: "not available", off: "switched off" };

  // a strip of minute cells: numbers as opacity, categories as colours
  function stripSvg(vals, n, now, kind, colors) {
    const W = 1440, H = 16, step = W / n;
    let cells = "";
    for (let i = 0; i < n; i++) {
      const v = vals[i];
      if (v === null || v === undefined) continue;
      if (kind === "cat") cells += `<rect x="${(i * step).toFixed(1)}" y="0" width="${(step + 0.4).toFixed(1)}" height="${H}" fill="${colors[v] || "var(--sw-gray)"}"><title>${String(Math.floor(i / 60)).padStart(2, "0")}:${String(i % 60).padStart(2, "0")} ${esc(v)}</title></rect>`;
      else if (v > 0) cells += `<rect x="${(i * step).toFixed(1)}" y="0" width="${(step + 0.4).toFixed(1)}" height="${H}" fill="var(--accent)" fill-opacity="${Math.min(1, Math.max(0.08, v)).toFixed(2)}"></rect>`;
    }
    const nowX = now >= 0 && now < n ? `<line x1="${now * step}" x2="${now * step}" y1="0" y2="${H}" stroke="var(--fg)" stroke-width="2"/>` : "";
    return `<svg class="dt-strip" viewBox="0 0 ${W} ${H}" preserveAspectRatio="none">${cells}${nowX}</svg>`;
  }

  function scoreSvg(vals, n, now) {
    const W = 1440, H = 70, step = W / n;
    const pts = [];
    let top = 1;
    vals.forEach((v) => { if (v !== null && v !== undefined) top = Math.max(top, v); });
    vals.forEach((v, i) => { if (v !== null && v !== undefined) pts.push(`${(i * step).toFixed(1)},${(H - 4 - (H - 8) * v / top).toFixed(1)}`); });
    return `<svg class="dt-score" viewBox="0 0 ${W} ${H}" preserveAspectRatio="none"><polyline points="${pts.join(" ")}" fill="none" stroke="var(--accent)" stroke-width="2.5" vector-effect="non-scaling-stroke"/></svg>`;
  }

  const norm = (vals, fn) => vals.map((v) => (v === null || v === undefined ? null : fn(v)));

  function todayHtml(t) {
    const n = t.minutes, s = t.strips;
    const row = (label, svg, hint) => `<div class="dt-row"><span title="${esc(hint || "")}">${label}</span>${svg}</div>`;
    const hours = [0, 3, 6, 9, 12, 15, 18, 21].map((h) => `<span style="left:${(h * 60 / n * 100).toFixed(2)}%">${h}:00</span>`).join("");
    return `<div class="dt-today">
      ${row("Score", scoreSvg(s.score, n, t.now), "the score curve")}
      ${row("Focus", stripSvg(s.focus, n, t.now, "num"), "inferred focus 0-1 (focus.go)")}
      ${row("Active", stripSvg(s.active, n, t.now, "num"), "input in the minute, not idle")}
      ${row("Activity", stripSvg(s.cat, n, t.now, "cat", CAT_COLORS), "site > window title > screen > app")}
      ${row("Screen", stripSvg(s.screen, n, t.now, "cat", CAT_COLORS), "on-screen activity class")}
      ${row("Typing", stripSvg(norm(s.keys.map((v, i) => (v ?? 0) + (s.nvim_keys[i] ?? 0) || null), (v) => Math.min(1, v / 200)), n, t.now, "num"), "keys per minute (all apps)")}
      ${row("At desk", stripSvg(s.present, n, t.now, "num"), "camera: face present")}
      ${row("Sound", stripSvg(norm(s.db, (v) => Math.min(1, Math.max(0, (v + 70) / 50))), n, t.now, "num"), "sound level")}
      ${row("Phone", stripSvg(s.phone, n, t.now, "num"), "iPhone use (Screen Time)")}
      ${row("Place", stripSvg(s.place, n, t.now, "cat", PLACE_COLORS), "your label for the Wi-Fi network")}
      ${row("Heart rate", stripSvg(norm(s.hr, (v) => Math.min(1, Math.max(0.05, (v - 45) / 80))), n, t.now, "num"), "Apple Watch")}
      <div class="dt-hours">${hours}</div>
      <div class="dt-legend">${Object.entries(CAT_COLORS).filter(([k]) => !["browser", "unknown"].includes(k)).map(([k, c]) => `<span><i style="background:${c}"></i>${k}</span>`).join("")}</div>
    </div>`;
  }

  function coverageHtml(o) {
    const h = o.helper;
    const helperNote = `<div class="data-card">${helperHtml(h) || `<p class="act-note set-warn">The sensor helper's launch agent isn't installed: run <code>~/dotfiles/bootstrap.sh</code>.</p>`}<p class="act-note">The helper (NotesViewSense) runs input, apps, window, browser, screen, camera, sound, place and music. It starts at login and restarts after a crash; Stop keeps it off until Start or the next login.</p></div>`;
    return `${helperNote}<table class="att-table dt-cov"><thead><tr><th></th><th>Sensor</th><th>Stores</th><th>State</th><th>Last 14 days</th></tr></thead><tbody>${o.sensors.map((r) => {
      const max = Math.max(60, ...r.Days);
      const bars = r.Days.map((d) => `<i style="height:${Math.round(100 * d / max)}%" title="${d} min"></i>`).join("");
      const perm = r.State === "denied" && r.Pane ? ` <button class="dt-perm" data-perm="${esc(r.Permission)}"${r.Permission === "Full Disk Access" ? ` title="Add notesview (shown in Finder) to the list: the server runs this importer, not the helper"` : ""}>Grant ${esc(r.Permission)}…</button>` : "";
      return `<tr><td><input type="checkbox" data-sensor="${esc(r.Name)}"${r.On ? " checked" : ""} title="Switch on/off"></td><td><b>${esc(r.Title)}</b><small>${esc(r.Permission || "no permission needed")}</small></td><td class="dt-stores">${esc(r.Stores)}</td>
        <td><span class="dt-state dt-${r.State}">${esc(STATE_LABEL[r.State] || r.State)}</span>${perm}</td><td><span class="dt-bars">${bars}</span></td></tr>`;
    }).join("")}</tbody></table>`;
  }

  function settingsHtml(o) {
    const c = o.config;
    return `<form class="set-grid" id="dt-settings">
      <label>City (weather)</label><div><input class="cp-name" name="city" value="${esc(c.city)}" placeholder="e.g. Atlanta" autocomplete="off"><small>${c.lat ? `${c.lat}, ${c.lon}` : "not set"}</small></div>
      <label>Health export folder</label><div><input class="cp-name" name="health_dir" value="${esc(c.health_dir)}" placeholder="iCloud folder Health Auto Export writes to" autocomplete="off"><small>for the Apple Watch</small></div>
      <label>Semester start</label><div><input class="cp-name set-pages" name="semester_start" value="${esc(c.semester_start)}" placeholder="YYYY-MM-DD" autocomplete="off"><small>for week of semester</small></div>
      <label></label><div><button class="dl-save">Save</button></div></form>`;
  }

  function labelsHtml(d) {
    const g = d.grid, names = ["1", "2", "3", "4", "5"];
    const grid = `<table class="dt-grid"><thead><tr><th>forecast ↓ / day →</th>${names.map((n) => `<th>${n}</th>`).join("")}</tr></thead><tbody>${g.map((row, i) => `<tr><th>${i + 1}</th>${row.map((v, j) => `<td class="${i === j ? "diag" : ""}" style="--a:${Math.min(1, v / 4)}">${v || ""}</td>`).join("")}</tr>`).join("")}</tbody></table>`;
    return `<div class="dt-labels">${grid}<p class="act-note">Forecast (morning, 1-5) against how the day turned out (its score's quintile among the 30 days before: 1 = bottom fifth). ${d.rated ? `${d.hits} of ${d.rated} days exactly right.` : "Needs a few finished days with a forecast."}</p></div>`;
  }

  function daysHtml(d) {
    if (!d.days.length) return `<p class="act-note">No days yet.</p>`;
    const c = (v) => (v === undefined || v === null ? "–" : v);
    return `<table class="att-table dt-days"><thead><tr><th>Day</th><th>Score</th><th>Forecast</th><th>Outcome</th><th>Check-out</th><th>Focus check-in</th><th>Focus min</th><th>Sleep h</th><th>Phone min</th><th>Temp</th></tr></thead><tbody>${d.days.map((r) => `<tr>
      <td><a href="#/data?day=${r.day}" data-day="${r.day}">${esc(r.day)}</a></td><td>${c(r.score)}</td><td>${c(r.forecast)}</td><td>${c(r.outcome)}</td>
      <td>${r.checkout ? `${r.checkout.productivity || "–"}/${r.checkout.energy || "–"}/${r.checkout.mood || "–"}/${r.checkout.sleep || "–"}` : "–"}</td><td>${c(r.checkin_mean)}</td><td>${c(r.focus_min)}</td><td>${c(r.sleep_h)}</td><td>${c(r.phone_min)}</td><td>${c(r.temp)}</td></tr>`).join("")}</tbody></table>
      <p class="act-note">Check-out is productivity / energy / mood / sleep (1-5).</p>`;
  }

  function featuresHtml(f) {
    const m = f.focus_model || {}, imp = f.importance;
    const impHtml = imp && imp.groups ? `<p class="act-note">Importance over ${imp.days} days (R² ${imp.r2.toFixed(2)}): how much predicting the day's score gets worse without each group.</p><table class="att-table">${Object.entries(imp.groups).sort((a, b) => b[1] - a[1]).map(([k, v]) => `<tr><td>${esc(k)}</td><td class="att-bar"><span style="width:${Math.max(0, Math.min(100, v * 400))}%"></span><b>${v.toFixed(3)}</b></td></tr>`).join("")}</table>`
      : `<p class="act-note">Importance (which sensors help predict your day) runs once there are 30 days of features: <code>notesview data importance</code>.</p>`;
    return `<div class="set-grid"><label>Schema</label><div>version ${f.schema} · ${f.files} day file${f.files === 1 ? "" : "s"} · ${(f.bytes / 1024).toFixed(0)} kB · last built ${esc(f.last_built || "never")}</div>
      <label>Focus model</label><div>${m.n ? `${m.n} check-ins · version 1 ${Math.round(100 * (m.acc_v1 || 0))}% · version 2 ${Math.round(100 * (m.acc_v2 || 0))}% · using version ${m.use ? 2 : 1}` : "version 1 (version 2 is fitted once there are 50 focus check-ins)"}</div>
      <label></label><div><button id="dt-rebuild" class="dl-save">Rebuild missing days</button></div></div>
      ${impHtml}
      <details class="dt-chan"><summary>Channels (${f.channels.length})</summary><table class="att-table">${f.channels.map(([k, v]) => `<tr><td><code>${esc(k)}</code></td><td>${esc(v)}</td></tr>`).join("")}</table><p class="act-note">z-scored: ${f.z.map(esc).join(", ")}.</p></details>`;
  }

  function privacyHtml(a, o) {
    return `<p class="act-note">Stored: counts, categories, hashes and model outputs only: no keystroke contents, window titles, URLs, screenshots, camera frames or audio. Titles, sites and network names the helper can't classify wait in a local queue outside the notes folder (mode 0600, at most 7 days) until you label or skip them. Raw records: <code>${esc(o.dirs.signals)}</code>, features: <code>${esc(o.dirs.features)}</code>, labels: <code>${esc(o.dirs.labels)}</code>.</p>
      <div class="set-grid"><label>Audit</label><div><span class="dt-state dt-${a.issues.length ? "denied" : "on"}">${esc(a.summary)}</span></div>
      <label>Backup</label><div>${a.backup.exists ? `private repo <code>${esc(a.backup.repo)}</code>, last commit ${esc(a.backup.last)}` : `not set up yet (<code>${esc(a.backup.repo)}</code>)`}</div>
      <label>Forget</label><div><input class="cp-name set-pages" id="dt-forget-day" placeholder="YYYY-MM-DD"><button class="dl-save" id="dt-forget">Delete that day's data</button></div></div>
      ${a.issues.length ? `<table class="att-table">${a.issues.slice(0, 20).map((i) => `<tr><td>${esc(i.file)}:${i.line}</td><td>${esc(i.key)}</td><td><code>${esc(i.value)}</code></td></tr>`).join("")}</table>` : ""}`;
  }

  // ── Live: the newest reading of each sensor (what they're seeing right now) ──
  const pctOf = (v) => (v === undefined || v === null ? "–" : Math.round(v * 100) + "%");
  const meter = (v) => `<span class="dt-meter"><i style="width:${Math.max(0, Math.min(100, v * 100)).toFixed(0)}%"></i></span>`;
  const LIVE = [
    ["camera", "Camera", (r) => `${meter(r.present)} at desk ${pctOf(r.present)} · facing ${pctOf(r.facing)} · eyes closed ${pctOf(r.perclos)}${r.yawn ? " · yawn" : ""}`],
    ["mic", "Sound", (r) => `${meter(Math.max(0, (r.db + 80) / 70))} ${r.db} dB · speech ${pctOf(r.speech)}${r.device ? " · " + esc(r.device) + " mic" : ""}`],
    ["screen", "Screen", (r) => `<b>${esc(r.class)}</b> · confidence ${pctOf(r.conf)} · ${r.words} words read${r.app ? " in " + esc(r.app) : ""}`],
    ["window", "Window", (r) => `<b>${esc(r.cat)}</b>${r.conf !== undefined ? " · confidence " + pctOf(r.conf) : ""}${r.app ? " · " + esc(r.app) : ""}`],
    ["browser", "Browser", (r) => `<b>${esc(r.cat)}</b> · ${esc(r.browser)}`],
    ["input", "Keyboard & mouse", (r) => `${r.keys * 4} keys/min · ${r.clicks * 4} clicks/min · idle ${r.idle} s`],
    ["apps", "Apps", (r) => `${esc(r.app)} · ${r.switches} switches · ${r.power === "ac" ? "charging" : "battery"} ${r.battery >= 0 ? r.battery + "%" : ""} · ${r.displays} display${r.displays === 1 ? "" : "s"}${r.locked ? " · locked" : ""}`],
    ["place", "Place", (r) => `<b>${esc(r.place)}</b>`],
    ["media", "Music", (r) => (r.playing ? `playing in ${esc(r.app)}` : "not playing")],
    ["nvim", "Neovim", (r) => `${r.keys} keys · ${r.bs || 0} backspaces${r.folder ? " · " + esc(r.folder) : ""}`],
    ["sys", "Server sampler", (r) => `${esc(r.app)} · idle ${r.idle} s`],
  ];
  function ago(at, now) {
    const s = Math.round((new Date(now) - new Date(at)) / 1000);
    return s < 60 ? `${s} s ago` : s < 3600 ? `${Math.round(s / 60)} min ago` : `${Math.round(s / 3600)} h ago`;
  }
  function liveHtml(l) {
    const rows = LIVE.filter(([k]) => l.readings[k]).map(([k, title, f]) => {
      const r = l.readings[k], stale = (new Date(l.now) - new Date(r.at)) / 1000 > 180;
      let body;
      try { body = f(r); } catch (e) { body = esc(JSON.stringify(r)); }
      return `<div class="dt-live-row${stale ? " stale" : ""}"><span>${title}</span><div>${body}</div><small>${ago(r.at, l.now)}</small></div>`;
    }).join("");
    return `<div class="dt-live">${rows || `<p class="act-note">No readings today yet.</p>`}</div>
      <p class="act-note">The newest reading of each sensor, refreshed every 15 seconds. Only these numbers and categories are kept: never the picture, the sound or the text on screen.</p>`;
  }

  // ── Quality: today's checks ──
  const Q_ICON = { fail: "✗", warn: "!", ok: "✓", info: "·" };
  function qualityHtml(q) {
    if (!q.checks.length) return `<p class="act-note">No checks yet: they need some active time with the helper running.</p>`;
    const bad = q.checks.filter((c) => c.status === "fail" || c.status === "warn").length;
    return `<p class="act-note">${bad ? `${bad} check${bad === 1 ? "" : "s"} need attention.` : "Everything looks plausible."} Coverage (readings while you were active), liveness, stuck values, physical ranges, and agreement between independent sensors (the camera should see you while you type; the screen should match the site or window).</p>
      <table class="att-table dt-quality">${q.checks.map((c) => `<tr class="q-${c.status}"><td class="q-icon">${Q_ICON[c.status] || ""}</td><td>${esc(c.sensor)}</td><td>${esc(c.check)}</td><td>${esc(c.detail)}</td></tr>`).join("")}</table>`;
  }

  let liveTimer;
  async function render(env) {
    const note = env.note, day = new URLSearchParams(location.hash.split("?")[1] || "").get("day") || "";
    const [o, t, d, f, a, live, q] = await Promise.all([env.api("/api/data/overview"), env.api("/api/data/today" + (day ? "?day=" + day : "")), env.api("/api/data/days"), env.api("/api/data/features"), env.api("/api/data/audit"),
      env.api("/api/data/live"), env.api("/api/data/quality" + (day ? "?day=" + day : ""))]);
    note.className = "note datatab";
    env.setTitle("Data");
    const sec = (id, title, html) => `<section class="set-sec" id="dt-${id}"><h2>${title}</h2>${html}</section>`;
    note.innerHTML = `<h1>Data</h1>
      <nav class="set-nav">${[["live", "Live"], ["quality", "Quality"], ["today", t.day === o.today ? "Today" : t.day], ["sensors", "Sensors"], ["labels", "Labels"], ["days", "Days"], ["features", "Features"], ["privacy", "Privacy"], ["settings", "Settings"]].map(([id, l]) => `<a href="#/data" data-jump="${id}">${esc(l)}</a>`).join("")}</nav>
      <p class="act-note">What the data layer collects for the score market's traders (and you): ${Object.keys(t.cover).length} sensors reported ${t.day === o.today ? "today" : "that day"}. Optional labels (check-ins, check-out, categories) come from home and carry no points.</p>
      ${sec("live", "What the sensors see now", liveHtml(live))}
      ${sec("quality", "Data quality", qualityHtml(q))}
      ${sec("today", t.day === o.today ? "Today" : esc(t.day), todayHtml(t))}
      ${sec("sensors", "Sensors", coverageHtml(o))}
      ${sec("labels", "Labels", labelsHtml(d))}
      ${sec("days", "Days", daysHtml(d))}
      ${sec("features", "Feature store", featuresHtml(f))}
      ${sec("privacy", "Privacy and backup", privacyHtml(a, o))}
      ${sec("settings", "Settings", settingsHtml(o))}`;
    clearInterval(liveTimer);
    liveTimer = setInterval(async () => {
      const el = note.querySelector("#dt-live");
      if (!el || !note.classList.contains("datatab")) return clearInterval(liveTimer);
      try { const l = await env.api("/api/data/live"); el.innerHTML = `<h2>What the sensors see now</h2>${liveHtml(l)}`; } catch (e) {}
    }, 15000);
  }

  function bindTab(env) {
    const note = env.note, on = () => note.classList.contains("datatab");
    note.addEventListener("change", async (ev) => {
      const t = ev.target;
      if (!on() || !t.dataset.sensor) return;
      try { await env.post("/api/data/sensor", { name: t.dataset.sensor, on: t.checked }); env.toast(`${t.dataset.sensor}: ${t.checked ? "on" : "off"}`); } catch (e) { env.toast(msg(e)); }
    });
    note.addEventListener("click", async (ev) => {
      if (!on()) return;
      const t = ev.target, q = (s) => t.closest && t.closest(s);
      let b;
      try {
        if ((b = q("[data-jump]"))) { ev.preventDefault(); const s = note.querySelector("#dt-" + b.dataset.jump); if (s) s.scrollIntoView({ behavior: "smooth", block: "start" }); return; }
        if ((b = q("[data-perm]"))) return env.post("/api/data/permission", { permission: b.dataset.perm });
        if (q("#dt-rebuild")) { const r = await env.post("/api/data/rebuild", {}); env.toast(`Rebuilt ${r.rebuilt.length} day(s)`); return env.refresh(); }
        if (q("#dt-forget")) {
          const day = note.querySelector("#dt-forget-day").value.trim();
          if (!day || !confirm(`Delete every raw record and the features of ${day}? Labels stay.`)) return;
          await env.post("/api/data/forget", { day }); env.toast("Deleted"); return env.refresh();
        }
      } catch (e) { env.toast(msg(e)); }
    });
    note.addEventListener("submit", async (ev) => {
      if (!on() || ev.target.id !== "dt-settings") return;
      ev.preventDefault();
      const f = ev.target.elements;
      try { await env.post("/api/data/settings", { city: f.city.value, health_dir: f.health_dir.value, semester_start: f.semester_start.value }); env.toast("Saved"); env.refresh(); }
      catch (e) { env.toast(msg(e)); }
    });
  }

  window.nvData = { cardHtml, load, bind: (env) => { bind(env); bindTab(env); }, render };
})();
