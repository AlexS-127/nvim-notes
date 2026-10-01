-- Headless smoke test: loads the config (already done by the surrounding nvim start),
-- checks plugins, keymaps and commands, and opens a markdown note. See ci.yml.
local failures = {}
local function check(ok, msg) if not ok then table.insert(failures, msg) end end

local notes = vim.env.NOTES_DIR
check(notes and notes ~= "", "NOTES_DIR not set")
vim.fn.mkdir(notes, "p")
local file = notes .. "/ci-check.md"
vim.fn.writefile({ "# CI check", "", "- [ ] a task", "", "```lua", "print('hi')", "```" }, file)

vim.cmd.edit(vim.fn.fnameescape(file))
vim.wait(500)
check(vim.bo.filetype == "markdown", "filetype is " .. vim.bo.filetype)
check(vim.o.updatetime == 400, "updatetime is " .. vim.o.updatetime)
check(vim.v.errmsg == "", "errmsg: " .. vim.v.errmsg)

for _, name in ipairs({ "nvim-treesitter", "snacks.nvim", "bullets.vim", "vim-table-mode", "img-clip.nvim" }) do
  local found = false
  for _, p in ipairs(vim.pack.get()) do if p.spec.name == name then found = true end end
  check(found, "plugin missing: " .. name)
end
for _, lhs in ipairs({ " p", " o", " nt", " nd", " nc", " ni", " no", " x" }) do
  check(vim.fn.maparg(lhs, "n") ~= "", "missing keymap <Space>" .. lhs:sub(2))
end
for _, cmd in ipairs({ "SaveMacro", "Macros", "Today" }) do
  check(vim.fn.exists(":" .. cmd) == 2, "missing command :" .. cmd)
end

check(vim.fn.hlexists("NotesMovedTask") == 1, "missing highlight NotesMovedTask")

-- With notesview on PATH: daily carry-over, natural due dates, lecture notes, capture.
if vim.fn.executable("notesview") == 1 then
  local function read(p) local f = io.open(p); if not f then return "" end local s = f:read("*a"); f:close(); return s end
  local today = os.date("%Y-%m-%d")
  local older = os.date("%Y-%m-%d", os.time() - 2 * 86400)
  vim.fn.mkdir(notes .. "/daily", "p")
  vim.fn.writefile({ "## Tasks", "- [ ] carry me", "  - [ ] sub", "- [ ] homework #act200" }, notes .. "/daily/" .. older .. ".md")
  vim.cmd("Today")
  check(read(notes .. "/daily/" .. today .. ".md"):find("## Tasks\n\n%- %[ %] carry me\n  %- %[ %] sub\n\n## Notes", 1) ~= nil,
    "daily carry-over: " .. read(notes .. "/daily/" .. today .. ".md"))
  check(read(notes .. "/daily/" .. older .. ".md"):find("- [>] carry me → [[" .. today .. "]]", 1, true) ~= nil,
    "moved marker: " .. read(notes .. "/daily/" .. older .. ".md"))

  vim.api.nvim_buf_set_lines(0, -1, -1, false, { "- [ ] due soon @tomorrow" })
  vim.api.nvim_win_set_cursor(0, { vim.api.nvim_buf_line_count(0), 0 })
  vim.cmd("doautocmd InsertLeave")
  local tomorrow = os.date("%Y-%m-%d", os.time() + 86400)
  check(vim.api.nvim_get_current_line() == "- [ ] due soon @" .. tomorrow, "date conversion: " .. vim.api.nvim_get_current_line())

  vim.ui.select = function(items, _, cb) cb(items[1]) end
  vim.api.nvim_feedkeys(vim.keycode("<Space>nc"), "x", false)
  check(vim.api.nvim_buf_get_name(0):sub(-#("act200/" .. today .. ".md")) == "act200/" .. today .. ".md",
    "lecture note: " .. vim.api.nvim_buf_get_name(0))

  vim.ui.input = function(_, cb) cb("worksheet @tomorrow #act200") end
  vim.api.nvim_feedkeys(vim.keycode("<Space>ni"), "x", false)
  check(read(notes .. "/inbox.md"):find("- [ ] worksheet @" .. tomorrow .. " #act200", 1, true) ~= nil, "capture")
  check(vim.v.errmsg == "", "errmsg: " .. vim.v.errmsg)
end

if #failures > 0 then
  io.stderr:write("nvim check FAILED:\n  " .. table.concat(failures, "\n  ") .. "\n")
  vim.cmd("cquit 1")
end
print("nvim check OK")
vim.cmd("qa!")
