-- lua/inline.lua — ask Claude from inside a buffer; the answer lands in the file. Loaded from init.lua.
--   <leader>A  ask about the cursor line (visual: the selection); the answer goes on new lines below it
--   <leader>E  edit the selection (normal: the current markdown section, else the line) as you describe; the answer replaces it
-- The reply streams in as grey virtual lines, then becomes real text in one change (u undoes it) and the
-- cursor jumps to it. Press the key again while it runs to cancel. Runs `claude -p` with no tools, no
-- CLAUDE.md/hooks/MCP (--safe-mode), so it is fast and only sees the buffer. Model: vim.g.inline_model (sonnet).
local M = {}

local uv = vim.uv or vim.loop
local ns = vim.api.nvim_create_namespace("inline_claude")
local SPIN = { "⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏" }
local MAX_LINES = 1500        -- bigger buffers send only this many lines around the cursor
local PREVIEW_LINES = 12
local jobs = {}               -- buf -> running job

local SYSTEM = [[
You are answering inside the user's text editor (Neovim). Your whole output is inserted verbatim into their
file, so output only that text: no preamble, no "Here is", no sign-off, no remarks about what you did.
Fit the file: in a markdown note write markdown that matches the surrounding notes (heading levels, bullets,
terseness); in a code file, answer a question as comments in the file's comment syntax and write code only
when asked for code. Never wrap the whole output in a code fence. Be concise and correct.]]

local function is_list(l) return l and (l:match("^%s*[-*+] ") or l:match("^%s*%d+[.)] ")) ~= nil end
local function blank(l) return l == nil or l:match("^%s*$") ~= nil end

local function cancel(buf)
  local j = jobs[buf]
  if not j then return end
  jobs[buf] = nil
  j.timer:stop(); j.timer:close()
  pcall(function() j.proc:kill(15) end)
  pcall(vim.api.nvim_buf_clear_namespace, buf, ns, 0, -1)
end

-- Lines sent to Claude with markers around the selection and where the answer goes.
local function context(buf, s, e, mode)
  local n = vim.api.nvim_buf_line_count(buf)
  local from, to = 1, n
  if n > MAX_LINES then
    from = math.max(1, s - math.floor(MAX_LINES / 2))
    to = math.min(n, from + MAX_LINES - 1)
    from = math.max(1, math.min(from, to - MAX_LINES + 1))
  end
  local lines = vim.api.nvim_buf_get_lines(buf, from - 1, to, false)
  local out = {}
  if from > 1 then out[#out + 1] = ("[… %d lines above not shown]"):format(from - 1) end
  for i, l in ipairs(lines) do
    local ln = from + i - 1
    if ln == s then out[#out + 1] = "<<<SELECTION>>>" end
    out[#out + 1] = l
    if ln == e then
      out[#out + 1] = "<<<END SELECTION>>>"
      if mode == "insert" then out[#out + 1] = "<<<YOUR ANSWER GOES HERE>>>" end
    end
  end
  if to < n then out[#out + 1] = ("[… %d lines below not shown]"):format(n - to) end
  return table.concat(out, "\n")
end

-- Claude's text → lines ready to put in the buffer.
local function clean(text, ft)
  local lines = vim.split(text, "\n", { plain = true })
  while #lines > 0 and blank(lines[1]) do table.remove(lines, 1) end
  while #lines > 0 and blank(lines[#lines]) do table.remove(lines) end
  -- a non-markdown file never wants the whole answer fenced
  if ft ~= "markdown" and #lines >= 2 and lines[1]:match("^```") and lines[#lines]:match("^```%s*$") then
    table.remove(lines, 1); table.remove(lines)
  end
  return lines
end

-- The streamed text as virtual lines under the anchor, soft-wrapped to the window.
local function preview(j)
  if not vim.api.nvim_buf_is_valid(j.buf) then return cancel(j.buf) end
  local pos = vim.api.nvim_buf_get_extmark_by_id(j.buf, ns, j.mark, { details = true })
  if not pos[1] then return cancel(j.buf) end
  local width = math.max(20, vim.api.nvim_win_get_width(0) - 8)
  local rows = {}
  for _, l in ipairs(vim.split(j.text, "\n", { plain = true })) do
    if l == "" then rows[#rows + 1] = "" end
    while #l > 0 do rows[#rows + 1] = vim.fn.strcharpart(l, 0, width); l = vim.fn.strcharpart(l, width) end
  end
  while #rows > 0 and rows[1] == "" do table.remove(rows, 1) end
  local virt = { { { SPIN[j.tick % #SPIN + 1] .. " Claude" .. (j.mode == "edit" and " (edit)" or ""), "Special" },
    { "  " .. j.question, "Comment" } } }
  local skip = math.max(0, #rows - PREVIEW_LINES)
  if skip > 0 then virt[#virt + 1] = { { "  …", "Comment" } } end
  for i = skip + 1, #rows do virt[#virt + 1] = { { "  " .. rows[i], "Comment" } } end
  local row = j.mode == "edit" and pos[3].end_row or pos[1]
  vim.api.nvim_buf_set_extmark(j.buf, ns, row, 0, {
    id = j.virt, virt_lines = virt, virt_lines_leftcol = false,
  })
end

local function finish(j, res)
  local buf = j.buf
  cancel(buf)
  if not vim.api.nvim_buf_is_valid(buf) then return end
  if not res or res.is_error then
    local msg = res and (res.result or res.subtype) or "no reply"
    return vim.notify("Claude: " .. tostring(msg), vim.log.levels.ERROR)
  end
  local lines = clean(res.result or j.text, vim.bo[buf].filetype)
  if #lines == 0 then return vim.notify("Claude: empty reply", vim.log.levels.WARN) end
  local pos = j.pos
  local first
  if j.mode == "edit" then
    local cur = vim.api.nvim_buf_get_lines(buf, pos[1], pos[3].end_row + 1, false)
    if not vim.deep_equal(cur, j.orig) then
      vim.fn.setreg("+", table.concat(lines, "\n"))
      return vim.notify("Claude: the selection changed meanwhile, so the edit is on the clipboard", vim.log.levels.WARN)
    end
    vim.api.nvim_buf_set_lines(buf, pos[1], pos[3].end_row + 1, false, lines)
    first = pos[1] + 1
  else
    local at = pos[1] + 1   -- insert after the anchor line
    if vim.bo[buf].filetype == "markdown" then
      local above = vim.api.nvim_buf_get_lines(buf, at - 1, at, false)[1]
      local below = vim.api.nvim_buf_get_lines(buf, at, at + 1, false)[1]
      local list = is_list(lines[1]) and is_list(above)
      if not blank(above) and not list then table.insert(lines, 1, "") end
      if below and not blank(below) and not (list and is_list(below)) then lines[#lines + 1] = "" end
    end
    vim.api.nvim_buf_set_lines(buf, at, at, false, lines)
    first = at + 1
    while blank(vim.api.nvim_buf_get_lines(buf, first - 1, first, false)[1]) and first < at + #lines do first = first + 1 end
  end
  for _, win in ipairs(vim.fn.win_findbuf(buf)) do
    if win == vim.api.nvim_get_current_win() then
      vim.api.nvim_win_set_cursor(win, { first, 0 })
      vim.cmd("normal! zv")
    end
  end
end

local function run(buf, s, e, mode, question)
  local name = vim.api.nvim_buf_get_name(buf)
  local ft = vim.bo[buf].filetype
  local orig = vim.api.nvim_buf_get_lines(buf, s - 1, e, false)
  local task = mode == "edit"
    and "Rewrite the text between <<<SELECTION>>> and <<<END SELECTION>>> as the instruction says. Output only the replacement for those lines (keep their indentation), nothing from outside them."
    or "Answer the request. Your output goes on new lines at <<<YOUR ANSWER GOES HERE>>>, right after the selected line(s), which are what the request is about."
  local msg = table.concat({
    ("File: %s (%s%s)"):format(name ~= "" and vim.fn.fnamemodify(name, ":~") or "[unnamed]", ft ~= "" and ft or "text",
      vim.bo[buf].commentstring ~= "" and (", comments look like " .. vim.bo[buf].commentstring) or ""),
    "", "<file>", context(buf, s, e, mode), "</file>", "", task, "", (mode == "edit" and "Instruction: " or "Request: ") .. question,
  }, "\n")

  local j = { buf = buf, mode = mode, question = question, text = "", tick = 0, orig = orig, out = "" }
  -- edit: the selection (highlighted); insert: the last selected line, the answer goes after it
  j.mark = mode == "edit"
    and vim.api.nvim_buf_set_extmark(buf, ns, s - 1, 0, { end_row = e - 1, end_col = #orig[#orig], hl_group = "Visual",
      right_gravity = false, end_right_gravity = true })
    or vim.api.nvim_buf_set_extmark(buf, ns, e - 1, 0, { right_gravity = false })
  j.virt = vim.api.nvim_buf_set_extmark(buf, ns, e - 1, 0, {})

  local result
  local function on_line(line)
    local ok, ev = pcall(vim.json.decode, line)
    if not ok or type(ev) ~= "table" then return end
    if ev.type == "stream_event" and ev.event and ev.event.type == "content_block_delta"
      and ev.event.delta and ev.event.delta.type == "text_delta" then
      j.text = j.text .. ev.event.delta.text
    elseif ev.type == "result" then
      result = ev
    end
  end
  local cmd = { "claude", "-p", "--safe-mode", "--tools", "", "--strict-mcp-config", "--no-session-persistence",
    "--model", vim.g.inline_model or "sonnet", "--system-prompt", SYSTEM,
    "--output-format", "stream-json", "--include-partial-messages", "--verbose" }
  local ok, proc = pcall(vim.system, cmd, {
    stdin = msg, text = true, cwd = vim.fn.stdpath("cache"),
    stdout = function(_, data)
      if not data then return end
      j.out = j.out .. data
      while true do
        local nl = j.out:find("\n", 1, true)
        if not nl then break end
        local line = j.out:sub(1, nl - 1)
        j.out = j.out:sub(nl + 1)
        vim.schedule(function() on_line(line) end)
      end
    end,
  }, function(r)
    vim.schedule(function()
      if jobs[buf] ~= j then return end   -- cancelled
      if j.out ~= "" then on_line(j.out) end
      j.pos = vim.api.nvim_buf_get_extmark_by_id(buf, ns, j.mark, { details = true })
      if not result and r.code ~= 0 then
        result = { is_error = true, result = vim.trim(r.stderr or "") ~= "" and vim.trim(r.stderr) or ("exit " .. r.code) }
      end
      finish(j, result)
    end)
  end)
  if not ok then
    vim.api.nvim_buf_clear_namespace(buf, ns, 0, -1)
    return vim.notify("Claude: can't run claude (" .. tostring(proc) .. ")", vim.log.levels.ERROR)
  end
  j.proc = proc
  j.timer = uv.new_timer()
  j.timer:start(0, 100, vim.schedule_wrap(function()
    if jobs[buf] ~= j then return end
    j.tick = j.tick + 1
    preview(j)
  end))
  jobs[buf] = j
end

-- The markdown section around the cursor: its heading (or line 1) up to the next heading, trailing blanks left out.
local function section(buf, cur)
  local lines = vim.api.nvim_buf_get_lines(buf, 0, -1, false)
  local heads, fence = {}, false
  for i, l in ipairs(lines) do
    if l:match("^%s*```") or l:match("^%s*~~~") then fence = not fence
    elseif not fence and l:match("^#+%s") then heads[#heads + 1] = i end
  end
  local s, e = 1, #lines
  for _, h in ipairs(heads) do
    if h <= cur then s = h else e = h - 1; break end
  end
  while e > s and blank(lines[e]) do e = e - 1 end
  return s, e
end

-- mode: "insert" (answer below) or "edit" (replace). Uses the visual selection when called from visual mode.
function M.ask(mode)
  local buf = vim.api.nvim_get_current_buf()
  if jobs[buf] then cancel(buf); return vim.notify("Claude: cancelled") end
  if vim.fn.executable("claude") == 0 then return vim.notify("Claude: `claude` is not on PATH", vim.log.levels.ERROR) end
  if not vim.bo[buf].modifiable then return vim.notify("Claude: buffer is not modifiable", vim.log.levels.WARN) end
  local s, e = vim.fn.line("."), vim.fn.line(".")
  local m = vim.fn.mode()
  if m == "v" or m == "V" or m == "\22" then
    s, e = vim.fn.line("v"), vim.fn.line(".")
    if s > e then s, e = e, s end
    vim.cmd("normal! \27")
  elseif mode == "edit" and vim.bo[buf].filetype == "markdown" then
    s, e = section(buf, s)
  end
  vim.ui.input({ prompt = mode == "edit" and "Edit with Claude: " or "Ask Claude: " }, function(q)
    if not q or vim.trim(q) == "" then return end
    if not vim.api.nvim_buf_is_valid(buf) then return end
    run(buf, s, e, mode, vim.trim(q))
  end)
end

function M.setup()
  vim.keymap.set({ "n", "x" }, "<leader>A", function() M.ask("insert") end, { desc = "Ask Claude (answer goes below the line/selection)" })
  vim.keymap.set({ "n", "x" }, "<leader>E", function() M.ask("edit") end, { desc = "Edit section/selection with Claude (replaces it)" })
end

return M
