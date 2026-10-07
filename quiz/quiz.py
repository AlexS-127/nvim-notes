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
Activity view. Time you spend away (over IDLE_CAP seconds on one question) isn't counted.

At the start you can practise everything (just press Enter) or only one word type,
taken from the "(noun)", "(verb)"... at the end of each definitions.md line, or, for a
question bank, one `## ` topic. Worked problems are idle-capped at PROBLEM_IDLE_CAP instead.

Usage: python3 ~/notes/quiz.py [subject]   (a link to nvim-notes/quiz/quiz.py)
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
IDLE_CAP = 90  # seconds; a longer pause on one question counts as this much


# ---- per-answer signals (renderer/signals.go reads them; counts only, never the answer text) ----


def sensor_on(name, default=True):
    try:
        c = json.loads((NOTES / ".signals" / "config.json").read_text())
        return bool(c.get("sensors", {}).get(name, default))
    except (OSError, ValueError):
        return default


def log_answer(subject, kind, latency, ok, revision="", conf=None):
    """One line per answer in .signals/YYYY-MM-DD.jsonl: how long it took, right or wrong, the
    question kind and (if asked) how sure you were. Answer latency and accuracy track focus and fatigue."""
    if not sensor_on("quiz"):
        return
    rec = {"at": datetime.now().strftime("%Y-%m-%dT%H:%M:%S"), "src": "quiz", "subject": subject, "kind": kind,
           "latency_ms": int(latency * 1000), "correct": bool(ok)}
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
    r = clock.input("  sure? 1 guess · 2 unsure · 3 sure (Enter skips) ").strip()
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


def record(stats, word, ok):
    e = stats.setdefault(word, {"right": 0, "wrong": 0, "streak": 0})
    e["right" if ok else "wrong"] += 1
    e["streak"] = e["streak"] + 1 if ok else 0
    e["last"] = date.today().isoformat()


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
    ws = [weight(stats, w) * cooldown(n - asked[w]) if w in asked else weight(stats, w) for w, _ in pool]
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

    def input(self, prompt, cap=IDLE_CAP):
        asked = time.monotonic()
        self.pending = (asked, cap)
        try:
            return input(prompt)
        finally:  # so quitting or Ctrl-C at a prompt still counts
            self.pending = None
            self.active += min(time.monotonic() - asked, cap)

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
        guess = clock.input(f"{q}  → ").strip() or "?"  # a blank Enter skips and reveals, like '?'
        if guess.lower() == "q":
            return None
        ok = guess != "?" and check(guess, a)
        if not ok and guess != "?":
            print(f"  ✗  {a}")
            if clock.input("  Actually right? y = count it as correct (Enter = no) ").strip().lower() == "y":
                if forward:
                    add_meaning(NOTES / subject / "definitions.md", pairs, word, guess)
                return True, "", ""
            return False, "", ""
        more = others(guess, a) if ok else []
        return ok, (f"  also: {', '.join(more)}" if more else ""), f"  ✗  {a}"

    return pairs, ask, lambda item: f"{item[0]} :: {item[1]}", "word"


# ---- question bank (questions.md) -------------------------------------------------------

PROBLEM_IDLE_CAP = 600  # seconds; a worked problem on paper really does take minutes
FIELD = re.compile(r"^(Q|A|Why|Src|Solution):[ \t]?(.*)$")
CHOICE = re.compile(r"^([a-hA-H])[).][ \t]+(.*)$")
REFERS_TO_LETTERS = re.compile(r"\babove\b|\b[A-H] and [A-H]\b", re.I)


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
        if cur["solution"] or (cur["a"] and (not cur["choices"] or cur["a"].lower() in letters)):
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
        return "mc"
    if norm(q["a"]) in ("true", "false"):
        return "tf"
    return "short"


def short_label(q, n=70):
    first = " ".join(q["q"].split())
    return first if len(first) <= n else first[: n - 1] + "…"


def ask_question(item, clock):
    _, q = item
    kind = qkind(q)
    print(q["q"])
    note = "".join(f"\n  {l.strip()}" for l in q["why"].splitlines())
    src = f"  ({q['src']})" if q["src"] else ""

    if kind == "problem":
        r = clock.input("  Work it out, then Enter to see the solution (q to quit) ", PROBLEM_IDLE_CAP)
        if r.strip().lower() == "q":
            return None
        print("\n".join(f"  {l}" for l in q["solution"].splitlines()) + note + ("\n" + src if src else ""))
        while True:
            r = clock.input("  Did you get it? [y/n] ").strip().lower()
            if r in ("y", "n"):
                return r == "y", "", ""
            if r == "q":
                return None

    if kind == "mc":
        order = list(range(len(q["choices"])))
        if not any(REFERS_TO_LETTERS.search(c) for c in q["choices"]):
            random.shuffle(order)
        letters = "abcdefgh"
        for i, j in enumerate(order):
            print(f"  {letters[i]}) {q['choices'][j]}")
        right = order.index(letters.index(q["a"].lower()))
        prompt, answer = "  → ", f"{letters[right]}) {q['choices'][order[right]]}"
        valid = set(letters[: len(order)]) | {str(i + 1) for i in range(len(order))}
    elif kind == "tf":
        prompt, answer, valid = "  [t/f] → ", q["a"].lower(), {"t", "f", "true", "false"}
    else:
        prompt, answer, valid = "  → ", q["a"], None

    while True:
        guess = clock.input(prompt).strip() or "?"
        g = guess.lower()
        if g == "q":
            return None
        if guess == "?" or valid is None or g in valid:
            break
        print("  Answer with " + ("a letter" if kind == "mc" else "t or f") + ", '?' to skip or 'q' to quit.")
    if guess == "?":
        ok = False
    elif kind == "mc":
        ok = g in (letters[right], str(right + 1))
    elif kind == "tf":
        ok = g in ("t", "true") if answer == "true" else g in ("f", "false")
    else:
        ok = check(guess, answer)
    if not ok and guess != "?":
        print(f"  ✗  {answer}" + note + src)
        if clock.input("  Actually right? y = count it as correct (Enter = no) ").strip().lower() == "y":
            if kind == "short":
                add_answer(q, guess)
            return True, note + src, ""
        return False, "", ""
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
        item = pick(items, stats, asked_at, total + 1, last)
        key = item[0]
        last = key
        asked_at[key] = total + 1
        t0 = time.monotonic()
        result = ask(item, clock)
        if result is None:
            break
        latency = time.monotonic() - t0
        ok, ok_note, wrong_note = result
        log_answer(subject, qkind(item[1]) if noun == "question" else "vocab", latency, ok, conf=ask_confidence(clock, subject))
        total += 1
        was_hard = struggling(stats, key)
        record(stats, key, ok)
        notes = []
        if ok:
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
        print()
        save_stats(stats_path, stats)
        clock.flush(subject)  # XP and level-ups score right away, not at the next minute tick

    if total:
        print(f"\nScore: {right}/{total} ({100 * right // total}%)")
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
        l = clock.input("  ", PROBLEM_IDLE_CAP)
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
        r = clock.input("How well did you remember it? 1) again  2) hard  3) good  4) easy → ").strip().lower()
        if r in grades:
            return grades[r]
        if r == "q":
            return None


MIX_TOPICS = 3  # a blended revision covers up to this many due topics of one folder
MIX_PER_TOPIC = 4  # questions drawn from each topic when blending


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
    qs = load_questions(bank) if bank.exists() else []
    for q in qs:
        q["rev_id"] = note_id
    topics = {note_id: title}
    if qs and not solo:
        banks = {}
        for t in due_companions(note_id, subject):
            more = load_questions(NOTES / ".revision" / "questions" / t["id"])
            if more:
                topics[t["id"]] = t.get("title") or title_of(t["id"])
                for q in more:
                    q["rev_id"] = t["id"]
                banks[t["id"]] = more
        if banks:
            banks[note_id] = qs
            qs = [q for b in banks.values() for q in random.sample(b, min(MIX_PER_TOPIC, len(b)))]
    if len(topics) > 1:
        print("\nRevision (mixed): " + " · ".join(topics.values()))
    else:
        print(f"\nRevision: {title}  ({note_id})")
    clock = Clock()
    clock.log_every_minute(subject)
    atexit.register(lambda: clock.flush(subject))
    stats_path = NOTES / subject / ".quiz_stats.json"
    stats = load_stats(stats_path)
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
        planned = {nid: sum(q["rev_id"] == nid for q in qs) for nid in topics}
        got = {nid: [0, 0] for nid in topics}  # correct, answered
        print(f"{len(qs)} questions. '?' or Enter reveals, 'q' quits.\n")
        combo = 0
        for i, q in enumerate(qs, 1):
            print(f"[{i}/{len(qs)}] ", end="")
            t0 = time.monotonic()
            try:
                r = ask_question((q["q"], q), clock)
            except (KeyboardInterrupt, EOFError):  # like q: what was answered still counts
                print()
                r = None
            if r is None:
                break
            ok, ok_note, wrong_note = r
            nid = q["rev_id"]
            log_answer(subject, qkind(q), time.monotonic() - t0, ok, revision=nid, conf=ask_confidence(clock, subject))
            got[nid][1] += 1
            record(stats, q["q"], ok)
            if ok:
                got[nid][0] += 1
                combo += 1
                gain = 10 * (1 + min(combo - 1, 9) // 3)
                old = level_of(pl["xp"])
                pl["xp"] += gain
                clock.xp += gain
                clock.levels += level_of(pl["xp"]) - old
                print(f"  ✓ +{gain} XP" + ok_note)
            else:
                combo = 0
                print(wrong_note)
            print()
            save_stats(stats_path, stats)
            clock.flush(subject)
        for nid, (c, n) in got.items():
            if n * 2 >= planned[nid] and n:
                results[nid] = (c / n, c, n)
            else:
                print(f"Stopped early on {topics[nid]}: not recorded, it stays due.")
        if not results:
            return
        done = sum(n for _, n in got.values())
        right = sum(c for c, _ in got.values())
        print(f"\nScore: {right}/{done} ({round(100 * right / done)}%)")
        if len(topics) > 1:
            for nid, (sc, c, n) in results.items():
                print(f"  {topics[nid]}: {c}/{n}")
    for nid, (sc, c, n) in results.items():
        r = subprocess.run([notesview_bin(), "revise", "done", nid, "--score", f"{sc:.3f}", "--correct", str(c), "--total", str(n)],
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
