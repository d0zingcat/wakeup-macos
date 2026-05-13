package notify

import (
	"fmt"
	"io"
	"os"
)

// Send emits an OSC 9 terminal notification and prints a text fallback to stdout.
func Send(message string) {
	SendTo(os.Stdout, message)
}

// SendTo writes the notification to the given writer (for testing).
func SendTo(w io.Writer, message string) {
	// OSC 9 notification (supported by Ghostty, iTerm2, kitty)
	fmt.Fprint(w, formatOSC9(message))
	// Text fallback (always printed)
	fmt.Fprintln(w, message)
}

// formatOSC9 returns the OSC 9 escape sequence for a notification.
func formatOSC9(message string) string {
	return "\033]9;" + message + "\007"
}
