import Cocoa
import WebKit

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

func glassCSS(_ opacity: Double) -> String {
    let pct = Int((opacity * 100).rounded())
    return """
    html { background: color-mix(in srgb, var(--bg) \(pct)%, transparent) !important; padding-top: \(Int(lip))px; box-sizing: border-box; }
    body { background: transparent !important; height: 100%; }
    #sidebar { background: color-mix(in srgb, var(--bg2) \(pct)%, transparent) !important; height: calc(100vh - \(Int(lip))px) !important; }
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

@_silgen_name("CGSMainConnectionID") func CGSMainConnectionID() -> Int32
@_silgen_name("CGSSetWindowBackgroundBlurRadius")
func CGSSetWindowBackgroundBlurRadius(_ cid: Int32, _ wid: Int, _ radius: Int32) -> Int32

final class AppDelegate: NSObject, NSApplicationDelegate, WKNavigationDelegate, WKUIDelegate {
    var window: NSWindow!
    var web: WKWebView!
    var pendingURL: String?

    func applicationDidFinishLaunching(_ n: Notification) {
        let opacity = ghosttyOpacity()
        let conf = WKWebViewConfiguration()
        let js = "(function(){var s=document.createElement('style');s.textContent=\(jsString(glassCSS(opacity)));document.documentElement.appendChild(s);})();"
        conf.userContentController.addUserScript(
            WKUserScript(source: js, injectionTime: .atDocumentEnd, forMainFrameOnly: true))

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
        window.setFrameAutosaveName("NotesViewMain")
        if !window.setFrameUsingName("NotesViewMain") { window.center() }
        window.makeKeyAndOrderFront(nil)
        // Same window blur Ghostty uses (background-blur = 30).
        _ = CGSSetWindowBackgroundBlurRadius(CGSMainConnectionID(), window.windowNumber, 30)

        buildMenu()
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
    }

    func load(_ s: String) {
        guard let u = URL(string: s) else { return }
        web?.load(URLRequest(url: u))
    }

    // `open -a NotesView --args URL` or a notesview://… style launch passes the target here.
    func application(_ app: NSApplication, open urls: [URL]) {
        if let u = urls.first { pendingURL = u.absoluteString; load(u.absoluteString) }
    }

    func applicationShouldTerminateAfterLastWindowClosed(_ app: NSApplication) -> Bool { true }

    // Server not up yet (launch agent restarting): retry instead of a blank window.
    func webView(_ w: WKWebView, didFailProvisionalNavigation nav: WKNavigation!, withError e: Error) {
        DispatchQueue.main.asyncAfter(deadline: .now() + 1.5) { [weak self] in
            if let u = w.url ?? URL(string: "\(base)/?app=1") { self?.web.load(URLRequest(url: u)) }
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
