import AppKit
import DiscoKit

// Menu-bar icon + menu, similar to KeePassXC:
// • closing the window → hide to tray (remove from Dock via policy .accessory);
// • show/hide window from the tray menu;
// • quit via tray menu / app menu / ⌘Q.
@MainActor
final class AppDelegate: NSObject, NSApplicationDelegate, NSWindowDelegate, NSMenuDelegate {
    private var statusItem: NSStatusItem!
    private weak var window: NSWindow?

    func applicationDidFinishLaunching(_ notification: Notification) {
        setupStatusItem()
        // The SwiftUI window is created slightly later — attach to it asynchronously.
        DispatchQueue.main.async { [weak self] in self?.attachWindow() }
    }

    // Don't terminate when the window is closed — keep running in the tray.
    func applicationShouldTerminateAfterLastWindowClosed(_ sender: NSApplication) -> Bool { false }

    // Dock icon click (when policy is .regular) — show the window.
    func applicationShouldHandleReopen(_ sender: NSApplication, hasVisibleWindows flag: Bool) -> Bool {
        if !flag { showWindow() }
        return true
    }

    // MARK: - Window

    private func attachWindow() {
        guard let w = NSApp.windows.first(where: { $0.isVisible }) ?? NSApp.windows.first else { return }
        window = w
        w.delegate = self
    }

    // Close button / ⌘W → hide to tray instead of closing.
    func windowShouldClose(_ sender: NSWindow) -> Bool {
        hideToTray()
        return false
    }

    private func hideToTray() {
        (window ?? NSApp.windows.first)?.orderOut(nil)
        NSApp.setActivationPolicy(.accessory)   // remove from Dock
    }

    private func showWindow() {
        if window == nil { attachWindow() }
        NSApp.setActivationPolicy(.regular)     // restore Dock icon
        NSApp.activate(ignoringOtherApps: true)
        window?.makeKeyAndOrderFront(nil)
    }

    // MARK: - Tray

    private func setupStatusItem() {
        statusItem = NSStatusBar.system.statusItem(withLength: NSStatusItem.variableLength)
        statusItem.button?.image = trayImage(for: .offline)
        statusItem.button?.appearsDisabled = true
        let menu = NSMenu()
        menu.delegate = self   // localize titles on open (language may have changed)
        menu.addItem(NSMenuItem(title: "", action: #selector(toggleWindow), keyEquivalent: ""))
        menu.addItem(.separator())
        menu.addItem(NSMenuItem(title: "", action: #selector(quit), keyEquivalent: "q"))
        menu.items.forEach { $0.target = self }
        statusItem.menu = menu
    }

    func menuNeedsUpdate(_ menu: NSMenu) {
        let lang = L10n.currentLanguage
        menu.items.first?.title = L10n.t("tray.toggle", lang)
        menu.items.last?.title = L10n.t("tray.quit", lang)
    }

    @objc private func toggleWindow() {
        if window == nil { attachWindow() }
        if let w = window, w.isVisible { hideToTray() } else { showWindow() }
    }

    @objc private func quit() { NSApp.terminate(nil) }

    // Custom About panel: "beta" badge in the version string + link to the website.
    func showAboutPanel() {
        let para = NSMutableParagraphStyle(); para.alignment = .center
        let credits = NSAttributedString(string: "discodrive.kosmosoid.dev", attributes: [
            .link: URL(string: "https://discodrive.kosmosoid.dev")!,
            .font: NSFont.systemFont(ofSize: 11),
            .paragraphStyle: para,
        ])
        NSApp.orderFrontStandardAboutPanel(options: [
            .applicationVersion: "0.1 beta",
            .credits: credits,
        ])
        NSApp.activate(ignoringOtherApps: true)
    }

    // MARK: - Tray Status Icon

    // One silhouette of the logo, the way WireGuard does it: full while the server is in
    // reach, dimmed while it is not, and blinking between the two while a pass runs. The
    // shape never changes, so the state is legible at menu bar size where a badge was not.
    private var blink: Timer?

    // Update the tray icon to reflect the current sync status (called from DiscoDriveApp).
    func setStatus(_ status: AppState.SyncStatus) {
        blink?.invalidate(); blink = nil
        guard let button = statusItem?.button else { return }
        switch status {
        case .idle:
            button.appearsDisabled = false
        case .offline:
            button.appearsDisabled = true
        case .syncing:
            button.appearsDisabled = false
            blink = Timer.scheduledTimer(withTimeInterval: 0.6, repeats: true) { [weak button] _ in
                button?.appearsDisabled.toggle()
            }
        }
    }

    // The logo's silhouette from Assets, or an SF Symbol when it is missing; a template
    // either way, so it follows the menu bar's light and dark looks.
    private func trayImage(for status: AppState.SyncStatus) -> NSImage? {
        let (asset, symbol) = ("TrayLogo", "opticaldisc")
        let img = NSImage(named: asset)
            ?? NSImage(systemSymbolName: symbol, accessibilityDescription: "DiscoDrive")
        img?.isTemplate = true
        return img
    }
}
