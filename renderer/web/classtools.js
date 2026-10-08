// Class page tools (below the class graph): Rebuild banks (which revision question banks most need
// rewriting; you pick, repair keeps good questions and their history) and Check notes (Claude's fact
// fixes and links for the class's notes, applied or dismissed one by one). Data: GET /api/classes/banks,
// /api/classes/check; POST …/banks/rebuild, …/check/run, …/check/apply, …/check/dismiss
// (classbanks.go, notecheck.go, classtools_server.go). classgraph.js mounts it with window.nvClassTools.
(() => {
  "use strict";
  const esc = (s) => String(s ?? "").replace(/[&<>"]/g, (c) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;" }[c]));
  const enc = (p) => p.split("/").map(encodeURIComponent).join("/");
  const day = (stamp) => stamp ? new Date(stamp.replace(" ", "T")).toLocaleDateString(undefined, { month: "short", day: "numeric" }) : "";
  const when = (stamp) => stamp ? new Date(stamp.replace(" ", "T")).toLocaleString(undefined, { month: "short", day: "numeric", hour: "numeric", minute: "2-digit" }) : "";
  const errText = (e) => String(e && e.message || e).trim();
  const picked = {};    // subject -> Set of topic ids picked for a rebuild (survives page refreshes)
  const showOld = {};   // subject -> show applied/dismissed suggestions
  let timer = 0;

  function mount(env, el, sub, tool) {
    clearTimeout(timer);
    el.hidden = !tool;
    if (tool === "banks") return banks(env, el, sub);
    if (tool === "check") return check(env, el, sub);
    el.innerHTML = "";
  }
  // while a job runs, look again in a few seconds (only if this panel is still the one showing)
  function poll(env, el, sub, tool, running) {
    clearTimeout(timer);
    if (running) timer = setTimeout(() => { if (el.isConnected && el.dataset.tool === tool) mount(env, el, sub, tool); }, 3000);
  }

  // ── Rebuild banks ──
  async function banks(env, el, sub) {
    el.dataset.tool = "banks";
    let d;
    try { d = await env.api("/api/classes/banks?subject=" + encodeURIComponent(sub)); }
    catch (e) { el.innerHTML = `<p class="act-note">${esc(errText(e))}</p>`; return; }
    const sel = (picked[sub] ||= new Set());
    for (const id of [...sel]) if (!d.rows.some((r) => r.id === id)) sel.delete(id);
    const job = d.job, running = job && job.running;
    const mode = env.store.get("ctMode", "repair");
    const maxNeed = Math.max(1, ...d.rows.map((r) => r.need));
    const needy = d.rows.filter((r) => r.need >= 1);
    const jobLine = !job ? "" : running
      ? `<p class="ct-job run">Rebuilding ${job.total} bank${job.total === 1 ? "" : "s"} (${job.mode})… ${job.done} done. Claude takes a few minutes per batch of up to 8.</p>`
      : `<p class="ct-job${job.failed ? " bad" : ""}">Rebuilt ${job.total - Object.keys(job.failed || {}).length} of ${job.total} (${job.mode}) · ${when(job.finished)}${job.failed ? "<br>" + Object.entries(job.failed).map(([id, e]) => `${esc(id)}: ${esc(e)}`).join("<br>") : ""}</p>`;
    const row = (r) => {
      const busy = r.gen.status === "rebuilding";
      const reasons = r.reasons.length ? r.reasons.map((x) => `<span class="ct-reason r-${x.code}" title="counts ${x.w}">${esc(x.text)}</span>`).join("") : `<span class="act-note">nothing to fix</span>`;
      const weak = r.weak.length ? `<details class="ct-weak"><summary>${r.weak.length} flagged</summary><ul>${r.weak.map((w) => `<li>${esc(w)}</li>`).join("")}</ul></details>` : "";
      return `<tr class="${busy ? "busy" : ""}"><td><input type="checkbox" data-id="${esc(r.id)}"${sel.has(r.id) ? " checked" : ""}${busy || running ? " disabled" : ""}></td>
        <td><a href="#/note/${enc(r.id)}">${esc(r.title)}</a>${r.edited ? ` <span class="ct-flag" title="Changed by hand after it was written; a rebuild changes your edits too">edited by hand</span>` : ""}</td>
        <td class="ct-need"><span class="cg-bar"><span style="width:${Math.round(r.need / maxNeed * 100)}%;background:${r.need >= 2 ? "var(--c-danger)" : r.need >= 1 ? "var(--c-warning)" : "var(--c-tip)"}"></span></span><small>${r.need}</small></td>
        <td class="ct-reasons">${reasons}${weak}</td>
        <td><small>${r.questions} good${r.seen ? ` · ${r.known} known well` : ""}</small></td>
        <td><small>${busy ? "rebuilding…" : r.gen.at ? day(r.gen.at) + (r.gen.mode ? ` (${r.gen.mode})` : "") : "–"}</small></td></tr>`;
    };
    el.innerHTML = `<div class="ct-head"><h3>Rebuild question banks</h3><span class="act-note">Most in need first. Nothing changes until you rebuild.</span></div>
      ${d.claude ? "" : `<p class="ct-job bad">Claude was not found, so banks can't be rebuilt.</p>`}
      <div class="ct-bar">
        <label>Mode <select id="ct-mode"${running ? " disabled" : ""}><option value="repair"${mode === "repair" ? " selected" : ""}>Repair</option><option value="full"${mode === "full" ? " selected" : ""}>Full rewrite</option></select></label>
        <button class="cg-chip" data-act="needy"${running || !needy.length ? " disabled" : ""}>Pick the ${needy.length} that need it</button>
        <button class="cg-chip" data-act="none"${running || !sel.size ? " disabled" : ""}>Clear</button>
        <button class="cg-chip on" data-act="rebuild"${running || !sel.size || !d.claude ? " disabled" : ""}>Rebuild ${sel.size || ""} selected</button>
      </div>
      <p class="act-note ct-mode-note">${mode === "repair"
        ? "Repair keeps the good questions word for word (and their answer history), replaces the flagged ones, and adds questions for what is new in the note."
        : "Full rewrite writes every question again. The old questions' answer history no longer applies, so these notes start fresh in the graph."}</p>
      ${jobLine}
      ${d.rows.length ? `<table class="ct-table"><thead><tr><th></th><th>Topic</th><th>Need</th><th>Why</th><th>Questions</th><th>Written</th></tr></thead><tbody>${d.rows.map(row).join("")}</tbody></table>`
        : `<p class="act-note">No note of this class is scheduled for revision yet, so it has no question banks.</p>`}`;
    el.querySelector("#ct-mode").onchange = (ev) => { env.store.set("ctMode", ev.target.value); banks(env, el, sub); };
    el.onchange = (ev) => {
      const id = ev.target.dataset && ev.target.dataset.id;
      if (!id) return;
      ev.target.checked ? sel.add(id) : sel.delete(id);
      banks(env, el, sub);
    };
    el.onclick = async (ev) => {
      const b = ev.target.closest("button[data-act]");
      if (!b) return;
      if (b.dataset.act === "needy") { needy.forEach((r) => { if (r.gen.status !== "rebuilding") sel.add(r.id); }); return banks(env, el, sub); }
      if (b.dataset.act === "none") { sel.clear(); return banks(env, el, sub); }
      const ids = [...sel], m = env.store.get("ctMode", "repair");
      const edited = d.rows.filter((r) => sel.has(r.id) && r.edited).length;
      const warn = [m === "full" ? `Full rewrite replaces every question of ${ids.length} topic${ids.length === 1 ? "" : "s"}; their answer history no longer applies.` : "",
        edited ? `${edited} of them ${edited === 1 ? "was" : "were"} edited by hand; ${m === "full" ? "those edits go" : "flagged questions you edited are replaced too"}.` : ""].filter(Boolean).join("\n");
      if (warn && !confirm(warn + "\n\nRebuild?")) return;
      b.disabled = true;
      try { await env.post("/api/classes/banks/rebuild", { subject: sub, ids, mode: m }); sel.clear(); }
      catch (e) { env.toast(errText(e)); }
      banks(env, el, sub);
    };
    poll(env, el, sub, "banks", running);
  }

  // ── Check notes ──
  const SEV = { wrong: "wrong", imprecise: "imprecise", unclear: "unclear", contradiction: "contradicts another note" };
  async function check(env, el, sub) {
    el.dataset.tool = "check";
    let d;
    try { d = await env.api("/api/classes/check?subject=" + encodeURIComponent(sub)); }
    catch (e) { el.innerHTML = `<p class="act-note">${esc(errText(e))}</p>`; return; }
    const c = d.check, job = d.job, running = job && job.running;
    const open = c.findings.filter((f) => f.status === "open"), old = c.findings.filter((f) => f.status !== "open");
    const links = open.filter((f) => f.kind === "link" && !f.stale);
    const jobLine = !job ? "" : running
      ? `<p class="ct-job run">Checking ${job.total} note${job.total === 1 ? "" : "s"} in one Claude call… this takes a few minutes (started ${when(job.started)}).</p>`
      : job.error ? `<p class="ct-job bad">Check failed: ${esc(job.error)}</p>`
      : `<p class="ct-job">Checked ${when(job.finished)}: ${job.found} new suggestion${job.found === 1 ? "" : "s"}.</p>`;
    const card = (f) => {
      const tag = f.kind === "link" ? `<span class="ct-sev s-link">link</span>` : `<span class="ct-sev s-${f.severity}">${esc(SEV[f.severity] || f.severity)}</span>`;
      const other = f.other ? ` · <a href="#/note/${enc(f.other)}">${esc(f.other)}</a>${f.heading ? ` # ${esc(f.heading)}` : ""}` : "";
      const acts = f.status !== "open" ? `<small class="act-note">${f.status} ${day(f.at)}</small>`
        : f.stale ? `<small class="act-note">the note changed and this text is gone; check again</small><button class="cg-chip" data-dismiss="${f.id}">Dismiss</button>`
        : `<button class="cg-chip on" data-apply="${f.id}">Apply</button><button class="cg-chip" data-edit="${f.id}">Edit</button><button class="cg-chip" data-dismiss="${f.id}">Dismiss</button>`;
      return `<div class="ct-find${f.stale ? " stale" : ""}" data-f="${f.id}"><div class="ct-find-head">${tag}<span>${esc(f.why)}${other}</span></div>
        <div class="ct-diff"><del>${esc(f.find)}</del><ins>${esc(f.replace)}</ins></div>
        <textarea class="ct-edit" hidden rows="2">${esc(f.replace)}</textarea><div class="ct-acts">${acts}</div></div>`;
    };
    const group = (list) => {
      const by = {};
      list.forEach((f) => (by[f.note] ||= []).push(f));
      return Object.entries(by).map(([n, fs]) => `<h4><a class="ct-goto" href="${goTo(n, fs)}" title="Open the note with this text highlighted"><span>go to</span> ${esc(n)}</a></h4>${fs.map(card).join("")}`).join("");
    };
    // the note, scrolled to the first open suggestion with each one's text highlighted (app.js markFinds)
    const goTo = (n, fs) => {
      const at = fs.filter((f) => f.status === "open" && f.line).sort((a, b) => a.line - b.line);
      return `#/note/${enc(n)}` + (at.length ? `?line=${at[0].line}` + at.map((f) => "&hl=" + encodeURIComponent(f.line + ":" + f.find)).join("") : "");
    };
    el.innerHTML = `<div class="ct-head"><h3>Check notes</h3><span class="act-note">Claude reads all ${d.notes} notes of this class in one call and suggests fact fixes and links. Nothing changes until you apply; what you add this way never counts as your words.</span></div>
      ${d.claude ? "" : `<p class="ct-job bad">Claude was not found, so notes can't be checked.</p>`}
      <div class="ct-bar"><button class="cg-chip on" data-act="run"${running || !d.claude ? " disabled" : ""}>${c.checked ? "Check again" : "Check all notes"}</button>
        ${c.checked ? `<span class="act-note">last checked ${when(c.checked)}${d.changed ? ` · ${d.changed} note${d.changed === 1 ? "" : "s"} changed since` : ""}</span>` : ""}
        ${links.length > 1 ? `<button class="cg-chip" data-act="links">Apply all ${links.length} links</button>` : ""}
        ${old.length ? `<button class="cg-chip${showOld[sub] ? " on" : ""}" data-act="old">History (${old.length})</button>` : ""}</div>
      ${jobLine}
      ${open.length ? group(open) : c.checked && !running ? `<p class="act-note">No open suggestions.</p>` : ""}
      ${showOld[sub] && old.length ? `<h3 class="ct-old">Applied and dismissed</h3>${group(old)}` : ""}`;
    el.onchange = null;
    el.onclick = async (ev) => {
      const b = ev.target.closest("button");
      if (!b) return;
      const act = b.dataset.act;
      if (act === "old") { showOld[sub] = !showOld[sub]; return check(env, el, sub); }
      if (b.dataset.edit) {
        const box = el.querySelector(`[data-f="${b.dataset.edit}"]`), ta = box.querySelector("textarea");
        ta.hidden = !ta.hidden; box.querySelector(".ct-diff ins").hidden = !ta.hidden;
        if (!ta.hidden) ta.focus();
        return;
      }
      b.disabled = true;
      try {
        if (act === "run") await env.post("/api/classes/check/run", { subject: sub });
        else if (act === "links") await env.post("/api/classes/check/apply", { subject: sub, ids: links.map((f) => f.id) });
        else if (b.dataset.apply) {
          const ta = el.querySelector(`[data-f="${b.dataset.apply}"] textarea`);
          await env.post("/api/classes/check/apply", { subject: sub, ids: [b.dataset.apply], replace: ta && !ta.hidden ? ta.value : "" });
        } else if (b.dataset.dismiss) await env.post("/api/classes/check/dismiss", { subject: sub, ids: [b.dataset.dismiss] });
      } catch (e) { env.toast(errText(e)); }
      check(env, el, sub);
    };
    poll(env, el, sub, "check", running);
  }

  window.nvClassTools = { mount };
})();
