//go:build tray && darwin

package main

import (
	"os/exec"
	"strings"
	"testing"
)

// Server-controlled text (a vault folder name, an error quoting a file name) reaches
// the dialogs. It must arrive as data: a quote, a backslash or "--" must not end the
// string and turn the rest into AppleScript.
func TestOsascriptPassesValuesAsData(t *testing.T) {
	hostile := []string{
		`Notes\" & (character id {73,78,74}) --`,
		`x" & (do shell script "echo pwned") --`,
		"line1\nline2 \\ \"q\"",
		"-e",
		"-l",
	}
	for _, v := range hostile {
		args := osascriptArgs([]string{"on run argv", "return item 1 of argv", "end run"}, v, "-e", `do shell script "echo pwned"`)
		out, err := exec.Command("osascript", args...).Output()
		if err != nil {
			t.Fatalf("osascript: %v", err)
		}
		if got := strings.TrimSuffix(string(out), "\n"); got != v {
			t.Fatalf("value altered or evaluated:\n got %q\nwant %q", got, v)
		}
	}
	for _, script := range [][]string{dialogScript(false), dialogScript(true), notifyScript} {
		for _, line := range script {
			for _, v := range hostile {
				if strings.Contains(line, v) {
					t.Fatalf("script embeds a value: %q", line)
				}
			}
		}
	}
}
