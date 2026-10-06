// Reading page (add books, log pages, to read / reading / read) and the reading card of the Activity view.
// app.js calls in through window.nvReading with its own helpers (env), so this file has no globals besides that.
(() => {
  "use strict";
  const esc = (s) => String(s).replace(/[&<>"]/g, (c) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;" }[c]));
  const fmtDay = (d) => (d ? new Date(d + "T00:00:00").toLocaleDateString(undefined, { month: "short", day: "numeric", year: "numeric" }) : "");

  const progress = (b) => b.pages ? `${b.page} / ${b.pages} pages` : `${b.page} page${b.page === 1 ? "" : "s"} read`;
  const bar = (b) => b.pct >= 0 ? `<div class="rd-bar" title="${b.pct}%"><span style="width:${b.pct}%"></span></div><b class="rd-pct">${b.pct}%</b>` : "";

  // one book being read: progress, and a box to log pages ("20" = pages read, "p150" = now at page 150)
  function readingRow(b) {
    return `<div class="rd-book" data-book="${b.id}">
      <div class="rd-head"><span class="rd-title">${esc(b.title)}</span><span class="rd-author">${esc(b.author)}</span></div>
      <div class="rd-prog">${bar(b)}<small>${esc(progress(b))}</small></div>
      <form class="rd-log" data-log="${b.id}"><input name="n" inputmode="numeric" placeholder="pages read" aria-label="Pages read (p150 = at page 150)" title="Pages read; p150 = I'm on page 150" autocomplete="off"><button>Log</button><button type="button" class="rd-done" data-act="read" title="Mark as read">Finished</button></form>
    </div>`;
  }

  // ── reading card (Activity view): books being read, with the same log box ──
  function cardHtml(r) {
    if (!r) return "";
    if (!r.reading.length) {
      return r.to_read || r.read ? "" : `<p class="act-note nc-hint"><a href="#/reading">Add a book to your reading list</a> to score ${r.pages_per_point === 1 ? "a point a page" : `a point per ${r.pages_per_point} pages`}.</p>`;
    }
    return `<section class="reading-card"><div class="rc-top"><small>Reading</small><a href="#/reading">${r.pages_today} page${r.pages_today === 1 ? "" : "s"} today · list</a></div>
      ${r.reading.slice(0, 3).map(readingRow).join("")}</section>`;
  }

  function addFormHtml() {
    return `<form class="rd-add" id="rd-add">
      <input name="title" placeholder="Title" required aria-label="Title" autocomplete="off">
      <input name="author" placeholder="Author" required aria-label="Author" autocomplete="off">
      <input name="pages" placeholder="Pages (optional)" inputmode="numeric" aria-label="Number of pages (optional)" autocomplete="off">
      <input name="at" placeholder="Already on page" inputmode="numeric" aria-label="Page you are already on (optional, no points)" title="Started before tracking? The page you're on now. Those pages score nothing." autocomplete="off">
      <button>Add to read</button></form>`;
  }

  function listRow(b) {
    const right = b.status === "read" ? `<small>read ${esc(fmtDay(b.finished))}</small><button data-act="to-read" title="Move back to the to-read list">↺</button>`
      : `${b.pages ? `<small>${b.pages} pages</small>` : ""}<button data-act="reading">Start</button>`;
    return `<li class="rd-item" data-book="${b.id}"><span class="rd-title">${esc(b.title)}</span><span class="rd-author">${esc(b.author)}</span><span class="spacer"></span>${right}<button class="cp-del" data-act="delete" title="Remove (logged pages keep their points)" aria-label="Remove">×</button></li>`;
  }

  async function render(env) {
    const d = await env.api("/api/reading"), note = env.note, r = d.summary;
    const by = (s) => d.books.filter((b) => b.status === s);
    const reading = r.reading, toRead = by("to-read"), read = by("read").sort((a, b) => (b.finished || "").localeCompare(a.finished || ""));
    note.className = "note reading";
    env.setTitle("Reading");
    note.innerHTML = `<h1>Reading</h1>
      <div class="act-tiles">
        <div class="tile"><small>Pages read</small><div class="big">${r.pages_today}<em>today</em></div><span class="sub">${r.pages_week} this week · ${r.pages_total} in all</span></div>
        <div class="tile"><small>Books read</small><div class="big">${r.read}</div><span class="sub">${r.to_read} to read</span></div>
      </div>
      <p class="act-note">Each page you log scores ${r.pages_per_point === 1 ? "1 point" : `1 point per ${r.pages_per_point} pages`}. Type the pages you just read, or <code>p150</code> for “I'm on page 150”. Started a book before tracking it? Put the page you're on in “Already on page” when adding it, or type <code>=150</code> in its log box: those pages score nothing. A book with a page count is finished when you reach its last page.</p>
      ${addFormHtml()}
      <h2>Reading</h2>${reading.length ? reading.map(readingRow).join("") : `<p class="act-note">Nothing on the go. Start a book below.</p>`}
      <h2>To read</h2>${toRead.length ? `<ul class="rd-list">${toRead.map(listRow).join("")}</ul>` : `<p class="act-note">The list is empty.</p>`}
      ${read.length ? `<h2>Read</h2><ul class="rd-list">${read.map(listRow).join("")}</ul>` : ""}`;
  }

  // "20" → 20 pages read, "p150" / "@150" → now at page 150, "=150" → already on page 150 (read before, no points)
  function parseLog(v) {
    v = v.trim();
    let m = /^=\s*(\d+)$/.exec(v);
    if (m) return { at: +m[1] };
    m = /^(?:p|@|page\s*)(\d+)$/i.exec(v);
    if (m) return { to: +m[1] };
    m = /^[+-]?\d+$/.exec(v);
    return m && +v !== 0 ? { pages: +v } : null;
  }

  function bind(env) {
    const note = env.note;
    note.addEventListener("submit", async (ev) => {
      const f = ev.target;
      if (f.id === "rd-add") {
        ev.preventDefault();
        const pages = f.elements.pages.value.trim(), at = f.elements.at.value.trim();
        if ((pages && !/^\d+$/.test(pages)) || (at && !/^\d+$/.test(at))) return env.toast("Pages must be a whole number");
        try {
          const b = await env.post("/api/reading/add", { title: f.elements.title.value, author: f.elements.author.value, pages: +pages || 0, at: +at || 0 });
          env.toast(`Added ${b.title}`);
          env.refresh();
        } catch (err) { env.toast(String(err.message || err).replace(/^reading: /, "").trim()); }
        return;
      }
      if (!f.dataset.log) return;
      ev.preventDefault();
      const q = parseLog(f.elements.n.value);
      if (!q) return env.toast("Type the pages read (20) or the page you're on (p150)");
      const btn = f.querySelector("button");
      btn.disabled = true;
      try {
        const r = await env.post("/api/reading/log", { id: +f.dataset.log, ...q });
        env.toast(r.entry.baseline ? `${r.book.title}: on page ${r.book.page} (no points)` : `${r.entry.pages > 0 ? "+" : ""}${r.entry.pages} pages · +${r.points}${r.book.status === "read" ? ` · finished ${r.book.title}!` : ""}`);
        env.refresh();
      } catch (err) { env.toast(String(err.message || err).replace(/^reading: /, "").trim()); btn.disabled = false; }
    });
    note.addEventListener("click", async (ev) => {
      const b = ev.target.closest && ev.target.closest(".rd-item [data-act], .rd-book [data-act]");
      if (!b) return;
      const id = +b.closest("[data-book]").dataset.book, act = b.dataset.act;
      try {
        if (act === "delete") {
          if (!confirm("Remove this book from the list? Pages you logged keep their points.")) return;
          await env.post("/api/reading/delete", { id });
        } else await env.post("/api/reading/update", { id, status: act });
        env.refresh();
      } catch (err) { env.toast(String(err.message || err).replace(/^reading: /, "").trim()); }
    });
  }

  window.nvReading = { render, bind, cardHtml };
})();
