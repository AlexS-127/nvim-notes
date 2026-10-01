-- ~/.config/nvim/init.lua — markdown notes (Neovim 0.12+, no plugin manager)
-- Leader is <Space>. Notes live in $NOTES_DIR (default ~/notes). Press <Space>? to search all shortcuts.

vim.g.mapleader = " "
vim.g.maplocalleader = " "
local NOTES = vim.fn.expand((vim.env.NOTES_DIR and vim.env.NOTES_DIR ~= "") and vim.env.NOTES_DIR or "~/notes")
local uv = vim.uv or vim.loop
-- Oldest notesview this config works with. Bump together with `version` in renderer/main.go.
local NOTESVIEW_MIN_VERSION = "0.2.0"

-- ── Options ──────────────────────────────────────────────────────
local o = vim.opt
o.termguicolors = false          -- use Ghostty's palette (Borland)
o.number = true
o.relativenumber = true
o.clipboard = "unnamedplus"
o.ignorecase = true
o.smartcase = true
o.undofile = true
o.splitright = true
o.signcolumn = "yes"
o.scrolloff = 5
o.autoread = true
o.updatetime = 400                -- CursorHold fires quickly (viewer scroll sync)
vim.cmd.colorscheme("default")

-- ── Plugins (built-in vim.pack) ──────────────────────────────────
vim.g.bullets_set_mappings = 0   -- we pick bullets.vim's keys ourselves
vim.g.bullets_custom_mappings = {
  { "imap", "<cr>", "<Plug>(bullets-newline)" },
  { "inoremap", "<C-cr>", "<cr>" },
  { "nmap", "o", "<Plug>(bullets-newline)" },
  { "nmap", "gN", "<Plug>(bullets-renumber)" },
  { "vmap", "gN", "<Plug>(bullets-renumber)" },
  { "imap", "<Tab>", "<Plug>(bullets-demote)" },
  { "imap", "<S-Tab>", "<Plug>(bullets-promote)" },
  { "nmap", ">>", "<Plug>(bullets-demote)" },
  { "nmap", "<<", "<Plug>(bullets-promote)" },
}
vim.g.table_mode_corner = "|"

vim.api.nvim_create_autocmd("PackChanged", {
  callback = function(ev)
    if ev.data.spec.name == "nvim-treesitter" and ev.data.kind == "update" then
      pcall(vim.cmd, "TSUpdate")
    end
  end,
})

vim.pack.add({
  { src = "https://github.com/nvim-treesitter/nvim-treesitter", version = "main" },
  "https://github.com/folke/snacks.nvim",
  "https://github.com/dkarter/bullets.vim",
  "https://github.com/dhruvasagar/vim-table-mode",
  "https://github.com/HakonHarnes/img-clip.nvim",
  { src = "https://github.com/catppuccin/nvim", name = "catppuccin" },
  "https://github.com/f-person/auto-dark-mode.nvim",
  "https://github.com/nvim-mini/mini.starter",
}, { confirm = false })

-- Code-block syntax highlighting (needs: brew install tree-sitter-cli)
vim.g.notes_parsers = {
  "bash", "python", "javascript", "typescript", "tsx", "json", "yaml", "toml",
  "html", "css", "go", "rust", "sql", "swift", "lua", "c",
}
require("nvim-treesitter").install(vim.g.notes_parsers)
require("snacks").setup({ picker = { enabled = true }, input = { enabled = true } })
require("img-clip").setup({ default = { dir_path = "assets", relative_to_current_file = true } })

-- ── notesview (CLI) ──────────────────────────────────────────────
-- Task parsing, folders and tags, due dates, daily carry-over and capture all
-- live in the notesview binary, so the rules are the same here, in the viewer
-- and in the `inbox` shell command.
local function nv_env() return { NOTES_DIR = NOTES } end



local function version_less(a, b)
  local pa, pb = vim.split(a, ".", { plain = true }), vim.split(b, ".", { plain = true })
  for i = 1, 3 do
    local x, y = tonumber(pa[i]) or 0, tonumber(pb[i]) or 0
    if x ~= y then return x < y end
  end
  return false
end

-- Runs notesview synchronously. Returns stdout, or nil and an error message.
local function nv_sync(args, timeout)
  if vim.fn.executable("notesview") == 0 then return nil, "notesview is not installed — run ./install.sh" end
  local ok, r = pcall(function()
    return vim.system(vim.list_extend({ "notesview" }, args), { text = true, env = nv_env() }):wait(timeout or 5000)
  end)
  if not ok then return nil, tostring(r) end
  if r.code ~= 0 then
    local err = vim.trim(r.stderr or "")
    return nil, err ~= "" and err or ("notesview exited with " .. r.code)
  end
  return r.stdout or ""
end

-- Decodes `notesview … --json` output; nil if notesview failed or is missing.
local function nv_json(args)
  local out = nv_sync(args)
  if not out then return nil end
  local ok, v = pcall(vim.json.decode, out, { luanil = { object = true, array = true } })
  return ok and v or nil
end

-- Warn once per session if the installed notesview is older than this config needs.
vim.defer_fn(function()
  if vim.fn.executable("notesview") == 0 then return end
  vim.system({ "notesview", "--version" }, { text = true }, function(r)
    local v = r.code == 0 and (r.stdout or ""):match("(%d+%.%d+%.?%d*)") or nil
    if v and not version_less(v, NOTESVIEW_MIN_VERSION) then return end
    vim.schedule(function()
      vim.notify(("notesview %s is older than %s, which this config needs — run ./install.sh in your nvim-notes checkout")
        :format(v or "(unknown version)", NOTESVIEW_MIN_VERSION), vim.log.levels.WARN)
    end)
  end)
end, 300)

-- ── Helpers ──────────────────────────────────────────────────────
-- Daily notes are created by notesview (template + carry-over of open tasks
-- from the most recent earlier daily note). Returns true if it created one.
local function ensure_daily(path)
  local date = path:match("^" .. vim.pesc(NOTES) .. "/daily/(%d%d%d%d%-%d%d%-%d%d)%.md$")
  if not date or uv.fs_stat(path) then return false end
  local out, err = nv_sync({ "daily", "--dir", NOTES, "--date", date, "--json" })
  if not out then
    if vim.fn.executable("notesview") == 1 then vim.notify("notesview daily: " .. err, vim.log.levels.WARN) end
    return false
  end
  local ok, res = pcall(vim.json.decode, out)
  if ok and type(res) == "table" then
    if (res.moved or 0) > 0 then
      vim.notify(("Carried over %d task(s) from %s"):format(res.moved, res.from))
    end
    if res.warning and res.warning ~= vim.NIL then vim.notify(res.warning, vim.log.levels.WARN) end
  end
  return true
end

local function open_note(path, title)
  if not path:match("%.md$") and uv.fs_stat(path) then return vim.ui.open(path) end
  local is_daily = path:match("^" .. vim.pesc(NOTES) .. "/daily/%d%d%d%d%-%d%d%-%d%d%.md$") ~= nil
  local created = is_daily and ensure_daily(path)
  vim.fn.mkdir(vim.fn.fnamemodify(path, ":h"), "p")
  local exists = uv.fs_stat(path)
  vim.cmd.edit(vim.fn.fnameescape(path))
  if not exists then
    if is_daily then -- notesview unavailable: same template, no carry-over
      vim.api.nvim_buf_set_lines(0, 0, -1, false, { "# " .. title, "", "## Tasks", "", "", "## Notes", "" })
    else
      vim.api.nvim_buf_set_lines(0, 0, -1, false, { "# " .. title, "", "" })
      return vim.api.nvim_win_set_cursor(0, { 3, 0 })
    end
  end
  if created or (is_daily and not exists) then
    local notes_row = vim.fn.search("^## Notes", "nw")
    if notes_row > 1 then vim.api.nvim_win_set_cursor(0, { notes_row - 1, 0 }) end
  end
end

local function daily(offset_days)
  local t = os.time() + (offset_days or 0) * 86400
  open_note(NOTES .. "/daily/" .. os.date("%Y-%m-%d", t) .. ".md", os.date("%A, %B %d %Y", t))
end

-- Picks a category or topic folder (with "none" first). Calls back with the
-- folder ({ path, tag, name, kind }) or nil for none / Esc / no notesview.
local function pick_folder(title, cb)
  local folders = nv_json({ "folders", "--json", "--dir", NOTES })
  if not folders or #folders == 0 then return cb(nil) end
  local items = { { text = "none", none = true } }
  for _, f in ipairs(folders) do table.insert(items, { text = f.tag .. " " .. f.path, folder = f }) end
  local chosen, done = nil, false
  local function finish()
    if done then return end
    done = true
    vim.schedule(function() cb(chosen) end)
  end
  Snacks.picker.pick({
    title = title,
    items = items,
    layout = { preset = "select" },
    format = function(item)
      if item.none then return { { "none", "Comment" }, { "  top level / no folder", "Comment" } } end
      local f = item.folder
      return { { "#" .. f.tag, f.kind == "category" and "Title" or "Normal" }, { "  " .. f.path, "Comment" } }
    end,
    confirm = function(picker, item)
      chosen = item and item.folder or nil
      picker:close()
      finish()
    end,
    on_close = finish,   -- Esc: no folder
  })
end

-- Opens a picker of the notes inside a folder ([[act-200]] links).
local function folder_notes_picker(r)
  if #(r.notes or {}) == 0 then return vim.notify("No notes in " .. r.path .. " yet") end
  local items = {}
  for _, n in ipairs(r.notes) do
    table.insert(items, { text = n.title .. " " .. n.path, file = NOTES .. "/" .. n.path, title = n.title, rel = n.path })
  end
  Snacks.picker.pick({
    title = "📁 " .. r.path,
    items = items,
    format = function(item) return { { item.title }, { "  " .. item.rel, "Comment" } } end,
    preview = "file",
    confirm = function(picker, item)
      picker:close()
      if item then vim.cmd.edit(vim.fn.fnameescape(item.file)) end
    end,
  })
end

-- Natural due dates (@tomorrow, @fri, @oct6, @10/6, @+3d) become @YYYY-MM-DD
-- through notesview, so files only ever hold ISO dates.
local function convert_due_dates(buf)
  local row = vim.api.nvim_win_get_cursor(0)[1]
  local line = vim.api.nvim_buf_get_lines(buf, row - 1, row, false)[1]
  if not line or not line:find("@", 1, true) then return end
  local candidate = false
  for tok in line:gmatch("@([%w/+]+)") do
    if not tok:match("^%d%d%d%d$") then candidate = true break end   -- @2026-10-06 is already ISO
  end
  if not candidate or vim.fn.executable("notesview") == 0 then return end
  local out = nv_sync({ "date", "--", line }, 2000)
  if not out then return end
  out = out:gsub("\r?\n$", "")
  if out ~= line and not out:find("\n") then
    pcall(vim.cmd, "undojoin")
    vim.api.nvim_buf_set_lines(buf, row - 1, row, false, { out })
  end
end

local function toggle_checkbox_line(line)
  if line:match("^%s*[-*+] %[>%]") then return line end   -- moved to a later note: leave it
  if line:match("^%s*[-*+] %[ %]") then return (line:gsub("%[ %]", "[x]", 1)) end
  if line:match("^%s*[-*+] %[[xX]%]") then return (line:gsub("%[[xX]%]", "[ ]", 1)) end
  if line:match("^%s*[-*+] ") then return (line:gsub("^(%s*[-*+] )", "%1[ ] ", 1)) end
  return (line:gsub("^(%s*)", "%1- [ ] ", 1))
end

local function toggle_checkbox_range(first, last)
  local lines = vim.api.nvim_buf_get_lines(0, first - 1, last, false)
  for i, l in ipairs(lines) do lines[i] = toggle_checkbox_line(l) end
  vim.api.nvim_buf_set_lines(0, first - 1, last, false, lines)
end

local function set_heading(level)
  local rest = vim.api.nvim_get_current_line():gsub("^#+%s*", "")
  vim.api.nvim_set_current_line((level > 0 and string.rep("#", level) .. " " or "") .. rest)
end

local function change_heading(delta)
  local hashes = vim.api.nvim_get_current_line():match("^(#*)")
  set_heading(math.max(0, math.min(6, #hashes + delta)))
end

local function follow_link()
  local line, col = vim.api.nvim_get_current_line(), vim.fn.col(".")
  for s, target, e in line:gmatch("()%[%[([^%]]+)%]%]()") do        -- [[wiki link]]
    if col >= s and col < e then
      target = target:gsub("|.*", "")
      local direct = NOTES .. "/" .. target .. (target:match("%.md$") and "" or ".md")
      if not uv.fs_stat(direct) then
        -- a note elsewhere with that name, or a folder ([[act-200]], [[act-200/chapter-5]])
        local r = nv_json({ "resolve", "--json", "--dir", NOTES, "--", target })
        if r and r.kind == "note" then return open_note(NOTES .. "/" .. r.path, target) end
        if r and r.kind == "folder" then return folder_notes_picker(r) end
      end
      return open_note(direct, target)
    end
  end
  for s, target, e in line:gmatch("()%[[^%]]*%]%(([^%)]+)%)()") do  -- [text](target)
    if col >= s and col < e then
      if target:match("^%a+://") then return vim.ui.open(target) end
      local path = target:sub(1, 1) == "/" and target or (vim.fn.expand("%:p:h") .. "/" .. target)
      return open_note(path, vim.fn.fnamemodify(path, ":t:r"))
    end
  end
  for s, url, e in line:gmatch("()(https?://[^%s%)>%]]+)()") do       -- bare URL
    if col >= s and col < e then return vim.ui.open(url) end
  end
  vim.cmd("normal! +")
end

local function archive_done()
  local keep, done, has_done = {}, {}, false
  for _, l in ipairs(vim.api.nvim_buf_get_lines(0, 0, -1, false)) do
    if l:match("^## Done%s*$") then has_done = true end
    if not has_done and l:match("^%s*[-*+] %[[xX]%]") then
      table.insert(done, (l:gsub("^%s+", "")))
    else
      table.insert(keep, l)
    end
  end
  if #done == 0 then return vim.notify("No completed tasks to archive") end
  if not has_done then vim.list_extend(keep, { "", "## Done" }) end
  vim.list_extend(keep, done)
  vim.api.nvim_buf_set_lines(0, 0, -1, false, keep)
  vim.notify(("Archived %d task(s) to ## Done"):format(#done))
end

local function insert_code_block()
  vim.ui.input({ prompt = "Language: " }, function(lang)
    if lang == nil then return end
    local row = vim.api.nvim_win_get_cursor(0)[1]
    vim.api.nvim_buf_set_lines(0, row, row, false, { "```" .. lang, "", "```" })
    vim.api.nvim_win_set_cursor(0, { row + 2, 0 })
    vim.cmd("startinsert")
  end)
end

local function insert_text(text)
  vim.api.nvim_put({ text }, "c", true, true)
end

-- Capture: task text → folder (picker, "none" or Esc skips) → due date (natural
-- forms, shown resolved before confirming; empty skips). The same steps as the
-- `inbox` shell command; notesview decides what the text already answers and
-- writes the line.
local function ask_due(cb)
  local function resolve(answer)
    if answer == "" then return cb("") end
    local r = nv_json({ "due", "--json", "--", answer })
    if not (r and r.ok) then   -- can't parse it: ask again rather than saving it raw
      return vim.ui.input({ prompt = ('Can\'t read "%s". Due (empty to skip): '):format(answer) }, function(again)
        resolve(vim.trim(again or ""))
      end)
    end
    vim.ui.input({ prompt = ("%s → %s  (Enter to confirm, or type another date): "):format(answer:gsub("^@", ""), r.label) },
      function(next)
        if next == nil then return ask_due(cb) end   -- Esc: back to the date question
        next = vim.trim(next)
        if next == "" then return cb(r.date) end
        resolve(next)
      end)
  end
  vim.ui.input({ prompt = "Due (fri, tomorrow, oct6, 10/6, +3d — empty to skip): " }, function(answer)
    resolve(vim.trim(answer or ""))
  end)
end

local function capture_to_inbox()
  vim.ui.input({ prompt = "Task: " }, function(text)
    if not text or vim.trim(text) == "" then return end
    if vim.fn.executable("notesview") == 0 then   -- no notesview: plain append
      local f = NOTES .. "/inbox.md"
      vim.fn.mkdir(NOTES, "p")
      if not uv.fs_stat(f) then vim.fn.writefile({ "# Inbox", "" }, f) end
      vim.fn.writefile({ "- [ ] " .. text .. " _(" .. os.date("%b %d %H:%M") .. ")_" }, f, "a")
      vim.cmd("checktime")
      return vim.notify("Captured to inbox")
    end
    local parsed = nv_json({ "capture", "--parse", "--dir", NOTES, "--", text }) or {}
    local function save(folder, due)
      local out, err = nv_sync({ "capture", "--dir", NOTES, "--folder", folder or "", "--due", due or "", "--", text })
      if not out then return vim.notify("Capture failed: " .. err, vim.log.levels.ERROR) end
      vim.cmd("checktime")
      vim.notify(vim.trim(out))
    end
    local function due_step(folder)
      if parsed.due and parsed.due ~= "" then return save(folder, "") end
      ask_due(function(due) save(folder, due) end)
    end
    if parsed.folder and parsed.folder ~= "" then return due_step(nil) end
    pick_folder("Folder for the task (Esc: none)", function(f) due_step(f and f.tag) end)
  end)
end

-- ── notesview integration ────────────────────────────────────────
-- A small local server renders the current note in its own window. It follows
-- the note you edit and scrolls with your cursor. Install: ./install.sh
local NV = { follow = true, warned = false, shown = nil, sent = nil }
local NOTES_REAL = uv.fs_realpath(NOTES) or NOTES

local function note_path(buf)
  local name = vim.api.nvim_buf_get_name(buf)
  if name == "" or not name:match("%.md$") or vim.bo[buf].buftype ~= "" then return nil end
  local real = uv.fs_realpath(name) or name
  if real:sub(1, #NOTES_REAL + 1) ~= NOTES_REAL .. "/" then return nil end
  return real:sub(#NOTES_REAL + 2)
end

local function notesview(args)
  if vim.fn.executable("notesview") == 0 then
    if not NV.warned then
      NV.warned = true
      vim.notify("notesview is not installed — run install.sh to get the note viewer", vim.log.levels.WARN)
    end
    return
  end
  vim.system(vim.list_extend({ "notesview" }, args), { env = nv_env(), stdout = false, stderr = false }, function() end)
end

local function open_tasks_picker()
  local tasks = nv_json({ "tasks", "--json", "--dir", NOTES })
  if type(tasks) ~= "table" then   -- no (or an older) notesview: plain grep
    return Snacks.picker.grep({ cwd = NOTES, search = "- \\[ \\]" })
  end
  local when = { overdue = "overdue", today = "today", tomorrow = "tomorrow", week = "this week", later = "later", none = "" }
  local items = {}
  for _, t in ipairs(tasks) do
    local label = not t.category and "General" or (t.topic and (t.category_name .. " · " .. t.topic_name) or t.category_name)
    table.insert(items, {
      text = table.concat({ t.display, label, t.due or "", t.file }, " "),
      file = NOTES .. "/" .. t.file,
      pos = { t.line, 0 },
      task = t, label = label, when = when[t.group] or "",
    })
  end
  if #items == 0 then return vim.notify("No open tasks 🎉") end
  Snacks.picker.pick({
    title = "Open tasks (by due date)",
    items = items,
    format = function(item)
      local t = item.task
      return {
        { ("%-9s"):format(item.when), t.group == "overdue" and "ErrorMsg" or "Comment" },
        { " " },
        { t.display },
        { "  " .. item.label, t.category and "Special" or "Comment" },
        { "  " .. t.file .. ":" .. t.line, "Comment" },
      }
    end,
    preview = "file",
    confirm = function(picker, item)
      picker:close()
      if not item then return end
      vim.cmd.edit(vim.fn.fnameescape(item.file))
      pcall(vim.api.nvim_win_set_cursor, 0, { item.pos[1], 0 })
    end,
  })
end

local function viewer_show(rel, line)
  notesview({ "open", NOTES_REAL .. "/" .. rel, "--line", tostring(line or 0) })
  NV.shown, NV.sent = rel, line
end

vim.api.nvim_create_autocmd("BufEnter", {
  pattern = "*.md",
  callback = function(ev)
    local rel = note_path(ev.buf)
    if rel and NV.follow and rel ~= NV.shown then viewer_show(rel, vim.fn.line(".")) end
  end,
})
vim.api.nvim_create_autocmd("CursorHold", {
  pattern = "*.md",
  callback = function(ev)
    local rel = note_path(ev.buf)
    if not (rel and NV.follow) then return end
    local line = vim.fn.line(".")
    if rel == NV.shown and line == NV.sent then return end   -- debounce: only when it moved
    if rel ~= NV.shown then return viewer_show(rel, line) end
    NV.sent = line
    notesview({ "scroll", rel, "--line", tostring(line) })
  end,
})

-- ── Macros that survive restarts ─────────────────────────────────
-- Record with qa … q, then run :SaveMacro a. Edit/delete them with :Macros.
local MACROS = vim.fn.stdpath("config") .. "/macros.lua"
if uv.fs_stat(MACROS) then dofile(MACROS) end

vim.api.nvim_create_user_command("SaveMacro", function(opts)
  local reg = opts.args
  local content = vim.fn.getreg(reg)
  if content == "" then return vim.notify("Register " .. reg .. " is empty", vim.log.levels.WARN) end
  local escaped = content:gsub('[%c\\"]', function(c) return ("\\%03d"):format(c:byte()) end)
  vim.fn.writefile({ ('vim.fn.setreg("%s", "%s")'):format(reg, escaped) }, MACROS, "a")
  vim.notify("Saved macro @" .. reg .. " — it will load every time")
end, { nargs = 1 })

vim.api.nvim_create_user_command("Macros", function() vim.cmd.edit(MACROS) end, {})
vim.api.nvim_create_user_command("Today", function() daily(0) end, {})

-- ── Global shortcuts ─────────────────────────────────────────────
local map = vim.keymap.set
map("n", "<leader>nn", function()
  vim.ui.input({ prompt = "Note title: " }, function(title)
    if not title or vim.trim(title) == "" then return end
    local slug = title:lower():gsub("[^%w]+", "-"):gsub("^%-+", ""):gsub("%-+$", "")
    if slug == "" then slug = os.date("note-%Y%m%d-%H%M%S") end
    pick_folder("Folder for the note (Esc: top level)", function(f)
      open_note(NOTES .. "/" .. (f and (f.path .. "/") or "") .. slug .. ".md", title)
    end)
  end)
end, { desc = "New note (pick a folder)" })
map("n", "<leader>nd", function() daily(0) end, { desc = "Today's daily note" })
map("n", "<leader>ny", function() daily(-1) end, { desc = "Yesterday's daily note" })
map("n", "<leader>ni", capture_to_inbox, { desc = "Capture a task: text, folder, due date" })
map("n", "<leader>nI", function() open_note(NOTES .. "/inbox.md", "Inbox") end, { desc = "Open inbox" })
map("n", "<leader>nf", function() Snacks.picker.files({ cwd = NOTES }) end, { desc = "Find note" })
map("n", "<leader>ng", function() Snacks.picker.grep({ cwd = NOTES }) end, { desc = "Search inside notes" })
map("n", "<leader>no", open_tasks_picker, { desc = "Open tasks across notes" })
map("n", "<leader>nr", function() Snacks.picker.recent({ filter = { cwd = NOTES } }) end, { desc = "Recent notes" })
map("n", "<leader>p", function()
  local rel = note_path(0)
  if rel then viewer_show(rel, vim.fn.line(".")) else vim.notify("Not a note in " .. NOTES, vim.log.levels.WARN) end
end, { desc = "Show note in viewer" })
map("n", "<leader>o", function()
  NV.follow = not NV.follow
  vim.notify("Viewer follow mode " .. (NV.follow and "on" or "off"))
  if NV.follow then NV.shown = nil end
end, { desc = "Toggle viewer follow mode" })
map("n", "<leader>nt", function() notesview({ "open", "--tasks" }) end, { desc = "Open Tasks view" })
map("n", "<leader>?", function() Snacks.picker.keymaps() end, { desc = "Search all shortcuts" })
map("n", "<leader>w", "<cmd>write<cr>", { desc = "Save" })
map("n", "<leader>q", "<cmd>quit<cr>", { desc = "Quit window" })

-- ── Markdown-only shortcuts ──────────────────────────────────────
vim.api.nvim_create_autocmd("FileType", {
  pattern = "markdown",
  callback = function(ev)
    local l = vim.opt_local
    l.wrap, l.linebreak, l.breakindent = true, true, true
    l.spell, l.spelllang = true, "en_us"
    l.conceallevel = 0
    pcall(vim.treesitter.start, ev.buf)

    local function bmap(mode, lhs, rhs, desc, opts)
      vim.keymap.set(mode, lhs, rhs, vim.tbl_extend("force", { buffer = ev.buf, desc = desc }, opts or {}))
    end

    -- Navigation
    bmap({ "n", "x" }, "j", "v:count == 0 ? 'gj' : 'j'", "Down (visual line)", { expr = true })
    bmap({ "n", "x" }, "k", "v:count == 0 ? 'gk' : 'k'", "Up (visual line)", { expr = true })
    bmap("n", "<CR>", follow_link, "Follow link under cursor")
    bmap("n", "<BS>", "<C-o>", "Go back")
    bmap("n", "]]", "<cmd>call search('^#\\+ ', 'W')<cr>", "Next heading")
    bmap("n", "[[", "<cmd>call search('^#\\+ ', 'bW')<cr>", "Previous heading")

    -- Tasks
    bmap("n", "<leader>x", function() local r = vim.fn.line("."); toggle_checkbox_range(r, r) end, "Toggle checkbox")
    bmap("x", "<leader>x", function()
      local a, b = vim.fn.line("v"), vim.fn.line(".")
      toggle_checkbox_range(math.min(a, b), math.max(a, b))
      vim.cmd("normal! \27")
    end, "Toggle checkboxes")
    bmap("n", "<leader>a", archive_done, "Archive completed tasks")

    -- Headings
    for i = 1, 4 do
      bmap("n", "<leader>" .. i, function()
        set_heading(i)
        vim.cmd("startinsert!")   -- keep typing at the end of the heading
      end, "Heading " .. i)
    end
    bmap("n", "<leader>0", function() set_heading(0) end, "Remove heading")
    bmap("n", "<leader>+", function() change_heading(1) end, "Heading level +1")
    bmap("n", "<leader>-", function() change_heading(-1) end, "Heading level -1")

    -- Inserts
    bmap("n", "<leader>c", insert_code_block, "Insert code block")
    bmap("n", "<leader>d", function() insert_text(os.date("%Y-%m-%d")) end, "Insert date")
    bmap("n", "<leader>T", function() insert_text(os.date("%H:%M")) end, "Insert time")
    bmap("n", "<leader>h", function()
      local row = vim.api.nvim_win_get_cursor(0)[1]
      vim.api.nvim_buf_set_lines(0, row, row, false, { "", "---", "" })
      vim.api.nvim_win_set_cursor(0, { row + 3, 0 })
    end, "Insert divider")
    bmap("n", "<leader>t", "<cmd>TableModeToggle<cr>", "Table mode")
    bmap("n", "<leader>v", "<cmd>PasteImage<cr>", "Paste image from clipboard")

    -- Move lines
    bmap("n", "<leader>j", "<cmd>move .+1<cr>==", "Move line down")
    bmap("n", "<leader>k", "<cmd>move .-2<cr>==", "Move line up")
    bmap("x", "J", ":move '>+1<cr>gv=gv", "Move selection down", { silent = true })
    bmap("x", "K", ":move '<-2<cr>gv=gv", "Move selection up", { silent = true })

    -- Formatting (select text first)
    bmap("x", "<leader>b", 'c**<C-r>"**<Esc>', "Bold")
    bmap("x", "<leader>i", 'c*<C-r>"*<Esc>', "Italic")
    bmap("x", "<leader>c", 'c`<C-r>"`<Esc>', "Inline code")
    bmap("x", "<leader>s", 'c~~<C-r>"~~<Esc>', "Strikethrough")
    bmap("x", "<leader>l", 'c[<C-r>"]()<Esc>i', "Make link (cursor lands in URL)")
    bmap("x", "<leader>w", 'c[[<C-r>"]]<Esc>', "Make wiki link")
  end,
})

-- Moved tasks ("- [>] … → [[date]]") are shown greyed out
local function set_task_hl()
  vim.api.nvim_set_hl(0, "NotesMovedTask", { ctermfg = 8, fg = "#7d8590", italic = true, default = true })
end
set_task_hl()
vim.api.nvim_create_autocmd("ColorScheme", { callback = set_task_hl })
local task_ns = vim.api.nvim_create_namespace("notes_tasks")
vim.api.nvim_set_decoration_provider(task_ns, {
  on_win = function(_, _, buf) return vim.bo[buf].filetype == "markdown" end,
  on_line = function(_, _, buf, row)
    local l = vim.api.nvim_buf_get_lines(buf, row, row + 1, false)[1]
    if l and l:find("^%s*[-*+] %[>%]") then
      vim.api.nvim_buf_set_extmark(buf, task_ns, row, 0,
        { end_row = row, end_col = #l, hl_group = "NotesMovedTask", ephemeral = true, priority = 200 })
    end
  end,
})

-- Convert natural due dates on the line just edited (runs before autosave below)
vim.api.nvim_create_autocmd("InsertLeave", {
  pattern = "*.md",
  callback = function(ev)
    if vim.bo[ev.buf].filetype == "markdown" then convert_due_dates(ev.buf) end
  end,
})

-- Autosave markdown when leaving insert mode or hiding the quick terminal
vim.api.nvim_create_autocmd({ "InsertLeave", "FocusLost", "BufLeave" }, {
  pattern = "*.md",
  callback = function(ev)
    if vim.bo[ev.buf].modified and vim.api.nvim_buf_get_name(ev.buf) ~= "" then
      vim.api.nvim_buf_call(ev.buf, function() vim.cmd("silent! write") end)
    end
  end,
})
vim.api.nvim_create_autocmd("FocusGained", { command = "silent! checktime" })
vim.opt.shortmess:append("I")

local starter = require("mini.starter")
local notes = vim.fn.expand("~/notes") 

local function greeting()
  local h = tonumber(os.date("%H"))
  local part = h < 5 and "night" or h < 12 and "morning" or h < 18 and "afternoon" or "evening"
  return ("good %s, alex\n%s"):format(part, os.date("%A, %B %d"))
end

local function open_tasks()
  local f = io.open(notes .. "/inbox.md")
  if not f then return "" end
  local n = 0
  for line in f:lines() do
    if line:match("^%s*[-*+] %[ %]") then n = n + 1 end
  end
  f:close()
  return n .. " open tasks in inbox"
end

starter.setup({
  header = greeting,
  footer = open_tasks,
  items = {
    { name = "Inbox", action = "edit " .. notes .. "/inbox.md", section = "Notes" },
    { name = "Notes folder", action = "edit " .. notes, section = "Notes" },
    starter.sections.recent_files(5, true),
    starter.sections.recent_files(5, false),
    starter.sections.builtin_actions(),
  },
  content_hooks = {
    starter.gen_hook.adding_bullet("» "),
    starter.gen_hook.aligning("center", "center"),
  },
})
