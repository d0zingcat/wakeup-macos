package tailscale

import (
	"net"
	"testing"
)

func TestParseIPv4(t *testing.T) {
	tests := []struct {
		name    string
		output  string
		want    string
		wantErr bool
	}{
		{"valid ip", "100.64.1.5\n", "100.64.1.5", false},
		{"with trailing whitespace", "  100.64.1.5  \n", "100.64.1.5", false},
		{"empty output", "", "", true},
		{"whitespace only", "  \n", "", true},
		{"error message", "The Tailscale GUI failed to start\n", "", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseIPv4Output(tt.output)
			if (err != nil) != tt.wantErr {
				t.Errorf("parseIPv4Output() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if got != tt.want {
				t.Errorf("parseIPv4Output() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestIPFromInterface(t *testing.T) {
	ip, err := ipFromInterface()
	if err != nil {
		t.Skipf("no Tailscale interface found (expected in CI): %v", err)
	}
	parsed := net.ParseIP(ip)
	if parsed == nil {
		t.Fatalf("ipFromInterface() returned invalid IP: %q", ip)
	}
	if !tailscaleCGNAT.Contains(parsed) {
		t.Fatalf("ipFromInterface() returned non-CGNAT IP: %s", ip)
	}
	t.Logf("found Tailscale IP: %s", ip)
}

func TestTailscaleCGNAT(t *testing.T) {
	tests := []struct {
		ip   string
		want bool
	}{
		{"100.64.0.1", true},
		{"100.112.10.78", true},
		{"100.127.255.255", true},
		{"100.63.255.255", false},
		{"100.128.0.0", false},
		{"192.168.1.1", false},
		{"10.0.0.1", false},
	}

	for _, tt := range tests {
		t.Run(tt.ip, func(t *testing.T) {
			ip := net.ParseIP(tt.ip)
			got := tailscaleCGNAT.Contains(ip)
			if got != tt.want {
				t.Errorf("tailscaleCGNAT.Contains(%s) = %v, want %v", tt.ip, got, tt.want)
			}
		})
	}
}
