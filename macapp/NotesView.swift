import Cocoa
import WebKit
import Carbon.HIToolbox

// NotesView.app: the notesview viewer in a native window with the same
// translucency and blur as Ghostty (background-opacity / background-blur).

let port = ProcessInfo.processInfo.environment["NOTESVIEW_PORT"] ?? "7777"
let base = "http://127.0.0.1:\(port)"

/// Reads `background-opacity` from the Ghostty config so the two always match.
func ghosttyOpacity() -> Double {
    let home = NSHomeDirectory()
    for p in ["\(home)/.config/ghostty/config",
              "\(home)/Library/Application Support/com.mitchellh.ghostty/config"] {
        guard let text = try? String(contentsOfFile: p, encoding: .utf8) else { continue }
        for line in text.split(separator: "\n") {
            let parts = line.split(separator: "=", maxSplits: 1).map { $0.trimmingCharacters(in: .whitespaces) }
            if parts.count == 2, parts[0] == "background-opacity", let v = Double(parts[1]) {
                return min(max(v, 0.05), 1)
            }
        }
    }
    return 0.9
}

let lip: CGFloat = 28

// --glass is set by the page from notesview's config.json ("opacity", Settings → Transparency),
// live; the value here (Ghostty's background-opacity) is only the fallback.
func glassCSS(_ opacity: Double) -> String {
    let pct = Int((opacity * 100).rounded())
    return """
    html { background: color-mix(in srgb, var(--bg) var(--glass, \(pct)%), transparent) !important; padding-top: \(Int(lip))px; box-sizing: border-box; }
    body { background: transparent !important; height: 100%; }
    #sidebar { background: color-mix(in srgb, var(--bg2) var(--glass, \(pct)%), transparent) !important; height: calc(100vh - \(Int(lip))px) !important; }
    #main { height: calc(100vh - \(Int(lip))px) !important; }
    #btn-expand { top: \(Int(lip) + 12)px !important; left: 78px !important; }
    """
}

/// Invisible strip over the top lip: drags the window, double-click zooms.
final class DragStrip: NSView {
    override var mouseDownCanMoveWindow: Bool { true }
    override func mouseDown(with e: NSEvent) {
        if e.clickCount == 2 { window?.performZoom(nil) } else { window?.performDrag(with: e) }
    }
}

/// Covers the whole overlay: drags it (the overlay has nothing to click), right-click for its menu.
final class OverlayDrag: NSView {
    weak var app: AppDelegate?
    override func mouseDown(with e: NSEvent) { window?.performDrag(with: e) }
    override func mouseUp(with e: NSEvent) { app?.saveOverlayFrame() }
    override func menu(for e: NSEvent) -> NSMenu? { app?.overlayMenu() }
}

/// The overlay window: borderless, see-through, above other apps (also full-screen ones and every Space).
final class OverlayPanel: NSPanel {
    override var canBecomeKey: Bool { false }
    override var canBecomeMain: Bool { false }
}

@_silgen_name("CGSMainConnectionID") func CGSMainConnectionID() -> Int32
@_silgen_name("CGSSetWindowBackgroundBlurRadius")
func CGSSetWindowBackgroundBlurRadius(_ cid: Int32, _ wid: Int, _ radius: Int32) -> Int32

final class AppDelegate: NSObject, NSApplicationDelegate, WKNavigationDelegate, WKUIDelegate, WKScriptMessageHandler {
    var window: NSWindow!
    var web: WKWebView!
    var pendingURL: String?
    var overlay: OverlayPanel?
    var overlayWeb: WKWebView?
    var hotKey: EventHotKeyRef?
    let defaults = UserDefaults.standard

    func applicationDidFinishLaunching(_ n: Notification) {
        let opacity = ghosttyOpacity()
        let conf = WKWebViewConfiguration()
        let js = "(function(){var s=document.createElement('style');s.textContent=\(jsString(glassCSS(opacity)));document.documentElement.appendChild(s);})();"
        conf.userContentController.addUserScript(
            WKUserScript(source: js, injectionTime: .atDocumentEnd, forMainFrameOnly: true))
        // window.webkit.messageHandlers.nv.postMessage("overlay") from the page (viewer key o, Settings)
        conf.userContentController.add(self, name: "nv")

        web = WKWebView(frame: .zero, configuration: conf)
        web.setValue(false, forKey: "drawsBackground")   // let the blur show through
        if #available(macOS 12.0, *) { web.underPageBackgroundColor = .clear }
        web.navigationDelegate = self
        web.uiDelegate = self
        web.autoresizingMask = [.width, .height]

        let root = NSView()
        root.autoresizingMask = [.width, .height]

        window = NSWindow(contentRect: NSRect(x: 0, y: 0, width: 1100, height: 760),
                          styleMask: [.titled, .closable, .miniaturizable, .resizable, .fullSizeContentView],
                          backing: .buffered, defer: false)
        window.titlebarAppearsTransparent = true
        window.titleVisibility = .hidden
        window.isOpaque = false
        window.backgroundColor = .clear
        window.contentView = root
        web.frame = root.bounds
        root.addSubview(web)
        let strip = DragStrip(frame: NSRect(x: 0, y: root.bounds.height - lip, width: root.bounds.width, height: lip))
        strip.autoresizingMask = [.width, .minYMargin]
        root.addSubview(strip)
        window.isReleasedWhenClosed = false   // the overlay can outlive it; the Dock icon brings it back
        NotificationCenter.default.addObserver(forName: NSWindow.willCloseNotification, object: window, queue: .main) { [weak self] _ in
            self?.windowWillCloseCheck()
        }
        window.setFrameAutosaveName("NotesViewMain")
        if !window.setFrameUsingName("NotesViewMain") { window.center() }
        window.makeKeyAndOrderFront(nil)
        // Same window blur Ghostty uses (background-blur = 30).
        _ = CGSSetWindowBackgroundBlurRadius(CGSMainConnectionID(), window.windowNumber, 30)

        buildMenu()
        registerHotKey()
        if defaults.bool(forKey: "overlayVisible") { showOverlay() }
        load(pendingURL ?? "\(base)/?app=1")
        debugEval()
        NSApp.activate(ignoringOtherApps: true)
    }

    func jsString(_ s: String) -> String {
        let d = try! JSONSerialization.data(withJSONObject: [s])
        let t = String(data: d, encoding: .utf8)!
        return String(t.dropFirst().dropLast())
    }

    // Debug: NV_EVAL=<js> NV_EVAL_OUT=<file> writes the JS result 4 s after load.
    func debugEval() {
        guard let js = ProcessInfo.processInfo.environment["NV_EVAL"],
              let out = ProcessInfo.processInfo.environment["NV_EVAL_OUT"] else { return }
        DispatchQueue.main.asyncAfter(deadline: .now() + 4) { [weak self] in
            self?.web.evaluateJavaScript(js) { r, e in
                try? "\(String(describing: r ?? e))".write(toFile: out, atomically: true, encoding: .utf8)
            }
        }
        // NV_EVAL_OVERLAY=<js>: the same against the overlay's page 10 s after load (into NV_EVAL_OUT.overlay)
        if let ojs = ProcessInfo.processInfo.environment["NV_EVAL_OVERLAY"] {
            DispatchQueue.main.asyncAfter(deadline: .now() + 10) { [weak self] in
                guard let w = self?.overlayWeb else {
                    try? "no overlay".write(toFile: out + ".overlay", atomically: true, encoding: .utf8); return
                }
                w.evaluateJavaScript(ojs) { r, e in
                    try? "\(String(describing: r ?? e))".write(toFile: out + ".overlay", atomically: true, encoding: .utf8)
                }
            }
        }
    }

    func load(_ s: String) {
        guard let u = URL(string: s) else { return }
        web?.load(URLRequest(url: u))
    }

    // `open -a NotesView --args URL` or a notesview://… style launch passes the target here.
    func application(_ app: NSApplication, open urls: [URL]) {
        if let u = urls.first { pendingURL = u.absoluteString; load(u.absoluteString) }
    }

    // Closing the main window quits, unless the overlay is up (then the Dock icon reopens the window).
    func applicationShouldTerminateAfterLastWindowClosed(_ app: NSApplication) -> Bool { false }
    func windowWillCloseCheck() {
        if overlay?.isVisible != true { NSApp.terminate(nil) }
    }
    func applicationShouldHandleReopen(_ app: NSApplication, hasVisibleWindows flag: Bool) -> Bool {
        window.makeKeyAndOrderFront(nil)
        return true
    }

    func userContentController(_ c: WKUserContentController, didReceive m: WKScriptMessage) {
        if (m.body as? String) == "overlay" { toggleOverlay() }
    }

    // ── overlay: today's score, pace, tasks done, quiz time and new words in a floating see-through strip ──
    @objc func toggleOverlay() {
        if overlay?.isVisible == true { hideOverlay() } else { showOverlay() }
    }

    func showOverlay() {
        if overlay == nil {
            let size = NSSize(width: 460, height: 64)
            let screen = NSScreen.main?.visibleFrame ?? NSRect(x: 0, y: 0, width: 1440, height: 900)
            let p = OverlayPanel(contentRect: NSRect(x: screen.maxX - size.width - 24, y: screen.maxY - size.height - 24, width: size.width, height: size.height),
                                 styleMask: [.borderless, .nonactivatingPanel], backing: .buffered, defer: false)
            p.isFloatingPanel = true
            p.level = .floating
            p.hidesOnDeactivate = false
            p.isOpaque = false
            p.backgroundColor = .clear
            p.hasShadow = false
            p.collectionBehavior = [.canJoinAllSpaces, .fullScreenAuxiliary, .stationary, .ignoresCycle]
            let root = NSView(frame: NSRect(origin: .zero, size: size))
            root.autoresizingMask = [.width, .height]
            let w = WKWebView(frame: root.bounds)
            w.setValue(false, forKey: "drawsBackground")
            if #available(macOS 12.0, *) { w.underPageBackgroundColor = .clear }
            w.autoresizingMask = [.width, .height]
            w.navigationDelegate = self
            root.addSubview(w)
            let drag = OverlayDrag(frame: root.bounds)
            drag.autoresizingMask = [.width, .height]
            drag.app = self
            root.addSubview(drag)
            p.contentView = root
            if let f = defaults.string(forKey: "overlayFrame") { p.setFrame(NSRectFromString(f), display: false) }
            p.ignoresMouseEvents = defaults.bool(forKey: "overlayClickThrough")
            w.load(URLRequest(url: URL(string: "\(base)/overlay.html")!))
            overlay = p
            overlayWeb = w
        }
        overlay?.orderFrontRegardless()
        defaults.set(true, forKey: "overlayVisible")
        updateOverlayMenu()
    }

    func hideOverlay() {
        overlay?.orderOut(nil)
        defaults.set(false, forKey: "overlayVisible")
        updateOverlayMenu()
        if window?.isVisible != true { NSApp.terminate(nil) }
    }

    func saveOverlayFrame() {
        if let f = overlay?.frame { defaults.set(NSStringFromRect(f), forKey: "overlayFrame") }
    }

    /// Click-through: clicks go to the app underneath (the overlay can't be dragged then; turn it off from the menu).
    @objc func toggleClickThrough() {
        let on = !defaults.bool(forKey: "overlayClickThrough")
        defaults.set(on, forKey: "overlayClickThrough")
        overlay?.ignoresMouseEvents = on
        updateOverlayMenu()
    }

    @objc func resetOverlay() {
        defaults.removeObject(forKey: "overlayFrame")
        overlay?.orderOut(nil)
        overlay = nil
        overlayWeb = nil
        showOverlay()
    }

    func overlayMenu() -> NSMenu {
        let m = NSMenu()
        m.addItem(withTitle: "Hide Overlay", action: #selector(toggleOverlay), keyEquivalent: "").target = self
        m.addItem(withTitle: "Click-Through", action: #selector(toggleClickThrough), keyEquivalent: "").target = self
        m.addItem(withTitle: "Reset Position", action: #selector(resetOverlay), keyEquivalent: "").target = self
        m.addItem(withTitle: "Open NotesView", action: #selector(showMain), keyEquivalent: "").target = self
        return m
    }

    @objc func showMain() { window.makeKeyAndOrderFront(nil); NSApp.activate(ignoringOtherApps: true) }

    var overlayItem: NSMenuItem?
    var clickItem: NSMenuItem?
    func updateOverlayMenu() {
        overlayItem?.state = overlay?.isVisible == true ? .on : .off
        clickItem?.state = defaults.bool(forKey: "overlayClickThrough") ? .on : .off
    }

    /// ⌥⌘O anywhere toggles the overlay (Carbon hot key: no accessibility permission needed).
    func registerHotKey() {
        var spec = EventTypeSpec(eventClass: OSType(kEventClassKeyboard), eventKind: UInt32(kEventHotKeyPressed))
        InstallEventHandler(GetApplicationEventTarget(), { _, _, ctx in
            let me = Unmanaged<AppDelegate>.fromOpaque(ctx!).takeUnretainedValue()
            DispatchQueue.main.async { me.toggleOverlay() }
            return noErr
        }, 1, &spec, Unmanaged.passUnretained(self).toOpaque(), nil)
        let id = EventHotKeyID(signature: OSType(0x4e564f56), id: 1)   // "NVOV"
        RegisterEventHotKey(UInt32(kVK_ANSI_O), UInt32(cmdKey | optionKey), id, GetApplicationEventTarget(), 0, &hotKey)
    }

    // Server not up yet (launch agent restarting): retry instead of a blank window.
    func webView(_ w: WKWebView, didFailProvisionalNavigation nav: WKNavigation!, withError e: Error) {
        DispatchQueue.main.asyncAfter(deadline: .now() + 1.5) { [weak self] in
            let fallback = w === self?.overlayWeb ? "\(base)/overlay.html" : "\(base)/?app=1"
            if let u = w.url ?? URL(string: fallback) { w.load(URLRequest(url: u)) }
        }
    }

    // Anything that is not the local viewer opens in the default browser.
    func webView(_ w: WKWebView, decidePolicyFor action: WKNavigationAction,
                 decisionHandler: @escaping (WKNavigationActionPolicy) -> Void) {
        if let u = action.request.url, let host = u.host, host != "127.0.0.1", host != "localhost",
           u.scheme == "http" || u.scheme == "https" || u.scheme == "mailto" {
            NSWorkspace.shared.open(u); decisionHandler(.cancel); return
        }
        decisionHandler(.allow)
    }

    func webView(_ w: WKWebView, createWebViewWith c: WKWebViewConfiguration,
                 for a: WKNavigationAction, windowFeatures f: WKWindowFeatures) -> WKWebView? {
        if let u = a.request.url { NSWorkspace.shared.open(u) }
        return nil
    }

    @objc func reload() { web.reload() }
    @objc func zoomIn() { web.pageZoom += 0.1 }
    @objc func zoomOut() { web.pageZoom = max(0.4, web.pageZoom - 0.1) }
    @objc func zoomReset() { web.pageZoom = 1 }

    func buildMenu() {
        let main = NSMenu()
        func add(_ title: String, _ items: [(String, Selector?, String)], target: AnyObject? = nil) {
            let item = NSMenuItem(); main.addItem(item)
            let m = NSMenu(title: title); item.submenu = m
            for (t, sel, key) in items {
                if t == "-" { m.addItem(.separator()); continue }
                let mi = NSMenuItem(title: t, action: sel, keyEquivalent: key)
                if target != nil { mi.target = target }
                m.addItem(mi)
            }
        }
        add("NotesView", [("Hide NotesView", #selector(NSApplication.hide(_:)), "h"),
                          ("-", nil, ""),
                          ("Quit NotesView", #selector(NSApplication.terminate(_:)), "q")])
        add("Edit", [("Cut", #selector(NSText.cut(_:)), "x"), ("Copy", #selector(NSText.copy(_:)), "c"),
                     ("Paste", #selector(NSText.paste(_:)), "v"), ("Select All", #selector(NSText.selectAll(_:)), "a")])
        add("View", [("Reload", #selector(reload), "r"), ("-", nil, ""),
                     ("Zoom In", #selector(zoomIn), "+"), ("Zoom Out", #selector(zoomOut), "-"),
                     ("Actual Size", #selector(zoomReset), "0")], target: self)
        add("Overlay", [("Show Overlay  (⌥⌘O anywhere)", #selector(toggleOverlay), "O"),
                        ("Click-Through", #selector(toggleClickThrough), ""),
                        ("Reset Position", #selector(resetOverlay), "")], target: self)
        if let m = main.items.last?.submenu {
            overlayItem = m.items.first
            clickItem = m.items.count > 1 ? m.items[1] : nil
        }
        updateOverlayMenu()
        add("Window", [("Close", #selector(NSWindow.performClose(_:)), "w"),
                       ("Minimize", #selector(NSWindow.performMiniaturize(_:)), "m")])
        NSApp.mainMenu = main
    }
}

let app = NSApplication.shared
let delegate = AppDelegate()
app.delegate = delegate
app.setActivationPolicy(.regular)
if let arg = CommandLine.arguments.dropFirst().first(where: { $0.hasPrefix("http") }) {
    delegate.pendingURL = arg
}
app.run()
