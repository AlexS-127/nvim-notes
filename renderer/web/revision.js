// Spaced-repetition revision (revision.go): the "Revise" card of the Activity view (the next topic due, Start opens
// the quiz in a terminal window) and the Revision page (#/revision, key v): what is due, what is coming up, every topic
// covered with its history, and adding or removing topics. app.js calls in through window.nvRevision with its own
// helpers (env), so this file has no globals besides that.
(() => {
  "use strict";
  const esc = (s) => String(s ?? "").replace(/[&<>"]/g, (c) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;" }[c]));
  const day = (s) => new Date(s + "T00:00:00");
  const fmtDay = (s, today) => {
    if (!s) return "";
    const d = Math.round((day(s) - day(today)) / 864e5);
    if (d === 0) return "today";
    if (d === 1) return "tomorrow";
    if (d === -1) return "yesterday";
    if (d > 1 && d < 7) return day(s).toLocaleDateString(undefined, { weekday: "long" });
    return day(s).toLocaleDateString(undefined, { month: "short", day: "numeric" });
  };
  const pct = (v) => Math.round((v || 0) * 100) + "%";
  const genLabel = (t) => ({ ok: `${t.gen.count || ""} questions`.trim(), pending: "writing questions…", none: t.questions ? "own questions" : "writing questions…", failed: "questions failed: recall", rebuilding: "rebuilding questions…" }[t.gen.status] || "");
  const msg = (e) => String(e.message || e).replace(/^revision: /, "").trim();

  // ── Activity card: the next topic, "1 of N", Start ──
  function cardHtml(r, today) {
    if (!r || !r.next) return "";
    const t = r.next, late = t.due < today ? ` · due ${fmtDay(t.due, today)}` : "";
    return `<section class="revision-card"><div class="rc-top"><small>Revise</small><a href="#/revision">1 of ${r.due} due${r.done_today ? ` · ${r.done_today} done today` : ""} · schedule</a></div>
      <div class="rv-main"><div><div class="rv-title">${esc(t.title)}</div><div class="nc-meta">${esc(t.subject)} · revision ${(t.count || 0) + 1}${late}</div></div>
      <button class="nc-btn" data-revise="${esc(t.id)}">Start  +${r.points}</button></div></section>`;
  }

  // Start opens the revision in the Quiz tab (quiz.js); the terminal quiz stays on the start page (p)
  function start(env, id) { location.hash = "#/quiz?revise=" + encodeURIComponent(id); }

  // ── Revision page ──
  function topicRow(t, today, actions) {
    const hist = t.count ? `revised ${t.count}× · last ${pct(t.score)} ${fmtDay(t.last, today)}` : "not revised yet";
    return `<div class="rv-row" data-id="${esc(t.id)}">
      <div class="rv-name"><a href="#/note/${t.id.split("/").map(encodeURIComponent).join("/")}">${esc(t.title)}</a><small>${esc(t.subject)} · learned ${esc(fmtDay(t.learned, today))}</small></div>
      <div class="rv-meta"><span class="rv-step" title="Step ${t.step + 1}: next gap ${t.interval} days">${"●".repeat(t.step + 1)}${"○".repeat(Math.max(0, t.steps - t.step - 1))}</span><small>${esc(hist)}</small><small class="rv-gen rv-${t.gen.status}" title="${esc(t.gen.error || "")}">${esc(genLabel(t))}</small></div>
      <div class="rv-acts">${actions}</div></div>`;
  }

  async function render(env) {
    const d = await env.api("/api/revision"), note = env.note, today = d.today;
    const iv = d.config.intervals, steps = iv.length;
    const topics = d.topics.map((t) => ({ ...t, steps, interval: iv[t.step] }));
    const due = topics.filter((t) => t.due <= today).sort((a, b) => a.due.localeCompare(b.due) || a.learned.localeCompare(b.learned));
    const upcoming = topics.filter((t) => t.due > today).sort((a, b) => a.due.localeCompare(b.due));
    const groups = {};
    for (const t of upcoming) (groups[t.due] = groups[t.due] || []).push(t);
    const doneToday = d.recent.filter((e) => e.kind === "done" && e.date === today);
    const btns = (t, startable) => `${startable ? `<button class="rv-start" data-revise="${esc(t.id)}">Start</button>` : ""}<button class="set-icon" data-act="gen" title="Write new questions">↻</button><button class="cp-del" data-act="remove" title="Stop revising this note" aria-label="Remove">×</button>`;
    note.className = "note revision";
    env.setTitle("Revision");
    note.innerHTML = `<h1>Revision</h1>
      <div class="act-tiles">
        <div class="tile"><small>Due today</small><div class="big">${due.length}</div><span class="sub">${doneToday.length} revised today</span></div>
        <div class="tile"><small>Topics</small><div class="big">${topics.length}</div><span class="sub">${topics.filter((t) => t.count).length} revised at least once</span></div>
        <div class="tile"><small>Next 7 days</small><div class="big">${upcoming.filter((t) => (day(t.due) - day(today)) / 864e5 <= 7).length}</div><span class="sub">coming up</span></div>
      </div>
      <p class="act-note">A note you write in (${d.config.min_words}+ words in a day, in ${d.config.folders.length ? d.config.folders.map(esc).join(", ") : "no folders yet: pick them in Settings"}) is revised the next day, then after ${iv.join(", ")} days. A score of 80%+ moves it to the next gap, under 50% starts again. Each revision scores ${d.points} points (once per topic a day, at least ${d.min_total} questions). Questions are written by Claude${d.claude ? "" : " (not found on this Mac: recall only)"}; edit them in <code>.revision/questions/</code>.</p>
      <form class="set-add" id="rv-add"><input name="id" placeholder="Add a note by path, e.g. act200/accruals.md" autocomplete="off" aria-label="Note to schedule"><button>Schedule</button></form>
      <h2>Due today</h2>${due.length ? `<div class="cal-panel">${due.map((t, i) => topicRow(t, today, btns(t, true))).join("")}</div>` : `<p class="act-note">Nothing due. ${doneToday.length ? "All revised for today." : ""}</p>`}
      <h2>Coming up</h2>${upcoming.length ? Object.keys(groups).map((k) => `<h3>${esc(fmtDay(k, today))} <small>${esc(k)}</small></h3><div class="cal-panel">${groups[k].map((t) => topicRow(t, today, btns(t, false))).join("")}</div>`).join("") : `<p class="act-note">Nothing scheduled yet.</p>`}
      <h2>Covered</h2>${topics.length ? `<table class="att-table rv-table"><thead><tr><th>Topic</th><th>Learned</th><th>Revisions</th><th>Last score</th><th>Next</th></tr></thead><tbody>${[...topics].sort((a, b) => a.subject.localeCompare(b.subject) || a.learned.localeCompare(b.learned)).map((t) => `<tr><td><a href="#/note/${t.id.split("/").map(encodeURIComponent).join("/")}">${esc(t.title)}</a> <small>${esc(t.subject)}</small></td><td>${esc(t.learned)}</td><td>${t.count || 0}</td><td>${t.count ? pct(t.score) : "–"}</td><td>${esc(fmtDay(t.due, today))}</td></tr>`).join("")}</tbody></table>` : `<p class="act-note">No topics yet.</p>`}
      <h2>Recent revisions</h2>${d.recent.filter((e) => e.kind === "done").length ? `<ul class="recent">${d.recent.filter((e) => e.kind === "done").slice(0, 15).map((e) => `<li><span class="rt-text">${esc(e.title || e.id)} · ${pct(e.score)} (${e.correct}/${e.total})${e.points ? ` · +${d.points}` : ""}</span><small>${esc(fmtDay(e.date, today))} → next ${esc(fmtDay(e.next_due, today))}</small></li>`).join("")}</ul>` : `<p class="act-note">None yet.</p>`}`;
  }

  function bind(env) {
    const note = env.note;
    note.addEventListener("click", async (ev) => {
      const t = ev.target, b = t.closest && t.closest("[data-revise], .rv-row [data-act]");
      if (!b) return;
      if (b.dataset.revise) return start(env, b.dataset.revise, b);
      const id = b.closest(".rv-row").dataset.id;
      if (b.dataset.act === "remove" && !confirm("Stop revising this note? Writing in it again schedules it again.")) return;
      try { await env.post(`/api/revision/${b.dataset.act === "gen" ? "gen" : "remove"}`, { id }); if (b.dataset.act === "gen") env.toast("New questions are being written"); env.refresh(); }
      catch (e) { env.toast(msg(e)); }
    });
    note.addEventListener("submit", async (ev) => {
      if (ev.target.id !== "rv-add") return;
      ev.preventDefault();
      const id = ev.target.elements.id.value.trim();
      if (!id) return;
      try { await env.post("/api/revision/add", { id }); env.toast("Scheduled: first revision tomorrow"); env.refresh(); }
      catch (e) { env.toast(msg(e)); }
    });
  }

  // Settings section: watched folders, minimum words, gaps
  async function settingsHtml(env) {
    const [d, w] = await Promise.all([env.api("/api/revision"), env.api("/api/words")]);
    const dirs = [...new Set([...w.candidates.filter((c) => !c.endsWith(".md")), ...d.config.folders])].sort();
    return `<div class="cal-panel set-words" id="rv-folders">${dirs.map((f) => `<label class="cp-class"><input type="checkbox" data-rvfolder="${esc(f)}"${d.config.folders.includes(f) ? " checked" : ""}> ${esc(f)}</label>`).join("")}</div>
      <div class="set-grid" style="margin-top:10px">
        <label>Words to schedule</label><div><input class="cp-name set-pages" id="rv-minwords" value="${d.config.min_words}" inputmode="numeric" aria-label="Words written in a note in a day to schedule it"><small>of your own words in a note in one day</small></div>
        <label>Gaps (days)</label><div><input class="cp-name" id="rv-intervals" value="${d.config.intervals.join(", ")}" aria-label="Days between revisions"><small>after each good revision</small></div>
      </div>
      <p class="act-note">Notes in ticked folders are scheduled when you write in them. See and manage the schedule on the <a href="#/revision">Revision</a> page.</p>`;
  }

  async function saveSettings(env) {
    const note = env.note;
    const folders = [...note.querySelectorAll("[data-rvfolder]")].filter((x) => x.checked).map((x) => x.dataset.rvfolder);
    const min_words = parseInt(note.querySelector("#rv-minwords").value, 10) || 50;
    const intervals = note.querySelector("#rv-intervals").value.split(/[\s,]+/).map((x) => parseInt(x, 10)).filter((x) => x > 0);
    try { await env.post("/api/revision/config", { folders, min_words, intervals }); env.toast("Saved"); } catch (e) { env.toast(msg(e)); }
  }

  window.nvRevision = { cardHtml, render, bind, settingsHtml, saveSettings };
})();
