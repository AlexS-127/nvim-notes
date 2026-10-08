import AppKit
import AVFoundation
import CoreAudio
import CoreLocation
import CoreWLAN
import CryptoKit
import IOKit.ps
import ScreenCaptureKit
import SoundAnalysis
import Vision

// notesview-sense (NotesViewSense.app): the Mac's sensors for the notes data layer
// (renderer/signals.go). Runs in the background (no Dock icon) from the local.notesview-sense
// launch agent and appends one JSON line per reading to $NOTES_DIR/.signals/YYYY-MM-DD.jsonl
// with "src":"sense" and "sensor":
//   input   15 s  system-wide key / click / scroll counts and idle seconds (no permission)
//   apps    15 s  front app, app switches, screen locked, display asleep, displays, power, battery
//   window  15 s  CATEGORY of the front window's title (Accessibility), plus a salted hash
//   browser 15 s  CATEGORY of the front tab's domain (Automation), plus a salted hash
//   screen  60 s  screenshot → on-device text recognition → activity class; image and text dropped
//   camera  15 s  a 1.5 s burst: face present, facing the screen, eye-closure fraction, yawn
//   mic     20 s  a 2 s burst: sound level (dBFS) and speech probability; never audio
//   place   60 s  Wi-Fi network and access point hashes (and the place label once you give one)
//   media   60 s  music playing or not
// Each sensor logs {sensor, state: on|off|denied} when that changes. Switches and the salt are in
// .signals/config.json, your labels in .signals/rules.json. Titles, domains and network names the
// classifier doesn't know wait in a local queue for you to label (queue.json in Application
// Support, mode 0600, outside the notes folder, at most 7 days), never in .signals. With the
// "previews" switch (off by default) an unsure screen also keeps a JPEG in previews/<hash>.jpg
// beside the queue (0700/0600) so you can see what you label: deleted when you label or skip it,
// when it leaves the queue, or after 24 h (renderer/labels.go: screenPreview).

let notesDir = URL(fileURLWithPath: ProcessInfo.processInfo.environment["NOTES_DIR"] ?? (NSHomeDirectory() + "/notes"))
let signalsDir = notesDir.appendingPathComponent(".signals")
let supportDir = URL(fileURLWithPath: NSHomeDirectory() + "/Library/Application Support/notesview-sense")

func stamp(_ d: Date = Date()) -> String {
    let f = DateFormatter(); f.locale = Locale(identifier: "en_US_POSIX"); f.dateFormat = "yyyy-MM-dd'T'HH:mm:ss"
    return f.string(from: d)
}

/// Appends one record to today's signals file.
func emit(_ sensor: String, _ fields: [String: Any]) {
    var rec = fields
    rec["src"] = "sense"; rec["sensor"] = sensor
    let at = stamp(); rec["at"] = at
    guard let data = try? JSONSerialization.data(withJSONObject: rec, options: [.sortedKeys]) else { return }
    try? FileManager.default.createDirectory(at: signalsDir, withIntermediateDirectories: true)
    let url = signalsDir.appendingPathComponent(String(at.prefix(10)) + ".jsonl")
    if !FileManager.default.fileExists(atPath: url.path) { FileManager.default.createFile(atPath: url.path, contents: nil) }
    guard let h = try? FileHandle(forWritingTo: url) else { return }
    h.seekToEndOfFile(); h.write(data); h.write("\n".data(using: .utf8)!); try? h.close()
}

// ── config, rules, hashing (same as renderer/signals.go and labels.go) ──

struct Config { var sensors: [String: Bool] = [:]; var salt = "" }

func loadJSON(_ url: URL) -> [String: Any] {
    guard let d = try? Data(contentsOf: url), let o = try? JSONSerialization.jsonObject(with: d) as? [String: Any] else { return [:] }
    return o
}

func loadConfig() -> Config {
    let o = loadJSON(signalsDir.appendingPathComponent("config.json"))
    var c = Config()
    c.sensors = o["sensors"] as? [String: Bool] ?? [:]
    c.salt = o["salt"] as? String ?? ""
    return c
}

func saltedHash(_ salt: String, _ text: String) -> String {
    let d = SHA256.hash(data: Data((salt + "|" + text.trimmingCharacters(in: .whitespacesAndNewlines).lowercased()).utf8))
    return d.prefix(8).map { String(format: "%02x", $0) }.joined()
}

func tokens(_ text: String) -> [String] {
    text.lowercased().split(whereSeparator: { !$0.isLetter && !$0.isNumber }).map(String.init)
        .filter { $0.count >= 3 && !$0.allSatisfy(\.isNumber) }
}

struct Rules {
    var titles: [String: String] = [:], domains: [String: String] = [:], places: [String: String] = [:]
    var tokens: [String: [String: Int]] = [:], skipped: Set<String> = []
    var shared: Set<String> = [] // network hashes that span several places (campus Wi-Fi): only access points count
}

func loadRules() -> Rules {
    let o = loadJSON(signalsDir.appendingPathComponent("rules.json"))
    var r = Rules()
    r.titles = (o["titles"] as? [String: String] ?? [:]).mapValues(canon)
    r.domains = (o["domains"] as? [String: String] ?? [:]).mapValues(canon)
    r.places = o["places"] as? [String: String] ?? [:]
    for (cat, counts) in (o["tokens"] as? [String: [String: Int]] ?? [:]) {
        r.tokens[canon(cat), default: [:]].merge(counts, uniquingKeysWith: +)
    }
    r.skipped = Set((o["skipped"] as? [String: Bool] ?? [:]).keys)
    r.shared = Set((o["shared_nets"] as? [String: Bool] ?? [:]).keys)
    return r
}

// ── classifier: your labels (naive Bayes over hashed tokens) + generic keyword lists ──

let categories = ["study", "reading", "surfing", "entertainment", "code", "shopping", "admin", "other"]
/// Earlier category names (labels made before the change keep working).
let categoryAlias = ["news": "reading", "social": "surfing", "comms": "admin"]
func canon(_ c: String) -> String { categoryAlias[c] ?? c }
var keywords: [String: Set<String>] = [
    "study": ["lecture", "chapter", "syllabus", "exam", "midterm", "final", "homework", "assignment", "canvas", "quiz", "notes", "textbook", "slides", "course", "module", "seminar", "tutorial", "study", "flashcards", "revision", "notesview",
              "problem", "problems", "exercise", "solution", "solutions", "calculate", "equation", "practice", "worksheet", "journal", "entry", "essay", "draft", "outline"],
    "code": ["github", "gitlab", "git", "commit", "repo", "python", "swift", "golang", "javascript", "typescript", "npm", "pip",
             "debug", "compile", "terminal", "xcode", "vscode", "cursor", "stackoverflow", "zsh", "bash", "nvim", "vim", "localhost", "api", "json", "traceback"],
    "reading": ["article", "paper", "journal", "kindle", "book", "reader", "pdf", "chapter", "news", "nytimes", "bbc", "cnn", "guardian", "bloomberg", "reuters", "wsj"],
    "entertainment": ["youtube", "netflix", "twitch", "spotify", "hulu", "disney", "prime", "video", "watch", "game", "steam", "episode", "trailer"],
    "surfing": ["instagram", "twitter", "tiktok", "reddit", "facebook", "snapchat", "threads", "linkedin", "feed", "forum", "wiki", "blog"],
    "shopping": ["amazon", "cart", "checkout", "ebay", "shop", "order", "etsy"],
    "admin": ["settings", "preferences", "finder", "bank", "calendar", "downloads", "mail", "inbox", "messages", "slack", "discord", "teams", "zoom", "whatsapp", "gmail", "outlook", "message", "chat", "meet"],
]

/// Course folders (top-level folders of the notes with a code like act200) count as study words.
func addCourseKeywords() {
    guard let names = try? FileManager.default.contentsOfDirectory(atPath: notesDir.path) else { return }
    for n in names where !n.hasPrefix(".") {
        var isDir: ObjCBool = false
        if FileManager.default.fileExists(atPath: notesDir.appendingPathComponent(n).path, isDirectory: &isDir), isDir.boolValue,
           n.range(of: #"^[a-z]{2,5}\d{3}$"#, options: .regularExpression) != nil {
            keywords["study", default: []].insert(n)
            let letters = n.prefix(while: { $0.isLetter }); keywords["study", default: []].insert(String(letters))
        }
    }
}

/// Classifies tokens: returns (category, confidence 0-1). No evidence → ("other", 0).
func classify(_ toks: [String], salt: String, rules: Rules) -> (String, Double) {
    var score: [String: Double] = [:]
    for t in toks {
        for (cat, words) in keywords where words.contains(t) { score[cat, default: 0] += 1 }
    }
    // your labels: per category token counts (Laplace-smoothed likelihood ratio, capped)
    let total = rules.tokens.values.reduce(0) { $0 + $1.values.reduce(0, +) }
    if total > 0 {
        for t in toks {
            let h = saltedHash(salt, t)
            for (cat, counts) in rules.tokens {
                let n = Double(counts[h] ?? 0)
                if n > 0 { score[cat, default: 0] += 2 * log(1 + n) }
            }
        }
    }
    guard let best = score.max(by: { $0.value < $1.value }), best.value > 0 else { return ("other", 0) }
    let sum = score.values.reduce(0, +)
    return (best.key, best.value / sum * min(1, best.value / 2))
}

// ── the labelling queue (local, 0600, outside the notes folder) ──

final class LabelQueue {
    var items: [String: [String: Any]] = [:]
    let url = supportDir.appendingPathComponent("queue.json")
    init() {
        if let d = try? Data(contentsOf: url), let a = try? JSONSerialization.jsonObject(with: d) as? [[String: Any]] {
            for it in a { if let h = it["hash"] as? String { items[h] = it } }
        }
    }
    /// Adds or counts an item; true when it is new in the queue.
    @discardableResult
    func add(kind: String, hash: String, text: String, app: String, guess: String, tokenHashes: [String], rules: Rules, net: String = "") -> Bool {
        if rules.skipped.contains(hash) || rules.titles[hash] != nil || rules.domains[hash] != nil || rules.places[hash] != nil { return false }
        if var it = items[hash] { it["count"] = (it["count"] as? Int ?? 1) + 1; items[hash] = it; save(rules); return false }
        if kind == "screen" && items.values.filter({ ($0["kind"] as? String) == "screen" }).count >= 5 { return false } // a few at a time
        items[hash] = ["kind": kind, "hash": hash, "text": text, "app": app, "guess": guess, "tokens": tokenHashes, "first": stamp(), "count": 1]
        if !net.isEmpty { items[hash]?["net"] = net }
        save(rules)
        return true
    }

    // ── screen previews (switch "previews") ──
    let previewDir = supportDir.appendingPathComponent("previews")

    /// Keeps a JPEG of a queued screen (quality 0.5, at most 1440 px wide) for you to look at while labelling.
    func savePreview(_ img: CGImage, hash: String) {
        let fm = FileManager.default
        try? fm.createDirectory(at: previewDir, withIntermediateDirectories: true, attributes: [.posixPermissions: 0o700])
        try? fm.setAttributes([.posixPermissions: 0o700], ofItemAtPath: previewDir.path)
        guard let jpg = NSBitmapImageRep(cgImage: img).representation(using: .jpeg, properties: [.compressionFactor: 0.5]) else { return }
        let url = previewDir.appendingPathComponent(hash + ".jpg")
        fm.createFile(atPath: url.path, contents: jpg, attributes: [.posixPermissions: 0o600])
    }

    /// Deletes previews whose screen is no longer queued (labelled, skipped, dropped) or older than 24 h.
    func prunePreviews() {
        let fm = FileManager.default
        guard let names = try? fm.contentsOfDirectory(atPath: previewDir.path) else { return }
        let cutoff = Date().addingTimeInterval(-24 * 3600)
        for n in names {
            let url = previewDir.appendingPathComponent(n)
            let h = n.hasSuffix(".jpg") ? String(n.dropLast(4)) : ""
            let mod = (try? fm.attributesOfItem(atPath: url.path)[.modificationDate] as? Date) ?? .distantPast
            if items[h] == nil || mod < cutoff { try? fm.removeItem(at: url) }
        }
    }
    func save(_ rules: Rules) {
        let cutoff = stamp(Date().addingTimeInterval(-7 * 86400))
        items = items.filter { h, it in
            !(rules.skipped.contains(h) || rules.titles[h] != nil || rules.domains[h] != nil || rules.places[h] != nil) && (it["first"] as? String ?? "") >= cutoff
        }
        if items.count > 100 { // keep the most seen
            let keep = items.sorted { ($0.value["count"] as? Int ?? 0) > ($1.value["count"] as? Int ?? 0) }.prefix(100)
            items = Dictionary(uniqueKeysWithValues: keep.map { ($0.key, $0.value) })
        }
        try? FileManager.default.createDirectory(at: supportDir, withIntermediateDirectories: true)
        if let d = try? JSONSerialization.data(withJSONObject: Array(items.values)) {
            try? d.write(to: url, options: .atomic)
            try? FileManager.default.setAttributes([.posixPermissions: 0o600], ofItemAtPath: url.path)
        }
        prunePreviews()
    }
}

// ── sensors ──

final class Sense: NSObject, NSApplicationDelegate, CLLocationManagerDelegate, AVCaptureVideoDataOutputSampleBufferDelegate, SNResultsObserving {
    var config = loadConfig()
    var rules = loadRules()
    let queue = LabelQueue()
    var states: [String: String] = [:]
    var lastCounts: (UInt32, UInt32, UInt32)? = nil
    var switches = 0, displayAsleep = false
    var lastApp = ""
    var lastWindow: (app: String, cat: String, conf: Double, at: Date) = ("", "", 0, .distantPast)
    // apps that show your own content: the window title says more than keywords in the screen text
    let ownContentApps: Set<String> = ["NotesView", "Ghostty", "Terminal", "iTerm2"]
    let location = CLLocationManager()
    var lastNet = ""

    func on(_ name: String) -> Bool { config.sensors[name] ?? false }

    func setState(_ sensor: String, _ state: String) {
        if states[sensor] != state { states[sensor] = state; emit(sensor, ["state": state]) }
    }

    func applicationDidFinishLaunching(_ n: Notification) {
        addCourseKeywords()
        let ws = NSWorkspace.shared.notificationCenter
        ws.addObserver(forName: NSWorkspace.didActivateApplicationNotification, object: nil, queue: .main) { [weak self] _ in self?.switches += 1 }
        ws.addObserver(forName: NSWorkspace.screensDidSleepNotification, object: nil, queue: .main) { [weak self] _ in self?.displayAsleep = true }
        ws.addObserver(forName: NSWorkspace.screensDidWakeNotification, object: nil, queue: .main) { [weak self] _ in self?.displayAsleep = false }
        location.delegate = self
        every(60) { [weak self] in self?.config = loadConfig(); self?.rules = loadRules(); self?.queue.prunePreviews() }
        every(15) { [weak self] in self?.input(); self?.apps(); self?.window(); self?.browser() }
        every(60) { [weak self] in self?.screen(); self?.place(); self?.media() }
        every(15) { [weak self] in self?.camera() }
        every(20) { [weak self] in self?.mic() }
        setState("bluetooth", "off") // scaffold for devices that come later
        input(); apps(); window(); browser(); place(); media()
    }

    func every(_ s: TimeInterval, _ f: @escaping () -> Void) {
        let t = Timer(timeInterval: s, repeats: true) { _ in f() }
        RunLoop.main.add(t, forMode: .common)
    }

    // input: system-wide counters need no permission
    func input() {
        guard on("input") else { return setState("input", "off") }
        setState("input", "on")
        let k = CGEventSource.counterForEventType(.combinedSessionState, eventType: .keyDown)
        let c = CGEventSource.counterForEventType(.combinedSessionState, eventType: .leftMouseDown) &+ CGEventSource.counterForEventType(.combinedSessionState, eventType: .rightMouseDown)
        let s = CGEventSource.counterForEventType(.combinedSessionState, eventType: .scrollWheel)
        let idle = CGEventSource.secondsSinceLastEventType(.combinedSessionState, eventType: CGEventType(rawValue: ~0)!)
        if let (k0, c0, s0) = lastCounts {
            emit("input", ["keys": Int(k &- k0), "clicks": Int(c &- c0), "scroll": Int(s &- s0), "idle": Int(idle)])
        }
        lastCounts = (k, c, s)
    }

    func apps() {
        guard on("apps") else { return setState("apps", "off") }
        setState("apps", "on")
        let front = NSWorkspace.shared.frontmostApplication
        var locked = false
        if let d = CGSessionCopyCurrentDictionary() as? [String: Any] { locked = (d["CGSSessionScreenIsLocked"] as? Bool) ?? false }
        var power = "ac", battery = -1
        if let info = IOPSCopyPowerSourcesInfo()?.takeRetainedValue() {
            if let type = IOPSGetProvidingPowerSourceType(info)?.takeUnretainedValue() as String? { power = type == kIOPSBatteryPowerValue ? "battery" : "ac" }
            if let list = IOPSCopyPowerSourcesList(info)?.takeRetainedValue() as? [CFTypeRef] {
                for ps in list {
                    if let d = IOPSGetPowerSourceDescription(info, ps)?.takeUnretainedValue() as? [String: Any], let cur = d[kIOPSCurrentCapacityKey] as? Int { battery = cur }
                }
            }
        }
        emit("apps", ["app": front?.localizedName ?? "", "bundle": front?.bundleIdentifier ?? "", "switches": switches,
                      "locked": locked, "display_sleep": displayAsleep, "displays": NSScreen.screens.count, "power": power, "battery": battery])
        switches = 0
        lastApp = front?.localizedName ?? ""
    }

    // window: Accessibility; only the category and a hash leave this process
    func window() {
        guard on("window") else { return setState("window", "off") }
        if !AXIsProcessTrusted() {
            if states["window"] == nil { _ = AXIsProcessTrustedWithOptions([kAXTrustedCheckOptionPrompt.takeUnretainedValue() as String: true] as CFDictionary) }
            return setState("window", "denied")
        }
        setState("window", "on")
        guard let app = NSWorkspace.shared.frontmostApplication else { return }
        let el = AXUIElementCreateApplication(app.processIdentifier)
        var win: CFTypeRef?
        guard AXUIElementCopyAttributeValue(el, kAXFocusedWindowAttribute as CFString, &win) == .success, let w = win else { return }
        var t: CFTypeRef?
        AXUIElementCopyAttributeValue(w as! AXUIElement, kAXTitleAttribute as CFString, &t)
        let title = ((t as? String) ?? "").trimmingCharacters(in: .whitespacesAndNewlines)
        // empty, or just the app's name: says nothing about what you're doing; never queued
        if title.isEmpty || title.lowercased() == (app.localizedName ?? "").lowercased() {
            return emit("window", ["cat": "none", "app": app.localizedName ?? ""])
        }
        let h = saltedHash(config.salt, title)
        var cat = rules.titles[h], conf = 1.0
        let toks = tokens(title)
        let appName = app.localizedName ?? ""
        if cat == nil && appName == "NotesView" {
            // the viewer's title is "<page or note> — notesview": its own pages are admin, notes are study
            let page = title.components(separatedBy: " — ").first?.lowercased() ?? ""
            cat = ["activity", "data", "settings", "tasks", "classes", "revision", "reading", "notesview"].contains(page) ? "admin" : "study"
        }
        if cat == nil {
            let (c, p) = classify(toks + tokens(app.localizedName ?? ""), salt: config.salt, rules: rules)
            cat = c; conf = p
            if p < 0.5 {
                queue.add(kind: "title", hash: h, text: title, app: app.localizedName ?? "", guess: c == "other" ? "" : c,
                          tokenHashes: toks.map { saltedHash(config.salt, $0) }, rules: rules)
            }
        }
        lastWindow = (appName, cat ?? "other", conf, Date())
        emit("window", ["cat": cat ?? "other", "conf": (conf * 100).rounded() / 100, "title_hash": h, "app": appName])
    }

    // browser: front tab's domain via AppleScript (Automation permission per browser)
    let browsers = ["com.apple.Safari": ("Safari", "URL of front document"), "com.google.Chrome": ("Google Chrome", "URL of active tab of front window"),
                    "company.thebrowser.Browser": ("Arc", "URL of active tab of front window"), "com.brave.Browser": ("Brave Browser", "URL of active tab of front window"),
                    "com.microsoft.edgemac": ("Microsoft Edge", "URL of active tab of front window")]
    let domainDefaults: [String: String] = [
        "youtube.com": "entertainment", "netflix.com": "entertainment", "twitch.tv": "entertainment", "disneyplus.com": "entertainment", "hulu.com": "entertainment", "open.spotify.com": "entertainment",
        "instagram.com": "surfing", "twitter.com": "surfing", "x.com": "surfing", "tiktok.com": "surfing", "reddit.com": "surfing", "facebook.com": "surfing", "linkedin.com": "surfing",
        "mail.google.com": "admin", "outlook.office.com": "admin", "outlook.live.com": "admin", "web.whatsapp.com": "admin", "discord.com": "admin", "slack.com": "admin", "zoom.us": "admin",
        "github.com": "code", "gitlab.com": "code", "stackoverflow.com": "code", "developer.apple.com": "code", "pkg.go.dev": "code", "docs.python.org": "code", "pypi.org": "code", "npmjs.com": "code", "canvas.instructure.com": "study", "instructure.com": "study", "claude.ai": "study", "chatgpt.com": "study", "quizlet.com": "study", "khanacademy.org": "study",
        "scholar.google.com": "reading", "jstor.org": "reading", "wikipedia.org": "reading", "amazon.com": "shopping", "ebay.com": "shopping",
        "nytimes.com": "reading", "bbc.com": "reading", "bbc.co.uk": "reading", "cnn.com": "reading", "theguardian.com": "reading", "wsj.com": "reading", "bloomberg.com": "reading",
    ]
    func browser() {
        guard on("browser") else { return setState("browser", "off") }
        guard let app = NSWorkspace.shared.frontmostApplication, let id = app.bundleIdentifier, let (name, expr) = browsers[id] else { return }
        var err: NSDictionary?
        let res = NSAppleScript(source: "tell application \"\(name)\" to get \(expr)")?.executeAndReturnError(&err)
        if let e = err, let code = e[NSAppleScript.errorNumber] as? Int, code == -1743 { return setState("browser", "denied") }
        guard let s = res?.stringValue, let host = URL(string: s)?.host?.lowercased() else { return }
        setState("browser", "on")
        let domain = host.hasPrefix("www.") ? String(host.dropFirst(4)) : host
        let h = saltedHash(config.salt, domain)
        var cat = rules.domains[h]
        if cat == nil {
            // the domain or any parent domain in the defaults ("x.instructure.com" → instructure.com)
            var parts = domain.split(separator: ".").map(String.init)
            while parts.count >= 2 && cat == nil {
                cat = domainDefaults[parts.joined(separator: ".")]
                parts.removeFirst()
            }
            if domain.hasSuffix(".edu") && cat == nil { cat = "study" }
        }
        if cat == nil { queue.add(kind: "domain", hash: h, text: domain, app: name, guess: "", tokenHashes: [], rules: rules) }
        emit("browser", ["cat": cat ?? "unknown", "domain_hash": h, "browser": name])
    }

    // screen: Screen Recording; the image and recognised text never leave this function
    var screenBusy = false
    func screen() {
        guard on("screen") else { return setState("screen", "off") }
        if !CGPreflightScreenCaptureAccess() {
            if states["screen"] == nil { CGRequestScreenCaptureAccess() }
            return setState("screen", "denied")
        }
        if screenBusy || displayAsleep { return }
        screenBusy = true
        let salt = config.salt, rules = self.rules, app = lastApp
        Task {
            defer { DispatchQueue.main.async { self.screenBusy = false } }
            guard let content = try? await SCShareableContent.excludingDesktopWindows(true, onScreenWindowsOnly: true),
                  let display = content.displays.first else { return }
            let cfg = SCStreamConfiguration()
            cfg.width = 1440; cfg.height = Int(1440 * Double(display.height) / Double(max(display.width, 1)))
            cfg.showsCursor = false
            guard let img = try? await SCScreenshotManager.captureImage(contentFilter: SCContentFilter(display: display, excludingWindows: []), configuration: cfg) else { return }
            let req = VNRecognizeTextRequest()
            req.recognitionLevel = .fast
            try? VNImageRequestHandler(cgImage: img).perform([req])
            let text = (req.results ?? []).compactMap { $0.topCandidates(1).first?.string }.joined(separator: " ")
            let toks = tokens(text)
            var (cls, conf) = classify(toks, salt: salt, rules: rules)
            DispatchQueue.main.async {
                self.setState("screen", "on")
                // your own notes (viewer, terminal): take the window title's category when it is sure
                var source = "text"
                let w = self.lastWindow
                if self.ownContentApps.contains(app) && w.app == app && w.conf >= 0.5 && Date().timeIntervalSince(w.at) < 60 && w.cat != "other" {
                    cls = w.cat; conf = w.conf; source = "window"
                }
                emit("screen", ["class": cls, "conf": (conf * 100).rounded() / 100, "words": toks.count, "app": app, "from": source])
                if conf < 0.4 && toks.count > 20 && source == "text" { // unsure: ask you (hashed words only, so it can learn)
                    let key = saltedHash(salt, "screen|" + stamp())
                    let added = self.queue.add(kind: "screen", hash: key, text: "", app: app, guess: cls == "other" ? "" : cls,
                                   tokenHashes: Array(Set(toks.map { saltedHash(salt, $0) })).prefix(200).map { $0 }, rules: rules)
                    if added && self.on("previews") { self.queue.savePreview(img, hash: key) } // after the queue, so it is never orphaned
                }
            }
        }
    }

    // camera: a short burst; landmarks only, no frame is kept
    var session: AVCaptureSession?
    var faces: [(present: Bool, facing: Double, closed: Bool, yawn: Bool)] = []
    var camStart = Date()
    let camQueue = DispatchQueue(label: "camera")
    func camera() {
        guard on("camera") else { return setState("camera", "off") }
        switch AVCaptureDevice.authorizationStatus(for: .video) {
        case .notDetermined: AVCaptureDevice.requestAccess(for: .video) { _ in }; return
        case .denied, .restricted: return setState("camera", "denied")
        default: break
        }
        if session != nil || displayAsleep { return }
        guard let dev = AVCaptureDevice.default(for: .video), let inp = try? AVCaptureDeviceInput(device: dev) else { return setState("camera", "absent") }
        setState("camera", "on")
        let s = AVCaptureSession()
        s.sessionPreset = .vga640x480   // .low (192×144) missed faces in dim light
        guard s.canAddInput(inp) else { return }
        s.addInput(inp)
        let out = AVCaptureVideoDataOutput()
        out.setSampleBufferDelegate(self, queue: camQueue)
        guard s.canAddOutput(out) else { return }
        s.addOutput(out)
        faces = []
        camStart = Date()
        session = s
        camQueue.async { s.startRunning() }
        DispatchQueue.main.asyncAfter(deadline: .now() + 2.3) { [weak self] in self?.endCamera() }
    }

    func captureOutput(_ o: AVCaptureOutput, didOutput b: CMSampleBuffer, from c: AVCaptureConnection) {
        guard let px = CMSampleBufferGetImageBuffer(b) else { return }
        if Date().timeIntervalSince(camStart) < 0.8 { return } // the camera is still adjusting exposure: no face found yet
        let req = VNDetectFaceLandmarksRequest()
        req.revision = VNDetectFaceLandmarksRequestRevision3 // continuous yaw/pitch (older revisions report yaw in 45° steps)
        try? VNImageRequestHandler(cvPixelBuffer: px).perform([req])
        guard let f = (req.results ?? []).max(by: { $0.boundingBox.width < $1.boundingBox.width }) else {
            DispatchQueue.main.async { self.faces.append((false, 0, false, false)) }; return
        }
        func ratio(_ r: VNFaceLandmarkRegion2D?) -> Double? {
            guard let p = r?.normalizedPoints, p.count > 2 else { return nil }
            let xs = p.map(\.x), ys = p.map(\.y)
            let w = Double((xs.max() ?? 0) - (xs.min() ?? 0)); return w > 0 ? Double((ys.max() ?? 0) - (ys.min() ?? 0)) / w : nil
        }
        let eye = [ratio(f.landmarks?.leftEye), ratio(f.landmarks?.rightEye)].compactMap { $0 }
        let closed = !eye.isEmpty && eye.reduce(0, +) / Double(eye.count) < 0.16
        let yawn = (ratio(f.landmarks?.innerLips) ?? 0) > 0.55
        let yaw = abs(f.yaw?.doubleValue ?? 0), pitch = abs(f.pitch?.doubleValue ?? 0)
        let facing = max(0, 1 - max(yaw, pitch) / 0.6)
        DispatchQueue.main.async { self.faces.append((true, facing, closed, yawn)) }
    }

    func endCamera() {
        guard let s = session else { return }
        camQueue.async { s.stopRunning() }
        session = nil
        guard !faces.isEmpty else { return }
        let seen = faces.filter(\.present)
        let present = Double(seen.count) / Double(faces.count)
        let facing = seen.isEmpty ? 0 : seen.map(\.facing).reduce(0, +) / Double(seen.count)
        let perclos = seen.isEmpty ? 0 : Double(seen.filter(\.closed).count) / Double(seen.count)
        emit("camera", ["present": (present * 100).rounded() / 100, "facing": (facing * 100).rounded() / 100,
                        "perclos": (perclos * 100).rounded() / 100, "yawn": seen.contains { $0.yawn }, "frames": faces.count])
    }

    // the built-in microphone's device id: the default input may be a virtual device (BlackHole,
    // Loopback…) that carries silence, so the helper always listens to the Mac's own microphone
    func builtInMic() -> AudioDeviceID? {
        var addr = AudioObjectPropertyAddress(mSelector: kAudioHardwarePropertyDevices, mScope: kAudioObjectPropertyScopeGlobal, mElement: kAudioObjectPropertyElementMain)
        var size: UInt32 = 0
        guard AudioObjectGetPropertyDataSize(AudioObjectID(kAudioObjectSystemObject), &addr, 0, nil, &size) == noErr else { return nil }
        var ids = [AudioDeviceID](repeating: 0, count: Int(size) / MemoryLayout<AudioDeviceID>.size)
        guard AudioObjectGetPropertyData(AudioObjectID(kAudioObjectSystemObject), &addr, 0, nil, &size, &ids) == noErr else { return nil }
        for id in ids {
            var t: UInt32 = 0, ts = UInt32(MemoryLayout<UInt32>.size)
            var ta = AudioObjectPropertyAddress(mSelector: kAudioDevicePropertyTransportType, mScope: kAudioObjectPropertyScopeGlobal, mElement: kAudioObjectPropertyElementMain)
            guard AudioObjectGetPropertyData(id, &ta, 0, nil, &ts, &t) == noErr, t == kAudioDeviceTransportTypeBuiltIn else { continue }
            var ca = AudioObjectPropertyAddress(mSelector: kAudioDevicePropertyStreamConfiguration, mScope: kAudioDevicePropertyScopeInput, mElement: kAudioObjectPropertyElementMain)
            var cs: UInt32 = 0
            guard AudioObjectGetPropertyDataSize(id, &ca, 0, nil, &cs) == noErr, cs > 0 else { continue }
            let buf = UnsafeMutableRawPointer.allocate(byteCount: Int(cs), alignment: 16)
            defer { buf.deallocate() }
            guard AudioObjectGetPropertyData(id, &ca, 0, nil, &cs, buf) == noErr else { continue }
            let list = UnsafeMutableAudioBufferListPointer(buf.assumingMemoryBound(to: AudioBufferList.self))
            if list.reduce(0, { $0 + Int($1.mNumberChannels) }) > 0 { return id }
        }
        return nil
    }

    // mic: level and speech probability from a short burst; samples are never stored
    var silentBursts = 0
    var micDevice = "default"
    var engine: AVAudioEngine?
    var analyzer: SNAudioStreamAnalyzer?
    var levels: [Float] = [], speech: [Double] = []
    let micQueue = DispatchQueue(label: "mic")
    func mic() {
        guard on("mic") else { return setState("mic", "off") }
        switch AVCaptureDevice.authorizationStatus(for: .audio) {
        case .notDetermined: AVCaptureDevice.requestAccess(for: .audio) { _ in }; return
        case .denied, .restricted: return setState("mic", "denied")
        default: break
        }
        if engine != nil { return }
        let e = AVAudioEngine()
        let node = e.inputNode
        micDevice = "default"
        if var dev = builtInMic(), let au = node.audioUnit {
            if AudioUnitSetProperty(au, kAudioOutputUnitProperty_CurrentDevice, kAudioUnitScope_Global, 0, &dev, UInt32(MemoryLayout<AudioDeviceID>.size)) == noErr { micDevice = "builtin" }
        }
        let fmt = node.inputFormat(forBus: 0)
        guard fmt.sampleRate > 0 else { return setState("mic", "absent") }
        setState("mic", "on")
        let an = SNAudioStreamAnalyzer(format: fmt)
        if let req = try? SNClassifySoundRequest(classifierIdentifier: .version1) { try? an.add(req, withObserver: self) }
        levels = []; speech = []
        node.installTap(onBus: 0, bufferSize: 4096, format: fmt) { [weak self] buf, when in
            guard let ch = buf.floatChannelData?[0] else { return }
            var sum: Float = 0
            for i in 0..<Int(buf.frameLength) { sum += ch[i] * ch[i] }
            let rms = sqrt(sum / Float(max(buf.frameLength, 1)))
            DispatchQueue.main.async { self?.levels.append(rms) }
            self?.micQueue.async { an.analyze(buf, atAudioFramePosition: when.sampleTime) }
        }
        engine = e; analyzer = an
        try? e.start()
        DispatchQueue.main.asyncAfter(deadline: .now() + 2.2) { [weak self] in self?.endMic() }
    }

    func request(_ r: SNRequest, didProduce result: SNResult) {
        guard let c = result as? SNClassificationResult, let s = c.classification(forIdentifier: "speech") else { return }
        DispatchQueue.main.async { self.speech.append(s.confidence) }
    }

    func endMic() {
        guard let e = engine else { return }
        e.inputNode.removeTap(onBus: 0); e.stop()
        engine = nil; analyzer = nil
        guard !levels.isEmpty else { return }
        let rms = levels.reduce(0, +) / Float(levels.count)
        if rms == 0 { // exact digital silence: no signal (a virtual device, muted input), not a quiet room
            silentBursts += 1
            if silentBursts >= 3 { setState("mic", "no-signal") }
            return
        }
        silentBursts = 0
        setState("mic", "on")
        let db = 20 * log10(Double(rms))
        emit("mic", ["db": (db * 10).rounded() / 10, "speech": ((speech.max() ?? 0) * 100).rounded() / 100, "device": micDevice])
    }

    // place: the Wi-Fi network and the access point (BSSID) as hashes; names only go to the local queue.
    // A campus network has one name everywhere, so the access point decides: your label for it first,
    // else the network's label (unless the network is shared: labelled as different places). An
    // unlabelled access point is asked about once you have stayed on it for 10 minutes.
    var apSince: (ap: String, at: Date) = ("", .distantPast)
    func place() {
        guard on("place") else { return setState("place", "off") }
        let st = location.authorizationStatus
        if st == .notDetermined { location.requestWhenInUseAuthorization(); return }
        if st == .denied || st == .restricted { return setState("place", "denied") }
        setState("place", "on")
        let wifi = CWWiFiClient.shared().interface()
        let ssid = wifi?.ssid() ?? "", bssid = wifi?.bssid() ?? ""
        let h = ssid.isEmpty ? "none" : saltedHash(config.salt, "wifi|" + ssid)
        let ap = bssid.isEmpty ? "" : saltedHash(config.salt, "ap|" + bssid.lowercased())
        lastNet = h
        let netLabel = rules.shared.contains(h) ? nil : rules.places[h]
        let apLabel = ap.isEmpty ? nil : rules.places[ap]
        if ap != apSince.ap { apSince = (ap, Date()) }
        if !ssid.isEmpty {
            if netLabel == nil && rules.places[h] == nil {
                queue.add(kind: "place", hash: h, text: ssid, app: "", guess: "", tokenHashes: [], rules: rules)
            } else if apLabel == nil && !ap.isEmpty && Date().timeIntervalSince(apSince.at) >= 600 {
                // short id of the access point so two of them can be told apart in the queue
                queue.add(kind: "place", hash: ap, text: "\(ssid) · access point …\(bssid.suffix(5))", app: "", guess: rules.places[h] ?? "",
                          tokenHashes: [], rules: rules, net: h)
            }
        }
        var rec: [String: Any] = ["net_hash": h, "place": apLabel ?? netLabel ?? (ssid.isEmpty ? "none" : "unknown"),
                                  "from": apLabel != nil ? "ap" : netLabel != nil ? "network" : "none"]
        if !ap.isEmpty { rec["ap_hash"] = ap }
        emit("place", rec)
    }

    func locationManagerDidChangeAuthorization(_ m: CLLocationManager) { place() }

    // media: is music playing (only asks apps that are already running, so nothing gets launched)
    func media() {
        guard on("media") else { return setState("media", "off") }
        var playing = false, app = ""
        for (id, name) in [("com.spotify.client", "Spotify"), ("com.apple.Music", "Music")] {
            guard !NSRunningApplication.runningApplications(withBundleIdentifier: id).isEmpty else { continue }
            var err: NSDictionary?
            let r = NSAppleScript(source: "tell application \"\(name)\" to get player state as string")?.executeAndReturnError(&err)
            if let e = err, let code = e[NSAppleScript.errorNumber] as? Int, code == -1743 { setState("media", "denied"); continue }
            if r?.stringValue == "playing" { playing = true; app = name }
        }
        if states["media"] != "denied" { setState("media", "on") }
        emit("media", ["playing": playing, "app": app])
    }
}

let app = NSApplication.shared
let sense = Sense()
app.delegate = sense
app.setActivationPolicy(.accessory)
app.run()
