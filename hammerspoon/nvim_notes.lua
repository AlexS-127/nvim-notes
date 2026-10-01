-- Global quick capture for nvim-notes (macOS, Hammerspoon).
-- Ctrl+Option+I opens a small prompt; the text goes to inbox.md through
-- `notesview capture`, which turns @tomorrow / @fri / @oct6 into real dates.
--
-- install.sh links this file to ~/.hammerspoon/nvim_notes.lua and adds to
-- ~/.hammerspoon/init.lua:
--   require("nvim_notes").start({ notesview = "…/notesview", notes_dir = "…/notes" })
--
-- One-time setup: open Hammerspoon, allow it under System Settings → Privacy &
-- Security → Accessibility, then choose "Reload Config" from its menu.
local M = {}

local function trim(s) return (s or ""):gsub("^%s+", ""):gsub("%s+$", "") end

function M.start(opts)
  opts = opts or {}
  local home = os.getenv("HOME")
  local bin = opts.notesview or (home .. "/.local/bin/notesview")
  local notes_dir = opts.notes_dir or (home .. "/notes")
  if M.hotkey then M.hotkey:delete() end
  M.hotkey = hs.hotkey.bind(opts.mods or { "ctrl", "alt" }, opts.key or "i", function()
    local prev = hs.application.frontmostApplication()
    hs.focus()
    local button, text = hs.dialog.textPrompt("Capture to inbox",
      "Due dates like @tomorrow or @fri and class tags like #act200 work.", "", "Capture", "Cancel")
    if prev then prev:activate() end
    text = trim(text)
    if button ~= "Capture" or text == "" then return end
    local task = hs.task.new(bin, function(code, out, err)
      if code == 0 then
        hs.alert.show("📥 " .. trim(out), 1.5)
      else
        hs.alert.show("Capture failed: " .. trim(err ~= "" and err or out), 4)
      end
    end, { "capture", "--dir", notes_dir, "--", text })
    if not (task and task:start()) then hs.alert.show("Capture failed: cannot run " .. bin, 4) end
  end)
  return M
end

return M
