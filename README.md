# nvim-notes

A portable note-taking setup: a Neovim config for markdown notes plus **notesview**, a
self-contained viewer that renders your notes live in their own window.

## Install

```sh
git clone https://github.com/alexs-127/nvim-notes.git
cd nvim-notes
./install.sh
```

Works on macOS (Homebrew) and Linux (apt). It installs Neovim ≥ 0.12 (the official release
tarball into `~/.local` if your distro's is missing or old), ripgrep, fd, git, tree-sitter-cli
(plus pngpaste on macOS) and the `notesview` binary (from the latest GitHub release, or built
from source if Go is available) into `~/.local/bin`. It symlinks `nvim/` to `~/.config/nvim`
(backing up any existing config to `~/.config/nvim.bak-<timestamp>`), creates your notes folder
and adds a `notes` alias (`cd` to the notes folder and run `nvim +Today`) to `~/.zshrc` or `~/.bashrc`.
Safe to re-run.

```sh
./install.sh --uninstall   # removes symlink, alias, binary, plugin data; restores the latest backup
```

The notes folder is `$NOTES_DIR`, default `~/notes`.

## notesview

```sh
notesview serve --dir ~/notes --port 7777   # binds to 127.0.0.1 only
notesview open [path] [--line N]            # start the server if needed, show a note, open the window
notesview open --tasks                      # show the Tasks view
```

One static Go binary; all HTML/CSS/JS is embedded and nothing touches the network at runtime.
The window opens as a tab-less app window if Chrome, Chromium, Brave or Edge is installed
(set `NOTESVIEW_BROWSER` to pick one), otherwise in your default browser.

- GitHub-flavored markdown, `[[wiki links]]` and `[[Note|alias]]` (missing notes are styled differently),
  callouts (`> [!NOTE]`, `[!TIP]`, `[!WARNING]`, …), highlighted code blocks, images relative to the note.
- Live updates over server-sent events; scroll position is kept. Neovim's cursor line drives the scroll.
- Sidebar tree (`inbox.md` pinned, `daily/` newest first) with title + full-text search,
  a **Tasks** view of every open `- [ ]`, and backlinks under each note.
- Clicking a checkbox toggles it in the file (atomic write, only that line changes).
- Dark theme by default, light toggle (◐), and `~/.config/notesview/custom.css` loads last.

Viewer keys: `j`/`k` scroll · `/` search · `t` tasks · `g` today's daily note.

## Shortcut cheat sheet

Leader is `<Space>`. Press `<Space>?` inside Neovim to search all shortcuts.

### Notes (anywhere)

| Keys | Action |
| --- | --- |
| `<Space>nn` | New note (asks for a title) |
| `<Space>nd` / `<Space>ny` | Today's / yesterday's daily note (`:Today`) |
| `<Space>ni` | Quick capture to `inbox.md` |
| `<Space>nI` | Open inbox |
| `<Space>nf` | Find note |
| `<Space>ng` | Search inside notes |
| `<Space>no` | Open todos across notes |
| `<Space>nr` | Recent notes |
| `<Space>nt` | Open the Tasks view in the viewer |
| `<Space>p` | Show the current note in the viewer |
| `<Space>o` | Turn viewer follow mode on/off |
| `<Space>?` | Search all shortcuts |
| `<Space>w` / `<Space>q` | Save / quit window |

Opening a note inside the notes folder shows it in the viewer automatically (follow mode), and
the viewer scrolls with your cursor (on `CursorHold`, `updatetime=400`). If `notesview` isn't
installed you get one warning and everything else keeps working.

### Markdown buffers

| Keys | Action |
| --- | --- |
| `<CR>` | Follow link under cursor (`[[wiki]]`, `[text](path)`, URLs) |
| `<BS>` | Go back |
| `]]` / `[[` | Next / previous heading |
| `j` / `k` | Move by visual line |
| `<Space>x` | Toggle checkbox (normal or visual selection) |
| `<Space>a` | Archive completed tasks under `## Done` |
| `<Space>1`–`4` | Set heading level 1–4 |
| `<Space>0` | Remove heading |
| `<Space>+` / `<Space>-` | Heading level up / down |
| `<Space>c` | Insert code block (asks for language) |
| `<Space>d` / `<Space>T` | Insert date / time |
| `<Space>h` | Insert divider |
| `<Space>t` | Toggle table mode |
| `<Space>v` | Paste image from clipboard (into `assets/`) |
| `<Space>j` / `<Space>k` | Move line down / up |
| `J` / `K` (visual) | Move selection down / up |

Visual mode, select text first:

| Keys | Action |
| --- | --- |
| `<Space>b` | **Bold** |
| `<Space>i` | *Italic* |
| `<Space>c` | `Inline code` |
| `<Space>s` | ~~Strikethrough~~ |
| `<Space>l` | Make link (cursor lands in the URL) |
| `<Space>w` | Make wiki link |

Lists use bullets.vim: `<CR>`/`o` continue the list, `<Tab>`/`<S-Tab>` (insert) and `>>`/`<<` demote/promote,
`gN` renumbers.

### Macros that survive restarts

Record with `qa … q`, then `:SaveMacro a`. Edit or delete saved macros with `:Macros`
(stored in `nvim/macros.lua`, which is git-ignored).

## Development

```sh
cd renderer && go vet ./... && go test ./...
```

CI runs those plus a headless Neovim check (loads the config, installs plugins, opens a note).
Pushing a `v*` tag builds `notesview` for darwin-arm64, darwin-amd64, linux-amd64 and linux-arm64
and attaches them to a GitHub release.
