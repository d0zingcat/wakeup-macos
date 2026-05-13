package tailscale

import (
	"fmt"
	"os/exec"
	"strings"
)

// Available returns true if the tailscale CLI is on PATH.
func Available() bool {
	_, err := exec.LookPath("tailscale")
	return err == nil
}

// IPv4 runs `tailscale ip -4` and returns the first IPv4 address.
func IPv4() (string, error) {
	out, err := exec.Command("tailscale", "ip", "-4").Output()
	if err != nil {
		return "", fmt.Errorf("tailscale ip -4: %w", err)
	}
	return parseIPv4Output(string(out))
}

// parseIPv4Output extracts the IP from command output.
func parseIPv4Output(output string) (string, error) {
	ip := strings.TrimSpace(output)
	if ip == "" {
		return "", fmt.Errorf("empty output from tailscale ip -4")
	}
	return ip, nil
}
