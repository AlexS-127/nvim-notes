-- Neovim's colours follow notesview's theme_light / theme_dark (~/.config/notesview/config.json,
-- set on the viewer's Settings page or with `notesview theme [light|dark] NAME`). auto-dark-mode.nvim
-- picks light or dark from macOS; this picks the colorscheme for that slot, installs its plugin
-- the first time (vim.pack), keeps the background see-through (Ghostty's theme, opacity and blur
-- show behind it; notesview keeps Ghostty's theme in step) and re-applies when the file changes.
local M = {}

local uv = vim.uv or vim.loop

-- notesview theme → colorscheme per appearance. `src` is installed on first use; `bg` = the scheme
-- reads vim.o.background instead of having separate names. Keep in step with themes.go.
local SCHEMES = {
  default = { src = "https://github.com/projekt0n/github-nvim-theme", light = "github_light", dark = "github_dark_default" },
  nord = { src = "https://github.com/EdenEast/nightfox.nvim", light = "dayfox", dark = "nordfox" },
  gruvbox = { src = "https://github.com/ellisonleao/gruvbox.nvim", light = "gruvbox", dark = "gruvbox", bg = true },
  solarized = { src = "https://github.com/maxmx03/solarized.nvim", light = "solarized", dark = "solarized", bg = true },
  catppuccin = { light = "catppuccin", dark = "catppuccin", bg = true },
  cappuccino = { light = "catppuccin", dark = "catppuccin", bg = true },
  ["rose-pine"] = { src = "https://github.com/rose-pine/neovim", name = "rose-pine", light = "rose-pine-dawn", dark = "rose-pine-main" },
  ["tokyo-night"] = { src = "https://github.com/folke/tokyonight.nvim", light = "tokyonight-day", dark = "tokyonight-night" },
  horizon = { src = "https://github.com/arzg/vim-colors-xcode", light = "xcodelight", dark = "xcodedark" },
  dracula = { src = "https://github.com/Mofiqul/dracula.nvim", light = "dracula", dark = "dracula", only = "dark" },
  borland = { light = "catppuccin", dark = "catppuccin", only = "light", after = function() require("borland").apply() end },
  terminal = { light = "default", dark = "default", only = "dark", after = function() M.phosphor() end },
}

local function config_path()
  local base = (vim.env.XDG_CONFIG_HOME and vim.env.XDG_CONFIG_HOME ~= "") and vim.env.XDG_CONFIG_HOME or vim.fn.expand("~/.config")
  return base .. "/notesview/config.json"
end

-- theme_light / theme_dark (the older single "theme" fills either), defaults borland / catppuccin
function M.read()
  local out = { light = "borland", dark = "catppuccin" }
  local f = io.open(config_path(), "r")
  if not f then return out end
  local ok, c = pcall(vim.json.decode, f:read("*a"))
  f:close()
  if not ok or type(c) ~= "table" then return out end
  if type(c.theme) == "string" and c.theme ~= "" then out.light, out.dark = c.theme, c.theme end
  if type(c.theme_light) == "string" and c.theme_light ~= "" then out.light = c.theme_light end
  if type(c.theme_dark) == "string" and c.theme_dark ~= "" then out.dark = c.theme_dark end
  return out
end

local installed = {}
local function ensure(spec)
  if not spec.src or installed[spec.src] then return true end
  local ok, err = pcall(vim.pack.add, { spec.name and { src = spec.src, name = spec.name } or spec.src }, { confirm = false })
  if not ok then
    vim.notify("theme: couldn't install " .. spec.src .. ": " .. tostring(err), vim.log.levels.WARN)
    return false
  end
  installed[spec.src] = true
  return true
end

-- See-through text areas, like catppuccin's transparent_background; floats and bars stay solid.
local function clear_bg()
  for _, g in ipairs({ "Normal", "NormalNC", "SignColumn", "EndOfBuffer", "FoldColumn", "LineNr", "CursorLineNr" }) do
    local h = vim.api.nvim_get_hl(0, { name = g, link = false })
    h.bg, h.ctermbg = nil, nil
    vim.api.nvim_set_hl(0, g, h)
  end
end

M.current = nil   -- notesview theme name in use

-- Apply the slot for `mode` ("light" or "dark", default vim.o.background).
function M.apply(mode)
  mode = mode or vim.o.background
  local name = M.read()[mode]
  local spec = SCHEMES[name] or SCHEMES.catppuccin
  if not ensure(spec) then name, spec = "catppuccin", SCHEMES.catppuccin end
  local bg = spec.only or mode
  vim.o.background = bg
  M.current = name
  local ok, err = pcall(vim.cmd.colorscheme, spec[bg] or spec.dark)
  if not ok then
    vim.notify("theme: " .. tostring(err), vim.log.levels.WARN)
    vim.cmd.colorscheme("catppuccin")
  end
  if spec.after then spec.after() end
  if spec.src then clear_bg() end
end

-- terminal: green phosphor on the terminal's own (Ghostty "Retro") background
function M.phosphor()
  local g, dim, dark = "#33ff66", "#1f9e45", "#0d3d1a"
  local function hl(n, v) vim.api.nvim_set_hl(0, n, v) end
  for _, n in ipairs({ "Normal", "NormalNC", "Identifier", "Function", "Statement", "Keyword", "Conditional", "Repeat", "Operator",
    "Type", "Special", "PreProc", "Include", "Constant", "String", "Character", "Number", "Boolean", "Title", "Directory", "@markup.heading" }) do
    hl(n, { fg = g })
  end
  hl("Comment", { fg = dim, italic = true })
  hl("LineNr", { fg = dim }); hl("CursorLineNr", { fg = g, bold = true })
  hl("CursorLine", { bg = "#0a2a12" }); hl("Visual", { fg = "#000000", bg = g })
  hl("Search", { fg = "#000000", bg = dim }); hl("NormalFloat", { fg = g, bg = "#051a0b" }); hl("FloatBorder", { fg = g, bg = "#051a0b" })
  hl("Pmenu", { fg = g, bg = "#051a0b" }); hl("PmenuSel", { fg = "#000000", bg = g })
  hl("StatusLine", { fg = "#000000", bg = g }); hl("StatusLineNC", { fg = g, bg = dark }); hl("WinSeparator", { fg = dim })
  hl("NonText", { fg = dark }); hl("EndOfBuffer", { fg = dark })
end

function M.setup()
  -- borland's overrides go on top of catppuccin; something resetting the highlights (:colorscheme)
  -- must not drop them
  vim.api.nvim_create_autocmd("ColorScheme", {
    pattern = "catppuccin*",
    callback = function()
      if M.current == "borland" then require("borland").apply() end
    end,
  })
  -- config.json is replaced atomically (rename), so watch its folder
  local dir = vim.fn.fnamemodify(config_path(), ":h")
  if vim.fn.isdirectory(dir) == 1 then
    local ev, timer = uv.new_fs_event(), uv.new_timer()
    local last = vim.inspect(M.read())
    ev:start(dir, {}, function(_, fname)
      if fname and fname ~= "config.json" then return end
      timer:start(150, 0, vim.schedule_wrap(function()
        local now = vim.inspect(M.read())
        if now ~= last then last = now; M.apply() end
      end))
    end)
  end
end

return M
