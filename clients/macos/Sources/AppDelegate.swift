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

    // Update the tray icon to reflect the current sync status (called from DiscoDriveApp).
    func setStatus(_ status: AppState.SyncStatus) {
        statusItem?.button?.image = trayImage(for: status)
    }

    // The status icons from Assets (Tray*) are the coloured brand set — the logo, with a
    // cross for offline and arrows while syncing — and are drawn as they are, like
    // Dropbox's. Only the SF Symbol fallback is a template.
    private func trayImage(for status: AppState.SyncStatus) -> NSImage? {
        let (asset, symbol): (String, String)
        switch status {
        case .idle:    (asset, symbol) = ("TrayIdle", "opticaldisc")
        case .syncing: (asset, symbol) = ("TraySyncing", "arrow.triangle.2.circlepath")
        case .offline: (asset, symbol) = ("TrayOffline", "opticaldisc.fill")
        }
        if let img = NSImage(named: asset) { return img }
        let img = NSImage(systemSymbolName: symbol, accessibilityDescription: "DiscoDrive")
        img?.isTemplate = true
        return img
    }
}
