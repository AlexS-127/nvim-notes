// Class graph (#/classes/<folder>): every note of a course is a node coloured by how well you know it, joined by
// how connected the topics are (links, mentions, shared vocabulary). Data: GET /api/classes/graph?subject=
// (classgraph.go). app.js calls in through window.nvClassGraph with its own helpers (env).
(() => {
  "use strict";
  const esc = (s) => String(s ?? "").replace(/[&<>"]/g, (c) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;" }[c]));
  const norm = (s) => String(s).toLowerCase().normalize("NFD").replace(/[̀-ͯ]/g, "").replace(/[^a-z0-9]/g, "");
  const enc = (p) => p.split("/").map(encodeURIComponent).join("/");
  const W = 1000, H = 640, NS = "http://www.w3.org/2000/svg";
  const pos = {};       // folder -> id -> {x, y, vx, vy}: layouts survive refreshes
  const KINDS = [["part", "Structure"], ["link", "Links"], ["mention", "Mentions"], ["similar", "Similar"]];
  let anim = 0;

  // knowledge colour: red → amber → green; grey when nothing was ever asked
  const knowColor = (m) => m == null ? "var(--bg2)"
    : m < 0.5 ? `color-mix(in srgb, var(--c-warning) ${Math.round(m * 200)}%, var(--c-danger))`
    : `color-mix(in srgb, var(--c-tip) ${Math.round((m - 0.5) * 200)}%, var(--c-warning))`;
  const pct = (m) => m == null ? "not asked yet" : Math.round(m * 100) + "%";
  // a note is the big node, its headings smaller by level; a little extra for longer sections
  const radius = (n, max) => 1.3 * ((n.kind === "note" ? 12 : [0, 8, 6.5, 5.5, 5][n.level] || 5) + 4 * Math.sqrt(Math.max(n.words, 1) / Math.max(max, 1)));

  // which folder is this class? the course code in its title ("ACT 200 …" → act200), else the longest folder name inside it
  async function open(env, title) {
    let map = {};
    try { map = JSON.parse(env.store.get("classMap", "{}")) || {}; } catch (e) {}
    const key = norm(title);
    let sub = map[key];
    if (!sub) {
      const d = await env.api("/api/classes/graph");
      const codes = [...String(title).matchAll(/([A-Za-z]{2,6})\s*(\d{2,4})/g)].map((m) => norm(m[1] + m[2]));  // "BUS 365 …" → bus365
      sub = d.subjects.find((s) => codes.includes(norm(s))) || d.subjects.filter((s) => key.includes(norm(s))).sort((a, b) => b.length - a.length)[0];
    }
    location.hash = sub ? "#/classes/" + encodeURIComponent(sub) : "#/classes/?pick=" + encodeURIComponent(title);
  }

  async function render(env, r) {
    try { await draw(env, r); }
    catch (e) { env.note.innerHTML = `<p><a href="#/classes">← Classes</a></p><p class="act-note">Could not draw the graph: ${esc(String(e && e.stack || e))}</p>`; }
  }

  async function draw(env, r) {
    const note = env.note;
    cancelAnimationFrame(anim);
    note.className = "note classes classgraph";
    note._graph = null;
    note._cal = null;
    if (!r.sub) return picker(env, r.pick || "");
    let g;
    try { g = await env.api("/api/classes/graph?subject=" + encodeURIComponent(r.sub)); }
    catch (e) { env.setTitle("Classes"); note.innerHTML = `<p><a href="#/classes">← Classes</a></p><p class="act-note">${esc(String(e.message || e).trim())}</p>`; return; }
    env.setTitle(r.sub);
    const subjects = (await env.api("/api/classes/graph")).subjects;
    const st = { g, sub: r.sub, sel: null, hover: null, kinds: new Set(JSON.parse(env.store.get("cgKinds2", '["part","link","mention","similar"]'))),
      labels: env.store.get("cgAllLabels", "0") === "1", k: 1, tx: 0, ty: 0 };
    const P = (pos[r.sub] ||= {});
    const ids = g.nodes.map((n) => n.id), maxW = Math.max(...g.nodes.map((n) => n.words));
    // the layout area grows with the graph; the view is then fitted to it
    const grow = 1 + 0.5 * Math.max(0, Math.sqrt(g.nodes.length / 35) - 1), LW = W * grow, LH = H * grow;
    const roots = g.nodes.filter((n) => n.kind === "note");
    const spot = (n) => {  // notes on a ring, their headings scattered around them
      const r = roots.findIndex((x) => x.id === n.note), a = r / roots.length * 2 * Math.PI, jit = n.kind === "note" ? 12 : 55;
      return { x: LW / 2 + Math.cos(a) * 250 * grow + (Math.random() - 0.5) * jit, y: LH / 2 + Math.sin(a) * 210 * grow + (Math.random() - 0.5) * jit, vx: 0, vy: 0 };
    };
    g.nodes.forEach((n) => { if (!P[n.id]) P[n.id] = { ...spot(n), fresh: true }; });
    for (const id of Object.keys(P)) if (!ids.includes(id)) delete P[id];
    const by = Object.fromEntries(g.nodes.map((n) => [n.id, n]));
    const nb = {};
    for (const e of g.edges) { (nb[e.a] ||= []).push([e.b, e]); (nb[e.b] ||= []).push([e.a, e]); }
    note._graph = st;

    const today = new Date().toISOString().slice(0, 10), notes = g.nodes.filter((n) => n.kind === "note");
    const known = notes.filter((n) => n.mastery != null).length, due = notes.filter((n) => n.scheduled && n.due <= today).length;
    note.innerHTML = `<p class="cg-back"><a href="#/classes">← Classes</a></p>
      <div class="cg-head"><h1>${esc(r.sub)}</h1>
        <select id="cg-sub" aria-label="Folder">${subjects.map((s) => `<option${s === r.sub ? " selected" : ""}>${esc(s)}</option>`).join("")}</select></div>
      <div class="cg-stats"><span><b>${g.average == null ? "–" : pct(g.average)}</b> average knowledge</span><span><b>${notes.length}</b> notes</span><span><b>${g.nodes.length - notes.length}</b> headings</span>
        <span><b>${known}</b> notes asked</span><span><b>${due}</b> due</span><span><b>${g.edges.length}</b> connections</span></div>
      <div class="cg-tools">${KINDS.map(([k, l]) => `<button class="cg-chip${st.kinds.has(k) ? " on" : ""}" data-kind="${k}"><i class="cg-key k-${k}"></i>${l}</button>`).join("")}
        <button class="cg-chip${st.labels ? " on" : ""}" data-labels>All labels</button><button class="cg-chip" data-relayout>Re-layout</button>
        <span class="cg-legend"><i style="background:${knowColor(0.05)}"></i>weak <i style="background:${knowColor(0.5)}"></i>shaky <i style="background:${knowColor(0.95)}"></i>solid <i class="unk"></i>not asked · big = note, small = heading</span></div>
      <div class="cg-body"><svg class="cg-svg" viewBox="0 0 ${W} ${H}" role="img" aria-label="Topic graph"><g class="cg-zoom"></g></svg><aside class="cg-side"></aside></div>`;
    const svg = note.querySelector("svg"), zoom = svg.querySelector(".cg-zoom"), side = note.querySelector(".cg-side");
    const mk = (tag, attrs, parent) => { const el = document.createElementNS(NS, tag); for (const k in attrs) el.setAttribute(k, attrs[k]); (parent || zoom).appendChild(el); return el; };

    const edgeEls = g.edges.map((e) => mk("line", { class: "cg-edge k-" + e.kind, "stroke-width": e.kind === "part" ? 1.4 : (e.kind === "link" ? 2.2 : e.kind === "mention" ? 1.5 : 1) * (0.6 + e.w) }));
    const nodeEls = {};
    for (const n of g.nodes) {
      const gr = mk("g", { class: "cg-node " + n.kind + (n.mastery != null && n.mastery < 0.4 ? " weak" : "") + (n.mastery == null ? " unk" : "") + (n.inferred ? " inferred" : ""), "data-id": n.id });
      mk("circle", { r: radius(n, maxW), fill: knowColor(n.mastery) }, gr);
      mk("text", { class: "cg-label", y: radius(n, maxW) + 13 }, gr).textContent = n.title.length > 24 ? n.title.slice(0, 23) + "…" : n.title;
      nodeEls[n.id] = gr;
    }

    function applyView() {
      zoom.setAttribute("transform", `translate(${st.tx} ${st.ty}) scale(${st.k})`);
      svg.classList.toggle("alllabels", st.labels || st.k >= 1.6);
    }
    function draw() {
      g.edges.forEach((e, i) => {
        const a = P[e.a], b = P[e.b], el = edgeEls[i];
        el.setAttribute("x1", a.x); el.setAttribute("y1", a.y); el.setAttribute("x2", b.x); el.setAttribute("y2", b.y);
        el.style.display = st.kinds.has(e.kind) ? "" : "none";
      });
      for (const n of g.nodes) nodeEls[n.id].setAttribute("transform", `translate(${P[n.id].x} ${P[n.id].y})`);
      const focus = st.hover || st.sel;
      const near = focus ? new Set([focus, ...(nb[focus] || []).filter(([, e]) => st.kinds.has(e.kind)).map(([o]) => o)]) : null;
      for (const n of g.nodes) {
        nodeEls[n.id].classList.toggle("dim", !!near && !near.has(n.id));
        nodeEls[n.id].classList.toggle("near", !!near && near.has(n.id));
        nodeEls[n.id].classList.toggle("sel", n.id === st.sel);
      }
      g.edges.forEach((e, i) => edgeEls[i].classList.toggle("dim", !!focus && e.a !== focus && e.b !== focus));
    }

    // force layout: nodes push apart, connected ones pull together (stronger when more related)
    let alpha = 1, dragging = null;
    function tick() {
      const ns = g.nodes, n = ns.length, rep = 16000 * Math.min(1, 40 / n) + 2500;
      for (let i = 0; i < n; i++) {
        const a = P[ns[i].id];
        for (let j = i + 1; j < n; j++) {
          const b = P[ns[j].id];
          let dx = a.x - b.x, dy = a.y - b.y, d2 = dx * dx + dy * dy + 0.01;
          const d = Math.sqrt(d2), f = Math.min(rep / d2, 10);
          dx = dx / d * f; dy = dy / d * f;
          a.vx += dx; a.vy += dy; b.vx -= dx; b.vy -= dy;
        }
      }
      for (const e of g.edges) {
        if (!st.kinds.has(e.kind)) continue;
        const a = P[e.a], b = P[e.b], dx = b.x - a.x, dy = b.y - a.y, d = Math.sqrt(dx * dx + dy * dy) || 1;
        const part = e.kind === "part", rest = part ? 48 : 250 - 110 * e.w, f = (d - rest) * (part ? 0.06 : 0.0035 * (0.3 + e.w));
        a.vx += dx / d * f; a.vy += dy / d * f; b.vx -= dx / d * f; b.vy -= dy / d * f;
      }
      let moved = 0;
      for (const nd of ns) {
        const p = P[nd.id];
        p.vx += (LW / 2 - p.x) * 0.0012; p.vy += (LH / 2 - p.y) * 0.0018;
        if (dragging === nd.id) { p.vx = p.vy = 0; continue; }
        p.vx *= 0.8; p.vy *= 0.8;
        p.x += p.vx * alpha; p.y += p.vy * alpha;
        p.x = Math.max(30, Math.min(LW - 30, p.x)); p.y = Math.max(30, Math.min(LH - 40, p.y));
        moved += Math.abs(p.vx) + Math.abs(p.vy);
      }
      alpha = Math.max(0.05, alpha * 0.985);
      return moved;
    }
    let frames = 0;
    function loop() {
      if (!note.isConnected || note._graph !== st) return;
      const settled = tick() < 0.4 * g.nodes.length && !dragging;
      draw();
      if (!settled && frames++ < 600) anim = requestAnimationFrame(loop);
    }
    const kick = (a = 0.6) => { alpha = Math.max(alpha, a); frames = 0; cancelAnimationFrame(anim); anim = requestAnimationFrame(loop); };
    for (let i = 0; i < (Object.values(P).some((p) => p.fresh) ? 180 : 0); i++) tick();  // settle new layouts before the first paint
    Object.values(P).forEach((p) => { delete p.fresh; });
    function fit() {  // scale and shift the view so the whole graph shows
      const xs = g.nodes.map((n) => P[n.id].x), ys = g.nodes.map((n) => P[n.id].y);
      const x0 = Math.min(...xs) - 50, x1 = Math.max(...xs) + 50, y0 = Math.min(...ys) - 40, y1 = Math.max(...ys) + 55;
      st.k = Math.max(0.35, Math.min(1.5, W / (x1 - x0), H / (y1 - y0)));
      st.tx = (W - (x1 - x0) * st.k) / 2 - x0 * st.k; st.ty = (H - (y1 - y0) * st.k) / 2 - y0 * st.k;
    }
    fit(); alpha = 0.3; applyView(); draw(); kick(0.3);

    // side panel: the topic you point at, else what to review next
    const bar = (m) => `<span class="cg-bar"><span style="width:${Math.round((m || 0) * 100)}%;background:${knowColor(m)}"></span></span>`;
    function nodeRow(n) { return `<button class="cg-row" data-pick="${esc(n.id)}">${bar(n.mastery)}<span>${esc(n.title)}${n.kind === "heading" ? `<em>${esc(by[n.note].title)}</em>` : ""}</span><small>${pct(n.mastery)}</small></button>`; }
    function panel() {
      const n = by[st.sel || st.hover];
      if (!n) {
        const next = [...g.nodes].sort((a, b) => (1 - (b.mastery ?? 0.3)) * (1 + 0.15 * b.degree) - (1 - (a.mastery ?? 0.3)) * (1 + 0.15 * a.degree)).slice(0, 8);
        side.innerHTML = `<h3>Review next</h3><p class="act-note">Weak first; topics many others lean on come before loose ends.</p>${next.map(nodeRow).join("")}
          <p class="act-note">Click a topic for details. Drag to rearrange, scroll to zoom.</p>`;
        return;
      }
      const links = (nb[n.id] || []).filter(([, e]) => st.kinds.has(e.kind)).sort((a, b) => b[1].w - a[1].w);
      const nt = by[n.note], q = n.questions ? `${n.seen} of ${n.questions} question${n.questions === 1 ? "" : "s"} seen${n.q_mastery != null ? ` · ${pct(n.q_mastery)}` : ""}${n.kind === "note" ? " (whole note)" : ""}` : "no questions on this yet";
      const rv = (n.inferred ? "from the note's revisions · " : "") + (nt.revisions ? `last revision ${pct(nt.last_score)}${nt.overdue ? `, ${nt.overdue} day${nt.overdue > 1 ? "s" : ""} overdue (fading)` : ""}${nt.due ? ` · next ${nt.due}` : ""}` : nt.scheduled ? `note scheduled, due ${nt.due}` : "note not scheduled for revision");
      side.innerHTML = `<h3>${esc(n.title)}</h3>${n.kind === "heading" ? `<p class="act-note">heading in ${esc(nt.title)}</p>` : `<p class="act-note">note</p>`}<div class="cg-big">${bar(n.mastery)}<b>${pct(n.mastery)}</b></div>
        <ul class="cg-facts"><li>${esc(q)}</li><li>${esc(rv)}</li><li>${n.words} words · ${n.degree} connection${n.degree === 1 ? "" : "s"}</li></ul>
        <div class="cg-actions"><a class="cg-chip" href="#/note/${enc(n.note)}${n.line ? "?line=" + n.line : ""}">Open ${n.kind === "heading" ? "section" : "note"}</a><button class="cg-chip on" data-revise="${esc(n.note)}">Revise note</button></div>
        <h4>Connected</h4>${links.length ? links.map(([o, e]) => `<div class="cg-link"><small class="k-${e.kind}">${e.kind}</small>${nodeRow(by[o])}</div>`).join("") : `<p class="act-note">Nothing connects to this topic yet.</p>`}
        ${st.sel ? `<button class="cg-chip" data-clear>Back to list</button>` : ""}`;
    }
    panel();

    // events
    const svgPoint = (ev) => { const m = svg.getScreenCTM().inverse(), p = new DOMPoint(ev.clientX, ev.clientY).matrixTransform(m); return [(p.x - st.tx) / st.k, (p.y - st.ty) / st.k, p.x, p.y]; };
    const nodeOf = (t) => t.closest && t.closest(".cg-node");
    svg.addEventListener("pointerover", (ev) => { const n = nodeOf(ev.target); if (n && !dragging && st.hover !== n.dataset.id) { st.hover = n.dataset.id; draw(); if (!st.sel) panel(); } });
    svg.addEventListener("pointerout", (ev) => { if (nodeOf(ev.target) && !dragging) { st.hover = null; draw(); if (!st.sel) panel(); } });
    svg.addEventListener("pointerdown", (ev) => {
      const n = nodeOf(ev.target), start = svgPoint(ev);
      svg.setPointerCapture(ev.pointerId);
      let moved = false;
      if (n) dragging = n.dataset.id;
      const move = (e) => {
        const p = svgPoint(e);
        if (Math.hypot(p[2] - start[2], p[3] - start[3]) > 3) moved = true;
        if (!moved) return;
        if (dragging) { P[dragging].x = p[0]; P[dragging].y = p[1]; kick(0.3); }
        else { st.tx += p[2] - start[2]; st.ty += p[3] - start[3]; start[2] = p[2]; start[3] = p[3]; applyView(); }
      };
      const up = () => {
        svg.removeEventListener("pointermove", move); svg.removeEventListener("pointerup", up);
        if (!moved) { st.sel = n ? (st.sel === n.dataset.id ? null : n.dataset.id) : null; panel(); draw(); }
        dragging = null; kick(0.2);
      };
      svg.addEventListener("pointermove", move); svg.addEventListener("pointerup", up);
    });
    svg.addEventListener("wheel", (ev) => {
      ev.preventDefault();
      const [, , px, py] = svgPoint(ev), k2 = Math.max(0.4, Math.min(3, st.k * (ev.deltaY < 0 ? 1.12 : 1 / 1.12)));
      st.tx = px - (px - st.tx) * k2 / st.k; st.ty = py - (py - st.ty) * k2 / st.k; st.k = k2; applyView();
    }, { passive: false });
    note.querySelector("#cg-sub").onchange = (ev) => { location.hash = "#/classes/" + encodeURIComponent(ev.target.value); };
    note.querySelector(".cg-tools").addEventListener("click", (ev) => {
      const b = ev.target.closest("button");
      if (!b) return;
      if (b.dataset.kind) { st.kinds.has(b.dataset.kind) ? st.kinds.delete(b.dataset.kind) : st.kinds.add(b.dataset.kind); b.classList.toggle("on"); env.store.set("cgKinds2", JSON.stringify([...st.kinds])); kick(0.5); panel(); }
      else if (b.dataset.labels !== undefined) { st.labels = !st.labels; b.classList.toggle("on"); env.store.set("cgAllLabels", st.labels ? "1" : "0"); applyView(); }
      else if (b.dataset.relayout !== undefined) { g.nodes.forEach((n) => Object.assign(P[n.id], spot(n))); for (let i = 0; i < 180; i++) tick(); fit(); applyView(); draw(); kick(1); }
    });
    side.addEventListener("click", async (ev) => {
      const b = ev.target.closest("button");
      if (!b) return;
      if (b.dataset.pick) { st.sel = b.dataset.pick; panel(); draw(); }
      else if (b.dataset.clear !== undefined) { st.sel = null; panel(); draw(); }
      else if (b.dataset.revise) {
        b.disabled = true;
        try {
          if (!by[b.dataset.revise].scheduled) await env.post("/api/revision/add", { id: b.dataset.revise });
          await env.post("/api/revision/start", { id: b.dataset.revise });
          env.toast("Quiz opened in a terminal window");
        } catch (e) { env.toast(String(e.message || e).replace(/^revision: /, "").trim()); }
        setTimeout(() => { b.disabled = false; }, 3000);
      }
    });
  }

  // no folder matched the class title: choose one (remembered for next time)
  async function picker(env, title) {
    const d = await env.api("/api/classes/graph"), note = env.note;
    env.setTitle("Classes");
    note.innerHTML = `<p class="cg-back"><a href="#/classes">← Classes</a></p><h1>${esc(title || "Class")}</h1>
      <p class="act-note">Which notes folder holds this class? It is remembered.</p>
      <div class="cg-pick">${d.subjects.map((s) => `<button class="cg-chip on" data-sub="${esc(s)}">${esc(s)}</button>`).join(" ")}</div>`;
    note.querySelector(".cg-pick").onclick = (ev) => {
      const b = ev.target.closest("button");
      if (!b) return;
      if (title) { let m = {}; try { m = JSON.parse(env.store.get("classMap", "{}")) || {}; } catch (e) {} m[norm(title)] = b.dataset.sub; env.store.set("classMap", JSON.stringify(m)); }
      location.hash = "#/classes/" + encodeURIComponent(b.dataset.sub);
    };
  }

  window.nvClassGraph = { render, open };
})();
