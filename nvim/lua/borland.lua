-- Borland (Turbo Pascal) look for light mode: blue desk, yellow text,
-- white keywords, cyan bars and selection. Applied on top of catppuccin's latte so
-- plugin highlight groups still get sane defaults. Every fg clears 4.5:1 on BG.
local M = {}

local c = {
  bg = "#0000aa", bg2 = "#0000d0", bg3 = "#3a3ae0",
  fg = "#ffff55", dim = "#aaaaaa",
  navy = "#ffffff", blue = "#55ffff", cyan = "#00aaaa",
  teal = "#55ffff", green = "#55ff55", red = "#ff5555", maroon = "#ffaa55",
  purple = "#ff77ff", white = "#ffffff", yellow = "#ffff55", black = "#000000",
}
M.colors = c

function M.apply()
  local function hl(name, v) vim.api.nvim_set_hl(0, name, v) end

  -- The desk is left transparent so Ghostty's own Borland background (same #0000aa,
  -- with its opacity + blur) shows through. Floats, popups and bars stay opaque.
  hl("Normal",       { fg = c.fg })
  hl("NormalNC",     { fg = c.fg })
  hl("NormalFloat",  { fg = c.fg, bg = c.bg2 })
  hl("FloatBorder",  { fg = c.white, bg = c.bg2 })
  hl("FloatTitle",   { fg = c.black, bg = c.cyan, bold = true })
  hl("EndOfBuffer",  { fg = c.bg3 })
  hl("SignColumn", {})
  hl("LineNr",       { fg = c.dim })
  hl("CursorLineNr", { fg = c.white, bg = c.bg2, bold = true })
  hl("CursorLine",   { bg = c.bg2 })
  hl("ColorColumn",  { bg = c.bg2 })
  hl("Visual",       { fg = c.black, bg = c.cyan })
  hl("Search",       { fg = c.black, bg = c.yellow })
  hl("IncSearch",    { fg = c.white, bg = "#aa0000" })
  hl("CurSearch",    { fg = c.white, bg = "#aa0000" })
  hl("MatchParen",   { fg = c.black, bg = c.teal, bold = true })
  hl("WinSeparator", { fg = c.white })
  hl("Folded",       { fg = c.blue, bg = c.bg2 })
  hl("NonText",      { fg = c.dim })
  hl("Whitespace",   { fg = c.bg3 })
  hl("Directory",    { fg = c.navy, bold = true })
  hl("Title",        { fg = c.navy, bold = true })
  hl("Question",     { fg = c.green, bold = true })
  hl("ErrorMsg",     { fg = c.white, bg = "#aa0000", bold = true })
  hl("WarningMsg",   { fg = c.maroon, bold = true })
  hl("MoreMsg",      { fg = c.green })

  -- bars: Turbo Vision's cyan status line, gray menu line
  hl("StatusLine",   { fg = c.black, bg = c.cyan })
  hl("StatusLineNC", { fg = c.black, bg = c.dim })
  hl("TabLine",      { fg = c.black, bg = c.dim })
  hl("TabLineFill",  { bg = c.dim })
  hl("TabLineSel",   { fg = c.black, bg = c.cyan, bold = true })
  hl("WinBar",       { fg = c.navy, bold = true })
  hl("WinBarNC",     { fg = c.dim })

  hl("Pmenu",        { fg = c.fg, bg = c.bg2 })
  hl("PmenuSel",     { fg = c.black, bg = c.cyan })
  hl("PmenuSbar",    { bg = c.bg3 })
  hl("PmenuThumb",   { bg = c.white })

  -- syntax
  hl("Comment",      { fg = c.dim, italic = true })
  hl("Constant",     { fg = c.maroon })
  hl("String",       { fg = c.teal })
  hl("Character",    { fg = c.teal })
  hl("Number",       { fg = c.maroon })
  hl("Boolean",      { fg = c.maroon, bold = true })
  hl("Identifier",   { fg = c.fg })
  hl("Function",     { fg = c.fg, bold = true })
  hl("Statement",    { fg = c.navy, bold = true })
  hl("Operator",     { fg = c.fg })
  hl("PreProc",      { fg = c.green })
  hl("Type",         { fg = c.purple })
  hl("Special",      { fg = c.red })
  hl("Delimiter",    { fg = c.fg })
  hl("Underlined",   { fg = c.blue, underline = true })
  hl("Todo",         { fg = c.black, bg = c.yellow, bold = true })
  hl("Error",        { fg = c.red, bold = true })

  for group, link in pairs({
    ["@comment"] = "Comment", ["@string"] = "String", ["@number"] = "Number",
    ["@boolean"] = "Boolean", ["@keyword"] = "Statement", ["@function"] = "Function",
    ["@function.call"] = "Function", ["@type"] = "Type", ["@constant"] = "Constant",
    ["@variable"] = "Identifier", ["@property"] = "Identifier", ["@operator"] = "Operator",
    ["@punctuation"] = "Delimiter", ["@module"] = "PreProc",
  }) do
    hl(group, { link = link })
  end

  -- catppuccin latte colours (dark purple/orange/slate) leak through groups the
  -- lists above don't cover; point them at the Borland palette instead.
  for group, link in pairs({
    Keyword = "Statement", Conditional = "Statement", Repeat = "Statement",
    Exception = "Statement", Include = "PreProc", Macro = "PreProc",
    StorageClass = "Type", Structure = "Type", Label = "Statement", Tag = "Statement",
    ["@keyword.conditional"] = "Statement", ["@keyword.repeat"] = "Statement",
    ["@keyword.exception"] = "Statement", ["@keyword.import"] = "PreProc",
    ["@keyword.type"] = "Type", ["@function.macro"] = "PreProc",
    ["@attribute"] = "PreProc", ["@label"] = "Statement", ["@tag"] = "Statement",
    ["@lsp.type.class"] = "Type", ["@lsp.type.enum"] = "Type",
    ["@lsp.type.interface"] = "Type", ["@lsp.type.struct"] = "Type",
    ["@lsp.type.namespace"] = "PreProc", ["@lsp.type.macro"] = "PreProc",
  }) do
    hl(group, { link = link })
  end
  for i = 1, 6 do hl("markdownH" .. i, { fg = i <= 2 and c.white or c.blue, bold = true }) end
  hl("markdownHeadingDelimiter", { fg = c.white, bold = true })
  hl("markdownCode",      { fg = c.teal })
  hl("markdownCodeBlock", { fg = c.teal })
  hl("markdownLinkText",  { fg = c.blue })
  hl("diffAdded",   { fg = c.green })
  hl("diffRemoved", { fg = c.red })
  hl("diffChanged", { fg = c.blue })
  hl("diffFile",    { fg = c.white, bold = true })
  hl("diffLine",    { fg = c.dim })
  hl("qfFileName",  { fg = c.blue })
  hl("qfLineNr",    { fg = c.maroon })
  hl("ModeMsg",     { fg = c.white, bold = true })
  hl("PmenuMatch",   { fg = c.white, bold = true })
  hl("PmenuExtra",   { fg = c.dim })
  hl("PmenuExtraSel",{ fg = c.black })
  hl("Conceal",      { fg = c.dim })
  hl("FoldColumn",   { fg = c.dim })
  hl("CursorLineFold", { fg = c.dim, bg = c.bg2 })
  hl("TitleBar",     { fg = c.black, bg = c.cyan })
  hl("TitleBarNC",   { fg = c.black, bg = c.dim })
  hl("VertSplit",    { fg = c.white })
  hl("Added",        { fg = c.green })
  hl("Changed",      { fg = c.blue })
  hl("Removed",      { fg = c.red })
  hl("@diff.plus",   { fg = c.green })
  hl("@diff.minus",  { fg = c.red })
  hl("@diff.delta",  { fg = c.blue })
  for _, g in ipairs({ "DiagnosticOk", "DiagnosticFloatingOk", "DiagnosticSignOk",
                       "DiagnosticVirtualTextOk", "DiagnosticVirtualLinesOk", "OkMsg" }) do
    hl(g, { fg = c.green })
  end

  hl("DiagnosticError", { fg = c.red })
  hl("DiagnosticWarn",  { fg = c.maroon })
  hl("DiagnosticInfo",  { fg = c.blue })
  hl("DiagnosticHint",  { fg = c.teal })
  hl("DiffAdd",         { bg = "#005500" })
  hl("DiffChange",      { bg = "#003a90" })
  hl("DiffDelete",      { fg = c.red, bg = "#700000" })
  hl("DiffText",        { bg = "#0070c0", bold = true })

  -- markdown (this config is for notes)
  for i = 1, 6 do
    hl("@markup.heading." .. i .. ".markdown", { fg = i <= 2 and c.white or c.blue, bold = true })
  end
  hl("@markup.raw",           { fg = c.teal })
  hl("@markup.raw.block",     { fg = c.teal })
  hl("@markup.link",          { fg = c.blue })
  hl("@markup.link.url",      { fg = c.blue, underline = true })
  hl("@markup.strong",        { bold = true })
  hl("@markup.italic",        { italic = true })
  hl("@markup.list",          { fg = c.red, bold = true })
  hl("@markup.quote",         { fg = c.dim, italic = true })

  vim.g.terminal_color_0, vim.g.terminal_color_8 = c.fg, c.dim

  -- Sweep: any group still carrying a latte colour (plugins, @lsp.*, @variable.builtin,
  -- @string.escape, ...) gets its fg/bg remapped so nothing is unreadable on the blue.
  local function rgb(n) return n >> 16 & 255, n >> 8 & 255, n & 255 end
  local function lum(n)
    local r, g, b = rgb(n)
    local function f(v) v = v / 255; return v <= 0.03928 and v / 12.92 or ((v + 0.055) / 1.055) ^ 2.4 end
    return 0.2126 * f(r) + 0.7152 * f(g) + 0.0722 * f(b)
  end
  local function contrast(a, b)
    local la, lb = lum(a), lum(b)
    return (math.max(la, lb) + 0.05) / (math.min(la, lb) + 0.05)
  end
  local function hue(n)
    local r, g, b = rgb(n)
    local mx, mn = math.max(r, g, b), math.min(r, g, b)
    if mx == mn then return nil end
    local d = mx - mn
    local h
    if mx == r then h = ((g - b) / d) % 6 elseif mx == g then h = (b - r) / d + 2 else h = (r - g) / d + 4 end
    return h * 60
  end
  local function hex(s) return tonumber(s:sub(2), 16) end
  local palette = { c.white, c.yellow, c.blue, c.green, c.red, c.maroon, c.purple }
  local function nearest(n)
    local h = hue(n)
    if not h then return hex(c.fg) end
    local best, bd = nil, 1e9
    for _, p in ipairs(palette) do
      local ph = hue(hex(p))
      local d = ph and math.min(math.abs(ph - h), 360 - math.abs(ph - h)) or 180
      if d < bd then best, bd = p, d end
    end
    return hex(best)
  end
  local bgn = hex(c.bg)
  for _, g in ipairs(vim.fn.getcompletion("", "highlight")) do
    local h = vim.api.nvim_get_hl(0, { name = g, link = false })
    if h.fg or h.bg then
      local changed = false
      if h.bg and lum(h.bg) > 0.35 and h.bg ~= hex(c.cyan) and h.bg ~= hex(c.dim) and h.bg ~= hex(c.yellow)
        and h.bg ~= hex(c.teal) and h.bg ~= hex(c.white) then
        h.bg, changed = hex(c.bg2), true   -- pale latte surface -> navy surface
      end
      if h.fg then
        local under = h.bg or bgn
        if contrast(h.fg, under) < 4.5 then
          local nf = nearest(h.fg)
          if contrast(nf, under) < 4.0 then
            nf = contrast(hex(c.white), under) >= contrast(hex(c.black), under) and hex(c.white) or hex(c.black)
          end
          h.fg, changed = nf, true
        end
      end
      if changed then
        h.link, h.default = nil, nil
        vim.api.nvim_set_hl(0, g, h)
      end
    end
  end
end

return M
