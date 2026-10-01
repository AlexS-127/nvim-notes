-- ~/.config/nvim/init.lua — markdown notes (Neovim 0.12+, no plugin manager)
-- Leader is <Space>. Notes live in $NOTES_DIR (default ~/notes). Press <Space>? to search all shortcuts.

vim.g.mapleader = " "
vim.g.maplocalleader = " "
local NOTES = vim.fn.expand((vim.env.NOTES_DIR and vim.env.NOTES_DIR ~= "") and vim.env.NOTES_DIR or "~/notes")
local uv = vim.uv or vim.loop

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
}, { confirm = false })

-- Code-block syntax highlighting (needs: brew install tree-sitter-cli)
vim.g.notes_parsers = {
  "bash", "python", "javascript", "typescript", "tsx", "json", "yaml", "toml",
  "html", "css", "go", "rust", "sql", "swift", "lua", "c",
}
require("nvim-treesitter").install(vim.g.notes_parsers)
require("snacks").setup({ picker = { enabled = true }, input = { enabled = true } })
require("img-clip").setup({ default = { dir_path = "assets", relative_to_current_file = true } })

-- ── Helpers ──────────────────────────────────────────────────────
local function open_note(path, title)
  if not path:match("%.md$") and uv.fs_stat(path) then return vim.ui.open(path) end
  vim.fn.mkdir(vim.fn.fnamemodify(path, ":h"), "p")
  local exists = uv.fs_stat(path)
  vim.cmd.edit(vim.fn.fnameescape(path))
  if not exists then
    vim.api.nvim_buf_set_lines(0, 0, -1, false, { "# " .. title, "", "" })
    vim.api.nvim_win_set_cursor(0, { 3, 0 })
  end
end

local function daily(offset_days)
  local t = os.time() + (offset_days or 0) * 86400
  open_note(NOTES .. "/daily/" .. os.date("%Y-%m-%d", t) .. ".md", os.date("%A, %B %d %Y", t))
end

local function toggle_checkbox_line(line)
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
      return open_note(NOTES .. "/" .. target .. (target:match("%.md$") and "" or ".md"), target)
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

local function capture_to_inbox()
  vim.ui.input({ prompt = "Capture: " }, function(text)
    if not text or text == "" then return end
    local f = NOTES .. "/inbox.md"
    vim.fn.mkdir(NOTES, "p")
    if not uv.fs_stat(f) then vim.fn.writefile({ "# Inbox", "" }, f) end
    vim.fn.writefile({ "- [ ] " .. text .. " _(" .. os.date("%b %d %H:%M") .. ")_" }, f, "a")
    vim.cmd("checktime")
    vim.notify("Captured to inbox")
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
  vim.system(vim.list_extend({ "notesview" }, args), { env = { NOTES_DIR = NOTES }, stdout = false, stderr = false }, function() end)
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
    if not title or title == "" then return end
    local slug = title:lower():gsub("[^%w]+", "-"):gsub("^%-+", ""):gsub("%-+$", "")
    open_note(NOTES .. "/" .. slug .. ".md", title)
  end)
end, { desc = "New note" })
map("n", "<leader>nd", function() daily(0) end, { desc = "Today's daily note" })
map("n", "<leader>ny", function() daily(-1) end, { desc = "Yesterday's daily note" })
map("n", "<leader>ni", capture_to_inbox, { desc = "Quick capture to inbox" })
map("n", "<leader>nI", function() open_note(NOTES .. "/inbox.md", "Inbox") end, { desc = "Open inbox" })
map("n", "<leader>nf", function() Snacks.picker.files({ cwd = NOTES }) end, { desc = "Find note" })
map("n", "<leader>ng", function() Snacks.picker.grep({ cwd = NOTES }) end, { desc = "Search inside notes" })
map("n", "<leader>no", function() Snacks.picker.grep({ cwd = NOTES, search = "- \\[ \\]" }) end,
  { desc = "Open todos across notes" })
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
    for i = 1, 4 do bmap("n", "<leader>" .. i, function() set_heading(i) end, "Heading " .. i) end
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
