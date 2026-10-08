#!/usr/bin/env python3
"""Vocab and question quiz. Reads `word :: translation` lines from notes/<subject>/definitions.md,
or a question bank from notes/<subject>/questions.md (multiple choice, true/false, short answer
and self-graded worked problems; format in notes/format/question-format.md). A subject with a
questions.md uses it.

Progress is saved per subject in notes/<subject>/.quiz_stats.json. Each question is
a weighted random draw: new words and words you keep missing come up most, words you
have answered correctly many times in a row come up rarely. After a correct answer the
other accepted meanings are shown. The quiz runs until you type 'q'.

Gamified: earn XP with combo multipliers, level up through Roman ranks, keep a daily
streak, and unlock badges. Player state lives in the same stats file.

Time spent is logged per session to notes/.quiz_log.jsonl and shows up in the viewer's
Activity view. Each kind of question has a cut-off (ANSWER_CAPS: 10 s for a vocab word, longer
for questions, 10 minutes for worked problems): past it you count as idle, the time beyond it is
not quiz time, and that answer's response time is not measured.

At the start you can practise everything (just press Enter) or only one word type,
taken from the "(noun)", "(verb)"... at the end of each definitions.md line, or, for a
question bank, one `## ` topic.

Every word has a score from how often it was right vs wrong (shown after each answer). A focused
quiz (asked at the start; `--focused` skips the question) leaves out what you know well (3 right in
a row) and asks the least-asked first.

A personalized quiz (`--personalized`) draws by need: what you keep missing, answer slowly, haven't
seen in a while or never saw, each shown with its reason.

Usage: python3 ~/notes/quiz.py [subject] [--focused | --personalized]   (a link to nvim-notes/quiz/quiz.py)
       python3 ~/notes/quiz.py --revise NOTE   (one scheduled revision, see revise())
"""

import atexit
import json
import shutil
import subprocess
import math
import os
import random
import re
import sys
import textwrap
import threading
import time
import unicodedata
from datetime import date, datetime
from pathlib import Path

# The script is symlinked into the notes folder from the nvim-notes repo, so find the notes
# through NOTES_DIR (like notesview and Neovim do), not through the script's own path.
NOTES = Path(os.environ.get("NOTES_DIR") or Path.home() / "notes").expanduser()
LOG = NOTES / ".quiz_log.jsonl"
# Seconds an answer may take before you count as idle (stopped quizzing rather than still thinking):
# time beyond it is not quiz time, and the answer's response time is not measured. Per question kind.
ANSWER_CAPS = {"vocab": 10, "tf": 20, "mc": 30, "multi": 45, "short": 45, "problem": 600, "recall": 600}
PROMPT_CAP = 10  # short follow-ups: "Actually right?", "sure?", "how much?"
READ_CAP = 120  # reading a worked solution or a whole note before grading yourself


# ---- per-answer signals (renderer/signals.go reads them; counts only, never the answer text) ----


def sensor_on(name, default=True):
    try:
        c = json.loads((NOTES / ".signals" / "config.json").read_text())
        return bool(c.get("sensors", {}).get(name, default))
    except (OSError, ValueError):
        return default


def log_answer(subject, kind, latency, ok, revision="", conf=None):
    """`latency` None = the answer came after the kind's cut-off: logged as idle, without a time."""
    ok = float(ok)  # partial credit: 0 < ok < 1 is logged as not correct, with its credit
    """One line per answer in .signals/YYYY-MM-DD.jsonl: how long it took, right or wrong, the
    question kind and (if asked) how sure you were. Answer latency and accuracy track focus and fatigue."""
    if not sensor_on("quiz"):
        return
    rec = {"at": datetime.now().strftime("%Y-%m-%dT%H:%M:%S"), "src": "quiz", "subject": subject, "kind": kind,
           "correct": ok >= 1}
    if latency is None:
        rec["idle"] = True
    else:
        rec["latency_ms"] = int(latency * 1000)
    if 0 < ok < 1:
        rec["credit"] = round(ok, 2)
    if revision:
        rec["revision"] = revision
    if conf:
        rec["conf"] = conf
    try:
        d = NOTES / ".signals"
        d.mkdir(exist_ok=True)
        with open(d / f"{date.today().isoformat()}.jsonl", "a") as f:
            f.write(json.dumps(rec) + "\n")
    except OSError:
        pass


NO_CONFIDENCE = {"lat101", "latin101"}  # subjects where per-question confidence is never asked


def ask_confidence(clock, subject=None):
    """Optional (the Data tab's "confidence" switch): how sure you were, 1 guess / 2 unsure / 3 sure."""
    if subject in NO_CONFIDENCE or not sensor_on("confidence", False):
        return None
    r = clock.input("  sure? 1 guess · 2 unsure · 3 sure (Enter skips) ", PROMPT_CAP).strip()
    return int(r) if r in ("1", "2", "3") else None


def log_session(subject, seconds, answered, correct, xp=0, levels=0):
    """Append one line per session; the viewer's Activity view sums them per day."""
    if seconds < 1 and not (answered or xp or levels):
        return
    entry = {
        "date": date.today().isoformat(),
        "at": datetime.now().isoformat(timespec="seconds"),
        "subject": subject,
        "seconds": round(seconds),
        "answered": answered,
        "correct": correct,
    }
    if xp or levels:  # quiz progress also scores in the viewer
        entry["xp"], entry["levels"] = xp, levels
    with LOG.open("a", encoding="utf-8") as f:
        f.write(json.dumps(entry, ensure_ascii=False) + "\n")


def fmt_time(seconds):
    m, s = divmod(int(seconds), 60)
    return f"{m}m {s:02d}s" if m else f"{s}s"


def load(path):
    pairs = []
    for line in path.read_text(encoding="utf-8").splitlines():
        line = re.sub(r"^\s*(?:[-*+]\s+|\d+\.\s+)?", "", line)  # strip list markers
        if "::" not in line:
            continue
        word, trans = (s.strip() for s in line.split("::", 1))
        if word and trans:
            pairs.append((word, trans))
    return pairs


def kind(trans):
    """Word type: the last '(...)' at the end of the line, e.g. 'noun'. '' if none."""
    m = re.search(r"\(([^()]*)\)\s*$", trans)
    return m[1].strip().lower() if m else ""


def choose_type(pairs):
    """Ask which word type to practise. Enter = everything. Returns the filtered pairs."""
    return choose_group(pairs, lambda p: kind(p[1]) or "untyped", "Word type")


def norm(s):
    s = unicodedata.normalize("NFD", s.lower())
    s = "".join(c for c in s if not unicodedata.combining(c))  # drop macrons/accents
    return re.sub(r"\s+", " ", re.sub(r"[^\w\s]", "", s)).strip()


PRONOUN = r"(?:I|you|he|she|it|we|they)"


def meanings(s):
    """Split 'to love, to like; be fond of (x)' into display strings."""
    s = re.sub(r"\([^)]*\)", "", s)
    out = []
    for part in s.split(";"):
        # 'he, she, it said' shares its verb: expand to 'he said', 'she said', 'it said'
        m = re.match(rf"\s*((?:{PRONOUN}\s*[,/]\s*)+{PRONOUN})\s+(\S.*)$", part, re.I)
        if m:
            out += [f"{p.strip()} {m[2].strip()}" for p in re.split(r"[,/]", m[1])]
        else:
            out += re.split(r"[,/]", part)
    return [o.strip() for o in out if norm(o)]


def options(s):
    return {norm(o) for o in meanings(s)}


def others(guess, answer):
    """Accepted meanings of `answer` other than the one that was guessed."""
    g = norm(guess)
    return [m for m in meanings(answer) if norm(m) != g] if g in options(answer) else []


def check(guess, answer):
    g = norm(guess)
    return bool(g) and (g == norm(answer) or g in options(answer))


def load_stats(path):
    try:
        return json.loads(path.read_text(encoding="utf-8"))
    except (OSError, ValueError):
        return {}


def save_stats(path, stats):
    tmp = path.with_suffix(".tmp")
    tmp.write_text(
        json.dumps(stats, ensure_ascii=False, indent=1, sort_keys=True),
        encoding="utf-8",
    )
    tmp.replace(path)


_mistake = None  # set by the askers when an answer is confirmed wrong; the loops take it with take_mistake()


def note_mistake(q, guess, answer):
    global _mistake
    _mistake = {"q": q, "guess": guess, "answer": answer}


def take_mistake():
    global _mistake
    m, _mistake = _mistake, None
    return m


def log_mistake(subject, m, notes=None):
    """List a confirmed-wrong answer in <subject>/mistakes.md (one line per question, newest data
    wins: `missed N×` counts up, `last` is today). Created excluded from word counts, and
    revisable() leaves it out of the revision topics and the class graph."""
    def one(t, n=300):
        t = " ⏎ ".join(x.strip() for x in str(t).splitlines() if x.strip())
        return t if len(t) <= n else t[: n - 1] + "…"
    path = NOTES / subject / "mistakes.md"
    q = one(m["q"], 400)
    topics = ", ".join(Path(n).stem for n in notes or [])
    head = f"- **Q:** {q} — "
    lines = path.read_text(encoding="utf-8").split("\n") if path.exists() else [
        "# Mistakes", "", "Answers the quiz marked wrong and you confirmed wrong, one line per question. Written by the quiz.", ""]
    while lines and not lines[-1].strip():
        lines.pop()
    today = date.today().isoformat()
    for i, line in enumerate(lines):
        if line.startswith(head):
            c = re.search(r"missed (\d+)×", line)
            lines[i] = re.sub(r"missed \d+× · last [\d-]+", f"missed {int(c[1]) + 1 if c else 2}× · last {today}", line)
            break
    else:
        if not lines[-1].startswith("- **Q:**"):
            lines.append("")
        lines.append(head + f"you: {one(m['guess']) or '–'} — answer: {one(m['answer'])}" + (f" — {topics}" if topics else "") + f" · missed 1× · last {today}")
    new = not path.exists()
    path.write_text("\n".join(lines).rstrip("\n") + "\n", encoding="utf-8")
    if new:  # what you wrote here is not your own writing
        subprocess.run([notesview_bin(), "words", "exclude", f"{subject}/mistakes.md"], capture_output=True, env={**os.environ, "NOTES_DIR": str(NOTES)})


def record(stats, word, ok, notes=None, rt=None):
    """Count an answer. `ok` is a credit from 0 to 1 (True/False work): only full credit is a right
    answer for the streak and scheduling; `credit` keeps a running average of how much you got (the
    class graph's knowledge colour), `notes` the revision topics the question belongs to."""
    credit = max(0.0, min(1.0, float(ok)))
    full = credit >= 1
    e = stats.setdefault(word, {"right": 0, "wrong": 0, "streak": 0})
    e["right" if full else "wrong"] += 1
    e["streak"] = e["streak"] + 1 if full else 0
    e["credit"] = round(credit if "credit" not in e else (e["credit"] + credit) / 2, 3)
    if full:
        e.pop("reask", None)  # a confirmed mistake is over once it is answered right
    e["last"] = date.today().isoformat()
    if rt is not None:  # response time (answers within the cut-off only): a running average, in ms
        ms = round(rt * 1000)
        e["rt_ms"] = ms if "rt_ms" not in e else round((e["rt_ms"] + ms) / 2)
    if notes:
        e["notes"] = sorted(set(notes) | set(e.get("notes", [])))


def word_score(stats, word):
    """How well a word is known, 0-1, from how often it was right vs wrong ((right+1)/(answers+2), so one
    answer never means 0 or 100%); None while it has never been answered."""
    e = stats.get(word)
    if not e or e["right"] + e["wrong"] == 0:
        return None
    return (e["right"] + 1) / (e["right"] + e["wrong"] + 2)


KNOWN_STREAK = 3  # right this many times in a row = known well (a focused quiz skips it)


def known_well(stats, word):
    e = stats.get(word)
    return bool(e) and e.get("streak", 0) >= KNOWN_STREAK


def times_asked(stats, word):
    e = stats.get(word)
    return e["right"] + e["wrong"] if e else 0


def median_rt(items, stats):
    """The middle response time (ms) of the answered items, for telling slow answers apart."""
    ts = sorted(stats[k]["rt_ms"] for k, _ in items if stats.get(k, {}).get("rt_ms"))
    return ts[len(ts) // 2] if ts else None


def need(stats, word, med_rt, today=None):
    """How much practice an item needs (0-1.5) and the main reason, from its answers: how much credit
    you got, a confirmed mistake, still shaky, answering slowly (1.5x your usual time or more), and
    not seen for longer than its streak holds (1, 2, 4… days). New items get 0.6; well-known, fast,
    recent ones next to nothing."""
    e = stats.get(word)
    if not e or e["right"] + e["wrong"] == 0:
        return 0.6, "new"
    n = e["right"] + e["wrong"]
    acc = e["credit"] if e.get("credit") is not None else e["right"] / n
    score, reasons = 1 - acc, []
    if e.get("reask"):
        score += 0.4
        reasons.append("missed last time")
    elif e["wrong"] and e["streak"] < KNOWN_STREAK:
        score += 0.2
        reasons.append("still shaky")
    rt = e.get("rt_ms")
    if rt and med_rt and rt >= 1.5 * med_rt:
        score += 0.3 * min(1, (rt / med_rt - 1) / 2)
        reasons.append(f"slow: {rt / 1000:.1f}s vs your usual {med_rt / 1000:.1f}s")
    if e.get("last"):
        days = ((today or date.today()) - date.fromisoformat(e["last"])).days
        holds = 2 ** min(e["streak"], 6)
        if days > holds:
            score += min(0.3, 0.1 * days / holds)
            reasons.append(f"not seen in {days} days")
    if e["streak"] >= MASTERED_STREAK and not reasons:
        score *= 0.3
    if not reasons and acc < 0.7:
        reasons.append(f"{acc:.0%} right so far")
    return min(score, 1.5), (reasons[0] if reasons else "")


def pick_personal(items, stats, asked, n, last=None):
    """Personalized quiz: a weighted draw where the weight is need² (so the neediest come up far more
    often), cut to almost nothing right after an item was asked like `pick`. Returns (item, reason)."""
    med = median_rt(items, stats)
    pool = [it for it in items if it[0] != last] or items
    scored = [(it, *need(stats, it[0], med)) for it in pool]
    ws = [(0.05 + sc * sc) * (cooldown(n - asked[it[0]]) if it[0] in asked else 1) for it, sc, _ in scored]
    it, _, why = random.choices(scored, ws)[0]
    return it, why


def personal_summary(items, stats):
    """What a personalized quiz will work on, by main reason, for the start line."""
    med, counts = median_rt(items, stats), {}
    for k, _ in items:
        sc, why = need(stats, k, med)
        if sc >= 0.3 and why:
            label = why.split(":")[0].split(" in ")[0].replace("% right so far", "")
            label = "low accuracy" if label.isdigit() else label
            counts[label] = counts.get(label, 0) + 1
    return ", ".join(f"{n} {k}" for k, n in sorted(counts.items(), key=lambda kv: -kv[1])) or "nothing stands out"


def pick_focused(items, stats, last=None):
    """Focused quiz: never what you know well (KNOWN_STREAK right in a row); of the rest, one of those
    asked the fewest times so far (random among ties; not the one just asked unless it is the only one left). None when all are known."""
    pool = [it for it in items if not known_well(stats, it[0])]
    if not pool:
        return None
    pool = [it for it in pool if it[0] != last] or pool  # never the same one twice in a row
    least = min(times_asked(stats, it[0]) for it in pool)
    return random.choice([it for it in pool if times_asked(stats, it[0]) == least])


def struggling(stats, word):
    """Missed at some point and hasn't yet answered correctly 3 times in a row."""
    e = stats.get(word)
    return bool(e) and e["wrong"] > 0 and e["streak"] < 3


MASTERED_STREAK = 5  # this many correct in a row (and not struggling) = confidently learnt
W_NEW, W_STRUGGLING, W_NORMAL, W_MASTERED = 6, 4, 1, 0.15


def weight(stats, word):
    """How likely a word is to be drawn: new > struggling > normal > mastered."""
    e = stats.get(word)
    if not e:
        return W_NEW
    if e.get("reask"):  # confirmed wrong last time: back soon
        return W_STRUGGLING * 2
    if struggling(stats, word):
        return W_STRUGGLING
    if e["streak"] >= MASTERED_STREAK:
        return W_MASTERED
    return W_NORMAL


COOLDOWN_FLOOR, COOLDOWN_QUESTIONS = 0.02, 20


def cooldown(age):
    """Multiplier on a word's weight `age` questions after it was asked: ~0, then back to 1."""
    return COOLDOWN_FLOOR + (1 - COOLDOWN_FLOOR) * min(1, age / COOLDOWN_QUESTIONS)


def pick(items, stats, asked, n, last=None):
    """Weighted random draw. A word just asked has its weight cut to almost nothing, and it
    climbs back linearly over COOLDOWN_QUESTIONS questions. `asked` maps word -> question
    number it was last asked in; `n` is the current question number."""
    pool = [p for p in items if p[0] != last] or items
    ws = [weight(stats, w) * cooldown((n - asked[w]) * (4 if stats.get(w, {}).get("reask") else 1)) if w in asked else weight(stats, w) for w, _ in pool]
    return random.choices(pool, ws)[0]


PLAYER = "__player__"  # key in the stats file; can't collide with a vocab word
RANKS = ["Tiro", "Miles", "Optio", "Centurio", "Tribunus", "Legatus", "Consul", "Imperator"]
BADGES = {
    "first_blood": "Primus Sanguis (first correct answer)",
    "combo5": "Ardens (5 in a row)",
    "combo10": "Fulmen (10 in a row)",
    "combo20": "Invictus (20 in a row)",
    "recovered": "Phoenix (turned a weak word around)",
    "perfect": "Perfectus (10+ answers, no mistakes)",
    "week": "Constans (7-day streak)",
}


def level_of(xp):
    return 1 + int(math.sqrt(xp / 50))


def rank_of(level):
    return RANKS[min(level - 1, len(RANKS) - 1)]


def xp_for(level):
    return 50 * (level - 1) ** 2


def progress_bar(xp, width=20):
    lo, hi = xp_for(level_of(xp)), xp_for(level_of(xp) + 1)
    filled = int(width * (xp - lo) / (hi - lo))
    return "█" * filled + "░" * (width - filled)


def start_player(stats):
    """Load player state and update the daily streak. Returns (player, streak message)."""
    pl = stats.setdefault(PLAYER, {})
    pl.setdefault("xp", 0)
    pl.setdefault("days", 0)
    pl.setdefault("best_combo", 0)
    pl.setdefault("badges", [])
    today = date.today()
    last = date.fromisoformat(pl["last_day"]) if pl.get("last_day") else None
    msg = ""
    if last != today:
        pl["days"] = pl["days"] + 1 if last and (today - last).days == 1 else 1
        pl["last_day"] = today.isoformat()
        msg = f"🔥 {pl['days']}-day streak!" if pl["days"] > 1 else ""
    return pl, msg


def award(pl, key):
    """Unlock a badge once; returns an announcement or ''."""
    if key in pl["badges"]:
        return ""
    pl["badges"].append(key)
    return f"  🏅 Badge unlocked: {BADGES[key]}"


def choose(prompt, choices):
    for i, c in enumerate(choices, 1):
        print(f"  {i}) {c}")
    while True:
        r = input(prompt).strip()
        if r.isdigit() and 1 <= int(r) <= len(choices):
            return int(r) - 1
        print("  Pick a number from the list.")


class Clock:
    """Active study time: each prompt adds the time until it is answered, capped at `cap`."""

    def __init__(self):
        self.active = 0.0
        self.pending = None  # (monotonic time the open prompt was shown, its cap)
        self.logged = 0  # whole seconds already written to the log
        self.xp = self.levels = 0  # XP earned and levels gained this session
        self.logged_xp = self.logged_levels = 0
        self.rt = None  # the last answer prompt: (seconds it took, its cut-off)

    def input(self, prompt, cap=PROMPT_CAP, answer=False):
        """`answer`: this prompt takes the answer itself, so its time is the response time."""
        asked = time.monotonic()
        self.pending = (asked, cap)
        try:
            return input(prompt)
        finally:  # so quitting or Ctrl-C at a prompt still counts
            self.pending = None
            took = time.monotonic() - asked
            self.active += min(took, cap)
            if answer:
                self.rt = (took, cap)

    def response_time(self):
        """Seconds the last answer took, or None if it came after its cut-off (idle) or none was asked."""
        rt, self.rt = self.rt, None
        return rt[0] if rt and rt[0] <= rt[1] else None

    def live(self):
        """Active seconds so far, including the prompt that is still open (capped like a finished one).
        Never decreases, and equals `active` once the prompt is answered."""
        p = self.pending
        return self.active + (min(time.monotonic() - p[0], p[1]) if p else 0.0)

    def flush(self, subject, answered=0, correct=0):
        """Log the seconds not written yet as their own line (the viewer sums lines per day)."""
        n = max(round(self.live()) - self.logged, 0)
        xp, levels = self.xp - self.logged_xp, self.levels - self.logged_levels
        if n > 0 or answered or xp or levels:
            self.logged += n
            self.logged_xp, self.logged_levels = self.xp, self.levels
            log_session(subject, n, answered, correct, xp, levels)

    def log_every_minute(self, subject, every=60):
        """Background thread: write the new study time once a minute, so score points arrive while quizzing."""
        def loop():
            while True:
                time.sleep(every)
                self.flush(subject)
        threading.Thread(target=loop, daemon=True).start()


# ---- vocab (definitions.md) -------------------------------------------------------------


def add_meaning(path, pairs, word, guess):
    """Accept `guess` as another meaning of `word`: in definitions.md (before the trailing
    '(type)') and in the loaded `pairs`, so it counts from now on."""
    def merged(trans):
        m = re.search(r"\s*(\([^()]*\))\s*$", trans)
        return f"{trans[:m.start()]}, {guess} {m[1]}" if m else f"{trans}, {guess}"

    for i, (w, t) in enumerate(pairs):
        if w == word:
            pairs[i] = (w, merged(t))
            break
    else:
        return
    lines = path.read_text(encoding="utf-8").split("\n")
    for i, line in enumerate(lines):
        head, sep, tail = line.partition("::")
        w = re.sub(r"^\s*(?:[-*+]\s+|\d+\.\s+)?", "", head).strip()
        if sep and w == word:
            lines[i] = f"{head}::{' ' if tail.startswith(' ') else ''}{merged(tail.strip())}"
            break
    path.write_text("\n".join(lines), encoding="utf-8")
    print(f"  + added \"{guess}\" to {word}")


def vocab_bank(subject):
    """Items, asker and display label for a definitions.md subject."""
    pairs = load(NOTES / subject / "definitions.md")
    if not pairs:
        sys.exit(f"No 'word :: translation' lines in {subject}/definitions.md")
    pairs = choose_type(pairs)

    print("\nDirection:")
    d = choose("> ", ["Latin → English", "English → Latin", "Mixed"])

    def ask(item, clock):
        word, trans = item
        forward = d == 0 or (d == 2 and random.random() < 0.5)
        q, a = (word, trans) if forward else (trans, word)
        guess = clock.input(f"{q}  → ", ANSWER_CAPS["vocab"], answer=True).strip() or "?"  # a blank Enter skips and reveals, like '?'
        if guess.lower() == "q":
            return None
        ok = guess != "?" and check(guess, a)
        if not ok and guess != "?":
            print(f"  ✗  {a}")
            if clock.input("  Actually right? y = count it as correct (Enter = no) ", PROMPT_CAP).strip().lower() == "y":
                if forward:
                    add_meaning(NOTES / subject / "definitions.md", pairs, word, guess)
                return True, "", ""
            note_mistake(q, guess, a)
            return False, "", ""
        more = others(guess, a) if ok else []
        return ok, (f"  also: {', '.join(more)}" if more else ""), f"  ✗  {a}"

    return pairs, ask, lambda item: f"{item[0]} :: {item[1]}", "word"


# ---- question bank (questions.md) -------------------------------------------------------

FIELD = re.compile(r"^(Q|A|Why|Src|Solution):[ \t]?(.*)$")
CHOICE = re.compile(r"^([a-hA-H])[).][ \t]+(.*)$")
REFERS_TO_LETTERS = re.compile(r"\babove\b|\b[A-H] and [A-H]\b", re.I)


def answer_letters(a):
    """Choice letters of a multiple-choice answer: 'c' -> ['c'], 'a, c' / 'a and c' / 'ac' -> ['a', 'c'].
    [] when it is not a list of letters."""
    t = re.sub(r"\band\b|[,;&\s]+", "", a.lower())
    return list(dict.fromkeys(t)) if t and re.fullmatch(r"[a-h]+", t) else []


def load_questions(path):
    """Parse questions.md (format in notes/format/question-format.md) into dicts with keys
    topic, q, choices, a, solution, why, src. A block runs from `Q:` to the next `Q:` or
    heading; `## ` headings are topics. Malformed blocks are reported and skipped."""
    out, bad, topic, cur, field = [], [], "", None, None

    def flush():
        if not cur:
            return
        for k in ("q", "a", "solution", "why", "src"):
            cur[k] = textwrap.dedent(cur[k]).strip()
        letters = "abcdefgh"[: len(cur["choices"])]
        picked = answer_letters(cur["a"])
        if cur["solution"] or (cur["a"] and (not cur["choices"] or (picked and all(x in letters for x in picked)))):
            out.append(cur)
        else:
            bad.append(cur["q"].splitlines()[0][:60] if cur["q"] else "(empty question)")

    for line in path.read_text(encoding="utf-8").splitlines():
        line = line.rstrip()
        if line.startswith("#"):
            flush()
            cur = field = None
            if line.startswith("## "):
                topic = line[3:].strip()
            continue
        m = FIELD.match(line)
        if m:
            name, val = m[1].lower(), m[2]
            if name == "q":
                flush()
                cur = {"file": path, "topic": topic, "q": "", "choices": [], "a": "", "solution": "", "why": "", "src": ""}
            if cur is not None:
                field = name
                cur[field] = val
            continue
        if cur is None or not line.strip():
            continue
        c = CHOICE.match(line.strip())
        if c and field in ("q", "choices"):
            cur["choices"].append(c[2].strip())
            field = "choices"
        elif field and field != "choices":
            cur[field] += "\n" + line
    flush()
    for b in bad:
        print(f"  skipped (no usable A:/Solution:): {b}", file=sys.stderr)
    return out


def qkind(q):
    if q["solution"]:
        return "problem"
    if q["choices"]:
        return "multi" if len(answer_letters(q["a"])) > 1 else "mc"
    if norm(q["a"]) in ("true", "false"):
        return "tf"
    return "short"


def short_label(q, n=70):
    first = " ".join(q["q"].split())
    return first if len(first) <= n else first[: n - 1] + "…"


def multi_choice(g, n):
    """Positions picked in a select-all answer ('a,c', 'a c', 'ac', '1 3'); [] if anything is invalid."""
    picked = set()
    for tok in re.split(r"[\s,;]+", g.strip()):
        parts = [tok] if tok.isdigit() else list(tok)
        for x in parts:
            i = int(x) - 1 if x.isdigit() else "abcdefgh".find(x)
            if not 0 <= i < n:
                return []
            picked.add(i)
    return sorted(picked)


def ask_question(item, clock):
    _, q = item
    kind = qkind(q)
    print(q["q"])
    note = "".join(f"\n  {l.strip()}" for l in q["why"].splitlines())
    src = f"  ({q['src']})" if q["src"] else ""

    if kind == "problem":
        r = clock.input("  Work it out, then Enter to see the solution (q to quit) ", ANSWER_CAPS["problem"], answer=True)
        if r.strip().lower() == "q":
            return None
        print("\n".join(f"  {l}" for l in q["solution"].splitlines()) + note + ("\n" + src if src else ""))
        while True:
            r = clock.input("  Did you get it? [y/n, p = partly] ", READ_CAP).strip().lower()
            if r in ("y", "n"):
                if r == "n":
                    note_mistake(q["q"], "", q["solution"])
                return r == "y", "", ""
            if r == "p":
                v = clock.input("  How much of it, 1-99 % ", PROMPT_CAP).strip().rstrip("%")
                if v.isdigit() and 0 < int(v) < 100:
                    note_mistake(q["q"], f"{v}% of it", q["solution"])
                    return int(v) / 100, "", ""
            if r == "q":
                return None

    if kind in ("mc", "multi"):
        order = list(range(len(q["choices"])))
        if not any(REFERS_TO_LETTERS.search(c) for c in q["choices"]):
            random.shuffle(order)
        letters = "abcdefgh"
        for i, j in enumerate(order):
            print(f"  {letters[i]}) {q['choices'][j]}")
        rights = sorted(order.index(letters.index(x)) for x in answer_letters(q["a"]))
        answer = "\n    ".join(f"{letters[r]}) {q['choices'][order[r]]}" for r in rights)
        if kind == "multi":
            print("  (select all that apply, e.g. a,c)")
            prompt = "  → "
            answer = "\n    " + answer
        else:
            prompt = "  → "
        valid = set(letters[: len(order)]) | {str(i + 1) for i in range(len(order))}
    elif kind == "tf":
        prompt, answer, valid = "  [t/f] → ", q["a"].lower(), {"t", "f", "true", "false"}
    else:
        prompt, answer, valid = "  → ", q["a"], None

    while True:
        guess = clock.input(prompt, ANSWER_CAPS[kind], answer=True).strip() or "?"
        g = guess.lower()
        if g == "q":
            return None
        if guess == "?":
            break
        if kind == "multi":
            chosen = multi_choice(g, len(order))
            if chosen:
                break
            print("  Answer with letters like a,c; '?' to skip or 'q' to quit.")
            continue
        if valid is None or g in valid:
            break
        print("  Answer with " + ("a letter" if kind == "mc" else "t or f") + ", '?' to skip or 'q' to quit.")
    if guess == "?":
        ok = False
    elif kind == "multi":
        hits, extra = len(set(chosen) & set(rights)), len(set(chosen) - set(rights))
        ok = max(0.0, (hits - extra) / len(rights))  # partial credit: each wrong pick cancels a right one
        if 0 < ok < 1:
            print(f"  ◐ {hits} of {len(rights)} right" + (f", {extra} wrong pick{'s' if extra != 1 else ''}" if extra else ""))
    elif kind == "mc":
        ok = g in (letters[rights[0]], str(rights[0] + 1))
    elif kind == "tf":
        ok = g in ("t", "true") if answer == "true" else g in ("f", "false")
    else:
        ok = check(guess, answer)
    if float(ok) < 1 and guess != "?":
        print(f"  ✗  {answer}" + note + src if ok == 0 else f"  answer: {answer}" + note + src)
        if clock.input("  Actually right? y = count it as correct (Enter = no) ").strip().lower() == "y":
            if kind == "short":
                add_answer(q, guess)
            return True, note + src, ""
        if kind == "multi":
            your = ", ".join(q["choices"][order[i]] for i in chosen)
        elif kind == "mc":
            your = q["choices"][order[letters.find(g) if g in letters else int(g) - 1]]
        else:
            your = guess
        note_mistake(q["q"], your, "; ".join(x.strip() for x in answer.splitlines() if x.strip()))
        return ok, "", ""
    return ok, note + src, f"  ✗  {answer}" + note + src


def add_answer(q, guess):
    """Accept `guess` as another answer to a short-answer question: appended to its first `A:` line
    in the bank file (`; guess`, which `meanings` splits on) and to the loaded question."""
    lines = q["file"].read_text(encoding="utf-8").split("\n")
    first = q["q"].splitlines()[0].strip()
    for i, line in enumerate(lines):
        m = FIELD.match(line.rstrip())
        if not (m and m[1] == "Q" and m[2].strip() == first):
            continue
        for j in range(i + 1, len(lines)):
            n = FIELD.match(lines[j].rstrip())
            if n and n[1] == "Q":
                break
            if n and n[1] == "A":
                lines[j] = f"{lines[j].rstrip()}; {guess}"
                q["a"] += f"; {guess}"
                q["file"].write_text("\n".join(lines), encoding="utf-8")
                print(f'  + added "{guess}" to this answer')
                return
        return


def questions_bank(subject):
    qs = load_questions(NOTES / subject / "questions.md")
    if not qs:
        sys.exit(f"No usable questions in {subject}/questions.md (format: notes/format/question-format.md)")
    items = [(q["q"], q) for q in qs]
    items = choose_group(items, lambda p: p[1]["topic"] or "no topic", "Topic", by_count=False)
    return items, ask_question, lambda item: short_label(item[1]), "question"


# ---- session ------------------------------------------------------------------------------


def choose_group(items, group_of, title, by_count=True):
    """Ask which group to practise (Enter = everything). Returns the filtered items."""
    counts = {}
    for it in items:
        counts[group_of(it)] = counts.get(group_of(it), 0) + 1
    if len(counts) < 2:
        return items
    groups = sorted(counts, key=lambda k: -counts[k]) if by_count else list(counts)
    print(f"\n{title} (Enter = everything):")
    for i, t in enumerate(groups, 1):
        print(f"  {i}) {t} ({counts[t]})")
    while True:
        r = input("> ").strip()
        if not r:
            return items
        if r.isdigit() and 1 <= int(r) <= len(groups):
            r = groups[int(r) - 1]
        match = next((g for g in groups if g.lower() == r.lower()), None)
        if match:
            return [it for it in items if group_of(it) == match]
        print("  Pick a number or name from the list, or press Enter.")


def main():
    subjects = sorted({p.parent.name for f in ("definitions.md", "questions.md") for p in NOTES.glob(f"*/{f}")})
    if not subjects:
        sys.exit(f"No definitions.md or questions.md files found under {NOTES}/<subject>/")
    if len(sys.argv) > 1 and sys.argv[1] in subjects:
        subject = sys.argv[1]
    else:
        print("Subject:")
        subject = subjects[choose("> ", subjects)]

    if (NOTES / subject / "questions.md").exists():
        items, ask, label, noun = questions_bank(subject)
    else:
        items, ask, label, noun = vocab_bank(subject)

    stats_path = NOTES / subject / ".quiz_stats.json"
    stats = load_stats(stats_path)
    if "--focused" in sys.argv[2:]:
        mode = "f"
    elif "--personalized" in sys.argv[2:]:
        mode = "p"
    else:
        print(f"\nMode: Enter = normal · f = focused (skips {noun}s you know well, least-asked first)"
              f" · p = personalized (what needs the most practice, from your answers)")
        mode = input("> ").strip().lower()[:1]
    focused, personal = mode == "f", mode == "p"
    if personal:
        print(f"\nPersonalized: {personal_summary(items, stats)}.")
    if focused:
        known = sum(known_well(stats, k) for k, _ in items)
        if known == len(items):
            sys.exit(f"You know all {len(items)} {noun}s well ({KNOWN_STREAK} right in a row): nothing to focus on.")
        print(f"\nFocused: {len(items) - known} {noun}s to go, {known} known well and skipped.")
    hard = sum(struggling(stats, k) for k, _ in items)
    pl, streak_msg = start_player(stats)
    lvl = level_of(pl["xp"])
    print(
        f"\n{len(items)} {noun}s ({hard} you're struggling with). Type 'q' to quit, '?' or Enter to skip and reveal."
    )
    print(f"{rank_of(lvl)} · Level {lvl} · {pl['xp']} XP [{progress_bar(pl['xp'])}] {streak_msg}\n")
    last = None
    asked_at = {}  # key -> question number it was last asked in (this session)
    right = total = combo = session_xp = 0
    missed = []
    clock = Clock()  # seconds spent on questions, idle time capped
    # atexit so Ctrl-C / EOF still log the time; reads the final values when it runs
    clock.log_every_minute(subject)
    atexit.register(lambda: clock.flush(subject, total, right))

    while True:
        why = ""
        if personal:
            item, why = pick_personal(items, stats, asked_at, total + 1, last)
        else:
            item = pick_focused(items, stats, last) if focused else pick(items, stats, asked_at, total + 1, last)
        if why:
            print(f"  · {why}")
        if item is None:
            print(f"Every {noun} here is now known well. Nothing left to focus on.")
            break
        key = item[0]
        last = key
        asked_at[key] = total + 1
        result = ask(item, clock)
        if result is None:
            break
        rt = clock.response_time()
        ok, ok_note, wrong_note = result
        credit = float(ok)
        log_answer(subject, qkind(item[1]) if noun == "question" else "vocab", rt, credit, conf=ask_confidence(clock, subject))
        total += 1
        was_hard = struggling(stats, key)
        record(stats, key, credit, rt=rt)
        if (m := take_mistake()):
            log_mistake(subject, m)
            stats[key]["reask"] = True
        notes = []
        if 0 < credit < 1:  # partial credit: XP in proportion, the combo neither grows nor breaks
            right += credit
            gain = max(1, round(10 * credit))
            old = level_of(pl["xp"])
            pl["xp"] += gain
            session_xp += gain
            clock.xp += gain
            clock.levels += level_of(pl["xp"]) - old
            print(f"  ◐ {credit:.0%} credit +{gain} XP" + ok_note)
            if wrong_note:
                print(wrong_note)
            if label(item) not in missed:
                missed.append(label(item))
            if level_of(pl["xp"]) > old:
                new = level_of(pl["xp"])
                notes.append(f"  ⬆ LEVEL UP! You are now a {rank_of(new)} (level {new})")
        elif credit >= 1:
            right += 1
            combo += 1
            mult = 1 + min(combo - 1, 9) // 3  # x1 → x4 as the combo grows
            gain = 10 * mult
            if was_hard and not struggling(stats, key):
                gain += 25
                notes.append(f"  ♻ Weak {noun} recovered! +25 XP")
                notes.append(award(pl, "recovered"))
            notes.append(award(pl, "first_blood"))
            for n in (5, 10, 20):
                if combo == n:
                    notes.append(award(pl, f"combo{n}"))
            if pl["days"] >= 7:
                notes.append(award(pl, "week"))
            old = level_of(pl["xp"])
            pl["xp"] += gain
            session_xp += gain
            clock.xp += gain
            clock.levels += level_of(pl["xp"]) - old
            pl["best_combo"] = max(pl["best_combo"], combo)
            combo_txt = f" 🔥x{combo}" if combo >= 3 else ""
            print(f"  ✓ +{gain} XP{combo_txt}" + ok_note)
            if level_of(pl["xp"]) > old:
                new = level_of(pl["xp"])
                notes.append(f"  ⬆ LEVEL UP! You are now a {rank_of(new)} (level {new})")
        else:
            if combo >= 5:
                print(f"  💔 Combo of {combo} broken")
            combo = 0
            if wrong_note:
                print(wrong_note)
            if label(item) not in missed:
                missed.append(label(item))
        for n in filter(None, notes):
            print(n)
        if noun == "word":
            e = stats[key]
            took = f"{rt:.1f}s" if rt is not None else f"idle (over {ANSWER_CAPS['vocab']}s, not timed)"
            print(f"  {word_score(stats, key):.0%} known ({e['right']} right, {e['wrong']} wrong) · {took}")
        print()
        save_stats(stats_path, stats)
        clock.flush(subject)  # XP and level-ups score right away, not at the next minute tick

    if total:
        print(f"\nScore: {right:.1f}/{total} ({round(100 * right / total)}%)".replace(".0/", "/"))
        if total >= 10 and right == total:
            print(award(pl, "perfect") or "  ✨ Another flawless run!")
            save_stats(stats_path, stats)
        lvl = level_of(pl["xp"])
        print(f"+{session_xp} XP this session · best combo {pl['best_combo']} · {fmt_time(clock.active)} studied")
        print(f"{rank_of(lvl)} · Level {lvl} [{progress_bar(pl['xp'])}] {pl['xp']}/{xp_for(lvl + 1)} XP")
        if pl["badges"]:
            print("Badges: " + ", ".join(BADGES[b].split(" (")[0] for b in pl["badges"]))
        sep = "; " if noun == "word" else "\n  "
        if missed:
            print("Missed:" + (" " if noun == "word" else "\n  ") + sep.join(missed))
        hard = [k if noun == "word" else label(it) for it in items for k in [it[0]] if struggling(stats, k)]
        solid = [k for k, _ in items if k in stats and not struggling(stats, k)]
        print(f"\nStruggling ({len(hard)}):" + (" " if noun == "word" else "\n  ") + (sep.join(hard) or "none"))
        print(f"Comfortable: {len(solid)}/{len(items)}")


# ---- scheduled revision (notesview revise) -------------------------------------------------


def notesview_bin():
    return shutil.which("notesview") or str(Path.home() / ".local/bin/notesview")


def recall(note_path, clock):
    """No questions yet: write what you remember, see the note, grade yourself. Returns a score 0-1 or None."""
    text = note_path.read_text(encoding="utf-8", errors="replace")
    heads = [l.lstrip("#").strip() for l in text.splitlines() if re.match(r"#{1,4} ", l)]
    print("No questions for this note yet, so: recall.\n")
    if heads:
        print("It covers:\n" + "\n".join(f"  · {h}" for h in heads[:12]))
    print("\nWrite down everything you remember (an empty line ends, q quits):")
    lines = []
    while True:
        l = clock.input("  ", ANSWER_CAPS["recall"])
        if l.strip().lower() == "q" and not lines:
            return None
        if not l.strip():
            break
        lines.append(l)
    print("\n" + "─" * 60)
    print(textwrap.indent(text.strip(), "  "))
    print("─" * 60)
    grades = {"1": 0.3, "a": 0.3, "2": 0.6, "h": 0.6, "3": 0.85, "g": 0.85, "4": 1.0, "e": 1.0}
    while True:
        r = clock.input("How well did you remember it? 1) again  2) hard  3) good  4) easy → ", READ_CAP).strip().lower()
        if r in grades:
            return grades[r]
        if r == "q":
            return None


MIX_TOPICS = 3  # a blended revision covers up to this many due topics of one folder
MIX_PER_TOPIC = 4  # questions drawn from each topic when blending
MIX_CROSS = 3  # cross-topic questions (.revision/questions/_mixed/<subject>.md) added to a blend


def sample_weighted(qs, stats, n):
    """n questions, those you miss or have not seen yet more likely (`weight`), no repeats."""
    keyed = sorted(qs, key=lambda q: random.random() ** (1 / weight(stats, q["q"])), reverse=True)
    return keyed[:n]


def cross_questions(subject, topics):
    """Cross-topic questions whose `Src:` names two or more of this session's topics; `rev_ids` are
    the ones in the session (each is scored toward all of them)."""
    path = NOTES / ".revision" / "questions" / "_mixed" / f"{subject}.md"
    if not path.exists():
        return []
    out = []
    for q in load_questions(path):
        ids = [i.strip() for i in re.sub(r"\[gen\]", "", q["src"]).split(",")]
        here = [i for i in ids if i in topics]
        if len(here) >= 2:
            q["rev_ids"], q["rev_id"] = here, here[0]
            out.append(q)
    return out


NEGATIVE = re.compile(r"\b(?:NOT|EXCEPT|LEAST)\b")
NONE_OF = re.compile(r"\b(?:all|none) of the above\b", re.I)


def load_revision_bank(path):
    """A revision bank, minus generated 'which is NOT…' / 'all of the above' questions (they test
    remembering the odd one out, not the idea; new banks never contain them) while enough others remain."""
    qs = load_questions(path)
    keep = [q for q in qs if not (q["choices"] and "[gen]" in q["q"] + q["src"]
                                  and (NEGATIVE.search(q["q"]) or any(NONE_OF.search(c) for c in q["choices"])))]
    return keep if len(keep) >= 3 else qs


def due_companions(note_id, subject):
    """Other topics due today from the same folder that have a question bank, oldest due first."""
    r = subprocess.run([notesview_bin(), "revise", "--dir", str(NOTES), "--json", "list"],
                       capture_output=True, text=True, env={**os.environ, "NOTES_DIR": str(NOTES)})
    try:
        topics = json.loads(r.stdout)
    except ValueError:
        return []
    today = date.today().isoformat()
    due = [t for t in topics if t["id"] != note_id and t.get("subject") == subject and t["due"] <= today
           and (NOTES / ".revision" / "questions" / t["id"]).exists()]
    due.sort(key=lambda t: (t["due"], t.get("learned", ""), t["id"]))
    return due[: MIX_TOPICS - 1]


def interleave(qs):
    """Shuffle, then swap so two questions from the same topic are not neighbours where possible."""
    random.shuffle(qs)
    for i in range(1, len(qs)):
        if qs[i]["rev_id"] == qs[i - 1]["rev_id"]:
            for j in range(i + 1, len(qs)):
                if qs[j]["rev_id"] != qs[i - 1]["rev_id"]:
                    qs[i], qs[j] = qs[j], qs[i]
                    break
    return qs


def revise(note_id, solo=False):
    """One revision of one scheduled note: its Claude-written questions (.revision/questions/<note>),
    or recall when there are none. When other topics of the same folder are due too, their questions
    are blended in (MIX_PER_TOPIC each, shuffled together, `--solo` turns this off); each topic is
    scored from its own questions and reported to `notesview revise done`, which schedules the next
    revision and scores the points. A topic with fewer than half of its questions answered records
    nothing (it stays due)."""
    note_path = NOTES / note_id
    if not note_path.exists():
        sys.exit(f"No such note: {note_path}")
    subject = note_id.split("/")[0]
    bank = NOTES / ".revision" / "questions" / note_id

    def title_of(nid):
        p = NOTES / nid
        return next((l.lstrip("#").strip() for l in p.read_text(encoding="utf-8", errors="replace").splitlines() if l.startswith("# ")), p.stem)

    title = title_of(note_id)
    stats_path = NOTES / subject / ".quiz_stats.json"
    stats = load_stats(stats_path)
    qs = load_revision_bank(bank) if bank.exists() else []
    for q in qs:
        q["rev_id"] = note_id
    topics = {note_id: title}
    if qs and not solo:
        banks = {}
        for t in due_companions(note_id, subject):
            more = load_revision_bank(NOTES / ".revision" / "questions" / t["id"])
            if more:
                topics[t["id"]] = t.get("title") or title_of(t["id"])
                for q in more:
                    q["rev_id"] = t["id"]
                banks[t["id"]] = more
        if banks:
            banks[note_id] = qs
            qs = [q for b in banks.values() for q in sample_weighted(b, stats, MIX_PER_TOPIC)]
            qs += random.sample(cross := cross_questions(subject, topics), min(MIX_CROSS, len(cross)))
    if len(topics) > 1:
        print("\nRevision (mixed): " + " · ".join(topics.values()))
    else:
        print(f"\nRevision: {title}  ({note_id})")
    clock = Clock()
    clock.log_every_minute(subject)
    atexit.register(lambda: clock.flush(subject))
    pl, _ = start_player(stats)
    results = {}  # topic id -> (score, correct, total), only for topics that count

    if not qs:
        score = recall(note_path, clock)
        if score is None:
            print("Not recorded: the note stays due.")
            return
        results[note_id] = (score, round(score * 3), 3)
    else:
        qs = interleave(qs)
        for q in qs:
            q.setdefault("rev_ids", [q["rev_id"]])
        planned = {nid: sum(nid in q["rev_ids"] for q in qs) for nid in topics}
        got = {nid: [0, 0] for nid in topics}  # correct, answered
        print(f"{len(qs)} questions. '?' or Enter reveals, 'q' quits.\n")
        combo = 0
        missed_qs, quit_early, done, right = [], False, 0, 0
        for i, q in enumerate(qs, 1):
            print(f"[{i}/{len(qs)}] ", end="")
            try:
                r = ask_question((q["q"], q), clock)
            except (KeyboardInterrupt, EOFError):  # like q: what was answered still counts
                print()
                r = None
            if r is None:
                quit_early = True
                break
            ok, ok_note, wrong_note = r
            credit = float(ok)
            rt = clock.response_time()
            log_answer(subject, qkind(q), rt, credit, revision=q["rev_id"], conf=ask_confidence(clock, subject))
            for nid in q["rev_ids"]:
                got[nid][1] += 1
                got[nid][0] += credit
            record(stats, q["q"], credit, notes=q["rev_ids"], rt=rt)
            if (m := take_mistake()):
                log_mistake(subject, m, q["rev_ids"])
                stats[q["q"]]["reask"] = True
            done += 1
            right += credit
            if credit < 1:
                missed_qs.append(q)
            if credit > 0:
                if credit >= 1:
                    combo += 1
                    gain = 10 * (1 + min(combo - 1, 9) // 3)
                else:  # partial credit: XP in proportion, the combo neither grows nor breaks
                    gain = max(1, round(10 * credit))
                old = level_of(pl["xp"])
                pl["xp"] += gain
                clock.xp += gain
                clock.levels += level_of(pl["xp"]) - old
                print((f"  ✓ +{gain} XP" if credit >= 1 else f"  ◐ {credit:.0%} credit +{gain} XP") + ok_note)
            else:
                combo = 0
                print(wrong_note)
            print()
            save_stats(stats_path, stats)
            clock.flush(subject)
        if missed_qs and not quit_early:  # the ones you missed, once more (not scored, no XP)
            print(f"Once more, the {len(missed_qs)} you missed (not scored):\n")
            for q in missed_qs:
                try:
                    r = ask_question((q["q"], q), clock)
                except (KeyboardInterrupt, EOFError):
                    print()
                    break
                if r is None:
                    break
                record(stats, q["q"], r[0], notes=q["rev_ids"])
                if (m := take_mistake()):
                    log_mistake(subject, m, q["rev_ids"])
                    stats[q["q"]]["reask"] = True
                print(("  ✓" + r[1]) if r[0] >= 1 else r[2])
                print()
            save_stats(stats_path, stats)
        for nid, (c, n) in got.items():
            if n * 2 >= planned[nid] and n:
                results[nid] = (c / n, c, n)
            else:
                print(f"Stopped early on {topics[nid]}: not recorded, it stays due.")
        if not results:
            return
        print(f"\nScore: {right:.1f}/{done} ({round(100 * right / done)}%)".replace(".0/", "/"))
        if len(topics) > 1:
            for nid, (sc, c, n) in results.items():
                print(f"  {topics[nid]}: {c:.1f}/{n}".replace(".0/", "/"))
    for nid, (sc, c, n) in results.items():
        r = subprocess.run([notesview_bin(), "revise", "done", nid, "--score", f"{sc:.3f}", "--correct", str(round(c)), "--total", str(n)],
                           capture_output=True, text=True, env={**os.environ, "NOTES_DIR": str(NOTES)})
        print((r.stdout or r.stderr).strip())


if __name__ == "__main__":
    try:
        if len(sys.argv) > 2 and sys.argv[1] == "--revise":
            revise(sys.argv[2], solo="--solo" in sys.argv[3:])
            try:  # the terminal is handed back to nvim as soon as we exit: keep the result readable
                input("\nPress Enter to close ")
            except (KeyboardInterrupt, EOFError):
                pass
            sys.exit(0)
        main()
    except (KeyboardInterrupt, EOFError):
        print()
