#!/usr/bin/env python3
"""Vocab quiz. Reads `word :: translation` lines from notes/<subject>/definitions.md.

Progress is saved per subject in notes/<subject>/.quiz_stats.json. Each question is
a weighted random draw: new words and words you keep missing come up most, words you
have answered correctly many times in a row come up rarely. After a correct answer the
other accepted meanings are shown. The quiz runs until you type 'q'.

Gamified: earn XP with combo multipliers, level up through Roman ranks, keep a daily
streak, and unlock badges. Player state lives in the same stats file.

Time spent is logged per session to notes/.quiz_log.jsonl and shows up in the viewer's
Activity view. Time you spend away (over IDLE_CAP seconds on one question) isn't counted.

At the start you can practise everything (just press Enter) or only one word type,
taken from the "(noun)", "(verb)"... at the end of each definitions.md line.

Usage: python3 ~/notes/quiz.py [subject]   (a link to nvim-notes/quiz/quiz.py)
"""

import atexit
import json
import math
import os
import random
import re
import sys
import time
import unicodedata
from datetime import date, datetime
from pathlib import Path

# The script is symlinked into the notes folder from the nvim-notes repo, so find the notes
# through NOTES_DIR (like notesview and Neovim do), not through the script's own path.
NOTES = Path(os.environ.get("NOTES_DIR") or Path.home() / "notes").expanduser()
LOG = NOTES / ".quiz_log.jsonl"
IDLE_CAP = 90  # seconds; a longer pause on one question counts as this much


def log_session(subject, seconds, answered, correct):
    """Append one line per session; the viewer's Activity view sums them per day."""
    if seconds < 1:
        return
    entry = {
        "date": date.today().isoformat(),
        "at": datetime.now().isoformat(timespec="seconds"),
        "subject": subject,
        "seconds": round(seconds),
        "answered": answered,
        "correct": correct,
    }
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
    counts = {}
    for _, t in pairs:
        counts[kind(t) or "untyped"] = counts.get(kind(t) or "untyped", 0) + 1
    if len(counts) < 2:
        return pairs
    types = sorted(counts, key=lambda k: -counts[k])
    print("\nWord type (Enter = everything):")
    for i, t in enumerate(types, 1):
        print(f"  {i}) {t} ({counts[t]})")
    while True:
        r = input("> ").strip().lower()
        if not r:
            return pairs
        if r.isdigit() and 1 <= int(r) <= len(types):
            r = types[int(r) - 1]
        if r in counts:
            return [p for p in pairs if (kind(p[1]) or "untyped") == r]
        print("  Pick a number or type name from the list, or press Enter.")


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


def main():
    subjects = sorted(p.parent.name for p in NOTES.glob("*/definitions.md"))
    if not subjects:
        sys.exit(f"No definitions.md files found under {NOTES}/<subject>/")
    if len(sys.argv) > 1 and sys.argv[1] in subjects:
        subject = sys.argv[1]
    else:
        print("Subject:")
        subject = subjects[choose("> ", subjects)]

    pairs = load(NOTES / subject / "definitions.md")
    if not pairs:
        sys.exit(f"No 'word :: translation' lines in {subject}/definitions.md")

    pairs = choose_type(pairs)

    print("\nDirection:")
    d = choose("> ", ["Latin → English", "English → Latin", "Mixed"])

    stats_path = NOTES / subject / ".quiz_stats.json"
    stats = load_stats(stats_path)
    hard = sum(struggling(stats, w) for w, _ in pairs)
    pl, streak_msg = start_player(stats)
    lvl = level_of(pl["xp"])
    print(
        f"\n{len(pairs)} entries ({hard} you're struggling with). Type 'q' to quit, '?' or Enter to skip and reveal."
    )
    print(f"{rank_of(lvl)} · Level {lvl} · {pl['xp']} XP [{progress_bar(pl['xp'])}] {streak_msg}\n")
    last = None
    asked_at = {}  # word -> question number it was last asked in (this session)
    right = total = combo = session_xp = 0
    missed = []
    active = 0.0  # seconds spent on questions, idle time capped
    # atexit so Ctrl-C / EOF still log the time; reads the final values when it runs
    atexit.register(lambda: log_session(subject, active, total, right))

    while True:
        word, trans = pick(pairs, stats, asked_at, total + 1, last)
        last = word
        asked_at[word] = total + 1
        forward = d == 0 or (d == 2 and random.random() < 0.5)
        q, a = (word, trans) if forward else (trans, word)
        asked = time.monotonic()
        try:
            guess = input(f"{q}  → ").strip() or "?"  # a blank Enter skips and reveals, like '?'
        finally:
            active += min(time.monotonic() - asked, IDLE_CAP)
        if guess.lower() == "q":
            break
        total += 1
        ok = guess != "?" and check(guess, a)
        was_hard = struggling(stats, word)
        record(stats, word, ok)
        notes = []
        if ok:
            right += 1
            combo += 1
            mult = 1 + min(combo - 1, 9) // 3  # x1 → x4 as the combo grows
            gain = 10 * mult
            if was_hard and not struggling(stats, word):
                gain += 25
                notes.append("  ♻ Weak word recovered! +25 XP")
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
            pl["best_combo"] = max(pl["best_combo"], combo)
            more = others(guess, a)
            combo_txt = f" 🔥x{combo}" if combo >= 3 else ""
            print(f"  ✓ +{gain} XP{combo_txt}" + (f"  also: {', '.join(more)}" if more else ""))
            if level_of(pl["xp"]) > old:
                new = level_of(pl["xp"])
                notes.append(f"  ⬆ LEVEL UP! You are now a {rank_of(new)} (level {new})")
        else:
            if combo >= 5:
                print(f"  💔 Combo of {combo} broken")
            combo = 0
            print(f"  ✗  {a}")
            if (word, trans) not in missed:
                missed.append((word, trans))
        for n in filter(None, notes):
            print(n)
        print()
        save_stats(stats_path, stats)

    if total:
        print(f"\nScore: {right}/{total} ({100 * right // total}%)")
        if total >= 10 and right == total:
            print(award(pl, "perfect") or "  ✨ Another flawless run!")
            save_stats(stats_path, stats)
        lvl = level_of(pl["xp"])
        print(f"+{session_xp} XP this session · best combo {pl['best_combo']} · {fmt_time(active)} studied")
        print(f"{rank_of(lvl)} · Level {lvl} [{progress_bar(pl['xp'])}] {pl['xp']}/{xp_for(lvl + 1)} XP")
        if pl["badges"]:
            print("Badges: " + ", ".join(BADGES[b].split(" (")[0] for b in pl["badges"]))
        if missed:
            print("Missed: " + "; ".join(f"{w} :: {t}" for w, t in missed))
        hard = [w for w, _ in pairs if struggling(stats, w)]
        solid = [w for w, _ in pairs if w in stats and not struggling(stats, w)]
        print(f"\nStruggling ({len(hard)}): " + ("; ".join(hard) or "none"))
        print(f"Comfortable: {len(solid)}/{len(pairs)}")


if __name__ == "__main__":
    try:
        main()
    except (KeyboardInterrupt, EOFError):
        print()
