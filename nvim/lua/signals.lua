-- Workflow signals for the data layer (renderer/signals.go; used by the Data tab and, later, the
-- score market's traders). Appends to $NOTES_DIR/.signals/YYYY-MM-DD.jsonl:
--   hb every 5 s while typing: keystroke COUNT, backspaces, seconds in insert mode, typing bursts
--      (runs of keys less than 2 s apart: how many started, the longest), pause seconds (2-30 s gaps),
--      and the current note's top-level folder
--   buf on buffer switch (a short hash of the path, never the name), focus / blur, start_enter /
--   start_leave, claude launches, and task ticks made in nvim
-- No keystroke contents or text are ever recorded. NVIM_NOTES_NO_SIGNALS=1 (or the "nvim" switch
-- on the Data tab) turns it off.
local M = {}

local uv = vim.uv or vim.loop
local NOTES = vim.fn.expand((vim.env.NOTES_DIR and vim.env.NOTES_DIR ~= "") and vim.env.NOTES_DIR or "~/notes")
local enabled = vim.env.NVIM_NOTES_NO_SIGNALS == nil or vim.env.NVIM_NOTES_NO_SIGNALS == ""

local queue = {}
local keys, bs, ins_since, ins_secs = 0, 0, nil, 0
local bursts, burst_len, max_burst, last_key, pause = 0, 0, 0, nil, 0
local BS = vim.keycode("<BS>")

local function stamp() return os.date("%Y-%m-%dT%H:%M:%S") end

-- the Data tab's "nvim" switch (.signals/config.json), re-read every minute
local switched_on, checked = true, 0
local function on()
  if not enabled then return false end
  if uv.now() - checked > 60000 then
    checked = uv.now()
    local f = io.open(NOTES .. "/.signals/config.json", "r")
    if f then
      local ok, c = pcall(vim.json.decode, f:read("*a"))
      f:close()
      switched_on = not (ok and type(c) == "table" and type(c.sensors) == "table" and c.sensors.nvim == false)
    end
  end
  return switched_on
end

local function push(rec)
  if not on() then return end
  rec.at = rec.at or stamp()
  rec.src = rec.src or "nvim"
  queue[#queue + 1] = rec
end

local function flush()
  if #queue == 0 then return end
  local dir = NOTES .. "/.signals"
  vim.fn.mkdir(dir, "p")
  local f = io.open(dir .. "/" .. os.date("%Y-%m-%d") .. ".jsonl", "a")
  if not f then queue = {}; return end
  for _, r in ipairs(queue) do f:write(vim.json.encode(r), "\n") end
  f:close()
  queue = {}
end

-- top-level folder of the current buffer when it is a note, "" otherwise
local function folder()
  local name = vim.api.nvim_buf_get_name(0)
  if name == "" or name:sub(1, #NOTES + 1) ~= NOTES .. "/" then return "" end
  return name:sub(#NOTES + 2):match("^([^/]+)/") or ""
end

local function heartbeat()
  if ins_since then
    local now = uv.now()
    ins_secs = ins_secs + (now - ins_since) / 1000
    ins_since = now
  end
  if keys > 0 or ins_secs > 0 then
    push({ ev = "hb", keys = keys, bs = bs, ins = math.floor(ins_secs + 0.5), bursts = bursts,
      max_burst = math.max(max_burst, burst_len), pause = math.floor(pause + 0.5), folder = folder() })
  end
  keys, bs, ins_secs, bursts, max_burst, pause = 0, 0, 0, 0, 0, 0
end

-- Public: one-off events (claude, start_enter, start_leave).
function M.emit(ev, fields)
  local r = fields or {}
  r.ev = ev
  push(r)
  if ev == "claude" then flush() end -- nvim is about to quit
end

-- Public: a task ticked or unticked in nvim (the Go side logs viewer ticks and captures itself).
function M.task(ev, line)
  local diff = tonumber(line:match("[%s%(%[]!([123])%f[^%w]") or "") or 0
  local r = { src = "task", ev = ev, diff = diff, folder = folder() }
  local mon, d, h, mi = line:match("_%((%a%a%a) (%d%d) (%d%d):(%d%d)%)_")
  if mon then
    local months = { Jan = 1, Feb = 2, Mar = 3, Apr = 4, May = 5, Jun = 6, Jul = 7, Aug = 8, Sep = 9, Oct = 10, Nov = 11, Dec = 12 }
    local y = tonumber(os.date("%Y"))
    local function at(year) return os.time({ year = year, month = months[mon] or 1, day = tonumber(d), hour = tonumber(h), min = tonumber(mi) }) end
    local t = at(y)
    if t > os.time() then t = at(y - 1) end
    r.age_min = math.floor((os.time() - t) / 60)
  end
  r.due = line:match("@(%d%d%d%d%-%d%d%-%d%d)")
  push(r)
end

function M.setup()
  if not enabled then return end
  local ns = vim.api.nvim_create_namespace("notes_signals")
  vim.on_key(function(_, typed)
    if not typed or typed == "" then return end
    keys = keys + 1                     -- counts only, never the key
    if typed == BS then bs = bs + 1 end
    local now = uv.now()
    local gap = last_key and (now - last_key) / 1000 or math.huge
    if gap < 2 then
      burst_len = burst_len + 1
    else
      max_burst = math.max(max_burst, burst_len)
      bursts, burst_len = bursts + 1, 1
      if gap < 30 then pause = pause + gap end
    end
    last_key = now
  end, ns)
  local group = vim.api.nvim_create_augroup("NotesSignals", { clear = true })
  vim.api.nvim_create_autocmd("ModeChanged", {
    group = group,
    callback = function()
      local m = vim.api.nvim_get_mode().mode
      local now = uv.now()
      if ins_since then ins_secs = ins_secs + (now - ins_since) / 1000 end
      ins_since = (m:sub(1, 1) == "i" or m:sub(1, 1) == "R") and now or nil
    end,
  })
  vim.api.nvim_create_autocmd("BufEnter", {
    group = group,
    callback = function(ev)
      local name = vim.api.nvim_buf_get_name(ev.buf)
      if name == "" or vim.bo[ev.buf].buftype ~= "" then return end
      push({ ev = "buf", note = vim.fn.sha256(name):sub(1, 10), folder = folder() })
    end,
  })
  vim.api.nvim_create_autocmd("FocusGained", { group = group, callback = function() push({ ev = "focus" }) end })
  vim.api.nvim_create_autocmd("FocusLost", { group = group, callback = function() push({ ev = "blur" }); flush() end })
  vim.api.nvim_create_autocmd("VimLeavePre", { group = group, callback = function() heartbeat(); flush() end })
  uv.new_timer():start(5000, 5000, vim.schedule_wrap(heartbeat))
  uv.new_timer():start(10000, 10000, vim.schedule_wrap(flush))
end

return M
