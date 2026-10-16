#!/usr/bin/env bash
# Link each course's notes folder with its class-files folder, both ways.
#   ./link-courses.sh           create links (safe to re-run)
#   ./link-courses.sh --unlink  remove the links it made
#
# The pairs live in <notes>/.courses.json: {"oam331": "~/Desktop/Emory/OAM 331", ...}.
# If the file is missing it is seeded by matching note folders to class folders by name
# (lowercase, spaces dropped: "OAM 331" -> oam331). For each pair:
#   <notes>/<course>/.files -> class folder   (hidden: not counted, quizzed or scheduled)
#   <class folder>/Notes    -> <notes>/<course>
set -euo pipefail

NOTES_HOME="${NOTES_DIR:-$HOME/notes}"
CLASSES_HOME="${CLASSES_DIR:-$HOME/Desktop/Emory}"
MAP="$NOTES_HOME/.courses.json"

say()  { printf '\033[1;34m==>\033[0m %s\n' "$*"; }
warn() { printf '\033[1;33mwarning:\033[0m %s\n' "$*" >&2; }

if [ ! -f "$MAP" ]; then
  [ -d "$CLASSES_HOME" ] || exit 0   # no class folder on this machine: nothing to link
  say "Seeding $MAP from folder names"
  python3 -I - "$NOTES_HOME" "$CLASSES_HOME" "$MAP" <<'PY'
import json, sys
from pathlib import Path
notes, classes, out = map(Path, sys.argv[1:])
home = str(Path.home())
pairs = {}
for d in sorted(p for p in classes.iterdir() if p.is_dir()):
    key = d.name.lower().replace(" ", "")
    if (notes / key).is_dir():
        pairs[key] = str(d).replace(home, "~", 1)
out.write_text(json.dumps(pairs, indent=2) + "\n")
PY
fi

# course<TAB>class folder, one per line
pairs="$(python3 -I -c 'import json, os, sys
for k, v in json.load(open(sys.argv[1])).items(): print(f"{k}\t{os.path.expanduser(v)}")' "$MAP")"

# link DEST SRC: make DEST a symlink to SRC unless something else is already there
link() {
  if [ -L "$1" ]; then [ "$(readlink "$1")" = "$2" ] && return 0; rm -f "$1"
  elif [ -e "$1" ]; then warn "$1 exists and is not a link; left alone"; return 0; fi
  ln -s "$2" "$1"
}
unlink_if() { if [ -L "$1" ] && [ "$(readlink "$1")" = "$2" ]; then rm -f "$1"; fi; }

while IFS=$'\t' read -r course dir; do
  [ -n "$course" ] || continue
  if [ "${1:-}" = "--unlink" ]; then
    unlink_if "$NOTES_HOME/$course/.files" "$dir"
    unlink_if "$dir/Notes" "$NOTES_HOME/$course"
    continue
  fi
  if [ ! -d "$dir" ]; then warn "$course: $dir not found"; continue; fi
  mkdir -p "$NOTES_HOME/$course"
  link "$NOTES_HOME/$course/.files" "$dir"
  link "$dir/Notes" "$NOTES_HOME/$course"
done <<< "$pairs"
