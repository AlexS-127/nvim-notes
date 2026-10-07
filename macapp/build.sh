#!/bin/sh
# Builds ~/Applications/NotesView.app (no Xcode project needed).
set -e
cd "$(dirname "$0")"
APP="$HOME/Applications/NotesView.app"
rm -rf "$APP"
mkdir -p "$APP/Contents/MacOS"
swiftc -O -o "$APP/Contents/MacOS/NotesView" NotesView.swift
cat > "$APP/Contents/Info.plist" <<PL
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
<key>CFBundleName</key><string>NotesView</string>
<key>CFBundleDisplayName</key><string>NotesView</string>
<key>CFBundleIdentifier</key><string>local.notesview.app</string>
<key>CFBundleExecutable</key><string>NotesView</string>
<key>CFBundlePackageType</key><string>APPL</string>
<key>CFBundleVersion</key><string>1</string>
<key>LSMinimumSystemVersion</key><string>12.0</string>
<key>NSHighResolutionCapable</key><true/>
<key>NSPrincipalClass</key><string>NSApplication</string>
<key>NSAppTransportSecurity</key><dict><key>NSAllowsLocalNetworking</key><true/></dict>
</dict></plist>
PL
# A stable signature keeps macOS permissions (Screen Recording, Accessibility) across rebuilds; ad-hoc
# signatures change every build and get them revoked. Create the "NotesView Signing" certificate once
# (Keychain Access → Certificate Assistant → Create a Certificate…, type Code Signing) to use it.
SIGN=-
security find-identity -p codesigning 2>/dev/null | grep -q '"NotesView Signing"' && SIGN="NotesView Signing"
codesign --force --sign "$SIGN" "$APP" >/dev/null 2>&1 || true
echo "built $APP"

# NotesViewSense.app: the background sensor helper (Sense.swift), run by the local.notesview-sense
# launch agent. An app bundle so macOS can ask for (and remember) its permissions.
SENSE="$HOME/Applications/NotesViewSense.app"
rm -rf "$SENSE"
mkdir -p "$SENSE/Contents/MacOS"
swiftc -O -o "$SENSE/Contents/MacOS/NotesViewSense" Sense.swift
cat > "$SENSE/Contents/Info.plist" <<PL
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
<key>CFBundleName</key><string>NotesViewSense</string>
<key>CFBundleDisplayName</key><string>NotesView Sense</string>
<key>CFBundleIdentifier</key><string>local.notesview.sense</string>
<key>CFBundleExecutable</key><string>NotesViewSense</string>
<key>CFBundlePackageType</key><string>APPL</string>
<key>CFBundleVersion</key><string>1</string>
<key>LSMinimumSystemVersion</key><string>14.0</string>
<key>LSUIElement</key><true/>
<key>NSCameraUsageDescription</key><string>Checks whether you are at the desk and how tired your eyes look (face landmarks only; no picture is kept).</string>
<key>NSMicrophoneUsageDescription</key><string>Measures how loud it is and whether people are talking nearby (a level only; no audio is kept).</string>
<key>NSLocationUsageDescription</key><string>Reads the Wi-Fi network name so you can label places (home, library, class). Only a hash is stored.</string>
<key>NSLocationWhenInUseUsageDescription</key><string>Reads the Wi-Fi network name so you can label places (home, library, class). Only a hash is stored.</string>
<key>NSAppleEventsUsageDescription</key><string>Reads the front browser tab's website and whether music is playing, to categorise what you are doing. Only a category and a hash are stored.</string>
</dict></plist>
PL
codesign --force --sign "$SIGN" --identifier local.notesview.sense "$SENSE" >/dev/null 2>&1 || true
echo "built $SENSE"
