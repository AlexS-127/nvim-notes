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
for _, lhs in ipairs({ " p", " o", " nt", " nd", " x" }) do
  check(vim.fn.maparg(lhs, "n") ~= "", "missing keymap <Space>" .. lhs:sub(2))
end
for _, cmd in ipairs({ "SaveMacro", "Macros", "Today" }) do
  check(vim.fn.exists(":" .. cmd) == 2, "missing command :" .. cmd)
end

if #failures > 0 then
  io.stderr:write("nvim check FAILED:\n  " .. table.concat(failures, "\n  ") .. "\n")
  vim.cmd("cquit 1")
end
print("nvim check OK")
vim.cmd("qa!")
