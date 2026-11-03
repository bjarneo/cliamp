// cliamp-am-ctl drives parts of Music.app that AppleScript does not reach.
//
//   cliamp-am-ctl playnext <pid> <title>   press "Play Next" on a track row
//   cliamp-am-ctl quality <pid>            print the player's audio badge
//
// It runs as a child of cliamp, so it uses the terminal's Accessibility access.
// It presses only the menu item named Play Next. Library changing items on the
// same menu are never touched.
import ApplicationServices
import Foundation

func attribute(_ element: AXUIElement, _ name: String) -> AnyObject? {
    var value: AnyObject?
    return AXUIElementCopyAttributeValue(element, name as CFString, &value) == .success ? value : nil
}

func text(_ element: AXUIElement, _ name: String) -> String {
    attribute(element, name) as? String ?? ""
}

func children(_ element: AXUIElement) -> [AXUIElement] {
    attribute(element, "AXChildren") as? [AXUIElement] ?? []
}

func normalized(_ s: String) -> String {
    s.folding(options: [.caseInsensitive, .diacriticInsensitive], locale: nil)
}

func findRow(_ element: AXUIElement, title: String, depth: Int = 0) -> AXUIElement? {
    guard depth < 40 else { return nil }
    if text(element, "AXIdentifier").contains("AlbumTrackLockup") {
        let names = [text(element, "AXTitle"), text(element, "AXDescription")].map(normalized)
        let wanted = normalized(title)
        if names.contains(wanted) || names.contains(where: { !$0.isEmpty && ($0.hasPrefix(wanted) || wanted.hasPrefix($0)) }) {
            return element
        }
    }
    for child in children(element) {
        if let found = findRow(child, title: title, depth: depth + 1) { return found }
    }
    return nil
}

func findIdentifier(_ element: AXUIElement, _ identifier: String, depth: Int = 0) -> AXUIElement? {
    guard depth < 40 else { return nil }
    if text(element, "AXIdentifier") == identifier { return element }
    for child in children(element) {
        if let found = findIdentifier(child, identifier, depth: depth + 1) { return found }
    }
    return nil
}

func findIdentifierPrefix(_ element: AXUIElement, _ prefix: String, depth: Int = 0) -> AXUIElement? {
    guard depth < 40 else { return nil }
    if text(element, "AXIdentifier").hasPrefix(prefix) { return element }
    for child in children(element) {
        if let found = findIdentifierPrefix(child, prefix, depth: depth + 1) { return found }
    }
    return nil
}

func findButton(_ element: AXUIElement, named name: String, depth: Int = 0) -> AXUIElement? {
    guard depth < 10 else { return nil }
    if text(element, "AXRole") == "AXButton",
       text(element, "AXTitle") == name || text(element, "AXDescription") == name {
        return element
    }
    for child in children(element) {
        if let found = findButton(child, named: name, depth: depth + 1) { return found }
    }
    return nil
}

func menuItems(_ element: AXUIElement, depth: Int = 0) -> [AXUIElement] {
    guard depth < 30, text(element, "AXRole") != "AXMenuBar" else { return [] }
    var items = text(element, "AXRole") == "AXMenuItem" ? [element] : []
    for child in children(element) { items += menuItems(child, depth: depth + 1) }
    return items
}

func fail(_ message: String) -> Never {
    FileHandle.standardError.write(Data((message + "\n").utf8))
    exit(1)
}

let arguments = CommandLine.arguments
guard arguments.count >= 3, let pid = pid_t(arguments[2]) else {
    fail("usage: cliamp-am-ctl playnext <pid> <title> | quality <pid>")
}
guard AXIsProcessTrusted() else {
    fail("Accessibility access is off for this terminal")
}
let app = AXUIElementCreateApplication(pid)

switch arguments[1] {
case "quality":
    guard let badge = findIdentifier(app, "Music.miniPlayer.audioBadge") else {
        print("none")
        exit(0)
    }
    print(text(badge, "AXDescription").isEmpty ? text(badge, "AXTitle") : text(badge, "AXDescription"))

case "albumplay":
    // An empty queue ignores Play Next, so the open album seeds it.
    var header: AXUIElement?
    for _ in 0..<25 {
        header = findIdentifierPrefix(app, "Music.shelfItem.AlbumDetailHeaderLockup")
        if header != nil { break }
        Thread.sleep(forTimeInterval: 0.2)
    }
    guard let header else { fail("album page not found") }
    guard let play = findButton(header, named: "Play") else { fail("album has no Play button") }
    let status = AXUIElementPerformAction(play, "AXPress" as CFString)
    guard status == .success else { fail("Play press failed: \(status.rawValue)") }

case "playnext":
    guard arguments.count == 4 else { fail("playnext needs a title") }
    var row: AXUIElement?
    for _ in 0..<25 {
        row = findRow(app, title: arguments[3])
        if row != nil { break }
        Thread.sleep(forTimeInterval: 0.2)
    }
    guard let row else { fail("track row not found: \(arguments[3])") }
    guard let more = children(row).first(where: {
        text($0, "AXRole") == "AXButton" && text($0, "AXDescription") == "More"
    }) else { fail("track row has no More button") }

    // The press can report a timeout (-25204) while the menu stays open.
    _ = AXUIElementPerformAction(more, "AXPress" as CFString)
    var items: [AXUIElement] = []
    for _ in 0..<20 {
        items = menuItems(app)
        if items.contains(where: { text($0, "AXTitle") == "Play Next" }) { break }
        Thread.sleep(forTimeInterval: 0.1)
    }
    guard let playNext = items.first(where: { text($0, "AXTitle") == "Play Next" }) else {
        if let first = items.first { _ = AXUIElementPerformAction(first, "AXCancel" as CFString) }
        fail("no Play Next item in the track menu")
    }
    let status = AXUIElementPerformAction(playNext, "AXPress" as CFString)
    guard status == .success else { fail("Play Next press failed: \(status.rawValue)") }

default:
    fail("unknown command \(arguments[1])")
}
