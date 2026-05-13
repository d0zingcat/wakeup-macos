package notify

import (
	"bytes"
	"testing"
)

func TestFormatOSC9(t *testing.T) {
	got := formatOSC9("Device office is online")
	want := "\033]9;Device office is online\007"
	if got != want {
		t.Errorf("formatOSC9() = %q, want %q", got, want)
	}
}

func TestSendTo(t *testing.T) {
	var buf bytes.Buffer
	SendTo(&buf, "Device office is online")

	output := buf.String()

	// Should contain OSC 9 sequence
	osc9 := "\033]9;Device office is online\007"
	if !bytes.Contains([]byte(output), []byte(osc9)) {
		t.Errorf("output missing OSC 9 sequence, got: %q", output)
	}

	// Should contain text fallback
	if !bytes.Contains([]byte(output), []byte("Device office is online")) {
		t.Errorf("output missing text fallback, got: %q", output)
	}
}
