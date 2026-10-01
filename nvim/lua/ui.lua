-- lua/ui.lua — which-key, statusline, completion. No icons. Loaded from init.lua.
vim.pack.add({
  "https://github.com/folke/which-key.nvim",
  "https://github.com/nvim-mini/mini.completion",
}, { confirm = false })

-- ── which-key: popup of keys after <Space> (text only, no icons) ──
local wk = require("which-key")
wk.setup({
  delay = 400,
  icons = { mappings = false, rules = false, breadcrumb = ">", separator = "->", group = "+" },
})
wk.add({
  { "<leader>n", group = "notes" },
  { "<leader>f", group = "find" },
})

-- ── Completion: mini.completion, only in C/C++/Python so notes stay quiet ─
-- (cp.lua re-enables it per buffer). Tab is left alone for bullets.vim; use <C-n>/<C-p>, <C-y>.
vim.g.minicompletion_disable = true
require("mini.completion").setup({})

-- ── Statusline: file, flags, diagnostics, filetype, position ──────
function _G.Statusline()
  local d = vim.diagnostic.count(0)
  local e, w = d[vim.diagnostic.severity.ERROR] or 0, d[vim.diagnostic.severity.WARN] or 0
  local diag = (e > 0 and (" E:" .. e) or "") .. (w > 0 and (" W:" .. w) or "")
  return " %f %m%r%=" .. diag .. " %y  %l:%c  %p%% "
end
vim.o.statusline = "%!v:lua.Statusline()"
