// Settings page (#/settings, key ","): appearance (light and dark themes, synced with Neovim and Ghostty;
// light/dark/auto; font), transparency of the native app, calendars (calendar.js panel), morning routine
// items, books, new-word folders, folder colours, and About (version, rebuild and restart).
// app.js calls in through window.nvSettings with its own helpers (env), so this file has no globals besides that.
(() => {
  "use strict";
  const esc = (s) => String(s ?? "").replace(/[&<>"]/g, (c) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;" }[c]));
  const SECTIONS = [["appearance", "Appearance"], ["transparency", "Transparency"], ["overlay", "Overlay"], ["calendars", "Calendars"], ["routine", "Morning routine"],
    ["books", "Books"], ["words", "New words"], ["colors", "Folder colours"], ["about", "About"]];
  const msg = (e) => String(e.message || e).replace(/^(routine|reading): /, "").trim();

  const opt = (v, label, cur) => `<option value="${esc(v)}"${v === cur ? " selected" : ""}>${esc(label)}</option>`;

  function appearanceHtml(d) {
    const c = d.config;
    const themeSel = (name, cur) => `<select data-cfg="${name}">${d.themes.map((t) => opt(t.name, t.name, cur)).join("")}</select>`;
    const isPreset = !c.font || d.fonts.some((f) => f.name === c.font);
    return `<div class="set-grid">
        <label>Light theme</label><div>${themeSel("theme_light", c.theme_light)}<small>${esc((d.themes.find((t) => t.name === c.theme_light) || {}).desc)}</small></div>
        <label>Dark theme</label><div>${themeSel("theme_dark", c.theme_dark)}<small>${esc((d.themes.find((t) => t.name === c.theme_dark) || {}).desc)}</small></div>
        <label>Appearance</label><div class="task-filter set-seg">${[["auto", "Follow macOS"], ["light", "Light"], ["dark", "Dark"]].map(([k, l]) => `<button data-appearance="${k}" class="${c.appearance === k ? "on" : ""}">${l}</button>`).join("")}</div>
        <label>Font</label><div><select data-cfg="font">${d.fonts.map((f) => opt(f.name === "default" ? "" : f.name, `${f.name} — ${f.desc}`, c.font || "")).join("")}${isPreset ? "" : opt(c.font, `${c.font} (custom)`, c.font)}</select></div>
        <label>Ghostty</label><div><label class="cp-class"><input type="checkbox" data-cfg="sync_ghostty"${c.sync_ghostty ? " checked" : ""}> Change Ghostty's theme too</label>
          <small>${d.ghostty.path ? `<code>${esc(d.ghostty.theme)}</code> in ${esc(d.ghostty.path)}` : "No Ghostty config found."}</small></div>
      </div>
      <p class="act-note">Neovim follows these two themes as well (it reads the same config file and switches live; the first time a theme is used it installs the matching colorscheme). Its background is see-through, so the terminal's theme should match: keep the Ghostty option on.</p>
      ${d.warning ? `<p class="act-note set-warn">${esc(d.warning)}</p>` : ""}`;
  }

  function transparencyHtml(d) {
    const follow = !d.config.opacity, v = Math.round((d.config.opacity || d.ghostty.opacity) * 100);
    return `<div class="set-grid">
        <label>Background</label><div class="set-row"><input type="range" id="set-opacity" min="20" max="100" step="1" value="${v}"${follow ? " disabled" : ""}><b id="set-opacity-v">${v}%</b></div>
        <label></label><div><label class="cp-class"><input type="checkbox" id="set-opacity-follow"${follow ? " checked" : ""}> Same as Ghostty (${Math.round(d.ghostty.opacity * 100)}%)</label></div>
      </div>
      <p class="act-note">How see-through the NotesView app's window is (with the blur behind it). A browser window can't be transparent, so this only shows in the app.</p>`;
  }

  function overlayHtml() {
    const inApp = !!(window.webkit && window.webkit.messageHandlers && window.webkit.messageHandlers.nv);
    return `<div class="set-row set-actions"><button id="set-overlay"${inApp ? "" : " disabled"}>Show / hide overlay</button></div>
      <p class="act-note">A small see-through strip that floats above every app (and full-screen apps, on every Space) with today's score, pace, tasks done, quiz time and new words. Toggle it with <code>o</code> here, <code>⌥⌘O</code> from anywhere, or the app's Overlay menu (⇧⌘O). Drag it to move it; right-click it for click-through (clicks pass to the app under it) and reset. It stays up when the main window is closed.${inApp ? "" : " Only in the NotesView app."}</p>`;
  }

  function routineHtml(items) {
    const rows = items.map((it, i) => `<div class="set-item" data-i="${i}">
        <span class="set-num">${i + 1}</span>
        <input class="cp-name" data-f="label" value="${esc(it.label)}" aria-label="Item ${i + 1}" spellcheck="false">
        ${it.kind === "forecast" ? `<small class="set-tag" title="Asks for the score you're 80% sure to reach today">forecast</small>` : ""}
        <button class="set-icon" data-f="up" title="Move up"${i ? "" : " disabled"}>↑</button>
        <button class="set-icon" data-f="down" title="Move down"${i < items.length - 1 ? "" : " disabled"}>↓</button>
        <button class="cp-del" data-f="del" title="Remove" aria-label="Remove">×</button></div>`).join("");
    const hasForecast = items.some((it) => it.kind === "forecast");
    return `<div class="cal-panel">${rows || `<p class="act-note">No items: the routine won't show.</p>`}
      <form class="set-add" id="set-routine-add"><input name="label" placeholder="New item" autocomplete="off" aria-label="New routine item"><button>Add</button>
        ${hasForecast ? "" : `<button type="button" id="set-routine-forecast">Add the call option</button>`}</form></div>
      <p class="act-note">Shown every day until all are done or you end the routine. 1 point each, 5 more for all. Changes apply from now; past days keep their points.</p>`;
  }

  function booksHtml(books) {
    if (!books.length) return `<p class="act-note">No books yet. Add them on the <a href="#/reading">Reading</a> page.</p>`;
    const st = [["to-read", "To read"], ["reading", "Reading"], ["read", "Read"]];
    return `<div class="cal-panel">${books.map((b) => `<div class="set-item set-book" data-book="${b.id}">
        <input class="cp-name" data-f="title" value="${esc(b.title)}" aria-label="Title" spellcheck="false">
        <input class="cp-name set-author" data-f="author" value="${esc(b.author)}" aria-label="Author" spellcheck="false">
        <input class="cp-name set-pages" data-f="pages" value="${b.pages || ""}" placeholder="pages" inputmode="numeric" aria-label="Pages" title="Number of pages (empty = unknown)">
        <select data-f="status" aria-label="Status">${st.map(([k, l]) => opt(k, l, b.status)).join("")}</select>
        <button class="cp-del" data-f="del" title="Remove (logged pages keep their points)" aria-label="Remove">×</button></div>`).join("")}</div>`;
  }

  function wordsHtml(w) {
    return `<div class="cal-panel set-words">${w.candidates.map((c) => `<label class="cp-class"><input type="checkbox" data-track="${esc(c)}"${w.tracked.includes(c) || w.tracked.includes(c.replace(/\.md$/, "")) ? " checked" : ""}> ${esc(c)}</label>`).join("")}
      ${w.tracked.filter((t) => !w.candidates.includes(t) && !w.candidates.includes(t + ".md")).map((t) => `<label class="cp-class"><input type="checkbox" data-track="${esc(t)}" checked> ${esc(t)}</label>`).join("")}</div>
      <h3>Not counted inside those</h3>
      <div class="cal-panel">${w.excluded.map((x) => `<div class="set-item"><span>${esc(x)}</span><span class="spacer"></span><button class="cp-del" data-unexclude="${esc(x)}" title="Count it again" aria-label="Count again">×</button></div>`).join("") || `<p class="act-note">Nothing excluded.</p>`}
        <form class="set-add" id="set-exclude"><input name="path" placeholder="e.g. lat101/definitions.md" autocomplete="off" aria-label="Note or folder to exclude"><button>Exclude</button></form></div>
      <p class="act-note">Only words you write in ticked folders count (1 point per ${w.per_point}). Leave out folders where Claude or pasted text writes.</p>`;
  }

  function colorsHtml(folders, colors, swatches) {
    if (!folders.length) return `<p class="act-note">No folders yet.</p>`;
    return `<div class="cal-panel">${folders.map((f) => {
      const own = colors[f.tag] || "", inherited = !own && f.kind === "topic" ? colors[f.category] || "" : "";
      return `<div class="set-item set-color${f.kind === "topic" ? " topic" : ""}" data-tag="${esc(f.tag)}"><span class="set-cname">${esc(f.kind === "topic" ? f.topic_name : f.category_name)}</span>
        <span class="set-swatches">${swatches.map((c) => `<button data-c="${c}" style="--tc: var(--sw-${c})" title="${c}" aria-label="${c}" class="${own === c ? "on" : inherited === c ? "inh" : ""}"></button>`).join("")}<button data-c="" class="none${own ? "" : " on"}" title="${f.kind === "topic" ? "Same as its category" : "No colour"}" aria-label="No colour">×</button></span></div>`;
    }).join("")}</div>`;
  }

  function aboutHtml(a) {
    const rows = [["Version", `notesview ${a.version}`], ["Notes folder", a.dir], ["Config", a.config], ["Server", `127.0.0.1:${a.port} · ${a.viewers} viewer${a.viewers === 1 ? "" : "s"} · since ${new Date(a.started).toLocaleString()}`],
      ["Source", a.source || "not found"], ["Go", a.go || "not found"]];
    return `<div class="set-grid">${rows.map(([k, v]) => `<label>${k}</label><div><code>${esc(v)}</code></div>`).join("")}</div>
      <div class="set-row set-actions"><button id="set-restart">Restart server</button><button id="set-rebuild"${a.can_rebuild ? "" : " disabled"}>Rebuild &amp; restart</button><small id="set-rebuild-msg"></small></div>
      <p class="act-note">Rebuild compiles notesview from the source folder (like the start page's <code>r</code>) and restarts the server on the new build; the page reloads when it is back.</p>`;
  }

  async function render(env) {
    const note = env.note;
    const [d, items, reading, words, folders, colors] = await Promise.all([env.api("/api/settings"), env.api("/api/routine/items"), env.api("/api/reading"),
      env.api("/api/words"), env.api("/api/folders"), env.api("/api/folder-colors")]);
    const cal = await window.nvCalendar.panel(env);
    note.className = "note settings";
    env.setTitle("Settings");
    note._set = { d, items };
    const sec = (id, title, html) => `<section class="set-sec" id="set-${id}"><h2>${title}</h2>${html}</section>`;
    note.innerHTML = `<h1>Settings</h1>
      <nav class="set-nav">${SECTIONS.map(([id, l]) => `<a href="#/settings" data-jump="${id}">${l}</a>`).join("")}</nav>
      ${sec("appearance", "Appearance", appearanceHtml(d))}
      ${sec("transparency", "Transparency", transparencyHtml(d))}
      ${sec("overlay", "Overlay", overlayHtml())}
      ${sec("calendars", "Calendars", cal + `<p class="act-note">Attendance is on the <a href="#/classes">Classes</a> page.</p>`)}
      ${sec("routine", "Morning routine", routineHtml(items))}
      ${sec("books", "Books", booksHtml(reading.books))}
      ${sec("words", "New words", wordsHtml(words))}
      ${sec("colors", "Folder colours", colorsHtml(folders, colors, d.swatches))}
      ${sec("about", "About", aboutHtml(d.about))}`;
  }

  async function saveRoutine(env, items) {
    try { await env.post("/api/routine/items", { items }); } catch (e) { env.toast(msg(e)); }
    env.refresh();
  }

  async function restart(env, build) {
    const m = env.note.querySelector("#set-rebuild-msg");
    if (m) m.textContent = build ? "Building…" : "Restarting…";
    env.note.querySelectorAll("#set-restart, #set-rebuild").forEach((b) => { b.disabled = true; });
    try { await env.post("/api/rebuild", { build }); }
    catch (e) { if (m) m.textContent = msg(e).slice(0, 400); env.note.querySelectorAll("#set-restart, #set-rebuild").forEach((b) => { b.disabled = false; }); return; }
    // wait for the server to come back, then reload the page (new web assets too)
    for (let i = 0; i < 60; i++) {
      await new Promise((r) => setTimeout(r, 500));
      try { await env.api("/api/status"); location.reload(); return; } catch (e) {}
    }
    if (m) m.textContent = "The server didn't come back. Check ~/Library/Logs/notesview.log.";
  }

  function bind(env) {
    const note = env.note, on = () => note.classList.contains("settings") && note._set;
    const appearance = async (patch) => {
      try {
        const r = await env.post("/api/settings/appearance", patch);
        if (r.ghostty) env.toast(r.ghostty.startsWith("Ghostty:") ? r.ghostty : "Ghostty: " + r.ghostty.replace(/^theme = /, ""));
      } catch (e) { env.toast(msg(e)); }
      await env.reloadConfig();
      env.refresh();
    };
    note.addEventListener("click", async (ev) => {
      if (!on()) return;
      const t = ev.target, q = (s) => t.closest && t.closest(s);
      let b;
      if ((b = q("[data-jump]"))) { ev.preventDefault(); const s = note.querySelector("#set-" + b.dataset.jump); if (s) s.scrollIntoView({ behavior: "smooth", block: "start" }); return; }
      if ((b = q("[data-appearance]"))) return appearance({ appearance: b.dataset.appearance });
      if (q("#set-overlay")) return env.toggleOverlay();
      if (q("#set-restart")) return restart(env, false);
      if (q("#set-rebuild")) return restart(env, true);
      if (q("#set-routine-forecast")) return saveRoutine(env, [...note._set.items, { label: "Buy a call option: today's score, 80% sure", kind: "forecast" }]);
      if ((b = q("#set-routine .set-item [data-f]"))) {
        const items = [...note._set.items], i = +b.closest(".set-item").dataset.i, f = b.dataset.f;
        if (f === "del") items.splice(i, 1);
        else if (f === "up" && i > 0) [items[i - 1], items[i]] = [items[i], items[i - 1]];
        else if (f === "down" && i < items.length - 1) [items[i + 1], items[i]] = [items[i], items[i + 1]];
        else return;
        return saveRoutine(env, items);
      }
      if ((b = q(".set-book [data-f=del]"))) {
        if (!confirm("Remove this book? Pages you logged keep their points.")) return;
        await env.post("/api/reading/delete", { id: +b.closest(".set-book").dataset.book }).catch((e) => env.toast(msg(e)));
        return env.refresh();
      }
      if ((b = q("[data-unexclude]"))) {
        const w = await env.api("/api/words");
        await env.post("/api/words", { excluded: w.excluded.filter((x) => x !== b.dataset.unexclude) });
        return env.refresh();
      }
      if ((b = q(".set-color [data-c]"))) {
        try { await env.post("/api/folder-color", { tag: b.closest(".set-color").dataset.tag, color: b.dataset.c }); } catch (e) { env.toast(msg(e)); }
        await env.reloadTree();
        return env.refresh();
      }
    });
    note.addEventListener("change", async (ev) => {
      if (!on()) return;
      const t = ev.target;
      if (t.dataset.cfg) return appearance({ [t.dataset.cfg]: t.type === "checkbox" ? t.checked : t.value });
      if (t.id === "set-opacity-follow") return appearance({ opacity: t.checked ? 0 : (note._set.d.ghostty.opacity || 0.9) });
      if (t.id === "set-opacity") return appearance({ opacity: +t.value / 100 });
      if (t.closest("#set-routine") && t.dataset.f === "label") {
        const items = [...note._set.items], i = +t.closest(".set-item").dataset.i;
        if (!t.value.trim()) { t.value = items[i].label; return env.toast("An item needs a name (× removes it)"); }
        items[i] = { ...items[i], label: t.value };
        return saveRoutine(env, items);
      }
      const book = t.closest(".set-book");
      if (book && t.dataset.f) {
        const f = t.dataset.f, patch = { id: +book.dataset.book };
        if (f === "pages") { if (t.value.trim() && !/^\d+$/.test(t.value.trim())) return env.toast("Pages must be a whole number"); patch.pages = +t.value || 0; }
        else if (f === "title" || f === "author") { if (!t.value.trim()) return env.toast(`A book needs a${f === "author" ? "n author" : " title"}`); patch[f] = t.value; }
        else patch[f] = t.value;
        try { await env.post("/api/reading/update", patch); env.toast("Saved"); } catch (e) { env.toast(msg(e)); }
        return env.refresh();
      }
      if (t.dataset.track !== undefined) {
        const w = await env.api("/api/words"), name = t.dataset.track;
        const tracked = w.tracked.filter((x) => x !== name && x !== name.replace(/\.md$/, ""));
        if (t.checked) tracked.push(name);
        await env.post("/api/words", { tracked });
        return env.refresh();
      }
    });
    note.addEventListener("input", (ev) => { // live preview while dragging the slider
      if (!on() || ev.target.id !== "set-opacity") return;
      document.documentElement.style.setProperty("--glass", ev.target.value + "%");
      const v = note.querySelector("#set-opacity-v"); if (v) v.textContent = ev.target.value + "%";
    });
    note.addEventListener("submit", async (ev) => {
      if (!on()) return;
      const f = ev.target;
      if (f.id === "set-routine-add") {
        ev.preventDefault();
        const label = f.elements.label.value.trim();
        if (label) await saveRoutine(env, [...note._set.items, { label }]);
      } else if (f.id === "set-exclude") {
        ev.preventDefault();
        const p = f.elements.path.value.trim();
        if (!p) return;
        const w = await env.api("/api/words");
        await env.post("/api/words", { excluded: [...w.excluded, p] }).catch((e) => env.toast(msg(e)));
        env.refresh();
      }
    });
  }

  window.nvSettings = { render, bind };
})();
