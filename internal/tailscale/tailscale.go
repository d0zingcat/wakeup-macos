package tailscale

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"strings"
	"time"
)

// tailscaleCGNAT is the Tailscale CGNAT range 100.64.0.0/10.
var tailscaleCGNAT = &net.IPNet{
	IP:   net.IP{100, 64, 0, 0},
	Mask: net.CIDRMask(10, 32),
}

// Ordered so that standalone/homebrew binaries (which talk to tailscaled)
// come before the App bundle CLI (which requires the GUI to be reachable).
var knownPaths = []string{
	"/opt/homebrew/bin/tailscale",
	"/usr/local/bin/tailscale",
	"/Applications/Tailscale.app/Contents/MacOS/Tailscale",
}

func candidates() []string {
	var paths []string
	seen := map[string]bool{}

	if p, err := exec.LookPath("tailscale"); err == nil {
		paths = append(paths, p)
		seen[p] = true
	}
	for _, p := range knownPaths {
		if seen[p] {
			continue
		}
		if _, err := os.Stat(p); err == nil {
			paths = append(paths, p)
			seen[p] = true
		}
	}
	return paths
}

// Available returns true if Tailscale is available (interface up or CLI found).
func Available() bool {
	if _, err := ipFromInterface(); err == nil {
		return true
	}
	return len(candidates()) > 0
}

// IPv4 returns the Tailscale IPv4 address. It first checks network interfaces
// for an IP in the CGNAT range (works as root without CLI, and never hangs),
// then falls back to running the tailscale CLI with a timeout.
func IPv4() (string, error) {
	if ip, err := ipFromInterface(); err == nil {
		return ip, nil
	}

	bins := candidates()
	if len(bins) == 0 {
		return "", fmt.Errorf("tailscale IP not found on interfaces and no CLI available")
	}

	var lastErr error
	for _, bin := range bins {
		ip, err := ipFromCLI(bin)
		if err != nil {
			lastErr = err
			continue
		}
		return ip, nil
	}
	return "", fmt.Errorf("all tailscale binaries failed: %w", lastErr)
}

// ipFromCLI runs `tailscale ip -4` with a 5-second timeout.
// The timeout prevents the daemon from hanging if the CLI stalls
// (e.g. when the Tailscale GUI crashes or is unresponsive).
func ipFromCLI(bin string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	out, err := exec.CommandContext(ctx, bin, "ip", "-4").Output()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok && len(exitErr.Stderr) > 0 {
			return "", fmt.Errorf("%s: %s", bin, strings.TrimSpace(string(exitErr.Stderr)))
		}
		return "", fmt.Errorf("%s: %w", bin, err)
	}
	return parseIPv4Output(string(out))
}

// ipFromInterface scans network interfaces for an IPv4 in the Tailscale CGNAT range.
func ipFromInterface() (string, error) {
	ifaces, err := net.Interfaces()
	if err != nil {
		return "", err
	}
	for _, iface := range ifaces {
		if iface.Flags&net.FlagUp == 0 {
			continue
		}
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, addr := range addrs {
			var ip net.IP
			switch v := addr.(type) {
			case *net.IPNet:
				ip = v.IP
			case *net.IPAddr:
				ip = v.IP
			}
			if ip == nil || ip.To4() == nil {
				continue
			}
			if tailscaleCGNAT.Contains(ip) {
				return ip.String(), nil
			}
		}
	}
	return "", fmt.Errorf("no interface with Tailscale CGNAT IP found")
}

// parseIPv4Output scans lines for the first valid IPv4 address.
// Rejects non-IP output such as error messages written to stdout.
func parseIPv4Output(output string) (string, error) {
	for _, line := range strings.Split(output, "\n") {
		candidate := strings.TrimSpace(line)
		if candidate == "" {
			continue
		}
		ip := net.ParseIP(candidate)
		if ip != nil && ip.To4() != nil {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("no valid IPv4 address in output: %q", strings.TrimSpace(output))
}
