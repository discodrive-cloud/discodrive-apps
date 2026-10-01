//go:build !linux

package main

import "github.com/wailsapp/wails/v2/pkg/options/linux"

// Launcher entries and window naming are Linux desktop concepts.
func linuxOptions() *linux.Options { return nil }
func installLauncher()             {}
