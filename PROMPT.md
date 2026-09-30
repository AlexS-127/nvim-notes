This repo will hold my portable note-taking setup: a Neovim config plus a
bespoke markdown renderer I can install on any Mac or Linux machine with one
command. init.lua in the repo root is my current, working Neovim config.
Build on it; don't reinvent it.

## Repo layout
- nvim/init.lua        (moved from the root and adapted as described below)
- renderer/            (the bespoke renderer, described below)
- install.sh           (one-command setup)
- README.md            (install steps plus a full shortcut cheat sheet)
- .github/workflows/   (CI + release builds)

## Neovim config changes
- Keep everything that exists: vim.pack (no lazy.nvim), Borland-friendly colors
  (termguicolors = false, default colorscheme), note shortcuts under <Space>n,
  link following, checkbox/heading/archive helpers, bullets.vim, table mode,
  img-clip, snacks picker, treesitter code-block highlighting, :SaveMacro
  persistent macros, debounced autosave.
- Remove Obsidian integration and live-preview.nvim entirely.
- Make the notes folder configurable: $NOTES_DIR, defaulting to ~/notes.
- Integrate the renderer (see "Neovim integration" below).

## The renderer ("notesview")
A single self-contained Go binary with all HTML/CSS/JS embedded, no CDN or
network use at runtime, and no runtime dependencies.

Server:
- `notesview serve --dir <notes> --port 7777` binds to 127.0.0.1 only.
- Watches the notes folder (fsnotify) and pushes changes to the page with
  server-sent events. The page re-renders without a full reload and keeps
  its scroll position.
- Refuses any path outside the notes folder (test this).
- `notesview open [path] [--line N]` starts the server in the background if
  needed, tells it to show that note, and opens the viewer window if none is
  open.
- The viewer window: if Chrome, Chromium, Brave, or Edge is installed, launch
  it with --app=<url> so it looks like a standalone app with no tabs or URL
  bar. Otherwise, fall back to the default browser.

Rendering (goldmark):
- GitHub-flavored markdown: tables, task lists, strikethrough, autolinks,
  footnotes.
- [[wiki links]] including [[Note|alias]], resolved within the notes folder.
  Links to notes that don't exist yet are styled differently.
- Callouts (> [!NOTE], [!TIP], [!WARNING], etc.).
- Server-side syntax highlighting of code blocks with chroma.
- Images relative to the note (for example assets/).
- Every block element gets a data-line attribute with its source line, so
  the view can scroll to match the cursor in Neovim.

UI:
- Left sidebar: a folder/file tree of the notes (daily/ sorted newest first,
  inbox.md pinned at the top) and a search box that filters by title and
  full text.
- A "Tasks" view that gathers every open "- [ ]" across all notes, grouped by
  note, each linking to its source. It updates live.
- A backlinks section at the bottom of each note.
- Clicking a checkbox in the viewer toggles it in the file on disk. Write the
  file atomically, change only that one line, and make sure Neovim picks it
  up (the config uses autoread + checktime).
- Clicking a link navigates within the viewer. External links open in the
  system browser.
- Keyboard: j/k scroll, / focuses search, t opens Tasks, g goes to today's
  daily note.
- A clean, readable dark theme by default, a light theme toggle, and a user
  override file at ~/.config/notesview/custom.css that loads last.

## Neovim integration
- When a note inside the notes folder is opened, call `notesview open` in
  the background (vim.system, never blocking) so the viewer follows along.
- Send the cursor line on CursorHold so the viewer scroll follows my cursor
  (debounced).
- Keymaps: <Space>p shows the current note in the viewer; <Space>o turns
  follow mode on and off; <Space>nt opens the Tasks view.
- If the notesview binary isn't installed, show one warning and keep
  everything else working.

## install.sh
- Works on macOS (Homebrew) and Linux (apt; handle a missing or old
  Neovim by installing the official release tarball into ~/.local).
- Installs: neovim >= 0.12, ripgrep, fd, git, tree-sitter-cli, and pngpaste
  on macOS only.
- Installs notesview: download the right prebuilt binary for this OS/arch
  from the latest GitHub release. If that fails and Go is available, build
  from source. Put it in ~/.local/bin and make sure that's on PATH.
- Back up any existing ~/.config/nvim to a timestamped folder, then symlink
  nvim/ to ~/.config/nvim.
- Create the notes folder if it doesn't exist.
- Add a `notes` alias (cd to the notes folder and run nvim +Today) to
  ~/.zshrc or ~/.bashrc, only once, even if install.sh is run repeatedly.
- Safe to re-run. Supports --uninstall, which removes symlinks, the alias,
  the binary, and Neovim's plugin data, and restores the latest backup.

## CI and releases
- On every push: go vet, go test, and a headless Neovim check that loads
  the config, installs plugins, and opens a markdown file with no errors.
- On tags (v*): build notesview for darwin-arm64, darwin-amd64,
  linux-amd64, and linux-arm64 and attach the binaries to a GitHub release.

## Before you finish
- Write Go tests for wiki link resolution, task gathering, the checkbox
  toggle writing only one line, and rejecting paths outside the notes
  folder.
- Run the renderer in this environment, request a sample note, and confirm
  the HTML includes highlighted code, a task list, a resolved wiki link, and
  data-line attributes.
- Run install.sh in this environment (Linux) and confirm Neovim starts
  cleanly with the config.
- Keep the README's shortcut cheat sheet in sync with the actual keymaps.