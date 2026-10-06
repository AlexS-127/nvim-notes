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
tarball into `~/.local` if your distro's is missing or old), ripgrep, fd, fzf, git, tree-sitter-cli
(plus pngpaste on macOS) and the `notesview` binary (from the latest GitHub release, or built
from source if Go is available, or if the release is older than the config needs) into `~/.local/bin`.
It symlinks `nvim/` to `~/.config/nvim` and `notesview/custom.css` to `~/.config/notesview/custom.css`
(backing up anything already there to `<name>.bak-<timestamp>`), creates your notes folder and adds a
block to `~/.zshrc` or `~/.bashrc` with:

- `notes` — `cd` to the notes folder and open today's daily note (`nvim +Today`)
- `inbox [text]` — capture a task (see [Capture](#capture)). An older `inbox` function or alias in that
  file is replaced (the file is backed up first).

It finishes by running `notesview doctor`. Safe to re-run.

```sh
./install.sh --uninstall   # removes the symlinks, shell block, binary and plugin data; restores backups
```

The notes folder is `$NOTES_DIR`, default `~/notes`.

## How folders and tags work

Folders are the structure; there is nothing to configure.

```
~/notes/
├── inbox.md
├── daily/2026-10-01.md        daily notes (not a category)
├── assets/                    images (not a category)
├── ACT 200/                   category  → #act-200
│   ├── syllabus.md
│   └── Chapter 5/             topic     → #act-200/chapter-5
│       └── costs.md
└── personal/                  category  → #personal
```

- **Categories** are the top-level folders, except `daily/`, `assets/` and hidden folders.
  **Topics** are the folders inside a category (except `assets/`). Deeper folders belong to their topic.
- **Tags** are those folder paths, slugified (lowercase, runs of other characters become `-`):
  `ACT 200` → `#act-200`, `ACT 200/Chapter 5` → `#act-200/chapter-5`.
- A task **inside a folder** automatically belongs to that folder's category and topic. A task
  **anywhere else** (inbox, daily notes, top-level notes) belongs to the first tag in it that names
  a folder: `- [ ] read ch 5 #act-200/chapter-5`. A tag for an unknown topic of a known category
  counts as the category; tags that name no folder (`#urgent`) are ordinary tags. Subtasks belong
  to their parent task's folder. Tasks with no folder are **General**.
- **Folder links.** `[[act-200]]` and `[[act-200/chapter-5]]` (or the real names, `[[ACT 200]]`) link to
  the folder itself. A note with the same name wins. In the viewer a folder link opens a generated page
  (nothing is written to disk) with the folder's notes, its topics and its open tasks by due date;
  the ↗ next to a folder in the sidebar opens the same page. In Neovim, `<CR>` on a folder link opens
  a picker of the notes inside it.
- `<Space>nn` asks for a title, then a folder (the capture picker, with "none" for the top level),
  and creates the note there.

## The viewer

- GitHub-flavored markdown, `[[wiki links]]` and `[[Note|alias]]` (missing notes are styled differently),
  callouts (`> [!NOTE]`, `[!TIP]`, `[!WARNING]`, …), highlighted code blocks, images relative to the note.
- Live updates over server-sent events; scroll position is kept. Neovim's cursor line drives the scroll.
- Sidebar tree (`inbox.md` pinned, `daily/` newest first) with title + full-text search,
  a **Tasks** view of every open `- [ ]` (task text rendered as inline markdown, with a link to
  its source line), generated folder pages, and backlinks under each note.
- Clicking a checkbox, in a note or in the Tasks view, toggles it in the file (atomic write, only that line changes).
- Light and dark themes (always follows the system, like Ghostty and Neovim). Reading text is set in a Georgia-style serif, with
  [Source Serif 4](https://github.com/adobe-fonts/source-serif) (SIL OFL 1.1) embedded as the
  fallback; code stays monospace.

### Folder colours

Each category and topic folder in the sidebar has a small square before its name (hollow until
you pick). Click it to choose one of ten colours, or × to clear. That colour tints the folder's
tag pills in the Tasks view and on folder pages; a topic without its own colour uses its
category's. Choices are saved in `~/.config/notesview/folder-colors.json`, shared by every window.

### Themes

Pick one with `notesview theme NAME` (list them with `notesview themes`), or edit
`~/.config/notesview/config.json` (`$XDG_CONFIG_HOME/notesview/config.json`). The open viewer
switches live when the file changes.

```json
{ "theme": "tokyo-night", "appearance": "auto" }
```

| Theme | Look |
| --- | --- |
| `default` | clean GitHub-like reading theme |
| `nord` | arctic blue-greys |
| `gruvbox` | warm retro earth tones |
| `solarized` | low-contrast classic |
| `catppuccin` | soft pastels (latte / mocha) |
| `cappuccino` | catppuccin colours in Times New Roman, with monospace numbers |
| `rose-pine` | dusky rose and gold (dawn / moon) |
| `tokyo-night` | neon-lit city night (day / night) |
| `horizon` | white, orange and blue: colourful headings, tinted sidebar, blue table headers |
| `dracula` | purple and pink on charcoal, dark only |
| `borland` | Turbo Pascal IDE: blue desk, yellow text, double-ruled headings, monospace, dark only |
| `terminal` | green phosphor CRT: monospace, glow, scanlines, dark only |

`appearance` is `auto` (follow the system, the default), `light` or `dark`. Themes that only
exist in one appearance (`dracula`, `borland`, `terminal`) ignore it. Each theme also sets the code
highlighting colours. An unknown theme falls back to `default`. Your `custom.css` loads after the theme, so it can still
override anything. Note that font variables set there beat the theme's: remove
`--font-body` from `custom.css` to let `terminal` be monospace.

### Fonts and custom CSS

The viewer loads `~/.config/notesview/custom.css` (or `$XDG_CONFIG_HOME/notesview/custom.css`)
after its own theme, and reloads the page whenever that file changes. `install.sh` symlinks it to
[`notesview/custom.css`](notesview/custom.css) in this repo, backing up any file already there, so
your tweaks are version-controlled. It ships with everything commented out; uncomment an example or
set the theme variables yourself:

```css
:root {
  --font-body: Georgia, "Source Serif 4", serif;  /* reading text */
  --font-heading: var(--font-body);               /* headings */
  --font-code: ui-monospace, Menlo, Consolas, monospace;
  --font-ui: -apple-system, "Segoe UI", Roboto, sans-serif;  /* sidebar and buttons */
  --font-size: 17px;
  --line-height: 1.65;
}
```

## How tasks work

A task is any list item with a checkbox, anywhere in your notes (fenced code is ignored):

```markdown
- [ ] open task
- [x] done
- [>] moved to a later daily note → [[2026-10-01]]
```

All task rules live in `notesview` (`notesview tasks --json`), so the viewer's Tasks view,
`<Space>no` in Neovim and the `inbox` command always agree.

- **Due dates** are `@YYYY-MM-DD` in the task text. You can type natural dates instead; when you
  leave insert mode Neovim rewrites the line through `notesview date`, and capture does the same,
  so files only ever contain ISO dates. Unknown forms are left alone.

  | You type | Becomes |
  | --- | --- |
  | `@today`, `@tomorrow` | today / tomorrow |
  | `@mon` … `@sun` | the next one, never today (on a Friday, `@fri` is next Friday) |
  | `@oct6`, `@10/6` | that day this year, or next year if it has passed |
  | `@+3d`, `@+2w` | 3 days / 2 weeks from today |

- **The Tasks view** lists every open task grouped by due date: Overdue (highlighted), then
  one heading per day for the next 7 days (Today, Friday, October 2, …), then More than a week,
  More than 2 weeks, Later, and Better late than never for tasks with no date. Each task shows its
  category and topic (or General). The categories sit in a row of toggle chips with a Select all
  box; "Due this week" limits to overdue and the next 7 days. Both are remembered.
  Task text renders inline markdown; ticking a checkbox updates the file. Moved and done tasks are
  not listed.
- **Daily carry-over.** When today's daily note is created (`:Today`, `<Space>nd`, `notes`, or
  Today in the viewer), notesview finds the most recent earlier daily note (so Monday picks up
  Friday) and moves its unchecked tasks that have **no category and no due date**, nested subtasks
  and all, into today's `## Tasks` section. In the old note each one becomes `- [>] … → [[today]]`,
  shown greyed out in Neovim and the viewer. Tasks with a category or a due date stay where they
  are; the Tasks view shows them by date. New daily notes start with `## Tasks` and `## Notes`.

## Activity

The **Activity** view (sidebar button, or `a` in the viewer) turns your tasks into a scoreboard:

- **Running total** of completed tasks (counts up when you open it), this week's count, a **streak**
  of consecutive days with a task done, today's done and made counts, and a **rank bar** with
  milestones at 10, 25, 50, 100, 250, 500 and 1000 tasks.
- A **calendar** of the last year, like a commit graph: *Done + made*, *Done* or *Made* per day.
- **Weekly:** average tasks per day for 12 weeks (done and made stacked).
- **Daily:** average per weekday (Mon to Sun) over those weeks, to show which days you get most done.
- **Distribution:** a histogram of tasks done per day, one entry per finished day since your first
  completion (zero days count, today doesn't until it's over), with the median and +1 sigma marked,
  and how many tasks today still needs to beat the median and reach 1 sigma.
- **Quiz time:** `quiz.py` logs how long each session took to `notes/.quiz_log.jsonl` (a pause of
  over 90 seconds on one question counts as 90, 10 minutes on a worked problem). The Activity view
  shows time today, this week and in all, and the calendar tooltip shows each day's quiz minutes.
  A subject is quizzed from `notes/<subject>/definitions.md` (`word :: translation` vocab) or, if
  present, `notes/<subject>/questions.md`: blocks of `Q:` with `a)`…`d)` choices and `A: c`,
  `A: true`/`false`, a short `A:` answer, or a `Solution:` you mark yourself right or wrong, with
  optional `Why:` and `Src:` lines and `## ` topic headings.
- **Done is automatic.** Ticking a task (in the viewer or with `<Space>x`) moves it, with its
  nested lines, to the end of the note's `## Done` section, creating it if needed. Unticking one
  in Done moves it back up with the open tasks. New captures land with the open tasks, above
  `## Done`, instead of at the bottom of the file.
- Ticking a task in the viewer throws confetti, with a toast when you pass a milestone.

Completion dates come from a stamp added when you tick a task, in the viewer or with `<Space>x`:
`- [x] read ch 5 ✅ 2026-10-01 14:30` (unticking removes it; older stamps have only the date). The time places the tick on the Today score curve. Ticking a box by hand-editing the file
leaves no stamp. Tasks done before stamps existed count toward the total but not the calendar.
"Made" uses the capture timestamp (`_(Oct 01 10:12)_`), or the date of the daily note the task was
written in; tasks carried over from an earlier daily note are not counted as new.
The data is at `/api/activity`.

## Classes and check-in

The **Classes** page (sidebar button, or `c` in the viewer) is where you import `.ics` schedules; there is no calendar display (use your calendar app for that).

- **Import** with *Import .ics…* (or drop files on the page), or `notesview calendar import --name School school.ics`. Each file is a separate calendar with a colour, a use/ignore switch and a *classes* switch. Importing a file with the same name updates that calendar. Files are copied to `<notes>/.calendar/` and committed with your notes.
- **Check in from home:** the next class is shown on the Activity view (Check-in button) and on the Neovim start page (`a`). The window opens 15 minutes before the class and closes when it ends; a check-in earns 5 points (`scoreCheckinPts` in `score.go`) on the day of the class.
- **Attendance:** an Activity tile shows the overall attendance %; the Classes page lists attended/held per class. Every ended class (attended or missed) is logged to `.calendar/attendance.jsonl` as a signal for later features.

CLI: `notesview calendar [list | import | remove | next | today | checkin | attendance]`.

## Reading list and log

The **Reading** page (sidebar button, or `r` in the viewer) keeps books to read, being read and read. Adding a book needs a title and an author; the number of pages is optional and gives a % read.

- **Log pages** in the box next to a book: `20` = pages just read, `p150` = "I'm on page 150" (a negative number corrects an overcount). Each page logged scores 1 point (`scoreReadPagesPer` in `score.go`). Started a book before tracking it? Put the page you're on in *Already on page* when adding it, or type `=150` in its log box (start page too): that sets the bookmark without scoring. A book with a page count is finished when you reach its last page; *Finished* marks any book read without logging pages.
- **From home:** the Activity view has a Reading card (same log box) and a Pages read tile; the Neovim start page lists the books being read with their %, `l` logs pages (Enter on a book's line for that book), `b` adds a book.
- Books are in `<notes>/.reading/books.json`, logged pages in the append-only `.reading/log.jsonl` (both committed with your notes). Removing a book keeps its points.

CLI: `notesview read [list | add "TITLE" "AUTHOR" [PAGES] | log [BOOK] PAGES | log [BOOK] --to PAGE | at [BOOK] PAGE | start | done | later | pages BOOK N | remove] BOOK` (BOOK = id or the start of the title).

## Capture

Capture adds `- [ ] text #folder-tag @due _(timestamp)_` to `inbox.md` in three steps:

1. **Task** — the text (required).
2. **Folder** — a fuzzy picker of every category and topic, with **none** at the top. Enter on
   none, or Esc, skips. The choice becomes the task's tag.
3. **Due** — anything the natural-date table above understands (`fri`, `tomorrow`, `oct6`, `10/6`,
   `+3d`, or `2026-10-06`). It shows the resolved date first (`fri → Fri Oct 2`); Enter confirms,
   or type another date. Empty skips; something it can't read is asked again, never saved raw.

Steps the text already answers are skipped: `inbox pay rent #personal @mon` asks nothing more.
Then one confirmation line shows the final task.

- **Terminal:** `inbox` runs the steps, using fzf for the folder; `inbox some text` fills in step 1.
  (In bash, quote text containing `#tags` — bash treats `#` as a comment.)
- **Neovim:** `<Space>ni` runs the same steps with the picker and prompts, without leaving the
  current note.

Both go through `notesview capture`, so they behave identically.

## Shortcut cheat sheet

Leader is `<Space>`. Press `<Space>?` inside Neovim to search all shortcuts.

### Notes (anywhere)

| Keys | Action |
| --- | --- |
| `<Space>nn` | New note: asks for a title, then a folder |
| `<Space>nd` / `<Space>ny` | Today's / yesterday's daily note (`:Today`; today's carries over open tasks) |
| `<Space>ni` | Capture a task to `inbox.md`: text, folder, due date |
| `<Space>nC` | Quit nvim and run `claude` in `~` (a shell in `~` is left when claude exits) |
| `<Space>nI` | Open inbox |
| `<Space>nf` | Find note |
| `<Space>ng` | Search inside notes |
| `<Space>no` | Open tasks across notes, by due date, with category and topic |
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
| `<CR>` | Follow link under cursor (`[[wiki]]`, `[[folder]]` → picker of its notes, `[text](path)`, URLs) |
| `<BS>` | Go back |
| `]]` / `[[` | Next / previous heading |
| `j` / `k` | Move by visual line |
| `<Space>x` | Toggle checkbox (normal or visual selection; moved `[>]` tasks are left alone) |
| `<Space>a` | Sweep any older completed tasks into `## Done` (ticking already does this) |
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

## notesview

```sh
notesview serve --dir ~/notes --port 7777   # binds to 127.0.0.1 only
notesview open [path] [--line N]            # start the server if needed, show a note, open the window
notesview open --tasks                      # show the Tasks view
notesview tasks [--json] [--all]            # list open tasks (the same data the Tasks view uses)
notesview folders [--json]                  # list category and topic folders with their tags
notesview resolve [--json] act-200          # what [[act-200]] points to (note, folder or missing)
notesview date "- [ ] quiz @fri"            # → "- [ ] quiz @2026-10-02"
notesview due fri                           # → 2026-10-02  Fri Oct 2
notesview capture -i [text]                 # capture step by step (what `inbox` runs)
notesview capture --folder act-200 --due fri -- "read ch 5"   # capture in one go
notesview daily [--date YYYY-MM-DD]         # create a daily note (today's: with carry-over), print its path
notesview doctor                            # check the installation, with a fix for each problem
notesview themes                            # list themes (* = current)
notesview theme tokyo-night                 # choose a theme
notesview --version
```

One static Go binary; all HTML/CSS/JS is embedded and nothing touches the network at runtime.
The window opens as a tab-less app window if Chrome, Chromium, Brave or Edge is installed
(set `NOTESVIEW_BROWSER` to pick one), otherwise in your default browser.

- GitHub-flavored markdown, `[[wiki links]]` and `[[Note|alias]]` (missing notes are styled differently),
  `[[folder]]` links to generated folder pages, callouts (`> [!NOTE]`, `[!TIP]`, `[!WARNING]`, …),
  highlighted code blocks, images relative to the note.
- Live updates over server-sent events; scroll position is kept. Neovim's cursor line drives the scroll.
- Sidebar tree (`inbox.md` pinned, `daily/` newest first) with title + full-text search, and backlinks
  under each note. It updates live as files and folders are added, renamed or removed. Collapse it
  with « at its top edge or `b`; when collapsed the note uses the full width (up to its max width).
- A **Tasks** view (see [How tasks work](#how-tasks-work)) with category and "Due this week" filters.
- Clicking a checkbox toggles it in the file (atomic write, only that line changes).
- Light and dark themes, following your system setting live (there is no manual toggle).
  Code highlighting, inline code, tables and callouts have colours for both. The collapsed
  sidebar (`b`, or « / ») is remembered. `~/.config/notesview/custom.css` loads last and overrides
  the theme: `:root { --accent: … }` changes both themes, `:root[data-theme="dark"] { … }` just one.

Viewer keys: `j`/`k` scroll · `/` search · `t` tasks · `a` activity · `c` classes · `r` reading · `g` today's daily note (created if needed) · `b` sidebar.

## Development

```sh
cd renderer && go vet ./... && go test ./...
```

CI runs those plus a headless Neovim check (loads the config, installs plugins, opens a note, and
with a freshly built `notesview` exercises carry-over, due-date conversion, capture, new notes in
folders and folder links).
Pushing a `v*` tag builds `notesview` for darwin-arm64, darwin-amd64, linux-amd64 and linux-arm64
and attaches them to a GitHub release; the tag must match `version` in `renderer/main.go`. When the
Neovim config starts depending on a new notesview feature, bump both `version` and
`NOTESVIEW_MIN_VERSION` in `nvim/init.lua`, then tag a release.

## Extras (optional power-ups)

`nvim/lua/extras/init.lua`, loaded by the last lines of `init.lua`. Nothing existing was changed.

| Keys | What |
|---|---|
| `<Space>` (wait) | which-key popup lists every shortcut |
| `s` / `S` | flash: jump to any text / select a syntax node |
| `gsa` `gsd` `gsr` | surround add / delete / replace (`gsaiw*`) |
| `<Space>nb` | backlinks to this note |
| `<Space>nl`, `<C-l>` (insert) | pick a note and insert `[[link]]` |
| `[[` while typing | completion popup of notes (`<C-y>` accepts) |
| `<Space>nh` | outline of the note's headings |
| `<Space>nT` | browse `#tags` |
| `<Space>nR` or `:NoteRename name` | rename a note and fix every `[[link]]` to it |
| `[d` / `]d` | previous / next daily note |
| `<Space>nm` | toggle in-buffer markdown rendering |
| `<Space>z` | zen mode |
| `<Space>nu` `<Space>,` `<Space>.` | undo history, buffers, resume last picker |

Also: a statusline with task progress (☑ done/total) and word count, heading folds (`za`, `zR`),
`:s` live preview, centered search/scroll jumps, and autopairs.

**Turn it off:** `NVIM_NOTES_PLAIN=1 nvim` for one session; delete the "Extras" block at the end of
`init.lua` for good; or `git checkout main` to drop the whole branch.
