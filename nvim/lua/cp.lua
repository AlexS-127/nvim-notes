-- lua/cp.lua — competitive programming / LeetCode layer. Loaded from init.lua.
--   <leader>r run file · <leader>R run with sanitizers · <leader>t run tests/ via test.sh
--   <leader>ff files · fg grep · fb buffers · fr recent · fs symbols · fd diagnostics · fp problems
-- Needs (brew): pyright ruff. clangd comes with the Xcode command line tools.
vim.pack.add({
  "https://github.com/neovim/nvim-lspconfig",
  "https://github.com/stevearc/conform.nvim",
  "https://github.com/nvim-lua/plenary.nvim",
  "https://github.com/nvim-telescope/telescope.nvim",
}, { confirm = false })

local CP_FT = { "c", "cpp", "python" }
local CP_DIR = vim.fn.expand(vim.env.CP_DIR or "~/cp")
local map = vim.keymap.set

-- ── Treesitter highlighting (parsers are installed from init.lua) ─
vim.api.nvim_create_autocmd("FileType", {
  pattern = CP_FT,
  callback = function(ev)
    pcall(vim.treesitter.start, ev.buf)
    vim.b[ev.buf].minicompletion_disable = false   -- completion is off by default (see ui.lua)
  end,
})

-- ── LSP: clangd + pyright (only enabled when installed) ───────────
vim.lsp.config("clangd", { cmd = { "clangd", "--header-insertion=never" } })
for name, bin in pairs({ clangd = "clangd", pyright = "pyright-langserver" }) do
  if vim.fn.executable(bin) == 1 then vim.lsp.enable(name) end
end
vim.diagnostic.config({ virtual_text = true, severity_sort = true })

vim.api.nvim_create_autocmd("LspAttach", {
  callback = function(ev)
    local function bmap(mode, lhs, rhs, desc) map(mode, lhs, rhs, { buffer = ev.buf, desc = desc }) end
    bmap("n", "gd", vim.lsp.buf.definition, "Go to definition")
    bmap("n", "<leader>d", vim.diagnostic.open_float, "Line diagnostics")
  end,
})

-- ── Format on save (conform; falls back to the LSP if no formatter is installed) ─
local function exe(bin, name) return vim.fn.executable(bin) == 1 and name or nil end
require("conform").setup({
  formatters_by_ft = {
    python = { exe("ruff", "ruff_format") },
    c = { exe("clang-format", "clang_format") },
    cpp = { exe("clang-format", "clang_format") },
  },
  format_on_save = function(buf)
    if vim.g.cp_noformat or not vim.tbl_contains(CP_FT, vim.bo[buf].filetype) then return end
    return { timeout_ms = 500, lsp_format = "fallback" }
  end,
})
vim.api.nvim_create_user_command("FormatToggle", function()
  vim.g.cp_noformat = not vim.g.cp_noformat
  vim.notify("Format on save " .. (vim.g.cp_noformat and "off" or "on"))
end, {})

-- ── Telescope (required lazily, so it costs nothing at startup) ───
local function tele(fn, opts)
  return function() require("telescope.builtin")[fn](opts) end
end
map("n", "<leader>ff", tele("find_files"), { desc = "Find files" })
map("n", "<leader>fg", tele("live_grep"), { desc = "Grep" })
map("n", "<leader>fb", tele("buffers"), { desc = "Buffers" })
map("n", "<leader>fr", tele("oldfiles"), { desc = "Recent files" })
map("n", "<leader>fs", tele("lsp_document_symbols"), { desc = "Symbols in file" })
map("n", "<leader>fd", tele("diagnostics"), { desc = "Diagnostics" })
map("n", "<leader>fp", tele("find_files", { cwd = CP_DIR, prompt_title = "Problems" }), { desc = "Find problem file" })

-- ── Run the current file in a bottom split ────────────────────────
local run_win
local function run_in_split(cmd, cwd, interactive)
  if run_win and vim.api.nvim_win_is_valid(run_win) then pcall(vim.api.nvim_win_close, run_win, true) end
  local from = vim.api.nvim_get_current_win()
  vim.cmd("botright 12new")
  run_win = vim.api.nvim_get_current_win()
  vim.bo.bufhidden = "wipe"
  vim.fn.jobstart(cmd, { term = true, cwd = cwd })
  map("n", "q", "<cmd>close<cr>", { buffer = true, silent = true, desc = "Close output" })
  map("t", "<Esc><Esc>", [[<C-\><C-n>]], { buffer = true, desc = "Leave terminal mode" })
  if interactive then
    vim.cmd.startinsert()          -- type your own input; Ctrl-D ends it
  else
    vim.api.nvim_set_current_win(from)
  end
end

-- input.txt next to the file is fed to stdin; otherwise you type input in the split
local function run_file(debug)
  local file = vim.api.nvim_buf_get_name(0)
  if file == "" then return vim.notify("Save the file first", vim.log.levels.WARN) end
  vim.cmd("silent update")
  local q, ft = vim.fn.shellescape, vim.bo.filetype
  local dir = vim.fs.dirname(file)
  local input = dir .. "/input.txt"
  local has_input = vim.uv.fs_stat(input) ~= nil
  local feed = has_input and (" < " .. q(input)) or ""
  local out = vim.fn.stdpath("cache") .. "/cp"
  vim.fn.mkdir(out, "p")
  local bin = q(out .. "/" .. vim.fn.fnamemodify(file, ":t:r"))
  local cmd
  if ft == "cpp" or ft == "c" then
    local cc = ft == "cpp" and "g++ -std=c++20" or "gcc -std=c17"
    local flags = debug and "-g -fsanitize=address,undefined" or "-O2"
    cmd = ("TIMEFMT='[%%*Es]'; %s %s -Wall -Wextra -DLOCAL %s -o %s && time %s%s; echo \"[exit $?]\""):format(cc, flags, q(file), bin, bin, feed)
  elseif ft == "python" then
    cmd = ("TIMEFMT='[%%*Es]'; time python3 %s%s; echo \"[exit $?]\""):format(q(file), feed)
  else
    return vim.notify("No runner for filetype '" .. ft .. "'", vim.log.levels.WARN)
  end
  run_in_split(cmd, dir, not has_input)
end

local function run_tests()
  local dir = vim.fs.dirname(vim.api.nvim_buf_get_name(0))
  if vim.fn.filereadable(dir .. "/test.sh") == 0 then
    return vim.notify("No test.sh here (create problems with `newproblem`)", vim.log.levels.WARN)
  end
  vim.cmd("silent update")
  run_in_split("bash test.sh", dir, false)
end

vim.api.nvim_create_autocmd("FileType", {
  pattern = CP_FT,
  callback = function(ev)
    local function bmap(lhs, rhs, desc) map("n", lhs, rhs, { buffer = ev.buf, desc = desc }) end
    bmap("<leader>r", function() run_file(false) end, "Run file")
    bmap("<leader>R", function() run_file(true) end, "Run file (sanitizers)")
    bmap("<leader>t", run_tests, "Run tests/ (test.sh)")
  end,
})
