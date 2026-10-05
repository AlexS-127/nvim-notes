-- Startup page (mini.starter): today's score and numbers, next tasks, and every action on one key.
local M = {}

local uv = vim.uv or vim.loop
local NOTES = vim.fn.expand((vim.env.NOTES_DIR and vim.env.NOTES_DIR ~= "") and vim.env.NOTES_DIR or "~/notes")
local RENDERER = vim.fn.fnamemodify(vim.fn.resolve(vim.fn.stdpath("config")), ":h") .. "/renderer"

local function feed(keys)
  vim.api.nvim_feedkeys(vim.keycode(keys), "m", false)
end

local function greeting()
  local h = tonumber(os.date("%H"))
  local part = h < 5 and "night" or h < 12 and "morning" or h < 18 and "afternoon" or "evening"
  return ("good %s, alex\n%s"):format(part, os.date("%A, %B %d"))
end

-- ── Today: score, its graph, tasks made/done/left, quiz time, words ──────
-- Read from the running notesview server (/api/activity, the same numbers as the viewer's Activity
-- view). The local.notesview-serve launch agent keeps it up; if it is down, the page says so.
local stats = nil   -- nil: still loading, false: server not reachable
local PORT = vim.env.NOTESVIEW_PORT or "7777"
local BARS = { "▁", "▂", "▃", "▄", "▅", "▆", "▇", "█" }

local function refresh()
  if vim.bo.filetype == "ministarter" then pcall(require("mini.starter").refresh) end
end

local function load_stats()
  vim.system({ "curl", "-sf", "--max-time", "2", "http://127.0.0.1:" .. PORT .. "/api/activity" }, { text = true }, function(r)
    vim.schedule(function()
      local ok, decoded = pcall(vim.json.decode, r.stdout or "", { luanil = { object = true, array = true } })
      stats = (r.code == 0 and ok and type(decoded) == "table") and decoded or false
      refresh()
    end)
  end)
end

local function clock(secs) return ("%02d:%02d"):format(math.floor(secs / 3600), math.floor(secs % 3600 / 60)) end

local function duration(secs)
  if secs >= 3600 then return ("%dh %02dm"):format(math.floor(secs / 3600), math.floor(secs % 3600 / 60)) end
  return ("%dm %02ds"):format(math.floor(secs / 60), secs % 60)
end

-- Today's score log (score_line) as a step curve from 00:00 to now, `rows` lines of block characters.
-- The bottom row always has at least ▁ so the elapsed part of the day reads as a baseline.
local function score_graph(line, width, rows)
  local t = os.date("*t")
  local now = t.hour * 3600 + t.min * 60 + t.sec
  local pts = {}
  for _, p in ipairs(line or {}) do
    local h, m, s = tostring(p.at):match("T(%d+):(%d+):(%d+)")
    if h then pts[#pts + 1] = { h * 3600 + m * 60 + s, tonumber(p.total) or 0 } end
  end
  table.sort(pts, function(a, b) return a[1] < b[1] end)
  local vals, top, j, cur = {}, 0, 1, 0
  for c = 1, width do
    local at = now * c / width
    while pts[j] and pts[j][1] <= at do cur = pts[j][2]; j = j + 1 end
    vals[c] = cur
    top = math.max(top, cur)
  end
  local out = {}
  for r = rows, 1, -1 do
    local cells = {}
    for c = 1, width do
      local eighths = top > 0 and math.floor(vals[c] / top * rows * 8 + 0.5) or 0
      local fill = math.max(0, math.min(8, eighths - (r - 1) * 8))
      if r == 1 then fill = math.max(fill, 1) end
      cells[c] = fill > 0 and BARS[fill] or " "
    end
    local label = r == rows and ("%3d"):format(top) or r == 1 and "  0" or "   "
    out[#out + 1] = label .. " ┤" .. table.concat(cells)
  end
  out[#out + 1] = "     " .. (" "):rep(math.max(1, width - 5)) .. clock(now)
  return out
end

local function stats_lines()
  if stats == nil then return { "score …" } end
  if not stats then return { "score: notesview server not running" } end
  local today = stats.today or os.date("%Y-%m-%d")
  local score = ((stats.scores or {})[today] or {}).total or 0
  local day = (stats.days or {})[today] or {}
  local lines = { ("score %d"):format(score) }
  vim.list_extend(lines, score_graph(stats.score_line, math.max(16, math.min(48, vim.o.columns - 30)), 3))
  lines[#lines + 1] = ("tasks  %d made · %d done · %d left    quiz %s    words %d"):format(
    day.created or 0, day.done or 0, stats.open or 0, duration(stats.study_today or 0), stats.words_today or 0)
  return lines
end

local function header()
  return greeting() .. "\n\n" .. table.concat(stats_lines(), "\n")
end

-- ── Next tasks ───────────────────────────────────────────────────
-- Scheduled tasks by due date (overdue first). If fewer than 3 are scheduled,
-- the rest are picked at random from the unscheduled ones.
local tasks = nil   -- nil: still loading

local function pick_next(all, n)
  local dated, undated = {}, {}
  for _, t in ipairs(all) do table.insert(t.due and dated or undated, t) end
  table.sort(dated, function(a, b) return a.due < b.due end)
  local out = {}
  for i = 1, math.min(n, #dated) do out[i] = dated[i] end
  math.randomseed(os.time())
  while #out < n and #undated > 0 do
    table.insert(out, table.remove(undated, math.random(#undated)))
  end
  return out
end

local function load_tasks()
  if vim.fn.executable("notesview") == 0 then tasks = {}; return end
  vim.system({ "notesview", "tasks", "--json", "--dir", NOTES }, { text = true, env = { NOTES_DIR = NOTES } }, function(r)
    vim.schedule(function()
      local ok, decoded = pcall(vim.json.decode, r.stdout or "")
      tasks = (r.code == 0 and ok and type(decoded) == "table") and pick_next(decoded, 3) or {}
      refresh()
    end)
  end)
end

-- mini.starter centers the block by its widest line, so a task line wider than the rest pushes
-- everything left (and past the window width, centering gives up and it all hugs the left edge).
-- Every "Next up" line is cut and padded to the same width, so the page doesn't move when the
-- random picks change.
local function next_width()
  return math.max(30, math.min(60, vim.o.columns - 16))
end

-- `s` cut to at most `w` display columns, ending in "…" when shortened.
local function cut(s, w)
  if vim.fn.strdisplaywidth(s) <= w then return s end
  local n = w - 1
  while n > 0 and vim.fn.strdisplaywidth(vim.fn.strcharpart(s, 0, n)) > w - 1 do n = n - 1 end
  return vim.fn.strcharpart(s, 0, n) .. "…"
end

-- `s` cut, then padded with spaces, to exactly `w` display columns.
local function fit(s, w)
  s = cut(s, w)
  return s .. (" "):rep(w - vim.fn.strdisplaywidth(s))
end

local function task_label(t, w)
  local text = t.display:gsub("%s*_%(.-%)_%s*$", "")
  local when = t.due and (t.group == "overdue" and "overdue" or t.due:sub(6)) or "anytime"
  local cat = t.category_name and ("  · " .. t.category_name) or ""
  local tail = cut(("  (%s)%s"):format(when, cat), w - 10)   -- keep at least 10 columns for the text
  return fit(cut(text, w - vim.fn.strdisplaywidth(tail)) .. tail, w)
end

local function task_items()
  local w = next_width()
  if tasks == nil then return { { name = fit("loading…", w), action = "", section = "Next up" } } end
  if #tasks == 0 then return { { name = fit("nothing open", w), action = "", section = "Next up" } } end
  local items = {}
  for i, t in ipairs(tasks) do
    items[i] = {
      name = ("%d  %s"):format(i, task_label(t, w - 3)),
      section = "Next up",
      action = function()
        vim.cmd.edit(vim.fn.fnameescape(NOTES .. "/" .. t.file))
        pcall(vim.api.nvim_win_set_cursor, 0, { t.line, 0 })
      end,
    }
  end
  return items
end

-- ── Renderer: rebuild from source, restart the server, open the viewer ──
local function restart_renderer()
  if vim.fn.executable("go") == 0 then return vim.notify("go is not installed — can't rebuild notesview", vim.log.levels.ERROR) end
  if vim.fn.isdirectory(RENDERER) == 0 then return vim.notify("renderer source not found: " .. RENDERER, vim.log.levels.ERROR) end
  local bin = vim.fn.expand("~/.local/bin/notesview")
  vim.notify("Rebuilding notesview…")
  vim.system({ "go", "build", "-o", bin .. ".new", "." }, { cwd = RENDERER, text = true }, function(b)
    vim.schedule(function()
      if b.code ~= 0 then
        return vim.notify("notesview build failed:\n" .. (b.stderr or ""), vim.log.levels.ERROR)
      end
      -- swap the binary first: when the local.notesview-serve launch agent runs the server,
      -- launchd restarts it the moment it is killed, and it must pick up the new build
      os.rename(bin .. ".new", bin)
      vim.system({ "pkill", "-f", "notesview serve" }):wait()
      -- give launchd up to 3 s to bring it back, so `open` doesn't start a second server
      vim.wait(3000, function()
        return vim.system({ "curl", "-sf", "-o", "/dev/null", "http://127.0.0.1:" .. (vim.env.NOTESVIEW_PORT or "7777") .. "/api/status" }):wait().code == 0
      end, 200)
      vim.system({ "notesview", "open", "--activity" }, { env = { NOTES_DIR = NOTES }, stdout = false, stderr = false }, function() end)
      vim.notify("notesview rebuilt and restarted")
    end)
  end)
end

-- Quiz in a full-screen terminal tab inside nvim. Used when nvim is a job of an interactive shell
-- (typed `nvim`, or the `notes` function): SIGSTOPping the TUI there makes the shell think the job
-- was suspended (like Ctrl-Z), so it grabs the terminal back and fights the quiz for keystrokes.
local function run_quiz_in_terminal(script)
  vim.cmd("tabnew")
  local buf = vim.api.nvim_get_current_buf()
  vim.fn.jobstart({ "sh", "-c", 'python3 "$1"; printf "\\n[Enter to go back]"; read _', "sh", script }, {
    term = true,
    env = { NOTES_DIR = NOTES },
    on_exit = function()
      vim.schedule(function()
        if vim.api.nvim_buf_is_valid(buf) then pcall(vim.api.nvim_buf_delete, buf, { force = true }) end
        load_tasks()
        load_stats()
      end)
    end,
  })
  vim.cmd("startinsert")
end

-- ── Vocab quiz: hand the real terminal to notes/quiz.py (outside nvim), come back Home after ──
local function run_quiz()
  local script = NOTES .. "/quiz.py"
  if vim.fn.filereadable(script) == 0 then return vim.notify("quiz not found: " .. script, vim.log.levels.ERROR) end
  local tui0 = vim.g.notes_tui or uv.os_getppid()
  -- Under job control the TUI leads its own process group; then freezing it is unsafe (see above).
  if vim.trim(vim.fn.system({ "ps", "-o", "pgid=", "-p", tostring(tui0) })) == tostring(tui0) then
    return run_quiz_in_terminal(script)
  end
  -- `:!` children get piped stdio and no controlling terminal (no /dev/tty), so input() would hit EOF
  -- at once. Find nvim's own tty device, point the quiz at it, leave the alternate screen, switch to
  -- cooked mode, turn focus/mouse reporting off (else cmd-tab types ^[[I / ^[[O), then restart nvim.
  -- (The server process has no tty; its parent, the TUI, does.)
  -- After `:restart` the new server is orphaned (parent 1), so the TUI pid is handed over via vim.g.
  local tui = tui0
  local tty = vim.trim(vim.fn.system({ "ps", "-o", "tty=", "-p", tostring(tui) }))
  if tty == "" or tty:find("?", 1, true) then return vim.notify("quiz: can't find the terminal", vim.log.levels.ERROR) end
  tty = "/dev/" .. (tty:match("^tty") and tty or "tty" .. tty)
  -- The TUI also reads this tty and would steal keystrokes (an eaten answer + Enter), so freeze it
  -- (SIGSTOP) while the quiz runs; the trap resumes it however the command ends.
  local cmd = table.concat({
    "T=" .. vim.fn.shellescape(tty),
    "trap 'kill -CONT " .. tui .. "' EXIT",
    "kill -STOP " .. tui,
    "saved=$(stty -g <$T)",
    "printf '\\033[?1049l\\033[?1004l\\033[?1000l\\033[?1002l\\033[?1003l\\033[?1006l' >$T",
    "stty sane <$T",
    "python3 " .. vim.fn.shellescape(script) .. " <$T >$T 2>&1",
    "stty \"$saved\" <$T",
    "printf '\\033[?1049h\\033[?1004h" .. (vim.o.mouse ~= "" and "\\033[?1002h\\033[?1006h" or "") .. "' >$T",
  }, "; ")
  vim.cmd("silent !" .. vim.fn.escape(cmd, "%#!"))
  -- Restart nvim so the normal start page comes up clean (redrawing in place after the quiz looks off).
  if not pcall(vim.cmd, ("restart lua vim.g.notes_tui = %d; require('mini.starter').open()"):format(tui)) then
    vim.cmd("redraw!")
    load_tasks()
    load_stats()
    pcall(require("mini.starter").open)
  end
end

-- ── One-key actions ──────────────────────────────────────────────
local actions = {
  { "t", "Task list", function() feed("<leader>no") end },
  { "v", "Capture a task", function() feed("<leader>ni") end },
  { "c", "Claude (in ~)", function() feed("<leader>nC") end },
  { "e", "Inbox", function() feed("<leader>nI") end },
  { "d", "Today's note", function() feed("<leader>nd") end },
  { "n", "New note", function() feed("<leader>nn") end },
  { "f", "Find note", function() feed("<leader>nf") end },
  { "o", "Go to folder", function() feed("<leader>nF") end },
  { "g", "Search notes", function() feed("<leader>ng") end },
  { "s", "Study (vocab quiz)", run_quiz },
  { "r", "Restart renderer (rebuild)", restart_renderer },
  { "q", "Terminal", function() vim.cmd("qa") end },
}

local function action_items()
  local items = {}
  for _, a in ipairs(actions) do
    items[#items + 1] = { name = ("%s  %s"):format(a[1], a[2]), action = a[3], section = "Go" }
  end
  return items
end

function M.setup()
  local starter = require("mini.starter")
  load_tasks()
  load_stats()

  starter.setup({
    header = header,
    items = { task_items, action_items },
    footer = "",
    content_hooks = { starter.gen_hook.aligning("center", "center") },
  })

  vim.keymap.set("n", "<leader>H", function() starter.open() end, { desc = "Home (start page)" })
  vim.api.nvim_create_user_command("Home", function() starter.open() end, {})

  -- The start buffer is named after creation; swap-file setup for "ministarter://..." sometimes
  -- fails (E303) and aborts the page. It never needs a swap file, so switch it off just before naming.
  vim.api.nvim_create_autocmd("BufFilePre", {
    callback = function(ev)
      if vim.api.nvim_buf_get_name(ev.buf) == "" and vim.bo[ev.buf].buftype == "" then
        vim.bo[ev.buf].swapfile = false
      end
    end,
  })

  -- Every nvim instance names its start buffer "ministarter://1/welcome", so a second instance
  -- collides with the first one's swap file (E325). Nothing to recover there: just edit anyway.
  vim.api.nvim_create_autocmd("SwapExists", {
    pattern = "*ministarter*",
    callback = function() vim.v.swapchoice = "e" end,
  })

  -- fresh tasks and numbers when you come back; the numbers also tick over once a minute while the page is up
  vim.api.nvim_create_autocmd("FocusGained", { callback = function() load_tasks(); load_stats() end })
  vim.api.nvim_create_autocmd("User", { pattern = "MiniStarterOpened", callback = load_stats })
  uv.new_timer():start(60000, 60000, vim.schedule_wrap(function()
    if vim.bo.filetype == "ministarter" then load_stats() end
  end))

  -- mini.starter turns letters into a search query; give them back as direct keys.
  vim.api.nvim_create_autocmd("User", {
    pattern = "MiniStarterOpened",
    callback = function(ev)
      local function bmap(key, fn) vim.keymap.set("n", key, fn, { buffer = ev.buf, nowait = true, silent = true }) end
      for _, a in ipairs(actions) do bmap(a[1], a[3]) end
      for i = 1, 3 do
        bmap(tostring(i), function() if tasks and tasks[i] then
          vim.cmd.edit(vim.fn.fnameescape(NOTES .. "/" .. tasks[i].file))
          pcall(vim.api.nvim_win_set_cursor, 0, { tasks[i].line, 0 })
        end end)
      end
      bmap("<CR>", function() starter.eval_current_item() end)
    end,
  })
end

return M
