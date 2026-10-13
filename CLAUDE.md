# nvim-notes

My notes system: a Neovim config plus **notesview** (a Go server/CLI/viewer) and a terminal quiz. How it all fits together is in `~/notes/architecture/architecture.md`; read it first for anything about behavior, keys, data files or timers, and keep it accurate (see the global CLAUDE.md).

## Layout
- `renderer/` — the Go code (`package main`, binary `notesview`): tasks, score, quiz engine, calendar, revision, settings, HTTP API. Web UI is in `renderer/web/`.
- `nvim/` — `init.lua` and `lua/` (start page `start.lua`, `signals.lua`, `themesync.lua`, …). Linked to `~/.config/nvim`.
- `quiz/` — `quiz.py` and `ingest.py` (linked from `~/notes`). Edit them here, not the links.
- `macapp/` — the native viewer app (Swift).
- `notesview/custom.css`, `install.sh`, `README.md`.
- Shell/launcher pieces live in `~/dotfiles` (`zsh/zshrc`, `local-bin/ghostty-nvim`); `install.sh` carries a copy of the `notes` function.

## Working here
- Run Go tests from `renderer/`: `go test ./...` (Go is in `/opt/homebrew/bin`). Add or update tests next to the change.
- Changing score values or point rules breaks tests and trained curves: update the tests and re-check the score docs.
- `./install.sh` rebuilds and installs the `notesview` binary; a code change is not live until then.
- Data lives in `~/notes` (git-backed, committed automatically every 10 minutes); don't hand-edit hidden data folders (`.revision`, `.quiz_*`, `.words.json`) unless asked.
- Anything adding a user-visible key, command or setting also goes in `README.md`.
- Commit to `main`; this repo is also on GitHub (cloud sessions can edit it), so pull first.
