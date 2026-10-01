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
from source if Go is available, or if the release is older than the config needs) into `~/.local/bin`.
It symlinks `nvim/` to `~/.config/nvim` and `notesview/config.toml` + `notesview/custom.css` to
`~/.config/notesview/` (backing up anything already there to `<name>.bak-<timestamp>`), creates your
notes folder and adds a block to `~/.zshrc` or `~/.bashrc` with:

- `notes` — `cd` to the notes folder and open today's daily note (`nvim +Today`)
- `inbox <text>` — add a task to `inbox.md` (`inbox` alone opens it). An older `inbox` function or
  alias in that file is replaced (the file is backed up first). In bash, quote text containing `#tags`.

On macOS it also installs [Hammerspoon](https://www.hammerspoon.org) and binds **Ctrl+Option+I** to a
small capture prompt (see [Global capture](#global-capture-macos)). On Linux that step is skipped.
It finishes by running `notesview doctor`. Safe to re-run.

```sh
./install.sh --uninstall   # removes symlinks, shell block, capture shortcut, binary, plugin data; restores backups
```

The notes folder is `$NOTES_DIR`, default `~/notes`.

### Global capture (macOS)

Press **Ctrl+Option+I** anywhere, type a task (`@fri`, `#act200` and friends work), press Return. A short
confirmation appears and the task is in `inbox.md`. One-time setup after `./install.sh`: open
Hammerspoon, allow it under **System Settings → Privacy & Security → Accessibility**, then choose
**Reload Config** from its menu-bar icon. The module lives in `hammerspoon/nvim_notes.lua`; if you
already had `~/.hammerspoon/init.lua`, install.sh only adds a marked `require("nvim_notes")` block to it.

### Classes

`notesview/config.toml` (linked to `~/.config/notesview/config.toml`) lists your classes. Both
notesview and Neovim read it and pick up edits without a restart:

```toml
[[class]]
id     = "act200"    # tag: #act200
name   = "ACT 200"   # display name
folder = "act200"    # notes go in classes/act200/
```

`<Space>nc` picks a class and opens today's lecture note, `classes/<folder>/YYYY-MM-DD.md`, with
sections for Topics, Notes, Key terms, Questions and Homework. Each class folder also gets an
`index.md`, and the viewer's sidebar lists the classes at the top.

## notesview

```sh
notesview serve --dir ~/notes --port 7777   # binds to 127.0.0.1 only
notesview open [path] [--line N]            # start the server if needed, show a note, open the window
notesview open --tasks                      # show the Tasks view
notesview tasks [--json] [--all]            # list open tasks (the same data the Tasks view uses)
notesview date "- [ ] quiz @fri"            # → "- [ ] quiz @2026-10-02"
notesview capture "read ch 5 @mon #psyc110" # append a task to inbox.md
notesview daily [--date YYYY-MM-DD]         # create a daily note (today's: with carry-over), print its path
notesview lecture act200                    # create today's lecture note for a class, print its path
notesview classes                           # list classes from config.toml
notesview doctor                            # check the installation, with a fix for each problem
notesview --version
```

One static Go binary; all HTML/CSS/JS is embedded and nothing touches the network at runtime.
The window opens as a tab-less app window if Chrome, Chromium, Brave or Edge is installed
(set `NOTESVIEW_BROWSER` to pick one), otherwise in your default browser.

- GitHub-flavored markdown, `[[wiki links]]` and `[[Note|alias]]` (missing notes are styled differently),
  callouts (`> [!NOTE]`, `[!TIP]`, `[!WARNING]`, …), highlighted code blocks, images relative to the note.
- Live updates over server-sent events; scroll position is kept. Neovim's cursor line drives the scroll.
- Sidebar with your **Classes**, then the tree (`inbox.md` pinned, `daily/` newest first), title +
  full-text search, and backlinks under each note. The sidebar updates live as files and folders are
  added, renamed or removed.
- A **Tasks** view (see [How tasks work](#how-tasks-work)) with a "Due this week" filter.
- Clicking a checkbox toggles it in the file (atomic write, only that line changes).
- Dark theme by default, light toggle (◐), and `~/.config/notesview/custom.css` loads last.

Viewer keys: `j`/`k` scroll · `/` search · `t` tasks · `g` today's daily note (created if needed).

## How tasks work

A task is any list item with a checkbox, anywhere in your notes (fenced code is ignored):

```markdown
- [ ] open task
- [x] done
- [>] moved to a later daily note → [[2026-10-01]]
```

All task rules live in `notesview` (`notesview tasks --json`), so the viewer's Tasks view and
`<Space>no` in Neovim always agree.

- **Due dates** are `@YYYY-MM-DD` in the task text. You can type natural dates instead; when you
  leave insert mode Neovim rewrites the line through `notesview date`, and capture does the same,
  so files only ever contain ISO dates. Unknown forms are left alone.

  | You type | Becomes |
  | --- | --- |
  | `@today`, `@tomorrow` | today / tomorrow |
  | `@mon` … `@sun` | the next one, never today (on a Friday, `@fri` is next Friday) |
  | `@oct6`, `@10/6` | that day this year, or next year if it has passed |
  | `@+3d`, `@+2w` | 3 days / 2 weeks from today |

- **Homework** is any task that belongs to a class: it is inside a class folder
  (`classes/act200/…`), or it has a class tag such as `#act200` anywhere else (inbox, daily notes…).
  Subtasks belong to their parent task's class. Every other task is **Other**.
- **The Tasks view** has two sections, Homework and Other, each grouped by due date: Overdue
  (highlighted), Today, Tomorrow, This week (the 7 days after today), Later and No date. Homework
  shows its class as a small label. "Due this week" hides Later and No date. Task text renders
  inline markdown; ticking a checkbox updates the file. Moved and done tasks are not listed.
- **Daily carry-over.** When today's daily note is created (`:Today`, `<Space>nd`, `notes`, or
  Today in the viewer), notesview finds the most recent earlier daily note (so Monday picks up
  Friday) and moves its unchecked tasks that are not homework, nested subtasks and all, into today's
  `## Tasks` section. In the old note each one becomes `- [>] … → [[today]]`, shown greyed out in
  Neovim and the viewer. Homework is never moved; it stays where it is and in the Tasks view.
  New daily notes start with `## Tasks` and `## Notes`.

## Shortcut cheat sheet

Leader is `<Space>`. Press `<Space>?` inside Neovim to search all shortcuts.

### Notes (anywhere)

| Keys | Action |
| --- | --- |
| `<Space>nn` | New note (asks for a title) |
| `<Space>nd` / `<Space>ny` | Today's / yesterday's daily note (`:Today`; today's carries over open tasks) |
| `<Space>nc` | Pick a class, open today's lecture note |
| `<Space>ni` | Quick capture to `inbox.md` (natural due dates and `#class` tags work) |
| `<Space>nI` | Open inbox |
| `<Space>nf` | Find note |
| `<Space>ng` | Search inside notes |
| `<Space>no` | Open tasks across notes (class, date group, due date) |
| `<Space>nr` | Recent notes |
| `<Space>nt` | Open the Tasks view in the viewer |
| `<Space>p` | Show the current note in the viewer |
| `<Space>o` | Turn viewer follow mode on/off |
| `<Space>?` | Search all shortcuts |
| `<Space>w` / `<Space>q` | Save / quit window |

Opening a note inside the notes folder shows it in the viewer automatically (follow mode), and
the viewer scrolls with your cursor (on `CursorHold`, `updatetime=400`). If `notesview` isn't
installed you get one warning and everything else keeps working (daily notes still get the template,
without carry-over). If it is older than the config needs (`NOTESVIEW_MIN_VERSION` in
`nvim/init.lua`), you get one warning asking you to run `./install.sh`.

### Markdown buffers

| Keys | Action |
| --- | --- |
| `<CR>` | Follow link under cursor (`[[wiki]]`, `[text](path)`, URLs) |
| `<BS>` | Go back |
| `]]` / `[[` | Next / previous heading |
| `j` / `k` | Move by visual line |
| `<Space>x` | Toggle checkbox (normal or visual selection; moved `[>]` tasks are left alone) |
| `<Space>a` | Archive completed tasks under `## Done` |
| `<Space>1`–`4` | Set heading level 1–4 and keep typing at the end of the line |
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

CI runs those plus a headless Neovim check (loads the config, installs plugins, opens a note, and
with a freshly built `notesview` exercises carry-over, due-date conversion, lecture notes and capture).
Pushing a `v*` tag builds `notesview` for darwin-arm64, darwin-amd64, linux-amd64 and linux-arm64
and attaches them to a GitHub release; the tag must match `version` in `renderer/main.go`. When the
Neovim config starts depending on a new notesview feature, bump both `version` and
`NOTESVIEW_MIN_VERSION` in `nvim/init.lua`, then tag a release.
