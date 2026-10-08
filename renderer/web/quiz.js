// Quiz tab (#/quiz, key q): practise a course's vocab or question bank, or revise a scheduled note, in the
// viewer. The engine is the server's (quiz.go, quiz_session.go: same files and stats as quiz.py), so this
// page only shows cards and sends answers. A running session's id is kept in the URL (#/quiz?s=ID), so a
// reload resumes it. app.js calls in through window.nvQuiz with its own helpers (env).
(() => {
  "use strict";
  const esc = (s) => String(s ?? "").replace(/[&<>"]/g, (c) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;" }[c]));
  const pct = (v) => Math.round((v || 0) * 100) + "%";
  const LETTERS = "abcdefgh";
  const MACRON = { a: "ā", e: "ē", i: "ī", o: "ō", u: "ū", y: "ȳ", A: "Ā", E: "Ē", I: "Ī", O: "Ō", U: "Ū", Y: "Ȳ" };
  const fmtTime = (s) => (s >= 60 ? `${Math.floor(s / 60)}m ${String(s % 60).padStart(2, "0")}s` : `${s}s`);
  const msg = (e) => String(e.message || e).replace(/^revision: /, "").trim();

  let env = null;
  let S = null;        // { state, result, picked: Set, revealed, recallShown, busy }
  let lastStart = null; // the request that started the session (for "Again")

  // ── home: what to revise, what to practise ──
  async function home() {
    const d = await env.api("/api/quiz"), note = env.note;
    env.setTitle("Quiz");
    const prefs = (() => { try { return JSON.parse(env.store.get("quizPrefs", "{}")) || {}; } catch (e) { return {}; } })();
    const due = d.due.length ? `<ul class="qz-due">${d.due.map((t) => `<li><span><b>${esc(t.title)}</b><small>${esc(t.subject)}${t.count ? ` · revision ${t.count + 1}` : " · first revision"}${t.questions ? "" : " · recall"}</small></span>
        <button class="qz-btn on" data-revise="${esc(t.id)}">Start  +${d.points}</button></li>`).join("")}</ul>`
      : `<p class="act-note">Nothing is due for revision today.</p>`;
    const subj = d.subjects.map((s) => {
      const p = prefs[s.name] || {};
      return `<section class="qz-subject" data-subject="${esc(s.name)}">
        <div class="qz-sh"><b>${esc(s.name)}</b><small>${s.count} ${s.kind === "vocab" ? "words" : "questions"} · ${s.seen} answered · ${s.weak} weak</small><small class="qz-rank">${esc(s.rank)} · level ${s.level} · ${s.xp} XP</small></div>
        <div class="qz-opts">
          <select data-f="group" aria-label="${s.kind === "vocab" ? "Word type" : "Topic"}"><option value="">${s.kind === "vocab" ? "All word types" : "All topics"}</option>${s.groups.map((g) => `<option${p.group === g ? " selected" : ""}>${esc(g)}</option>`).join("")}</select>
          ${s.kind === "vocab" ? `<select data-f="dir" aria-label="Direction">${["Latin → English", "English → Latin", "Mixed"].map((l, i) => `<option value="${i}"${(p.dir || 0) === i ? " selected" : ""}>${l}</option>`).join("")}</select>` : ""}
          <label class="qz-weak" title="Skips what you know well (3 right in a row) and asks the least-asked first"><input data-f="focused" type="checkbox"${p.focused ? " checked" : ""}> focused${s.known ? ` (skips ${s.known} known well)` : ""}</label>
          <button class="qz-btn on" data-practise>Practise</button>
        </div></section>`;
    }).join("");
    note.innerHTML = `<h1>Quiz</h1>
      <h2>Revise</h2>${due}
      <h2>Practise</h2>${subj || `<p class="act-note">No <code>definitions.md</code> or <code>questions.md</code> in any folder yet.</p>`}
      <p class="act-note">Same questions, stats and XP as the terminal quiz. Keys in a quiz: Enter checks and goes on, letters or 1-8 pick a choice, y = "Actually right?", ? skips, Esc ends.</p>`;
  }

  async function start(req) {
    try {
      const r = await env.post("/api/quiz/start", req);
      lastStart = req;
      S = { state: r.state, result: null, picked: new Set() };
      history.replaceState(null, "", "#/quiz?s=" + r.state.id);
      const cur = env.current();  // a refresh (file changes after each answer) re-renders this route: keep the session
      if (cur && cur.view === "quiz") { cur.session = r.state.id; cur.revise = ""; }
      draw();
    } catch (e) { env.toast(msg(e)); }
  }

  // ── a running session ──
  function header(st) {
    const c = st.card || S.lastCard;
    const prog = !st.done && c && c.total ? `${c.retry ? "once more · " : ""}${c.n} of ${c.total}` : `${st.answered} answered`;
    const score = st.answered ? ` · ${String(st.right).replace(/\.0$/, "")}/${st.answered} right` : "";
    return `<div class="qz-top"><a href="#/quiz" class="qz-back">← Quiz</a><b>${esc(st.title)}</b>
      <span class="qz-meta">${esc(prog)}${score} · +${st.xp} XP${st.combo >= 3 ? ` · combo ${st.combo}` : ""}${st.streak ? ` · ${esc(st.streak)}` : ""}</span>
      ${st.done ? "" : `<button class="qz-btn" data-end title="End (Esc)">End</button>`}</div>`;
  }

  function inputHtml(c, st) {
    const r = S.result;
    if (r && !r.solution) return "";
    switch (c.kind) {
      case "vocab": case "short":
        return `<form class="qz-form"><input class="qz-input" autocomplete="off" spellcheck="false" placeholder="${st.latin ? "your answer (; then a vowel for ā)" : "your answer"}">
          <button class="qz-btn on">Check</button><button type="button" class="qz-btn" data-skip title="Skip and show (?)">Skip</button></form>`;
      case "tf":
        return `<div class="qz-choices"><button class="qz-choice" data-tf="true"><kbd>t</kbd>True</button><button class="qz-choice" data-tf="false"><kbd>f</kbd>False</button></div>`;
      case "mc": case "multi":
        return `${c.kind === "multi" ? `<p class="qz-hint">Select all that apply, then Check.</p>` : ""}<div class="qz-choices">${c.choices.map((ch, i) => `<button class="qz-choice${S.picked.has(i) ? " picked" : ""}" data-choice="${i}"><kbd>${LETTERS[i]}</kbd>${esc(ch)}</button>`).join("")}</div>
          ${c.kind === "multi" ? `<div class="qz-row"><button class="qz-btn on" data-check>Check</button><button class="qz-btn" data-skip>Skip</button></div>` : ""}`;
      case "problem":
        if (!r) return `<div class="qz-row"><button class="qz-btn on" data-reveal>Show the solution</button><span class="act-note">Work it out first (Enter shows it).</span></div>`;
        return `<div class="qz-solution"><pre>${esc(r.solution)}</pre>${r.why ? `<p>${esc(r.why)}</p>` : ""}</div>
          <div class="qz-row"><span>Did you get it?</span><button class="qz-btn on" data-grade="y"><kbd>y</kbd>Yes</button><button class="qz-btn" data-grade="p"><kbd>p</kbd>Partly</button><button class="qz-btn" data-grade="n"><kbd>n</kbd>No</button>
          <span class="qz-partly" hidden><input type="number" min="1" max="99" placeholder="%"> <button class="qz-btn on" data-partly>OK</button></span></div>`;
      case "recall":
        if (!S.recallShown) return `<p class="qz-hint">No questions for this note yet, so: recall. It covers:</p><ul>${c.headings.map((h) => `<li>${esc(h)}</li>`).join("")}</ul>
          <textarea class="qz-recall" rows="7" placeholder="Write down everything you remember (it stays on this page)."></textarea>
          <div class="qz-row"><button class="qz-btn on" data-recall-show>Show the note</button></div>`;
        return `<div class="qz-note">${S.recallHtml || ""}</div><div class="qz-row"><span>How well did you remember it?</span>${["again", "hard", "good", "easy"].map((l, i) => `<button class="qz-btn${i === 2 ? " on" : ""}" data-recall="${i + 1}"><kbd>${i + 1}</kbd>${l}</button>`).join("")}</div>`;
    }
    return "";
  }

  function resultHtml(r, c) {
    if (!r || (c && c.kind === "problem" && r.pending)) return "";
    const state = r.skipped ? ["skip", "Skipped"] : r.credit >= 1 ? ["right", r.overridden ? "Counted as right" : "Right"] : r.credit > 0 ? ["part", `${pct(r.credit)} credit${r.partial ? " · " + r.partial : ""}`] : ["wrong", "Wrong"];
    const known = r.known != null && !r.pending ? `<small>${pct(r.known)} known (${r.right} right, ${r.wrong} wrong)</small>` : "";
    return `<div class="qz-result" data-state="${state[0]}">
      <div class="qz-verdict"><b>${state[1]}</b>${r.xp ? `<span>+${r.xp} XP${r.combo >= 3 ? ` · combo ${r.combo}` : ""}</span>` : ""}${known}</div>
      ${(r.credit < 1 || r.overridden) && c.kind !== "problem" ? `<div class="qz-answer">${r.guess && !r.overridden ? `<small>You answered</small><p class="qz-yours">${esc(r.guess)}</p>` : ""}<small>Answer</small><pre>${esc(r.answer)}</pre></div>` : ""}
      ${r.also && r.also.length ? `<p class="qz-also">Also: ${esc(r.also.join(", "))}</p>` : ""}
      ${r.saved ? `<p class="qz-also">Saved “${esc(r.saved)}” as an accepted answer.</p>` : ""}
      ${r.why && c.kind !== "problem" ? `<p class="qz-why">${esc(r.why)}</p>` : ""}
      ${r.src ? `<p class="qz-src">${esc(r.src)}</p>` : ""}
      ${(r.notes || []).map((n) => `<p class="qz-badge">${esc(n)}</p>`).join("")}
      <div class="qz-row">${r.override ? `<button class="qz-btn" data-override title="y"><kbd>y</kbd>Actually right</button>` : ""}<button class="qz-btn on" data-next>Next <kbd>Enter</kbd></button></div>
    </div>`;
  }

  function summaryHtml(st) {
    const s = st.summary, span = Math.max(1, s.level_hi - s.level_lo);
    return `<div class="qz-summary"><h2>Done</h2>
      <div class="qz-tiles"><div><b>${s.answered ? Math.round(s.right / s.answered * 100) : 0}%</b><small>${String(s.right).replace(/\.0$/, "")} of ${s.answered} right</small></div>
        <div><b>+${s.xp}</b><small>XP</small></div><div><b>${s.best_combo}</b><small>best combo</small></div><div><b>${fmtTime(s.seconds)}</b><small>studied</small></div></div>
      ${(s.revisions || []).length ? `<h3>Revision</h3><ul>${s.revisions.map((l) => `<li>${esc(l)}</li>`).join("")}</ul>` : ""}
      ${(s.notes || []).map((n) => `<p class="qz-badge">${esc(n)}</p>`).join("")}
      <p class="qz-level">${esc(s.rank)} · level ${s.level} <span class="cg-bar"><span style="width:${Math.round((s.player_xp - s.level_lo) / span * 100)}%;background:var(--accent)"></span></span> ${s.player_xp}/${s.level_hi} XP</p>
      ${s.missed.length ? `<h3>Missed</h3><ul class="qz-missed">${s.missed.map((m) => `<li>${esc(m)}</li>`).join("")}</ul>` : ""}
      <div class="qz-row"><a class="qz-btn" href="#/quiz">Back to Quiz</a>${lastStart && lastStart.mode === "practice" ? `<button class="qz-btn on" data-again>Again</button>` : ""}</div></div>`;
  }

  function draw() {
    const note = env.note, st = S.state, c = st.card;
    env.setTitle("Quiz");
    if (st.done) { note.innerHTML = header(st) + summaryHtml(st); return; }
    if (!c && !S.result) { note.innerHTML = header(st) + `<p class="act-note">Loading…</p>`; return; }
    const card = c || S.lastCard;
    note.innerHTML = header(st) + `<div class="qz-card" data-kind="${esc(card.kind)}">
      ${card.hint ? `<p class="qz-hint">${esc(card.hint)}</p>` : ""}
      <div class="qz-prompt">${esc(card.prompt)}</div>
      ${card.known != null && !S.result ? `<p class="qz-known">${pct(card.known)} known so far</p>` : ""}
      ${c ? inputHtml(card, st) : ""}
      ${S.result ? resultHtml(S.result, card) : ""}
    </div>`;
    const inp = note.querySelector(".qz-input, .qz-recall");
    if (inp) inp.focus();
  }

  async function call(path, body) {
    if (S.busy) return;
    S.busy = true;
    try {
      const r = await env.post(path, { session: S.state.id, ...body });
      S.state = r.state;
      return r;
    } catch (e) { env.toast(msg(e)); return null; }
    finally { S.busy = false; }
  }

  async function answer(body) {
    const card = S.state.card;
    const r = await call("/api/quiz/answer", body);
    if (!r) return;
    if (card && card.kind === "problem" && body.reveal) { S.result = r.result; draw(); return; }
    S.lastCard = card;
    S.result = r.result;
    S.picked = new Set();
    if (S.state.done) { S.result = null; }
    draw();
  }

  async function next() {
    const r = await call("/api/quiz/next", {});
    if (!r) return;
    S.result = null; S.picked = new Set(); S.recallShown = false; S.recallHtml = "";
    draw();
  }

  async function override() {
    const r = await call("/api/quiz/override", {});
    if (r) { S.result = r.result; draw(); }
  }

  async function end() {
    const r = await call("/api/quiz/end", {});
    if (r) { S.result = null; draw(); }
  }

  async function showRecall() {
    try {
      const d = await env.api("/api/note?path=" + encodeURIComponent(S.state.card.note));
      S.recallHtml = d.html;
    } catch (e) { S.recallHtml = `<p class="act-note">${esc(msg(e))}</p>`; }
    S.recallShown = true;
    draw();
  }

  function submitText() {
    const inp = env.note.querySelector(".qz-input");
    if (inp) answer({ response: inp.value });
  }

  // ── entry ──
  async function render(e, r) {
    env = e;
    env.note.className = "note quiz";
    const sid = r.session;
    if (sid) {
      if (S && S.state.id === sid) { if (!env.note.querySelector(".qz-top")) draw(); return; }  // a refresh: leave the open card (and what you typed) alone
      try {
        const d = await env.api("/api/quiz/session?id=" + encodeURIComponent(sid));
        S = { state: d.state, result: d.state.result || null, picked: new Set(), lastCard: d.state.last || null };
        return draw();
      } catch (err) { env.toast(msg(err)); history.replaceState(null, "", "#/quiz"); }
    }
    S = null;
    if (r.revise) { history.replaceState(null, "", "#/quiz"); return start({ mode: "revise", id: r.revise }); }
    await home();
  }

  function bind(e) {
    env = e;
    const note = e.note, active = () => e.isActive("quiz");
    note.addEventListener("click", (ev) => {
      if (!active()) return;
      const t = ev.target, q = (sel) => t.closest && t.closest(sel);
      let b;
      if ((b = q("[data-revise]"))) return start({ mode: "revise", id: b.dataset.revise });
      if ((b = q("[data-practise]"))) {
        const sec = b.closest(".qz-subject"), name = sec.dataset.subject, f = (k) => sec.querySelector(`[data-f="${k}"]`);
        const req = { mode: "practice", subject: name, group: f("group").value, dir: f("dir") ? Number(f("dir").value) : 0, focused: f("focused").checked };
        try { const p = JSON.parse(e.store.get("quizPrefs", "{}")) || {}; p[name] = { group: req.group, dir: req.dir, focused: req.focused }; e.store.set("quizPrefs", JSON.stringify(p)); } catch (err) {}
        return start(req);
      }
      if (!S) return;
      if (q("[data-end]")) return end();
      if (q("[data-again]")) return start(lastStart);
      if (q("[data-next]")) return next();
      if (q("[data-override]")) return override();
      if (q("[data-skip]")) { ev.preventDefault(); return answer({ skip: true }); }
      if ((b = q("[data-tf]"))) return answer({ response: b.dataset.tf });
      if ((b = q("[data-choice]"))) {
        const i = Number(b.dataset.choice);
        if (S.state.card.kind === "mc") return answer({ choices: [i] });
        S.picked.has(i) ? S.picked.delete(i) : S.picked.add(i);
        return b.classList.toggle("picked");
      }
      if (q("[data-check]")) return answer({ choices: [...S.picked] });
      if (q("[data-reveal]")) return answer({ reveal: true });
      if ((b = q("[data-grade]"))) {
        if (b.dataset.grade === "p") { const p = note.querySelector(".qz-partly"); p.hidden = false; return p.querySelector("input").focus(); }
        return answer({ grade: b.dataset.grade });
      }
      if (q("[data-partly]")) return answer({ grade: "p", percent: Number(note.querySelector(".qz-partly input").value) || 0 });
      if (q("[data-recall-show]")) return showRecall();
      if ((b = q("[data-recall]"))) return answer({ grade: b.dataset.recall });
    });
    note.addEventListener("submit", (ev) => {
      if (!active() || !ev.target.classList.contains("qz-form")) return;
      ev.preventDefault();
      submitText();
    });
    // Latin: ; then a vowel types its macron (as in the notes' Neovim mapping)
    note.addEventListener("input", (ev) => {
      const t = ev.target;
      if (!active() || !S || !S.state.latin || !t.classList.contains("qz-input")) return;
      const v = t.value.replace(/;([aeiouyAEIOUY])/g, (_, c) => MACRON[c]);
      if (v !== t.value) { const pos = t.selectionStart - (t.value.length - v.length); t.value = v; t.setSelectionRange(pos, pos); }
    });
    // keys: before app.js's own shortcuts (letters there switch pages)
    window.addEventListener("keydown", (ev) => {
      if (!active() || !S || ev.ctrlKey || ev.metaKey || ev.altKey) return;
      const st = S.state, c = st.card, k = ev.key, typing = ["INPUT", "TEXTAREA", "SELECT"].includes(document.activeElement.tagName);
      const eat = () => { ev.preventDefault(); ev.stopImmediatePropagation(); };
      if (k === "Escape" && !st.done) { eat(); return end(); }
      if (st.done) return;
      if (S.result && !(c && c.kind === "problem" && S.result.pending)) {
        if (k === "Enter") { eat(); return next(); }
        if (k === "y" && S.result.override) { eat(); return override(); }
        return;
      }
      if (!c || typing) {
        if (typing && k === "?" && c && (c.kind === "vocab" || c.kind === "short") && !document.activeElement.value) { eat(); answer({ skip: true }); }
        return;
      }
      if (k === "?") { eat(); return answer({ skip: true }); }
      if (c.kind === "tf" && (k === "t" || k === "f")) { eat(); return answer({ response: k === "t" ? "true" : "false" }); }
      if (c.kind === "mc" || c.kind === "multi") {
        let i = LETTERS.indexOf(k.toLowerCase());
        if (i < 0 && /^[1-8]$/.test(k)) i = Number(k) - 1;
        if (i >= 0 && i < c.choices.length) {
          eat();
          if (c.kind === "mc") return answer({ choices: [i] });
          S.picked.has(i) ? S.picked.delete(i) : S.picked.add(i);
          return draw();
        }
        if (k === "Enter" && c.kind === "multi") { eat(); return answer({ choices: [...S.picked] }); }
      }
      if (c.kind === "problem") {
        if (!S.result && k === "Enter") { eat(); return answer({ reveal: true }); }
        if (S.result && (k === "y" || k === "n")) { eat(); return answer({ grade: k }); }
        if (S.result && k === "p") { eat(); const p = env.note.querySelector(".qz-partly"); p.hidden = false; return p.querySelector("input").focus(); }
      }
      if (c.kind === "recall") {
        if (!S.recallShown && k === "Enter") { eat(); return showRecall(); }
        if (S.recallShown && /^[1-4]$/.test(k)) { eat(); return answer({ grade: k }); }
      }
    }, true);
    note.addEventListener("keydown", (ev) => {  // Enter in the partly box
      if (active() && ev.key === "Enter" && ev.target.closest && ev.target.closest(".qz-partly")) { ev.preventDefault(); answer({ grade: "p", percent: Number(ev.target.value) || 0 }); }
    });
  }

  window.nvQuiz = { render, bind };
})();
