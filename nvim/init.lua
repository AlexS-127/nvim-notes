-- ~/.config/nvim/init.lua — markdown notes (Neovim 0.12+, no plugin manager)
-- Leader is <Space>. Notes live in $NOTES_DIR (default ~/notes). Press <Space>? to search all shortcuts.

-- Ghostty starts nvim without a login shell, so PATH can be empty: add the usual places.
do
  local extra = { vim.fn.expand("~/.local/bin"), "/opt/homebrew/bin", "/usr/local/bin", "/usr/bin", "/bin", "/usr/sbin", "/sbin" }
  local cur = vim.env.PATH or ""
  for _, d in ipairs(extra) do
    if not (":" .. cur .. ":"):find(":" .. d .. ":", 1, true) then cur = cur == "" and d or (cur .. ":" .. d) end
  end
  vim.env.PATH = cur
end

vim.g.mapleader = " "
vim.g.maplocalleader = " "
local NOTES = vim.fn.expand((vim.env.NOTES_DIR and vim.env.NOTES_DIR ~= "") and vim.env.NOTES_DIR or "~/notes")
local uv = vim.uv or vim.loop
-- Oldest notesview this config works with. Bump together with `version` in renderer/main.go.
local NOTESVIEW_MIN_VERSION = "0.11.0"

-- ── Options ──────────────────────────────────────────────────────
local o = vim.opt
o.termguicolors = true           -- needed for Catppuccin's true colours
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

-- ── Theme: notesview's light / dark themes (default Borland / Catppuccin mocha), follows macOS ─
require("catppuccin").setup({
  background = { light = "latte", dark = "mocha" },
  transparent_background = true,   -- keep the terminal's own background (Ghostty's opacity + blur)
  float = { transparent = true },
  -- Transparent text surfaces stay transparent, but popups need a solid fill to be
  -- readable over buffer text, and the dimmest greys need lifting against the blur.
  custom_highlights = function(C)
    return {
      NormalFloat  = { fg = C.text, bg = C.mantle },
      Pmenu        = { fg = C.text, bg = C.mantle },
      PmenuSel     = { fg = C.text, bg = C.surface1, bold = true },
      PmenuSbar    = { bg = C.surface0 },
      PmenuThumb   = { bg = C.overlay0 },
      LineNr       = { fg = C.overlay0 },
      StatusLineNC = { fg = C.overlay1 },
      WinSeparator = { fg = C.overlay0 },
    }
  end,
})
-- The colorscheme for each appearance comes from notesview's theme_light / theme_dark (viewer
-- Settings page), see lua/themesync.lua; auto-dark-mode tells it which one applies.
local themesync = require("themesync")
themesync.setup()
themesync.apply(vim.o.background)   -- no flash before auto-dark-mode's first check
require("auto-dark-mode").setup({
  fallback = "dark",
  set_dark_mode = function() themesync.apply("dark") end,
  set_light_mode = function() themesync.apply("light") end,
})

-- Code-block syntax highlighting (needs: brew install tree-sitter-cli)
vim.g.notes_parsers = {
  "bash", "python", "javascript", "typescript", "tsx", "json", "yaml", "toml",
  "html", "css", "go", "rust", "sql", "swift", "lua", "c", "cpp",
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
-- Typing a name that matches nothing and pressing Enter picks a new folder
-- ({ path, tag, new = true }); the caller creates it.
local function pick_folder(title, cb)
  local folders = nv_json({ "folders", "--json", "--dir", NOTES })
  if not folders then return cb(nil) end
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
      if not item then
        local typed = vim.trim(picker.input and picker.input:get() or "")
        if typed ~= "" then chosen = { path = typed, tag = typed, name = typed, new = true } end
      end
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
    title = r.path,
    items = items,
    format = function(item) return { { item.title }, { "  " .. item.rel, "Comment" } } end,
    preview = "file",
    confirm = function(picker, item)
      picker:close()
      if item then vim.cmd.edit(vim.fn.fnameescape(item.file)) end
    end,
  })
end

-- Search for a folder, then pick a note inside it (start page `o`, <Space>nF).
local function go_to_folder()
  pick_folder("Go to folder", function(f)
    if not f then return end
    if f.new then return vim.notify("No folder called " .. f.path) end
    local r = nv_json({ "resolve", "--json", "--dir", NOTES, "--", f.tag })
    if r and r.kind == "folder" then return folder_notes_picker(r) end
    vim.notify("Could not open folder " .. f.path, vim.log.levels.WARN)
  end)
end

-- Delete the note being edited (<Space>nD): confirm, move the file to the Trash
-- (`trash`, else ~/.Trash), close the buffer and go Home.
local function delete_current_note()
  local buf = vim.api.nvim_get_current_buf()
  local path = vim.api.nvim_buf_get_name(buf)
  if path == "" or vim.bo[buf].buftype ~= "" or vim.fn.filereadable(path) == 0 then
    return vim.notify("Not a note file", vim.log.levels.WARN)
  end
  if vim.fn.confirm("Delete " .. vim.fn.fnamemodify(path, ":t") .. "?", "&Yes\n&No", 2) ~= 1 then return end
  local cmd = vim.fn.executable("trash") == 1 and { "trash", path } or { "mv", path, vim.fn.expand("~/.Trash/") }
  local r = vim.system(cmd, { text = true }):wait()
  if r.code ~= 0 then
    return vim.notify("Delete failed: " .. (r.stderr or ""), vim.log.levels.ERROR)
  end
  vim.cmd("silent! Home")
  pcall(vim.cmd, "bdelete! " .. buf)
  vim.notify("Moved to Trash: " .. vim.fn.fnamemodify(path, ":t"))
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
  -- ticking stamps the moment (✅ YYYY-MM-DD HH:MM) for the viewer's Activity view and the
  -- score curve; unticking removes it (older stamps have the date only)
  if line:match("^%s*[-*+] %[ %]") then
    local l = line:gsub("%[ %]", "[x]", 1)
    if not l:find("✅ %d%d%d%d%-%d%d%-%d%d") then l = l:gsub("%s+$", "") .. " ✅ " .. os.date("%Y-%m-%d %H:%M") end
    pcall(function() require("signals").task("done", line) end)
    return l
  end
  if line:match("^%s*[-*+] %[[xX]%]") then
    pcall(function() require("signals").task("undone", line) end)
    local l = line:gsub("%[[xX]%]", "[ ]", 1)
    l = l:gsub("%s*✅ %d%d%d%d%-%d%d%-%d%d %d%d:%d%d", "")
    return (l:gsub("%s*✅ %d%d%d%d%-%d%d%-%d%d", ""))
  end
  if line:match("^%s*[-*+] ") then return (line:gsub("^(%s*[-*+] )", "%1[ ] ", 1)) end
  return (line:gsub("^(%s*)", "%1- [ ] ", 1))
end

-- Ticking moves a task (with its nested lines) to the end of `## Done`; unticking one in
-- Done moves it back with the open tasks. Mirrors renderer/done.go.
local function indent_of(l) return #l:match("^[ \t]*") end

local function task_end(lines, i)
  local e, ind = i + 1, indent_of(lines[i])
  while e <= #lines and lines[e]:match("%S") and indent_of(lines[e]) > ind do e = e + 1 end
  return e  -- exclusive
end

local function done_head(lines)
  for i, l in ipairs(lines) do if l:match("^##%s+Done%s*$") then return i end end
end

local function done_end(lines, head)
  for i = head + 1, #lines do if lines[i]:match("^##?%s") then return i end end
  return #lines + 1
end

local function take_block(lines, i)
  local e, block = task_end(lines, i), {}
  for k = i, e - 1 do block[#block + 1] = lines[k] end
  for _ = i, e - 1 do table.remove(lines, i) end
  return block
end

local function insert_at(lines, at, block)
  for k, l in ipairs(block) do table.insert(lines, at + k - 1, l) end
end

local function move_to_done(lines, i)
  local head = done_head(lines)
  if head and i > head and i < done_end(lines, head) then return false end
  local block = take_block(lines, i)
  head = done_head(lines)
  if not head then
    while #lines > 0 and not lines[#lines]:match("%S") do table.remove(lines) end
    lines[#lines + 1] = ""
    lines[#lines + 1] = "## Done"
    insert_at(lines, #lines + 1, block)
    return true
  end
  local at = done_end(lines, head)
  while at > head + 1 and not lines[at - 1]:match("%S") do at = at - 1 end
  insert_at(lines, at, block)
  return true
end

local function move_from_done(lines, i)
  local head = done_head(lines)
  if not head or i < head or i >= done_end(lines, head) then return end
  local block = take_block(lines, i)
  head = done_head(lines)
  local at, k = nil, 1
  while k < head do
    if lines[k]:match("^%s*[-*+] %[[ xX>]%]") then at = math.min(task_end(lines, k), head); k = at else k = k + 1 end
  end
  if not at then
    at = head
    while at > 1 and not lines[at - 1]:match("%S") do at = at - 1 end
  end
  insert_at(lines, at, block)
end

local function toggle_checkbox_range(first, last, buf)
  buf = buf or 0   -- another buffer: no cursor to restore
  local lines = vim.api.nvim_buf_get_lines(buf, 0, -1, false)
  local was_done = {}
  for i = first, last do
    was_done[i] = lines[i]:match("^%s*[-*+] %[[xX]%]") ~= nil
    lines[i] = toggle_checkbox_line(lines[i])
  end
  local cursor = buf == 0 and vim.api.nvim_win_get_cursor(0)
  for i = last, first, -1 do  -- bottom first so the rows above stay put
    if lines[i]:match("^%s*[-*+] %[ %]") and was_done[i] then move_from_done(lines, i) end
  end
  local i = first
  while i <= last do  -- top down, so ticked tasks land in Done in the order they were
    local e = task_end(lines, i)
    if lines[i]:match("^%s*[-*+] %[[xX]%]") and not was_done[i] and move_to_done(lines, i) then
      last = last - (e - i)  -- the block left; the next row is now at i
    else
      i = i + 1
    end
  end
  vim.api.nvim_buf_set_lines(buf, 0, -1, false, lines)
  if cursor then vim.api.nvim_win_set_cursor(0, { math.min(cursor[1], #lines), cursor[2] }) end
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

local function ask_difficulty(cb)
  vim.ui.input({ prompt = "Difficulty (1-3, 3 hardest — empty to skip): " }, function(answer)
    answer = vim.trim((answer or ""):gsub("^!", ""))
    if answer == "" then return cb("") end
    if not answer:match("^[123]$") then
      vim.notify(('Difficulty must be 1, 2 or 3, not "%s"'):format(answer), vim.log.levels.WARN)
      return ask_difficulty(cb)
    end
    cb(answer)
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
      local function write(diff)
        local args = { "capture", "--dir", NOTES, "--folder", folder or "", "--due", due or "" }
        if diff ~= "" then vim.list_extend(args, { "--difficulty", diff }) end
        vim.list_extend(args, { "--", text })
        local out, err = nv_sync(args)
        if not out then return vim.notify("Capture failed: " .. err, vim.log.levels.ERROR) end
        vim.cmd("checktime")
        vim.notify(vim.trim(out))
      end
      if parsed.difficulty and parsed.difficulty ~= 0 then return write("") end
      ask_difficulty(write)
    end
    local function due_step(folder)
      if parsed.due and parsed.due ~= "" then return save(folder, "") end
      ask_due(function(due) save(folder, due) end)
    end
    if parsed.folder and parsed.folder ~= "" then return due_step(nil) end
    pick_folder("Folder for the task (type a new name to create it; Esc: none)", function(f) due_step(f and f.tag) end)
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

-- Task actions (Enter in the task picker): tick, copy, edit, change due date or difficulty.
-- Edits go through the file's buffer (loaded hidden if needed) and are written straight away.
local STAMP = "%s+_%b()_%s*$"   -- created stamp at the end of the line

-- Column (1-based, inclusive) where the task's text ends: the trailing created stamp,
-- difficulty, due date and tags are not part of it.
local function task_text_end(line)
  local n = #line:gsub("%s+$", "")
  local floor = #(line:match("^%s*[-*+] %[.%]%s*") or "")
  local pats = { "%s+_%b()_$", "%s+@%d%d%d%d%-%d%d%-%d%d$", "%s+![123]$", "%s+#%S+$" }
  local found = true
  while found do
    found = false
    for _, pat in ipairs(pats) do
      local a = line:sub(1, n):find(pat)
      if a and a - 1 > floor then n, found = a - 1, true end
    end
  end
  return n
end

-- The task's words alone: no checkbox, tags, due date, difficulty or stamps.
local function task_plain_text(t)
  local s = " " .. t.text .. " "
  s = s:gsub("%s_%b()_", " "):gsub("%s#%S+", " "):gsub("%s@%d%d%d%d%-%d%d%-%d%d", " "):gsub("%s![123]%f[%W]", " ")
  s = vim.trim(s:gsub("%s%s+", " "))
  return s ~= "" and s or t.display
end

local function set_task_due(line, date)
  local pat = "(%s)@%d%d%d%d%-%d%d%-%d%d%f[%W]"
  if line:find(pat) then return (line:gsub(pat, date ~= "" and ("%1@" .. date) or "", 1)) end
  if date == "" then return line end
  local at = line:find("%s+![123]%f[%W]") or line:find(STAMP)
  if not at then return (line:gsub("%s+$", "")) .. " @" .. date end
  return line:sub(1, at - 1) .. " @" .. date .. line:sub(at)
end

local function set_task_difficulty(line, diff)
  local pat = "(%s)![123]%f[%W]"
  if line:find(pat) then return (line:gsub(pat, diff ~= "" and ("%1!" .. diff) or "", 1)) end
  if diff == "" then return line end
  local at = line:find(STAMP)
  if not at then return (line:gsub("%s+$", "")) .. " !" .. diff end
  return line:sub(1, at - 1) .. " !" .. diff .. line:sub(at)
end

-- Runs fn(buf, line) on a task's line if it is still where the picker saw it.
-- A note that is open in this instance is edited in its buffer. Otherwise the file is edited in a
-- scratch buffer and written straight to disk: loading it as a real buffer hits E325 whenever another
-- nvim instance has the note open, which aborts the load and leaves an empty buffer behind.
local function with_task_line(t, fn)
  local path = NOTES .. "/" .. t.file
  local buf = vim.fn.bufnr(path)
  local open = buf > 0 and vim.api.nvim_buf_is_loaded(buf)
  if not open then
    if vim.fn.filereadable(path) == 0 then return vim.notify("Could not read " .. t.file, vim.log.levels.ERROR) end
    buf = vim.api.nvim_create_buf(false, true)
    vim.api.nvim_buf_set_lines(buf, 0, -1, false, vim.fn.readfile(path))
  end
  local function finish(write)
    if write then write() end
    if not open then vim.api.nvim_buf_delete(buf, { force = true }) end
  end
  local line = vim.api.nvim_buf_get_lines(buf, t.line - 1, t.line, false)[1]
  if not (line and line:match("^%s*[-*+] %[ %]") and line:find(t.text, 1, true)) then
    finish()
    return vim.notify("That task has moved: reopen the task list", vim.log.levels.WARN)
  end
  local dirty = open and vim.bo[buf].modified
  fn(buf, line)
  if dirty then return vim.notify("Changed in the open buffer (it has unsaved edits, so not written)", vim.log.levels.WARN) end
  finish(function()
    if open then
      vim.api.nvim_buf_call(buf, function() vim.cmd("silent write") end)
    elseif vim.fn.writefile(vim.api.nvim_buf_get_lines(buf, 0, -1, false), path) ~= 0 then
      vim.notify("Could not write " .. t.file, vim.log.levels.ERROR)
    end
  end)
end

local function edit_task_line(t, change, msg)
  with_task_line(t, function(buf, line)
    vim.api.nvim_buf_set_lines(buf, t.line - 1, t.line, false, { change(line) })
    vim.notify(msg)
  end)
end

local function task_actions(item)
  local t = item.task
  local actions = {
    { "t", "Tick off", function()
      with_task_line(t, function(buf) toggle_checkbox_range(t.line, t.line, buf); vim.notify("Done: " .. t.display) end)
    end },
    { "c", "Copy text", function()
      local s = task_plain_text(t)
      vim.fn.setreg("+", s); vim.fn.setreg('"', s)
      vim.notify("Copied: " .. s)
    end },
    { "e", "Edit task", function()
      vim.cmd.edit(vim.fn.fnameescape(item.file))
      local line = vim.api.nvim_buf_get_lines(0, t.line - 1, t.line, false)[1] or ""
      local col = task_text_end(line)
      pcall(vim.api.nvim_win_set_cursor, 0, { t.line, math.max(0, col - 1 + vim.str_utf_start(line, col)) })
    end },
    { "d", "Edit due date", function()
      ask_due(function(date) edit_task_line(t, function(l) return set_task_due(l, date) end,
        date == "" and "Due date removed" or ("Due " .. date)) end)
    end },
    { "f", "Edit difficulty", function()
      ask_difficulty(function(d) edit_task_line(t, function(l) return set_task_difficulty(l, d) end,
        d == "" and "Difficulty removed" or ("Difficulty " .. d)) end)
    end },
  }
  -- one keypress picks: no list to scroll, no Enter to confirm
  local chunks = { { t.display .. "\n", "Title" } }
  for _, a in ipairs(actions) do
    vim.list_extend(chunks, { { "[" .. a[1] .. "]", "Special" }, { " " .. a[2] .. "   " } })
  end
  vim.api.nvim_echo(chunks, false, {})
  vim.cmd("redraw")
  local ok, key = pcall(vim.fn.getcharstr)
  vim.cmd("echo ''")
  if not ok then return end
  for _, a in ipairs(actions) do
    if key:lower() == a[1] then return a[3]() end
  end
end

-- Run claude from ~ outside nvim (optionally with a first message). Without a launcher it runs through `:!`
-- instead and returns to nvim afterwards. The launcher (ghostty-nvim, or the
-- `notes` shell function) sets $NVIM_NOTES_CLAUDE to a file path; nvim writes the prompt there and
-- quits, then the launcher runs claude in the real terminal and leaves a normal shell when it exits.
local function open_claude(prompt)
  pcall(function() require("signals").emit("claude") end)
  local handoff = vim.env.NVIM_NOTES_CLAUDE
  if not handoff or handoff == "" then
    -- Not started through a launcher (plain `nvim`, restored window, ...): run claude in this terminal
    -- from ~ with `:!` and come back to nvim when it exits.
    pcall(vim.cmd, "silent! wall")
    local cmd = 'cd ~ && PATH="$HOME/.local/bin:$PATH" claude'
    if prompt and prompt ~= "" then cmd = cmd .. " " .. vim.fn.shellescape(prompt) end
    vim.cmd("!" .. vim.fn.escape(cmd, "!%#"))
    return vim.cmd("redraw!")
  end
  pcall(vim.cmd, "silent! wall")
  if vim.fn.writefile(vim.split(prompt or "", "\n", { plain = true }), handoff) ~= 0 then
    return vim.notify("claude: can't write " .. handoff, vim.log.levels.ERROR)
  end
  local ok, err = pcall(vim.cmd, "qa")
  if not ok then   -- e.g. an unsaved unnamed buffer: stay in nvim and cancel the handoff
    os.remove(handoff)
    vim.notify("claude: " .. tostring(err), vim.log.levels.ERROR)
  end
end

-- Shift+Enter on a task: run claude from ~ with the task as its first message.
local function claude_it(item)
  local t = item.task
  open_claude(table.concat({
    "Task from my notes (" .. t.file .. ":" .. t.line .. "): " .. task_plain_text(t),
    "",
    "Do this task now, from this directory (~). When you are done, if the work changed files in any git repo,",
    "commit them in that repo and push to main. Don't commit or push repos you didn't change.",
  }, "\n"))
end

local function open_tasks_picker()
  local tasks = nv_json({ "tasks", "--json", "--dir", NOTES })
  if type(tasks) ~= "table" then   -- no (or an older) notesview: plain grep
    return Snacks.picker.grep({ cwd = NOTES, search = "- \\[ \\]" })
  end
  local when = { overdue = "overdue", today = "today", tomorrow = "tomorrow", week = "this week", week2 = "next week", week3 = "2+ weeks", later = "later", none = "" }
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
  if #items == 0 then return vim.notify("No open tasks") end
  Snacks.picker.pick({
    title = "Open tasks (by due date)",
    items = items,
    sort = { fields = { "idx" } },   -- keep notesview's order (due date, newest first) while filtering
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
      if item then vim.schedule(function() task_actions(item) end) end
    end,
    actions = {
      claude_it = function(picker, item)
        picker:close()
        if item then vim.schedule(function() claude_it(item) end) end
      end,
    },
    win = { input = { keys = { ["<S-CR>"] = { "claude_it", mode = { "n", "i" } } } } },
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

-- ── Window title: "folder · note" for notes (e.g. "act200 · accruals"), the file name otherwise ──
-- Ghostty shows it, and the notesview-sense window sensor categorises terminal time from it.
function _G.NotesTitle()
  local name = vim.api.nvim_buf_get_name(0)
  if name == "" or vim.bo.filetype == "ministarter" then return "nvim · notes" end
  if name:sub(1, #NOTES + 1) == NOTES .. "/" then
    local rel = name:sub(#NOTES + 2)
    local folder, file = rel:match("^([^/]+)/.-([^/]+)%.md$")
    if folder then return folder .. " · " .. file end
    return (rel:gsub("%.md$", ""))
  end
  return vim.fn.fnamemodify(name, ":t") .. " — nvim"
end
vim.o.title = true
vim.o.titlestring = "%{v:lua.NotesTitle()}"

-- ── Workflow signals for the data layer (lua/signals.lua; counts only) ──
pcall(function() require("signals").setup() end)

-- Spaced-repetition revision (notesview revise): schedule the current note, or take it off.
local function revise_cmd(sub)
  return function()
    local file = vim.api.nvim_buf_get_name(0)
    if not vim.startswith(file, NOTES .. "/") then return vim.notify("Not a note in " .. NOTES, vim.log.levels.WARN) end
    local out, err = nv_sync({ "revise", sub, file:sub(#NOTES + 2) })
    vim.notify(vim.trim(out or err or ""), out and vim.log.levels.INFO or vim.log.levels.WARN)
  end
end
vim.api.nvim_create_user_command("Revise", revise_cmd("add"), {})
vim.api.nvim_create_user_command("ReviseRemove", revise_cmd("remove"), {})

-- ── Global shortcuts ─────────────────────────────────────────────
local map = vim.keymap.set
map("n", "<leader>nn", function()
  vim.ui.input({ prompt = "Note title: " }, function(title)
    if not title or vim.trim(title) == "" then return end
    local slug = title:lower():gsub("[^%w]+", "-"):gsub("^%-+", ""):gsub("%-+$", "")
    if slug == "" then slug = os.date("note-%Y%m%d-%H%M%S") end
    pick_folder("Folder for the note (type a new name to create it; Esc: top level)", function(f)
      if f and f.new then
        f.path = f.path:gsub("\\", "/"):gsub("^/+", ""):gsub("/+$", "")
        vim.fn.mkdir(NOTES .. "/" .. f.path, "p")
      end
      open_note(NOTES .. "/" .. (f and (f.path .. "/") or "") .. slug .. ".md", title)
    end)
  end)
end, { desc = "New note (pick a folder)" })
map("n", "<leader>nd", function() daily(0) end, { desc = "Today's daily note" })
map("n", "<leader>ny", function() daily(-1) end, { desc = "Yesterday's daily note" })
map("n", "<leader>ni", capture_to_inbox, { desc = "Capture a task: text, folder, due date" })
map("n", "<leader>nI", function() open_note(NOTES .. "/inbox.md", "Inbox") end, { desc = "Open inbox" })
map("n", "<leader>nf", function() Snacks.picker.files({ cwd = NOTES }) end, { desc = "Find note" })
map("n", "<leader>nD", delete_current_note, { desc = "Delete this note (to Trash)" })
map("n", "<leader>nF", go_to_folder, { desc = "Go to folder (search, then pick a note)" })
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
map("n", "<leader>nC", function() open_claude() end, { desc = "Claude (in ~, quits nvim)" })
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

-- Latin macrons, only in notes under $NOTES_DIR/lat101: in insert mode ; then a vowel
-- types its macron (;a→ā, ;E→Ē). Any other key after ; types the ; and that key as usual.
local MACRONS = {
  a = "ā", e = "ē", i = "ī", o = "ō", u = "ū", y = "ȳ",
  A = "Ā", E = "Ē", I = "Ī", O = "Ō", U = "Ū", Y = "Ȳ",
}
vim.api.nvim_create_autocmd({ "BufRead", "BufNewFile", "BufEnter" }, {
  pattern = NOTES .. "/lat101/*",
  callback = function(ev)
    vim.keymap.set("i", ";", function()
      local ok, ch = pcall(vim.fn.getcharstr)
      if not ok then return ";" end
      return MACRONS[ch] or (";" .. ch)
    end, { buffer = ev.buf, expr = true, desc = "Latin macron (; then a/e/i/o/u/y)" })
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

-- ── Startup page (lua/start.lua) ─────────────────────────────────
require("start").setup()

-- ── Competitive programming / LeetCode (lua/cp.lua) ──────────────
local ok, err = pcall(require, "cp")
if not ok then vim.notify("cp failed to load: " .. tostring(err), vim.log.levels.WARN) end

-- ── UI: which-key, statusline, completion (lua/ui.lua) ───────────
local ok_ui, err_ui = pcall(require, "ui")
if not ok_ui then vim.notify("ui failed to load: " .. tostring(err_ui), vim.log.levels.WARN) end

-- ── Extras (optional power-ups; see nvim/lua/extras/init.lua) ────
-- Off for one session: NVIM_NOTES_PLAIN=1 nvim. Off for good: delete this block.
if vim.g.notes_extras ~= false and (vim.env.NVIM_NOTES_PLAIN or "") == "" then
  local ok, err = pcall(require, "extras")
  if not ok then vim.notify("extras failed to load: " .. tostring(err), vim.log.levels.WARN) end
end
