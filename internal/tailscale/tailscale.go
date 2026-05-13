package tailscale

import (
	"fmt"
	"net"
	"os"
	"os/exec"
	"strings"
)

// Known paths where the Tailscale CLI may be installed on macOS.
var knownPaths = []string{
	"/Applications/Tailscale.app/Contents/MacOS/Tailscale",
	"/usr/local/bin/tailscale",
	"/opt/homebrew/bin/tailscale",
}

// findBinary returns the path to the tailscale CLI binary.
// Checks PATH first, then known macOS installation paths.
func findBinary() string {
	if p, err := exec.LookPath("tailscale"); err == nil {
		return p
	}
	for _, p := range knownPaths {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return ""
}

// Available returns true if the tailscale CLI can be found.
func Available() bool {
	return findBinary() != ""
}

// IPv4 runs `tailscale ip -4` and returns the first IPv4 address.
func IPv4() (string, error) {
	bin := findBinary()
	if bin == "" {
		return "", fmt.Errorf("tailscale CLI not found")
	}
	out, err := exec.Command(bin, "ip", "-4").Output()
	if err != nil {
		return "", fmt.Errorf("tailscale ip -4: %w", err)
	}
	return parseIPv4Output(string(out))
}

// parseIPv4Output extracts and validates the IPv4 address from command output.
func parseIPv4Output(output string) (string, error) {
	// Take first line only — error messages may follow on subsequent lines
	ip := strings.TrimSpace(strings.SplitN(output, "\n", 2)[0])
	if ip == "" {
		return "", fmt.Errorf("empty output from tailscale ip -4")
	}
	// Validate it is actually an IP — reject error messages from the CLI
	if net.ParseIP(ip) == nil {
		return "", fmt.Errorf("tailscale ip -4 returned non-IP output: %q", ip)
	}
	return ip, nil
}
