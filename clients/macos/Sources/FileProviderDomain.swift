import FileProvider

// The Finder side of the app: one domain, "DiscoDrive", registered while paired.
enum FileProviderDomain {
    // Built fresh each time: NSFileProviderDomain is a plain value the system copies.
    static var domain: NSFileProviderDomain { NSFileProviderDomain(identifier: .init("DiscoDrive"), displayName: "DiscoDrive") }

    static func register() {
        NSFileProviderManager.add(domain) { error in
            if let error { NSLog("DiscoDrive: File Provider domain not added: %@", String(describing: error)) }
        }
    }

    static func unregister() {
        NSFileProviderManager.remove(domain) { error in
            if let error { NSLog("DiscoDrive: File Provider domain not removed: %@", String(describing: error)) }
        }
    }

    // The server said something changed: have the extension ask for the delta.
    static func signalChanges() {
        NSFileProviderManager(for: domain)?.signalEnumerator(for: .workingSet) { _ in }
    }
}
