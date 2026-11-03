#!/bin/sh
# Builds the Apple Music helpers into <output dir>/applemusic-helper, the
# directory cliamp looks for next to its binary.
set -eu

src=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
out=${1:-.}/applemusic-helper
app=$out/CliampTap.app

mkdir -p "$app/Contents/MacOS"
swiftc -O "$src/tap.swift" -o "$app/Contents/MacOS/CliampTap"
swiftc -O "$src/ctl.swift" -o "$out/cliamp-am-ctl"

cat > "$app/Contents/Info.plist" <<'EOF'
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
<key>CFBundleIdentifier</key><string>com.cliamp.applemusic.tap</string>
<key>CFBundleName</key><string>CliampTap</string>
<key>CFBundleExecutable</key><string>CliampTap</string>
<key>CFBundlePackageType</key><string>APPL</string>
<key>LSUIElement</key><true/>
<key>NSAudioCaptureUsageDescription</key><string>cliamp plays Apple Music through its own volume and EQ. Audio is passed to cliamp and is not saved.</string>
</dict></plist>
EOF

codesign -s - --force "$app"
echo "built $out"
