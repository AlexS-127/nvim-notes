-- extras: optional power-ups for the notes config. Loaded by one line at the end of init.lua.
-- Disable without touching any code:
--   NVIM_NOTES_PLAIN=1 nvim      (one session)
--   vim.g.notes_extras = false   (set before the require in init.lua, permanently)
-- Remove entirely: delete this folder and the `require("extras")` line in init.lua.
local M = {}

local uv = vim.uv or vim.loop
local NOTES = vim.fn.expand((vim.env.NOTES_DIR and vim.env.NOTES_DIR ~= "") and vim.env.NOTES_DIR or "~/notes")
local NOTES_REAL = uv.fs_realpath(NOTES) or NOTES

local function warn(msg) vim.notify(msg, vim.log.levels.WARN) end
local function try_require(name)
  local ok, mod = pcall(require, name)
  return ok and mod or nil
end

-- ── Plugins ──────────────────────────────────────────────────────
-- All optional: if a download fails the rest of the config still loads.
local ok_pack, pack_err = pcall(vim.pack.add, {
  "https://github.com/folke/which-key.nvim",
  "https://github.com/folke/flash.nvim",
  "https://github.com/nvim-mini/mini.surround",
  "https://github.com/nvim-mini/mini.pairs",
  "https://github.com/MeanderingProgrammer/render-markdown.nvim",
}, { confirm = false })
if not ok_pack then warn("extras: plugin install failed (" .. tostring(pack_err) .. ")") end

local wk = try_require("which-key")
if wk then
  wk.setup({ delay = 400, preset = "helix", icons = { mappings = false } })
  wk.add({
    { "<leader>n", group = "notes" },
    { "gs", group = "surround" },
  })
end

local flash = try_require("flash")
if flash then
  flash.setup({ modes = { search = { enabled = false }, char = { enabled = false } } })
  vim.keymap.set({ "n", "x", "o" }, "s", function() flash.jump() end, { desc = "Flash: jump to text" })
  vim.keymap.set({ "n", "x", "o" }, "S", function() flash.treesitter() end, { desc = "Flash: select syntax node" })
end

local surround = try_require("mini.surround")
if surround then
  surround.setup({ mappings = {   -- gs-prefixed so `s` stays free for flash
    add = "gsa", delete = "gsd", replace = "gsr", find = "gsf", find_left = "gsF",
    highlight = "gsh", update_n_lines = "gsn",
  } })
end
local pairs_ = try_require("mini.pairs")
if pairs_ then pairs_.setup() end

local render = try_require("render-markdown")
if render then
  render.setup({
    completions = { lsp = { enabled = false } },
    checkbox = { custom = { moved = { raw = "[>]", rendered = "󰒊 ", highlight = "NotesMovedTask" } } },
    heading = { sign = false },
    code = { sign = false, width = "block", left_pad = 1, right_pad = 1 },
  })
  vim.keymap.set("n", "<leader>nm", function() render.toggle() end, { desc = "Toggle in-buffer markdown rendering" })
end

-- ── Options ──────────────────────────────────────────────────────
local o = vim.opt
o.cursorline = true
o.cursorlineopt = "number"
o.inccommand = "split"           -- live preview for :s
o.confirm = true                 -- ask instead of failing on :q with changes
o.timeoutlen = 400
o.pumheight = 12
o.jumpoptions = "view"
o.fillchars = { eob = " ", fold = " ", foldsep = " " }
o.foldlevel = 99
o.foldtext = ""                  -- show the folded line itself, syntax-highlighted
o.undolevels = 5000

-- ── Statusline: mode · note · tasks done/total · words · position ─
local MODES = { n = "NORMAL", i = "INSERT", v = "VISUAL", V = "V-LINE", ["\22"] = "V-BLOCK", c = "COMMAND", R = "REPLACE", t = "TERMINAL" }
local stats_cache = {}
local function note_stats(buf)
  local tick = vim.b[buf].changedtick
  local c = stats_cache[buf]
  if c and c.tick == tick then return c end
  local done, total = 0, 0
  for _, l in ipairs(vim.api.nvim_buf_get_lines(buf, 0, -1, false)) do
    local mark = l:match("^%s*[-*+] %[(.)%]")
    if mark and mark ~= ">" then
      total = total + 1
      if mark == "x" or mark == "X" then done = done + 1 end
    end
  end
  c = { tick = tick, done = done, total = total }
  stats_cache[buf] = c
  return c
end

function M.statusline()
  local buf = vim.api.nvim_get_current_buf()
  local mode = MODES[vim.fn.mode()] or vim.fn.mode()
  local name = vim.api.nvim_buf_get_name(buf)
  if name:sub(1, #NOTES_REAL + 1) == NOTES_REAL .. "/" then name = name:sub(#NOTES_REAL + 2)
  elseif name:sub(1, #NOTES + 1) == NOTES .. "/" then name = name:sub(#NOTES + 2)
  else name = vim.fn.fnamemodify(name, ":~:.") end
  if name == "" then name = "[no name]" end
  local right = {}
  if vim.bo[buf].filetype == "markdown" then
    local s = note_stats(buf)
    if s.total > 0 then table.insert(right, ("☑ %d/%d"):format(s.done, s.total)) end
    table.insert(right, vim.fn.wordcount().words .. "w")
  end
  table.insert(right, "%l:%c")
  return table.concat({
    "%#StatusLineMode# ", mode, " %*  ", name, vim.bo[buf].modified and " ●" or "",
    "%=", table.concat(right, "  "), " ",
  })
end
vim.o.statusline = "%!v:lua.require'extras'.statusline()"
vim.api.nvim_set_hl(0, "StatusLineMode", { bold = true, reverse = true, default = true })

-- ── Folding: markdown headings (tree-sitter) ─────────────────────
vim.api.nvim_create_autocmd("FileType", {
  pattern = "markdown",
  callback = function(ev)
    local win = vim.fn.bufwinid(ev.buf)
    if win == -1 then return end
    vim.wo[win][0].foldmethod = "expr"
    vim.wo[win][0].foldexpr = "v:lua.vim.treesitter.foldexpr()"
    -- wiki-link completion must not auto-insert the first match
    vim.bo[ev.buf].completeopt = "menu,menuone,noselect"
  end,
})

-- ── Helpers ──────────────────────────────────────────────────────
local function note_list()
  local files = vim.fn.globpath(NOTES, "**/*.md", false, true)
  local out = {}
  for _, f in ipairs(files) do
    local rel = f:sub(#NOTES + 2):gsub("%.md$", "")
    if not rel:match("^%.") and not rel:match("/%.") then table.insert(out, rel) end
  end
  table.sort(out)
  return out
end

local function current_note()
  local name = vim.api.nvim_buf_get_name(0)
  local real = uv.fs_realpath(name) or name
  if name == "" or real:sub(1, #NOTES_REAL + 1) ~= NOTES_REAL .. "/" then return nil end
  local rel = real:sub(#NOTES_REAL + 2):gsub("%.md$", "")
  return rel, vim.fn.fnamemodify(rel, ":t"), real
end

local function rg_escape(s) return (s:gsub("[%p%s]", "\\%0")) end

-- ── Backlinks: who links to this note? ───────────────────────────
function M.backlinks()
  local rel, base = current_note()
  if not rel then return warn("Not a note in " .. NOTES) end
  local alts = { rg_escape(rel) }
  if base ~= rel then table.insert(alts, rg_escape(base)) end
  Snacks.picker.grep({
    cwd = NOTES, title = "Backlinks → " .. rel, regex = true,
    search = "\\[\\[(" .. table.concat(alts, "|") .. ")(\\||#|\\]\\])",
    live = false,
  })
end

-- ── Insert a wiki link with a fuzzy picker ───────────────────────
function M.insert_link()
  Snacks.picker.files({
    cwd = NOTES, title = "Link to note", ft = "md",
    confirm = function(picker, item)
      picker:close()
      if not item then return end
      local target = (item.file or item.text):gsub("%.md$", "")
      vim.schedule(function() vim.api.nvim_put({ "[[" .. target .. "]]" }, "c", true, true) end)
    end,
  })
end

-- ── Outline of the current note ──────────────────────────────────
function M.outline()
  local buf, items, in_fence = vim.api.nvim_get_current_buf(), {}, false
  local file = vim.api.nvim_buf_get_name(buf)
  for i, l in ipairs(vim.api.nvim_buf_get_lines(buf, 0, -1, false)) do
    if l:match("^```") or l:match("^~~~") then in_fence = not in_fence end
    local hashes, title = l:match("^(#+)%s+(.*)")
    if hashes and not in_fence then
      table.insert(items, { text = title, file = file, pos = { i, 0 }, level = #hashes, title = title })
    end
  end
  if #items == 0 then return vim.notify("No headings in this note") end
  Snacks.picker.pick({
    title = "Outline", items = items, preview = "file",
    format = function(item) return { { string.rep("  ", item.level - 1) .. item.title, item.level == 1 and "Title" or "Normal" } } end,
  })
end

-- ── Tags: every #tag in your notes ───────────────────────────────
function M.tags()
  Snacks.picker.grep({
    cwd = NOTES, title = "Tags (type a tag name)", regex = true,
    search = "(^|\\s)#[A-Za-z][\\w/-]*", live = false,
  })
end

-- ── Daily note navigation ([d previous, ]d next) ─────────────────
local function daily_step(dir)
  local files = vim.fn.globpath(NOTES .. "/daily", "*.md", false, true)
  table.sort(files)
  local cur = vim.api.nvim_buf_get_name(0)
  cur = uv.fs_realpath(cur) or cur
  local here = cur:match("(%d%d%d%d%-%d%d%-%d%d)%.md$")
  if not here then return vim.cmd("Today") end
  local target
  for _, f in ipairs(files) do
    local d = f:match("(%d%d%d%d%-%d%d%-%d%d)%.md$")
    if d then
      if dir < 0 and d < here then target = f end
      if dir > 0 and d > here then target = f break end
    end
  end
  if target then vim.cmd.edit(vim.fn.fnameescape(target)) else vim.notify(dir < 0 and "No earlier daily note" or "No later daily note") end
end

-- ── Rename a note and fix every [[link]] that points to it ───────
local function rename_note(newname)
  local rel, base, real = current_note()
  if not rel then return warn("Not a note in " .. NOTES) end
  newname = vim.trim(newname):gsub("%.md$", "")
  if newname == "" then return end
  local new_rel = newname:find("/", 1, true) and newname or ((vim.fn.fnamemodify(rel, ":h") ~= "." and (vim.fn.fnamemodify(rel, ":h") .. "/") or "") .. newname)
  local new_base = vim.fn.fnamemodify(new_rel, ":t")
  local new_path = NOTES_REAL .. "/" .. new_rel .. ".md"
  if uv.fs_stat(new_path) then return warn(new_rel .. ".md already exists") end
  vim.cmd("silent! write")
  vim.fn.mkdir(vim.fn.fnamemodify(new_path, ":h"), "p")
  local ok, err = uv.fs_rename(real, new_path)
  if not ok then return warn("Rename failed: " .. tostring(err)) end

  local changed = 0
  local function fix(content)
    local n = 0
    for _, pair in ipairs({ { rel, new_rel }, { base, new_base } }) do
      content = content:gsub("%[%[" .. vim.pesc(pair[1]) .. "([%]|#])", function(tail)
        n = n + 1
        return "[[" .. pair[2] .. tail
      end)
    end
    return content, n
  end
  for _, f in ipairs(vim.fn.globpath(NOTES_REAL, "**/*.md", false, true)) do
    local fh = io.open(f, "r")
    if fh then
      local content = fh:read("*a")
      fh:close()
      local updated, n = fix(content)
      if n > 0 and updated ~= content then
        local w = io.open(f, "w")
        if w then w:write(updated) w:close() changed = changed + 1 end
      end
    end
  end
  local old_buf = vim.api.nvim_get_current_buf()
  vim.cmd.edit(vim.fn.fnameescape(new_path))
  pcall(vim.api.nvim_buf_delete, old_buf, { force = true })
  vim.cmd("checktime")
  vim.notify(("Renamed to %s — updated links in %d note(s)"):format(new_rel, changed))
end
vim.api.nvim_create_user_command("NoteRename", function(o_) rename_note(o_.args) end, { nargs = 1, complete = "file" })
vim.api.nvim_create_user_command("Backlinks", M.backlinks, {})
vim.api.nvim_create_user_command("Outline", M.outline, {})

-- ── [[ completion ────────────────────────────────────────────────
local link_cache = { t = 0, list = {} }
vim.api.nvim_create_autocmd("TextChangedI", {
  pattern = "*.md",
  callback = function()
    if not vim.fn.mode():find("^i") then return end
    local col = vim.fn.col(".")
    local before = vim.api.nvim_get_current_line():sub(1, col - 1)
    local query = before:match("%[%[([^%]|]*)$")
    if not query then return end
    if uv.now() - link_cache.t > 10000 then link_cache = { t = uv.now(), list = note_list() } end
    local q, matches = query:lower(), {}
    for _, rel in ipairs(link_cache.list) do
      if q == "" or rel:lower():find(q, 1, true) then
        table.insert(matches, { word = rel, abbr = rel, menu = "[note]" })
      end
    end
    if #matches > 0 then vim.fn.complete(col - #query, matches) end
  end,
})
vim.api.nvim_create_autocmd("CompleteDone", {
  pattern = "*.md",
  callback = function()   -- close the link after accepting a match
    if (vim.v.completed_item or {}).menu ~= "[note]" then return end
    local line, col = vim.api.nvim_get_current_line(), vim.fn.col(".")
    if line:sub(col, col + 1) ~= "]]" then vim.api.nvim_put({ "]]" }, "c", false, false) end
  end,
})

-- ── Keymaps ──────────────────────────────────────────────────────
local map = vim.keymap.set
map("n", "<leader>nb", M.backlinks, { desc = "Backlinks to this note" })
map("n", "<leader>nl", M.insert_link, { desc = "Insert [[link]] (pick a note)" })
map("n", "<leader>nh", M.outline, { desc = "Outline (headings) of this note" })
map("n", "<leader>nT", M.tags, { desc = "Browse #tags" })
map("n", "<leader>nR", function()
  local rel = current_note()
  if not rel then return warn("Not a note in " .. NOTES) end
  vim.ui.input({ prompt = "Rename note (links update): ", default = vim.fn.fnamemodify(rel, ":t") }, function(n)
    if n and vim.trim(n) ~= "" then rename_note(n) end
  end)
end, { desc = "Rename note + fix links" })
map("n", "<leader>nu", function() Snacks.picker.undo() end, { desc = "Undo history" })
map("n", "<leader>z", function() Snacks.zen() end, { desc = "Zen mode" })
map("n", "<leader>,", function() Snacks.picker.buffers() end, { desc = "Open buffers" })
map("n", "<leader>.", function() Snacks.picker.resume() end, { desc = "Resume last picker" })
map("n", "]d", function() daily_step(1) end, { desc = "Next daily note" })
map("n", "[d", function() daily_step(-1) end, { desc = "Previous daily note" })
-- insert-mode: link picker without leaving the keyboard flow
map("i", "<C-l>", function() vim.cmd("stopinsert") M.insert_link() end, { desc = "Insert [[link]]" })
-- keep the cursor centered when jumping and searching
map("n", "n", "nzzzv")
map("n", "N", "Nzzzv")
map("n", "<C-d>", "<C-d>zz")
map("n", "<C-u>", "<C-u>zz")
-- Esc clears search highlight
map("n", "<Esc>", "<cmd>nohlsearch<cr><Esc>")

return M
