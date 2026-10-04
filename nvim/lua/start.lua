-- Startup page (mini.starter): next tasks, and every action on one key.
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
      if vim.bo.filetype == "ministarter" then pcall(require("mini.starter").refresh) end
    end)
  end)
end

local function task_label(t)
  local text = t.display:gsub("%s*_%(.-%)_%s*$", "")
  local when = t.due and (t.group == "overdue" and "overdue" or t.due:sub(6)) or "anytime"
  local cat = t.category_name and ("  · " .. t.category_name) or ""
  return ("%s  (%s)%s"):format(text, when, cat)
end

local function task_items()
  if tasks == nil then return { { name = "loading…", action = "", section = "Next up" } } end
  if #tasks == 0 then return { { name = "nothing open", action = "", section = "Next up" } } end
  local items = {}
  for i, t in ipairs(tasks) do
    items[i] = {
      name = ("%d  %s"):format(i, task_label(t)),
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
      vim.system({ "pkill", "-f", "notesview serve" }):wait()
      os.rename(bin .. ".new", bin)
      vim.system({ "notesview", "open", "--tasks" }, { env = { NOTES_DIR = NOTES }, stdout = false, stderr = false }, function() end)
      vim.notify("notesview rebuilt and restarted")
    end)
  end)
end

-- ── Vocab quiz: hand the real terminal to notes/quiz.py (outside nvim), come back Home after ──
local function run_quiz()
  local script = NOTES .. "/quiz.py"
  if vim.fn.filereadable(script) == 0 then return vim.notify("quiz not found: " .. script, vim.log.levels.ERROR) end
  -- `:!` children get piped stdio and no controlling terminal (no /dev/tty), so input() would hit EOF
  -- at once. Find nvim's own tty device, point the quiz at it, leave the alternate screen, switch to
  -- cooked mode, turn focus/mouse reporting off (else cmd-tab types ^[[I / ^[[O), then restart nvim.
  -- (The server process has no tty; its parent, the TUI, does.)
  local tty = vim.trim(vim.fn.system({ "ps", "-o", "tty=", "-p", tostring(uv.os_getppid()) }))
  if tty == "" or tty:find("?", 1, true) then return vim.notify("quiz: can't find the terminal", vim.log.levels.ERROR) end
  tty = "/dev/" .. (tty:match("^tty") and tty or "tty" .. tty)
  -- The TUI also reads this tty and would steal keystrokes (an eaten answer + Enter), so freeze it
  -- (SIGSTOP) while the quiz runs; the trap resumes it however the command ends.
  local tui = uv.os_getppid()
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
  if not pcall(vim.cmd, 'restart lua require("mini.starter").open()') then
    vim.cmd("redraw!")
    load_tasks()
    pcall(require("mini.starter").open)
  end
end

-- ── One-key actions ──────────────────────────────────────────────
local actions = {
  { "t", "Task list", function() feed("<leader>no") end },
  { "v", "Task view (window)", function() feed("<leader>nt") end },
  { "c", "Capture a task", function() feed("<leader>ni") end },
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

  starter.setup({
    header = greeting,
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

  vim.api.nvim_create_autocmd("FocusGained", { callback = load_tasks })  -- fresh tasks when you come back

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
