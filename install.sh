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
MARK_BEGIN="# >>> nvim-notes >>>"
MARK_END="# <<< nvim-notes <<<"
MIN_NVIM_MINOR=12

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

remove_block() { # remove our marked block from a file
  local f="$1"
  [ -f "$f" ] && grep -qF "$MARK_BEGIN" "$f" || return 0
  local tmp; tmp="$(mktemp)"
  awk -v b="$MARK_BEGIN" -v e="$MARK_END" '$0==b{skip=1} !skip{print} $0==e{skip=0}' "$f" > "$tmp"
  cat "$tmp" > "$f"; rm -f "$tmp"
}

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
  say "Removing shell alias"
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
  brew install neovim ripgrep fd git tree-sitter-cli pngpaste
  nvim_ok || { echo "Neovim 0.$MIN_NVIM_MINOR+ is required (got $(nvim --version | head -1)); run: brew upgrade neovim" >&2; exit 1; }
else
  if have apt-get; then
    say "Installing packages with apt"
    $SUDO apt-get update -y
    $SUDO apt-get install -y git curl ca-certificates tar gzip ripgrep fd-find gcc make
    if have fdfind && ! have fd; then ln -sf "$(command -v fdfind)" "$BIN_DIR/fd"; fi
  else
    warn "apt not found: install git, ripgrep, fd, curl and a C compiler yourself"
  fi
  nvim_ok || install_nvim_tarball
  nvim_ok || { echo "Neovim 0.$MIN_NVIM_MINOR+ could not be installed" >&2; exit 1; }
  install_tree_sitter_cli
fi

# ── notesview ────────────────────────────────────────────────────
say "Installing notesview"
tmp="$(mktemp -d)"
url="https://github.com/$REPO_SLUG/releases/latest/download/notesview-$GOOS-$GOARCH"
if curl -fsSL "$url" -o "$tmp/notesview" 2>/dev/null && [ -s "$tmp/notesview" ]; then
  install -m 755 "$tmp/notesview" "$BIN_DIR/notesview"
elif have go && [ -d "$REPO_DIR/renderer" ]; then
  warn "no prebuilt binary available; building from source"
  (cd "$REPO_DIR/renderer" && go build -o "$BIN_DIR/notesview" .)
else
  warn "could not download notesview and Go is not installed; the viewer will be unavailable"
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

mkdir -p "$NOTES_HOME"

RC="$(rc_file)"
if [ -f "$RC" ] && grep -qF "$MARK_BEGIN" "$RC"; then
  say "Shell alias already present in $RC"
else
  say "Adding PATH and 'notes' alias to $RC"
  {
    echo ""
    echo "$MARK_BEGIN"
    echo 'case ":$PATH:" in *":$HOME/.local/bin:"*) ;; *) export PATH="$HOME/.local/bin:$PATH" ;; esac'
    echo "alias notes='cd \"\${NOTES_DIR:-\$HOME/notes}\" && nvim +Today'"
    echo "$MARK_END"
  } >> "$RC"
fi

say "Installing plugins and treesitter parsers (first run downloads them)"
nvim --headless "+lua local ok, ts = pcall(require, 'nvim-treesitter'); if ok then pcall(function() ts.install(vim.g.notes_parsers or {}):wait(300000) end) end" +qa >/dev/null 2>&1 \
  || warn "plugin/parser setup did not finish; it will complete the next time you start nvim"

say "Done. Open a new shell, then run: notes"
