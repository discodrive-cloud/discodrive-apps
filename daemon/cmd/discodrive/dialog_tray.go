//go:build tray

package main

import (
	"os/exec"
	"runtime"
	"strings"
)

// promptText shows a native input dialog (hidden for passwords) and returns the entered value.
// macOS uses osascript, Linux uses zenity. On error or cancel returns ("", false).
func promptText(title, prompt string, hidden bool) (string, bool) {
	switch runtime.GOOS {
	case "darwin":
		out, err := exec.Command("osascript", osascriptArgs(dialogScript(hidden), prompt, title)...).Output()
		if err != nil {
			return "", false // cancel or error
		}
		// output format: button returned:OK, text returned:<value>
		s := string(out)
		i := strings.Index(s, "text returned:")
		if i < 0 {
			return "", false
		}
		return strings.TrimRight(s[i+len("text returned:"):], "\n"), true
	default: // linux
		args := []string{"--entry", "--title=" + title, "--text=" + prompt}
		if hidden {
			args = []string{"--password", "--title=" + title}
		}
		out, err := exec.Command("zenity", args...).Output()
		if err != nil {
			return "", false
		}
		return strings.TrimRight(string(out), "\n"), true
	}
}

// The dialog texts can carry server-controlled data (a vault folder name, an error
// quoting a file name), so they are never spliced into AppleScript source: the scripts
// are constants and the values arrive through argv.
func dialogScript(hidden bool) []string {
	show := `display dialog (item 1 of argv) default answer "" with title (item 2 of argv)`
	if hidden {
		show += " with hidden answer"
	}
	return []string{"on run argv", show, "end run"}
}

var notifyScript = []string{"on run argv", "display notification (item 1 of argv) with title (item 2 of argv)", "end run"}

// osascriptArgs builds `osascript -e line… -- value…`. The "--" keeps a value that
// starts with a dash (e.g. "-e …") from being parsed as another script line.
func osascriptArgs(script []string, values ...string) []string {
	args := make([]string, 0, 2*len(script)+1+len(values))
	for _, line := range script {
		args = append(args, "-e", line)
	}
	args = append(args, "--")
	return append(args, values...)
}

// notify shows a native desktop notification (best-effort).
func notify(title, msg string) {
	switch runtime.GOOS {
	case "darwin":
		_ = exec.Command("osascript", osascriptArgs(notifyScript, msg, title)...).Start()
	default:
		_ = exec.Command("notify-send", "--", title, msg).Start()
	}
}
