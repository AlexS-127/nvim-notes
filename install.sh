#!/usr/bin/env bash
# One-command setup for the notes environment (macOS or Linux).
#   ./install.sh              install / update (safe to re-run)
#   ./install.sh --uninstall  remove everything it installed, restore your old config
set -euo pipefail

REPO_SLUG="${NOTESVIEW_REPO:-alexs-127/nvim-notes}"
REPO_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
BIN_DIR="$HOME/.local/bin"
CONFIG_LINK="${XDG_CONFIG_HOME:-$HOME/.config}/nvim"
DATA_DIR="${XDG_DATA_HOME:-$HOME/.local/share}/nvim"
NOTES_HOME="${NOTES_DIR:-$HOME/notes}"
NV_CONFIG_DIR="${XDG_CONFIG_HOME:-$HOME/.config}/notesview"
MARK_BEGIN="# >>> nvim-notes >>>"
MARK_END="# <<< nvim-notes <<<"
MIN_NVIM_MINOR=12
# the notesview version the Neovim config needs (NOTESVIEW_MIN_VERSION in nvim/init.lua)
MIN_NOTESVIEW="$(sed -nE 's/^local NOTESVIEW_MIN_VERSION = "([0-9.]+)".*/\1/p' "$REPO_DIR/nvim/init.lua")"

OS="$(uname -s)"; ARCH="$(uname -m)"
case "$OS" in Darwin) GOOS=darwin ;; Linux) GOOS=linux ;; *) echo "Unsupported OS: $OS" >&2; exit 1 ;; esac
case "$ARCH" in x86_64|amd64) GOARCH=amd64 ;; arm64|aarch64) GOARCH=arm64 ;; *) echo "Unsupported arch: $ARCH" >&2; exit 1 ;; esac

say()  { printf '\033[1;34m==>\033[0m %s\n' "$*"; }
warn() { printf '\033[1;33mwarning:\033[0m %s\n' "$*" >&2; }
have() { command -v "$1" >/dev/null 2>&1; }

rc_file() {
  case "${SHELL:-}" in */zsh) echo "$HOME/.zshrc" ;; */bash) echo "$HOME/.bashrc" ;;
    *) if [ "$OS" = Darwin ]; then echo "$HOME/.zshrc"; else echo "$HOME/.bashrc"; fi ;;
  esac
}

remove_block() { # remove our marked block from a file: remove_block FILE [BEGIN END]
  local f="$1" b="${2:-$MARK_BEGIN}" e="${3:-$MARK_END}"
  [ -f "$f" ] && grep -qF -- "$b" "$f" || return 0
  local tmp; tmp="$(mktemp)"
  awk -v b="$b" -v e="$e" '$0==b{skip=1} !skip{print} $0==e{skip=0}' "$f" > "$tmp"
  cat "$tmp" > "$f"; rm -f "$tmp"
}

# remove_old_inbox FILE: drop an older inbox function or alias defined outside our block
remove_old_inbox() {
  local f="$1" tmp
  [ -f "$f" ] || return 0
  tmp="$(mktemp)"
  awk '
    skip { if ($0 ~ /^[[:space:]]*}[[:space:]]*$/) skip = 0; next }
    /^[[:space:]]*(function[[:space:]]+)?inbox[[:space:]]*(\(\))?[[:space:]]*\{/ { if ($0 !~ /}[[:space:]]*$/) skip = 1; next }
    /^[[:space:]]*alias[[:space:]]+inbox=/ { next }
    { print }' "$f" > "$tmp"
  if ! cmp -s "$f" "$tmp"; then
    cp "$f" "$f.bak-nvim-notes-$(date +%Y%m%d-%H%M%S)"
    say "Replacing an older inbox function in $f (backup saved next to it)"
    cat "$tmp" > "$f"
  fi
  rm -f "$tmp"
}

# link_file SRC DEST: symlink DEST to SRC, backing up anything else already there
link_file() {
  local src="$1" dest="$2"
  mkdir -p "$(dirname "$dest")"
  if [ -L "$dest" ] && [ "$(readlink "$dest")" = "$src" ]; then return 0; fi
  if [ -e "$dest" ] || [ -L "$dest" ]; then
    local backup; backup="$dest.bak-$(date +%Y%m%d-%H%M%S)"
    say "Backing up $dest to $backup"
    mv "$dest" "$backup"
  fi
  ln -s "$src" "$dest"
}

# unlink_file SRC DEST: remove DEST if it links to SRC, restore the latest backup
unlink_file() {
  local src="$1" dest="$2" latest
  if [ -L "$dest" ] && [ "$(readlink "$dest")" = "$src" ]; then rm -f "$dest"; fi
  latest="$(ls -d "$dest".bak-* 2>/dev/null | sort | tail -n 1 || true)"
  if [ -n "$latest" ] && [ ! -e "$dest" ]; then say "Restoring $latest"; mv "$latest" "$dest"; fi
}

# version_ge A B: true if version A >= B (dotted numbers)
version_ge() {
  awk -v a="$1" -v b="$2" 'BEGIN { sub(/^v/, "", a); sub(/^v/, "", b); split(a, x, "."); split(b, y, ".")
    for (i = 1; i <= 3; i++) { if (x[i] + 0 > y[i] + 0) exit 0; if (x[i] + 0 < y[i] + 0) exit 1 } exit 0 }'
}
nv_version() { "$1" --version 2>/dev/null | awk 'NR == 1 { print $2 }'; }

# ── uninstall ────────────────────────────────────────────────────
if [ "${1:-}" = "--uninstall" ]; then
  say "Removing config symlink"
  if [ -L "$CONFIG_LINK" ] && [ "$(readlink "$CONFIG_LINK")" = "$REPO_DIR/nvim" ]; then rm -f "$CONFIG_LINK"; fi
  say "Removing notesview and helper symlinks"
  rm -f "$BIN_DIR/notesview"
  [ -L "$BIN_DIR/fd" ] && case "$(readlink "$BIN_DIR/fd")" in *fdfind) rm -f "$BIN_DIR/fd" ;; esac
  if [ -L "$BIN_DIR/nvim" ]; then
    case "$(readlink "$BIN_DIR/nvim")" in "$HOME"/.local/nvim-*) rm -f "$BIN_DIR/nvim"; rm -rf "$HOME"/.local/nvim-* ;; esac
  fi
  say "Removing the custom.css link"
  unlink_file "$REPO_DIR/notesview/custom.css" "$NV_CONFIG_DIR/custom.css"
  unlink_file "$REPO_DIR/quiz/quiz.py" "$NOTES_HOME/quiz.py"
  say "Removing shell alias and inbox command"
  for f in "$HOME/.zshrc" "$HOME/.bashrc" "$HOME/.bash_profile"; do remove_block "$f"; done
  say "Removing Neovim plugin data"
  rm -rf "$DATA_DIR/site"
  latest="$(ls -d "$CONFIG_LINK".bak-* 2>/dev/null | sort | tail -n 1 || true)"
  if [ -n "$latest" ] && [ ! -e "$CONFIG_LINK" ]; then
    say "Restoring $latest"; mv "$latest" "$CONFIG_LINK"
  fi
  say "Done. Your notes in $NOTES_HOME were left untouched; system packages were not removed."
  exit 0
fi

# ── packages ─────────────────────────────────────────────────────
nvim_ok() { # Neovim >= 0.$MIN_NVIM_MINOR
  have nvim || return 1
  local v major minor
  v="$(nvim --version | head -1 | sed -E 's/^NVIM v([0-9]+)\.([0-9]+).*/\1 \2/')"
  major="${v% *}"; minor="${v#* }"
  [ "$major" -gt 0 ] 2>/dev/null || [ "$minor" -ge "$MIN_NVIM_MINOR" ] 2>/dev/null
}

SUDO=""; if [ "$(id -u)" -ne 0 ] && have sudo; then SUDO="sudo"; fi
mkdir -p "$BIN_DIR"
export PATH="$BIN_DIR:$PATH"

install_nvim_tarball() {
  local asset="nvim-linux-x86_64"; [ "$GOARCH" = arm64 ] && asset="nvim-linux-arm64"
  say "Installing official Neovim release ($asset) into ~/.local"
  local tmp; tmp="$(mktemp -d)"
  curl -fsSL "https://github.com/neovim/neovim/releases/latest/download/$asset.tar.gz" -o "$tmp/nvim.tgz"
  rm -rf "$HOME/.local/nvim-$GOARCH"; mkdir -p "$HOME/.local/nvim-$GOARCH"
  tar -xzf "$tmp/nvim.tgz" -C "$HOME/.local/nvim-$GOARCH" --strip-components=1
  ln -sf "$HOME/.local/nvim-$GOARCH/bin/nvim" "$BIN_DIR/nvim"
  rm -rf "$tmp"
}

install_tree_sitter_cli() {
  have tree-sitter && return 0
  local asset="tree-sitter-linux-x64"; [ "$GOARCH" = arm64 ] && asset="tree-sitter-linux-arm64"
  say "Installing tree-sitter CLI"
  local tmp; tmp="$(mktemp -d)"
  if curl -fsSL "https://github.com/tree-sitter/tree-sitter/releases/latest/download/$asset.gz" -o "$tmp/ts.gz"; then
    gunzip -c "$tmp/ts.gz" > "$BIN_DIR/tree-sitter"; chmod +x "$BIN_DIR/tree-sitter"
  else
    warn "could not download tree-sitter-cli; code-block highlighting needs it (npm i -g tree-sitter-cli)"
  fi
  rm -rf "$tmp"
}

if [ "$OS" = Darwin ]; then
  have brew || { echo "Homebrew is required on macOS: https://brew.sh" >&2; exit 1; }
  say "Installing packages with Homebrew"
  brew install neovim ripgrep fd fzf git tree-sitter-cli pngpaste
  nvim_ok || { echo "Neovim 0.$MIN_NVIM_MINOR+ is required (got $(nvim --version | head -1)); run: brew upgrade neovim" >&2; exit 1; }
else
  if have apt-get; then
    say "Installing packages with apt"
    $SUDO apt-get update -y
    $SUDO apt-get install -y git curl ca-certificates tar gzip ripgrep fd-find fzf gcc make
    if have fdfind && ! have fd; then ln -sf "$(command -v fdfind)" "$BIN_DIR/fd"; fi
  else
    warn "apt not found: install git, ripgrep, fd, fzf, curl and a C compiler yourself"
  fi
  nvim_ok || install_nvim_tarball
  nvim_ok || { echo "Neovim 0.$MIN_NVIM_MINOR+ could not be installed" >&2; exit 1; }
  install_tree_sitter_cli
fi

# ── notesview ────────────────────────────────────────────────────
say "Installing notesview (needs $MIN_NOTESVIEW or newer)"
tmp="$(mktemp -d)"
url="https://github.com/$REPO_SLUG/releases/latest/download/notesview-$GOOS-$GOARCH"
nv_ok=0
if curl -fsSL "$url" -o "$tmp/notesview" 2>/dev/null && [ -s "$tmp/notesview" ]; then
  chmod +x "$tmp/notesview"
  v="$(nv_version "$tmp/notesview")"
  if [ -n "$v" ] && version_ge "$v" "$MIN_NOTESVIEW"; then
    install -m 755 "$tmp/notesview" "$BIN_DIR/notesview"; nv_ok=1
  else
    warn "the latest notesview release (${v:-unknown version}) is older than $MIN_NOTESVIEW"
  fi
fi
if [ "$nv_ok" = 0 ]; then
  if have go && [ -d "$REPO_DIR/renderer" ]; then
    say "Building notesview from source"
    (cd "$REPO_DIR/renderer" && go build -o "$BIN_DIR/notesview" .) && nv_ok=1
    # a stable signature keeps Full Disk Access (Screen Time, Messages importers) across rebuilds
    codesign --force --sign "NotesView Signing" --identifier local.notesview "$BIN_DIR/notesview" >/dev/null 2>&1 || true
  elif [ -s "$tmp/notesview" ]; then
    install -m 755 "$tmp/notesview" "$BIN_DIR/notesview"
    warn "installed the older release; tasks, folders and capture need notesview $MIN_NOTESVIEW — install Go and re-run ./install.sh"
  else
    warn "could not download notesview and Go is not installed; the viewer will be unavailable"
  fi
fi
rm -rf "$tmp"

# ── config, notes folder, alias ──────────────────────────────────
say "Linking Neovim config"
mkdir -p "$(dirname "$CONFIG_LINK")"
if [ -L "$CONFIG_LINK" ] && [ "$(readlink "$CONFIG_LINK")" = "$REPO_DIR/nvim" ]; then
  :
else
  if [ -e "$CONFIG_LINK" ] || [ -L "$CONFIG_LINK" ]; then
    backup="$CONFIG_LINK.bak-$(date +%Y%m%d-%H%M%S)"
    say "Backing up existing config to $backup"
    mv "$CONFIG_LINK" "$backup"
  fi
  ln -s "$REPO_DIR/nvim" "$CONFIG_LINK"
fi

say "Linking custom.css for the viewer"
link_file "$REPO_DIR/notesview/custom.css" "$NV_CONFIG_DIR/custom.css"
# an earlier version linked a config.toml for classes; it is no longer used
if [ -L "$NV_CONFIG_DIR/config.toml" ] && [ "$(readlink "$NV_CONFIG_DIR/config.toml")" = "$REPO_DIR/notesview/config.toml" ]; then
  rm -f "$NV_CONFIG_DIR/config.toml"
fi

mkdir -p "$NOTES_HOME"
say "Linking the vocab quiz (quiz.py) into $NOTES_HOME"
link_file "$REPO_DIR/quiz/quiz.py" "$NOTES_HOME/quiz.py"

RC="$(rc_file)"
touch "$RC"
remove_block "$RC"          # re-written every run so updates land
remove_old_inbox "$RC"
say "Adding PATH, the 'notes' alias and the 'inbox' command to $RC"
# keep a single blank line before our block
while [ -s "$RC" ] && [ -z "$(tail -n 1 "$RC")" ]; do
  tmp="$(mktemp)"; sed '$d' "$RC" > "$tmp"; cat "$tmp" > "$RC"; rm -f "$tmp"
done
{
  [ -s "$RC" ] && echo ""
  echo "$MARK_BEGIN"
  cat <<'SHELL'
case ":$PATH:" in *":$HOME/.local/bin:"*) ;; *) export PATH="$HOME/.local/bin:$PATH" ;; esac
# notes: open today's note. If nvim quits to run claude (start page c, task picker Shift+Enter),
# claude runs here in ~ with the prompt nvim left in $NVIM_NOTES_CLAUDE.
unalias notes 2>/dev/null
notes() {
  cd "${NOTES_DIR:-$HOME/notes}" || return
  local h="${TMPDIR:-/tmp}/nvim-notes-claude.$$" p
  rm -f "$h"
  NVIM_NOTES_CLAUDE="$h" nvim +Today "$@"
  [ -e "$h" ] || return 0
  p="$(<"$h")"; rm -f "$h"
  cd ~ && if [ -n "$p" ]; then claude "$p"; else claude; fi
}
# inbox [text]: capture a task to inbox.md — asks for the text (unless given), a folder (fzf) and a due date
inbox() { notesview capture -i -- "$@"; }
SHELL
  echo "$MARK_END"
} >> "$RC"

if have notesview; then
  say "Checking the installation (notesview doctor)"
  notesview doctor || warn "see the fixes above (a new shell may be needed for PATH changes)"
fi

say "Done. Open a new shell, then run: notes"
