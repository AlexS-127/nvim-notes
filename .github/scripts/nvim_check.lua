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
for _, lhs in ipairs({ " p", " o", " nt", " nd", " nn", " ni", " no", " x" }) do
  check(vim.fn.maparg(lhs, "n") ~= "", "missing keymap <Space>" .. lhs:sub(2))
end
for _, cmd in ipairs({ "SaveMacro", "Macros", "Today" }) do
  check(vim.fn.exists(":" .. cmd) == 2, "missing command :" .. cmd)
end

check(vim.fn.hlexists("NotesMovedTask") == 1, "missing highlight NotesMovedTask")

-- With notesview on PATH: daily carry-over, natural due dates, folders, capture.
if vim.fn.executable("notesview") == 1 then
  local function read(p) local f = io.open(p); if not f then return "" end local s = f:read("*a"); f:close(); return s end
  local function day(n) return os.date("%Y-%m-%d", os.time() + n * 86400) end
  local today, tomorrow = day(0), day(1)
  vim.fn.mkdir(notes .. "/daily", "p")
  vim.fn.mkdir(notes .. "/ACT 200/Chapter 5", "p")
  vim.fn.writefile({ "# Costs" }, notes .. "/ACT 200/Chapter 5/costs.md")
  vim.fn.writefile({ "## Tasks", "- [ ] carry me", "  - [ ] sub", "- [ ] stays #act-200", "- [ ] dated @" .. day(3) },
    notes .. "/daily/" .. day(-2) .. ".md")
  vim.cmd("Today")
  check(read(notes .. "/daily/" .. today .. ".md"):find("## Tasks\n\n%- %[ %] carry me\n  %- %[ %] sub\n\n## Notes", 1) ~= nil,
    "daily carry-over: " .. read(notes .. "/daily/" .. today .. ".md"))
  local old = read(notes .. "/daily/" .. day(-2) .. ".md")
  check(old:find("- [>] carry me → [[" .. today .. "]]\n- [ ] stays #act-200\n- [ ] dated @", 1, true) ~= nil, "old daily: " .. old)

  vim.api.nvim_buf_set_lines(0, -1, -1, false, { "- [ ] due soon @tomorrow" })
  vim.api.nvim_win_set_cursor(0, { vim.api.nvim_buf_line_count(0), 0 })
  vim.cmd("doautocmd InsertLeave")
  check(vim.api.nvim_get_current_line() == "- [ ] due soon @" .. tomorrow, "date conversion: " .. vim.api.nvim_get_current_line())

  -- pickers: answer with the item whose text starts with `want` (nil = Esc)
  local want
  Snacks.picker.pick = function(opts)
    local p = { close = function() if opts.on_close then opts.on_close() end end }
    for _, item in ipairs(opts.items) do
      if want and item.text:sub(1, #want) == want then return opts.confirm(p, item) end
    end
    p.close()
  end
  local answers
  vim.ui.input = function(_, cb) cb(table.remove(answers, 1)) end
  local function capture(a, folder)
    answers, want = a, folder
    vim.api.nvim_feedkeys(vim.keycode("<Space>ni"), "x", false)
    vim.wait(100)
  end
  capture({ "read ch 5", "someday", "fri", "" }, "act-200/chapter-5")   -- topic, a typo, then a date
  capture({ "email prof", "tomorrow", "" }, "act-200")                -- category
  capture({ "call mom", "" }, nil)                                     -- skip both
  local inbox = read(notes .. "/inbox.md")
  check(inbox:find("%- %[ %] read ch 5 #act%-200/chapter%-5 @%d%d%d%d%-%d%d%-%d%d _%(") ~= nil, "capture topic:\n" .. inbox)
  check(inbox:find("- [ ] email prof #act-200 @" .. tomorrow .. " _(", 1, true) ~= nil, "capture category:\n" .. inbox)
  check(inbox:find("- [ ] call mom _(", 1, true) ~= nil, "capture skip:\n" .. inbox)

  -- <Space>nn: title, then a folder
  answers, want = { "Lecture 7" }, "act-200/chapter-5"
  vim.api.nvim_feedkeys(vim.keycode("<Space>nn"), "x", false)
  vim.wait(100)
  check(vim.api.nvim_buf_get_name(0):sub(-#"ACT 200/Chapter 5/lecture-7.md") == "ACT 200/Chapter 5/lecture-7.md",
    "new note: " .. vim.api.nvim_buf_get_name(0))

  -- Enter on [[act-200/chapter-5]] opens a picker of the folder's notes
  vim.cmd.edit(notes .. "/links.md")
  vim.api.nvim_buf_set_lines(0, 0, -1, false, { "see [[act-200/chapter-5]]" })
  vim.api.nvim_win_set_cursor(0, { 1, 8 })
  want = "Costs"
  vim.api.nvim_feedkeys(vim.api.nvim_replace_termcodes("<CR>", true, false, true), "x", false)
  vim.wait(100)
  check(vim.api.nvim_buf_get_name(0):sub(-#"Chapter 5/costs.md") == "Chapter 5/costs.md", "folder link: " .. vim.api.nvim_buf_get_name(0))
  check(vim.v.errmsg == "", "errmsg: " .. vim.v.errmsg)
end

if #failures > 0 then
  io.stderr:write("nvim check FAILED:\n  " .. table.concat(failures, "\n  ") .. "\n")
  vim.cmd("cquit 1")
end
print("nvim check OK")
vim.cmd("qa!")
