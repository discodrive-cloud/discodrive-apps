//go:build !darwin && !windows

package desktop

// writeQuarantine is a no-op: Linux desktops have no download mark to set.
func writeQuarantine(string) error { return nil }
