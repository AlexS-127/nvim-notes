// Score pace (points per minute, 1 = 30 points per 30 minutes), shared by the Activity view (app.js) and the
// overlay (overlay.js): each jump in today's score curve adds a spike that decays with a 20-minute half-life.
(() => {
  "use strict";
  const SLOPE_HALF_LIFE_MIN = 20;
  const paceTier = (v) => (v > 1 ? "max" : v < .25 ? "low" : v < .5 ? "mid" : v < .75 ? "good" : "high"); // colour band of a pace value ("max": above 1, animated)
  const slopeFmt = (v) => (Math.round(v * 100) / 100 || 0).toFixed(2);
  function slopeFn(a) {
    const jumps = a.score_line.slice(1).map((p, i) => ({ t: new Date(p.at).getTime(), dv: p.total - a.score_line[i].total })).filter((j) => j.dv);
    const k = Math.LN2 / (SLOPE_HALF_LIFE_MIN * 60e3), unit = 60e3;
    return { jumps, at: (t) => jumps.reduce((s, j) => (j.t <= t ? s + j.dv * k * Math.exp(-k * (t - j.t)) : s), 0) * unit };
  }
  window.nvPace = { SLOPE_HALF_LIFE_MIN, paceTier, slopeFmt, slopeFn };
})();
