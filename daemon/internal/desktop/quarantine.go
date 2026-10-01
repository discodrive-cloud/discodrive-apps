package desktop

import "log"

// markDownloaded tags a file fetched from the server as coming from the internet, so the
// OS applies its usual checks when it is opened: Gatekeeper on macOS, SmartScreen and the
// Office/Script Host prompts on Windows. The name is server-controlled — a compromised
// server can rename "report.pdf" to "report.pdf.js" — and without the mark such a file
// would run with no warning. Failure only loses that warning, so it is logged, not fatal.
func markDownloaded(path string) {
	if err := writeQuarantine(path); err != nil {
		log.Printf("desktop: could not mark %s as downloaded: %v", path, err)
	}
}
