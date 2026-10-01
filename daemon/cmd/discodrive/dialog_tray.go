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

// recoveryReadScript reads the recovery dialog's text from the file named by argv item 1:
// the phrase on the first line, the message after it. The phrase never appears on a
// command line, where other local users could read it (ps).
var recoveryReadScript = []string{
	"on run argv",
	"set input to read (POSIX file (item 1 of argv)) as «class utf8»",
	"set phrase to paragraph 1 of input",
	"set AppleScript's text item delimiters to linefeed",
	"set msg to (paragraphs 2 thru -1 of input) as text",
}

// recoveryScript shows the recovery phrase (also in a text field, so it can be copied)
// with two buttons: argv = text file, title, save label, done label. Done is the default.
var recoveryScript = append(append([]string{}, recoveryReadScript...),
	"set r to display dialog msg default answer phrase with title (item 2 of argv) buttons {(item 3 of argv), (item 4 of argv)} default button 2",
	"return button returned of r",
	"end run",
)

// showRecoveryPhrase shows a new vault's recovery phrase and reports whether the user
// asked to save a copy to a file. Closing the dialog, or any error, means no.
func showRecoveryPhrase(syncDir, title, msg, phrase string) bool {
	save, done := recoveryButtonLabels()
	switch runtime.GOOS {
	case "darwin":
		textFile, cleanup, err := writeDialogText(syncDir, phrase+"\n"+msg)
		if err != nil {
			return false
		}
		defer cleanup()
		out, err := exec.Command("osascript", osascriptArgs(recoveryScript, textFile, title, save, done)...).Output()
		return err == nil && strings.TrimRight(string(out), "\n") == save
	default: // linux: the text arrives on stdin; OK ("Done") is the default, Save is extra
		cmd := exec.Command("zenity", "--text-info", "--title="+title, "--width=520", "--height=260",
			"--ok-label="+done, "--cancel-label="+done, "--extra-button="+save)
		cmd.Stdin = strings.NewReader(msg + "\n\n" + phrase + "\n")
		out, _ := cmd.Output() // the extra button exits 1 and prints its label
		return strings.TrimRight(string(out), "\n") == save
	}
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
